package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kamalgs/infermesh/api"
	"github.com/kamalgs/infermesh/internal/testutil"
	"github.com/nats-io/nats.go"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// setupProxy creates a Proxy with a test HTTP server (not listening on a real port).
func setupProxy(t *testing.T, nc *nats.Conn) *Proxy {
	t.Helper()
	return New(nc, ":0", noopLogger())
}

func TestProxy_HealthCheck(t *testing.T) {
	_, nc := testutil.StartNATS(t)
	p := setupProxy(t, nc)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("body: got %v", body)
	}
}

func TestProxy_ChatCompletion(t *testing.T) {
	_, nc := testutil.StartNATS(t)

	// Mock gateway subscriber
	nc2, _ := nats.Connect(nc.ConnectedUrl())
	t.Cleanup(func() { nc2.Close() })

	nc2.Subscribe("llm.chat.complete", func(msg *nats.Msg) {
		resp := api.ChatResponse{
			ID:     "test-123",
			Object: "chat.completion",
			Model:  "gpt-4o",
			Choices: []api.Choice{{
				Index:        0,
				Message:      &api.Message{Role: "assistant", Content: "proxy works"},
				FinishReason: "stop",
			}},
		}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})
	nc2.Flush()

	p := setupProxy(t, nc)
	chatReq := api.ChatRequest{
		Model:    "gpt-4o",
		Messages: []api.Message{{Role: "user", Content: "hello"}},
	}
	body, _ := json.Marshal(chatReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}

	var resp api.ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Choices[0].Message.Content != "proxy works" {
		t.Errorf("content: got %q", resp.Choices[0].Message.Content)
	}
}

func TestProxy_ChatCompletionErrorPropagation(t *testing.T) {
	_, nc := testutil.StartNATS(t)

	nc2, _ := nats.Connect(nc.ConnectedUrl())
	t.Cleanup(func() { nc2.Close() })

	nc2.Subscribe("llm.chat.complete", func(msg *nats.Msg) {
		errResp := api.ErrorResponse{
			Error: api.APIError{Message: "model not found", Type: "error", Code: "model_not_found"},
		}
		data, _ := json.Marshal(errResp)
		msg.Respond(data)
	})
	nc2.Flush()

	p := setupProxy(t, nc)
	chatReq := api.ChatRequest{
		Model:    "nonexistent",
		Messages: []api.Message{{Role: "user", Content: "hello"}},
	}
	body, _ := json.Marshal(chatReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", w.Code)
	}

	var errResp api.ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp.Error.Code != "model_not_found" {
		t.Errorf("code: got %q", errResp.Error.Code)
	}
}

func TestProxy_InvalidJSON(t *testing.T) {
	_, nc := testutil.StartNATS(t)
	p := setupProxy(t, nc)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("{bad")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestProxy_NATSTimeout(t *testing.T) {
	_, nc := testutil.StartNATS(t)

	// No subscriber — will timeout
	p := New(nc, ":0", noopLogger())

	chatReq := api.ChatRequest{
		Model:    "gpt-4o",
		Messages: []api.Message{{Role: "user", Content: "hello"}},
	}
	body, _ := json.Marshal(chatReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	start := time.Now()
	p.server.Handler.ServeHTTP(w, req)
	elapsed := time.Since(start)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", w.Code)
	}

	// Should fail fast with no responders, not wait full 30s
	if elapsed > 5*time.Second {
		t.Errorf("took too long: %v", elapsed)
	}
}
