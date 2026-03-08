package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kamalgs/nats-llm-gateway/api"
	"github.com/kamalgs/nats-llm-gateway/internal/config"
	"github.com/kamalgs/nats-llm-gateway/internal/testutil"
	"github.com/nats-io/nats.go"
)

func testConfig() *config.Config {
	return &config.Config{
		Models: map[string]config.ModelConfig{
			"gpt-4o": {Provider: "openai", UpstreamModel: "gpt-4o-2024-08-06"},
			"llama3": {Provider: "ollama", UpstreamModel: "llama3:70b"},
		},
	}
}

func TestGateway_RoutesToCorrectProvider(t *testing.T) {
	_, nc := testutil.StartNATS(t)

	// Set up a mock provider that echoes back
	nc2, _ := nats.Connect(nc.ConnectedUrl())
	t.Cleanup(func() { nc2.Close() })

	nc2.QueueSubscribe("llm.provider.openai", "test", func(msg *nats.Msg) {
		var provReq api.ProviderRequest
		json.Unmarshal(msg.Data, &provReq)

		resp := api.ChatResponse{
			ID:     "test-123",
			Object: "chat.completion",
			Model:  provReq.UpstreamModel,
			Choices: []api.Choice{{
				Index:        0,
				Message:      &api.Message{Role: "assistant", Content: "routed to " + provReq.UpstreamModel},
				FinishReason: "stop",
			}},
		}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})

	gw := New(nc, testConfig(), noopLogger())
	if err := gw.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gw.Stop()

	// Send a request
	req := api.ChatRequest{
		Model:    "gpt-4o",
		Messages: []api.Message{{Role: "user", Content: "hello"}},
	}
	data, _ := json.Marshal(req)

	msg, err := nc.Request(SubjectChatComplete, data, 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var resp api.ChatResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Model != "gpt-4o-2024-08-06" {
		t.Errorf("model: got %q, want gpt-4o-2024-08-06", resp.Model)
	}
	if resp.Choices[0].Message.Content != "routed to gpt-4o-2024-08-06" {
		t.Errorf("content: got %q", resp.Choices[0].Message.Content)
	}
}

func TestGateway_UnknownModelReturnsError(t *testing.T) {
	_, nc := testutil.StartNATS(t)

	gw := New(nc, testConfig(), noopLogger())
	gw.Start()
	defer gw.Stop()

	req := api.ChatRequest{
		Model:    "nonexistent",
		Messages: []api.Message{{Role: "user", Content: "hello"}},
	}
	data, _ := json.Marshal(req)

	msg, err := nc.Request(SubjectChatComplete, data, 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var errResp api.ErrorResponse
	if err := json.Unmarshal(msg.Data, &errResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if errResp.Error.Code != "model_not_found" {
		t.Errorf("code: got %q, want model_not_found", errResp.Error.Code)
	}
}

func TestGateway_InvalidJSONReturnsError(t *testing.T) {
	_, nc := testutil.StartNATS(t)

	gw := New(nc, testConfig(), noopLogger())
	gw.Start()
	defer gw.Stop()

	msg, err := nc.Request(SubjectChatComplete, []byte("{invalid"), 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var errResp api.ErrorResponse
	json.Unmarshal(msg.Data, &errResp)
	if errResp.Error.Code != "invalid_request" {
		t.Errorf("code: got %q, want invalid_request", errResp.Error.Code)
	}
}

func TestGateway_ProviderTimeoutReturnsError(t *testing.T) {
	_, nc := testutil.StartNATS(t)

	// No provider subscriber — request will timeout
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"slow": {Provider: "nonexistent", UpstreamModel: "x"},
		},
	}

	gw := New(nc, cfg, noopLogger())
	gw.Start()
	defer gw.Stop()

	req := api.ChatRequest{Model: "slow", Messages: []api.Message{{Role: "user", Content: "hi"}}}
	data, _ := json.Marshal(req)

	// Use a short timeout for the test — the gateway's internal timeout to the
	// provider will fire first as nats.ErrNoResponders
	msg, err := nc.Request(SubjectChatComplete, data, 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var errResp api.ErrorResponse
	json.Unmarshal(msg.Data, &errResp)
	if errResp.Error.Code != "provider_error" {
		t.Errorf("code: got %q, want provider_error", errResp.Error.Code)
	}
}
