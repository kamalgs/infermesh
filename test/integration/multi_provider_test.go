package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kamalgs/infermesh/api"
	"github.com/kamalgs/infermesh/internal/config"
	"github.com/kamalgs/infermesh/internal/gateway"
	anthropicAdapter "github.com/kamalgs/infermesh/internal/provider/anthropic"
	ollamaAdapter "github.com/kamalgs/infermesh/internal/provider/ollama"
	openaiAdapter "github.com/kamalgs/infermesh/internal/provider/openai"
	"github.com/kamalgs/infermesh/internal/testutil"
)

// mockOpenAIServer returns a mock that serves OpenAI-compatible responses.
func mockOpenAIServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)

		resp := api.ChatResponse{
			ID:      "chatcmpl-multi",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []api.Choice{{
				Index:        0,
				Message:      &api.Message{Role: "assistant", Content: "openai: " + model},
				FinishReason: "stop",
			}},
			Usage: &api.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

// mockAnthropicServer returns a mock that serves Anthropic Messages API responses.
func mockAnthropicServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)

		resp := map[string]any{
			"id":    "msg-multi",
			"type":  "message",
			"model": model,
			"role":  "assistant",
			"content": []map[string]any{{
				"type": "text",
				"text": "anthropic: " + model,
			}},
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":  7,
				"output_tokens": 4,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

// mockOllamaServer returns a mock that serves Ollama-compatible responses.
func mockOllamaServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)

		resp := api.ChatResponse{
			ID:      "ollama-multi",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []api.Choice{{
				Index:        0,
				Message:      &api.Message{Role: "assistant", Content: "ollama: " + model},
				FinishReason: "stop",
			}},
			Usage: &api.Usage{PromptTokens: 6, CompletionTokens: 3, TotalTokens: 9},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestMultiProvider_Routing(t *testing.T) {
	openaiMock := mockOpenAIServer(t)
	defer openaiMock.Close()
	anthropicMock := mockAnthropicServer(t)
	defer anthropicMock.Close()
	ollamaMock := mockOllamaServer(t)
	defer ollamaMock.Close()

	_, nc := testutil.StartNATS(t)

	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"gpt-4o":        {Provider: "openai", UpstreamModel: "gpt-4o-2024-08-06"},
			"gpt-4o-mini":   {Provider: "openai", UpstreamModel: "gpt-4o-mini-2024-07-18"},
			"claude-sonnet": {Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
			"claude-haiku":  {Provider: "anthropic", UpstreamModel: "claude-haiku-4-5-20251001"},
			"llama3":        {Provider: "ollama", UpstreamModel: "llama3:8b"},
			"mistral":       {Provider: "ollama", UpstreamModel: "mistral:7b"},
		},
		Providers: map[string]config.ProviderConfig{
			"openai":    {BaseURL: openaiMock.URL, APIKey: "test-key"},
			"anthropic": {BaseURL: anthropicMock.URL, APIKey: "test-key"},
			"ollama":    {BaseURL: ollamaMock.URL},
		},
	}

	log := silentLogger()

	// Start all three provider adapters
	oa := openaiAdapter.NewAdapter(cfg.Providers["openai"], log)
	oaSub, err := oa.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe openai: %v", err)
	}
	defer oaSub.Drain()

	aa := anthropicAdapter.NewAdapter(cfg.Providers["anthropic"], log)
	aaSub, err := aa.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe anthropic: %v", err)
	}
	defer aaSub.Drain()

	ol := ollamaAdapter.NewAdapter(cfg.Providers["ollama"], log)
	olSub, err := ol.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe ollama: %v", err)
	}
	defer olSub.Drain()

	// Start gateway
	gw := gateway.New(nc, cfg, log)
	if err := gw.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer gw.Stop()

	tests := []struct {
		model         string
		wantUpstream  string
		wantProvider  string
	}{
		{"gpt-4o", "gpt-4o-2024-08-06", "openai"},
		{"gpt-4o-mini", "gpt-4o-mini-2024-07-18", "openai"},
		{"claude-sonnet", "claude-sonnet-4-20250514", "anthropic"},
		{"claude-haiku", "claude-haiku-4-5-20251001", "anthropic"},
		{"llama3", "llama3:8b", "ollama"},
		{"mistral", "mistral:7b", "ollama"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			req := api.ChatRequest{
				Model:    tt.model,
				Messages: []api.Message{{Role: "user", Content: "test"}},
			}
			data, _ := json.Marshal(req)

			msg, err := nc.Request("llm.chat.complete", data, 5*time.Second)
			if err != nil {
				t.Fatalf("request: %v", err)
			}

			var resp api.ChatResponse
			if err := json.Unmarshal(msg.Data, &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if resp.Model != tt.wantUpstream {
				t.Errorf("model: got %q, want %q", resp.Model, tt.wantUpstream)
			}

			wantContent := tt.wantProvider + ": " + tt.wantUpstream
			if resp.Choices[0].Message.Content != wantContent {
				t.Errorf("content: got %q, want %q", resp.Choices[0].Message.Content, wantContent)
			}
		})
	}
}

