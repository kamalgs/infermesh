// Package integration tests the full request path:
// HTTP proxy → NATS → Gateway → NATS → Provider Adapter → mock upstream
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kamalgs/nats-llm-gateway/api"
	"github.com/kamalgs/nats-llm-gateway/internal/config"
	"github.com/kamalgs/nats-llm-gateway/internal/gateway"
	openaiAdapter "github.com/kamalgs/nats-llm-gateway/internal/provider/openai"
	"github.com/kamalgs/nats-llm-gateway/internal/proxy"
	"github.com/kamalgs/nats-llm-gateway/internal/testutil"
	"github.com/nats-io/nats.go"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockLLM creates a mock LLM HTTP server that returns canned responses.
func mockLLM(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		messages, _ := body["messages"].([]any)

		content := "mock response"
		if len(messages) > 0 {
			if m, ok := messages[len(messages)-1].(map[string]any); ok {
				if c, ok := m["content"].(string); ok {
					content = "echo: " + c
				}
			}
		}

		resp := api.ChatResponse{
			ID:      "chatcmpl-integration",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []api.Choice{{
				Index:        0,
				Message:      &api.Message{Role: "assistant", Content: content},
				FinishReason: "stop",
			}},
			Usage: &api.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

// startStack starts NATS, gateway, provider adapter, and HTTP proxy.
// Returns the proxy HTTP URL and a cleanup function.
func startStack(t *testing.T) (proxyURL string) {
	t.Helper()

	mock := mockLLM(t)
	t.Cleanup(mock.Close)

	ns, nc := testutil.StartNATS(t)
	natsURL := testutil.NATSUrl(ns)

	cfg := &config.Config{
		NATS: config.NATSConfig{URL: natsURL},
		Models: map[string]config.ModelConfig{
			"gpt-4o":       {Provider: "openai", UpstreamModel: "gpt-4o-2024-08-06"},
			"claude-sonnet": {Provider: "openai", UpstreamModel: "claude-sonnet-mock"},
		},
		Providers: map[string]config.ProviderConfig{
			"openai": {BaseURL: mock.URL, APIKey: "test-key"},
		},
	}

	log := silentLogger()

	// Start provider adapter
	adapter := openaiAdapter.NewAdapter(cfg.Providers["openai"], log)
	sub, err := adapter.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe adapter: %v", err)
	}
	t.Cleanup(func() { sub.Drain() })

	// Start gateway
	gw := gateway.New(nc, cfg, log)
	if err := gw.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(gw.Stop)

	// Start proxy on a random port
	nc2, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect proxy: %v", err)
	}
	t.Cleanup(func() { nc2.Close() })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	proxyURL = fmt.Sprintf("http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)

	p := proxy.New(nc2, listener.Addr().String(), log)
	go func() {
		p.Start()
	}()
	// Give proxy time to bind — it's already listening via the addr above
	// Actually we need to use the listener. Let me just use the proxy's HTTP handler directly.
	listener.Close()

	// Use a simpler approach: start the proxy server directly
	listener2, _ := net.Listen("tcp", "127.0.0.1:0")
	proxyURL = fmt.Sprintf("http://%s", listener2.Addr().String())
	p2 := proxy.New(nc2, listener2.Addr().String(), log)
	listener2.Close()
	go p2.Start()
	time.Sleep(100 * time.Millisecond) // wait for bind

	return proxyURL
}

func TestE2E_HTTPProxyChatCompletion(t *testing.T) {
	proxyURL := startStack(t)

	// Test health
	resp, err := http.Get(proxyURL + "/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health status: %d", resp.StatusCode)
	}

	// Test chat completion
	chatReq := api.ChatRequest{
		Model:    "gpt-4o",
		Messages: []api.Message{{Role: "user", Content: "integration test"}},
	}
	body, _ := json.Marshal(chatReq)

	resp, err = http.Post(proxyURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("chat status: %d, body: %s", resp.StatusCode, respBody)
	}

	var chatResp api.ChatResponse
	json.NewDecoder(resp.Body).Decode(&chatResp)

	if chatResp.Model != "gpt-4o-2024-08-06" {
		t.Errorf("model: got %q", chatResp.Model)
	}
	if chatResp.Choices[0].Message.Content != "echo: integration test" {
		t.Errorf("content: got %q", chatResp.Choices[0].Message.Content)
	}
	if chatResp.Usage.TotalTokens != 15 {
		t.Errorf("total_tokens: got %d", chatResp.Usage.TotalTokens)
	}
}