func TestMultiProvider_UnifiedResponseFormat(t *testing.T) {
	openaiMock := mockOpenAIServer(t)
	defer openaiMock.Close()
	anthropicMock := mockAnthropicServer(t)
	defer anthropicMock.Close()
	ollamaMock := mockOllamaServer(t)
	defer ollamaMock.Close()

	_, nc := testutil.StartNATS(t)

	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"gpt-4o":        {Provider: "openai", UpstreamModel: "gpt-4o-2024-08-06"},
			"claude-sonnet": {Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
			"llama3":        {Provider: "ollama", UpstreamModel: "llama3:8b"},
		},
		Providers: map[string]config.ProviderConfig{
			"openai":    {BaseURL: openaiMock.URL, APIKey: "test-key"},
			"anthropic": {BaseURL: anthropicMock.URL, APIKey: "test-key"},
			"ollama":    {BaseURL: ollamaMock.URL},
		},
	}

	log := silentLogger()

	oa := openaiAdapter.NewAdapter(cfg.Providers["openai"], log)
	oaSub, _ := oa.Subscribe(nc)
	defer oaSub.Drain()

	aa := anthropicAdapter.NewAdapter(cfg.Providers["anthropic"], log)
	aaSub, _ := aa.Subscribe(nc)
	defer aaSub.Drain()

	ol := ollamaAdapter.NewAdapter(cfg.Providers["ollama"], log)
	olSub, _ := ol.Subscribe(nc)
	defer olSub.Drain()

	gw := gateway.New(nc, cfg, log)
	gw.Start()
	defer gw.Stop()

	// All responses should have the same unified format regardless of provider
	models := []string{"gpt-4o", "claude-sonnet", "llama3"}
	for _, model := range models {
		req := api.ChatRequest{
			Model:    model,
			Messages: []api.Message{{Role: "user", Content: "test"}},
		}
		data, _ := json.Marshal(req)

		msg, err := nc.Request("llm.chat.complete", data, 5*time.Second)
		if err != nil {
			t.Fatalf("%s request: %v", model, err)
		}

		var resp api.ChatResponse
		if err := json.Unmarshal(msg.Data, &resp); err != nil {
			t.Fatalf("%s unmarshal: %v", model, err)
		}

		// Verify unified response structure
		if resp.Object != "chat.completion" {
			t.Errorf("%s: object = %q, want chat.completion", model, resp.Object)
		}
		if len(resp.Choices) != 1 {
			t.Errorf("%s: choices count = %d, want 1", model, len(resp.Choices))
		}
		if resp.Choices[0].Message == nil {
			t.Errorf("%s: message is nil", model)
		}
		if resp.Choices[0].FinishReason != "stop" {
			t.Errorf("%s: finish_reason = %q, want stop", model, resp.Choices[0].FinishReason)
		}
		if resp.Usage == nil {
			t.Errorf("%s: usage is nil", model)
		}
	}
}