func TestE2E_HTTPProxyModelNotFound(t *testing.T) {
	proxyURL := startStack(t)

	chatReq := api.ChatRequest{
		Model:    "nonexistent-model",
		Messages: []api.Message{{Role: "user", Content: "hello"}},
	}
	body, _ := json.Marshal(chatReq)

	resp, err := http.Post(proxyURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}

	var errResp api.ErrorResponse
	json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp.Error.Code != "model_not_found" {
		t.Errorf("code: got %q", errResp.Error.Code)
	}
}

func TestE2E_NATSDirectChatCompletion(t *testing.T) {
	mock := mockLLM(t)
	defer mock.Close()

	ns, nc := testutil.StartNATS(t)
	natsURL := testutil.NATSUrl(ns)
	_ = natsURL

	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"gpt-4o": {Provider: "openai", UpstreamModel: "gpt-4o-2024-08-06"},
		},
		Providers: map[string]config.ProviderConfig{
			"openai": {BaseURL: mock.URL, APIKey: "test-key"},
		},
	}

	log := silentLogger()

	adapter := openaiAdapter.NewAdapter(cfg.Providers["openai"], log)
	sub, _ := adapter.Subscribe(nc)
	defer sub.Drain()

	gw := gateway.New(nc, cfg, log)
	gw.Start()
	defer gw.Stop()

	// Simulate what the JS SDK does — direct NATS request
	req := api.ChatRequest{
		Model:    "gpt-4o",
		Messages: []api.Message{{Role: "user", Content: "direct nats"}},
	}
	data, _ := json.Marshal(req)

	msg, err := nc.Request("llm.chat.complete", data, 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var resp api.ChatResponse
	json.Unmarshal(msg.Data, &resp)

	if resp.Choices[0].Message.Content != "echo: direct nats" {
		t.Errorf("content: got %q", resp.Choices[0].Message.Content)
	}
}

func TestE2E_MultipleModelRouting(t *testing.T) {
	mock := mockLLM(t)
	defer mock.Close()

	_, nc := testutil.StartNATS(t)

	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"gpt-4o":        {Provider: "openai", UpstreamModel: "gpt-4o-2024-08-06"},
			"claude-sonnet":  {Provider: "openai", UpstreamModel: "claude-sonnet-mock"},
		},
		Providers: map[string]config.ProviderConfig{
			"openai": {BaseURL: mock.URL, APIKey: "test-key"},
		},
	}

	log := silentLogger()
	adapter := openaiAdapter.NewAdapter(cfg.Providers["openai"], log)
	sub, _ := adapter.Subscribe(nc)
	defer sub.Drain()

	gw := gateway.New(nc, cfg, log)
	gw.Start()
	defer gw.Stop()

	// Request gpt-4o
	req1 := api.ChatRequest{Model: "gpt-4o", Messages: []api.Message{{Role: "user", Content: "hi"}}}
	data1, _ := json.Marshal(req1)
	msg1, _ := nc.Request("llm.chat.complete", data1, 5*time.Second)
	var resp1 api.ChatResponse
	json.Unmarshal(msg1.Data, &resp1)

	if resp1.Model != "gpt-4o-2024-08-06" {
		t.Errorf("gpt-4o routed to: %q", resp1.Model)
	}

	// Request claude-sonnet
	req2 := api.ChatRequest{Model: "claude-sonnet", Messages: []api.Message{{Role: "user", Content: "hi"}}}
	data2, _ := json.Marshal(req2)
	msg2, _ := nc.Request("llm.chat.complete", data2, 5*time.Second)
	var resp2 api.ChatResponse
	json.Unmarshal(msg2.Data, &resp2)

	if resp2.Model != "claude-sonnet-mock" {
		t.Errorf("claude-sonnet routed to: %q", resp2.Model)
	}
}
