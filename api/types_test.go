package api

import (
	"encoding/json"
	"testing"
)

func TestChatRequestMarshal(t *testing.T) {
	temp := 0.7
	req := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello!"},
		},
		Temperature: &temp,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ChatRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Model != req.Model {
		t.Errorf("model: got %q, want %q", decoded.Model, req.Model)
	}
	if len(decoded.Messages) != 2 {
		t.Fatalf("messages: got %d, want 2", len(decoded.Messages))
	}
	if decoded.Messages[1].Content != "Hello!" {
		t.Errorf("content: got %q, want %q", decoded.Messages[1].Content, "Hello!")
	}
	if decoded.Temperature == nil || *decoded.Temperature != 0.7 {
		t.Errorf("temperature: got %v, want 0.7", decoded.Temperature)
	}
	if decoded.MaxTokens != nil {
		t.Errorf("max_tokens: got %v, want nil", decoded.MaxTokens)
	}
}

func TestChatResponseMarshal(t *testing.T) {
	resp := ChatResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "gpt-4o",
		Choices: []Choice{
			{
				Index:        0,
				Message:      &Message{Role: "assistant", Content: "Hello!"},
				FinishReason: "stop",
			},
		},
		Usage: &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify it matches OpenAI JSON field names
	var raw map[string]any
	json.Unmarshal(data, &raw)

	if raw["object"] != "chat.completion" {
		t.Errorf("object field: got %v", raw["object"])
	}
	if raw["id"] != "chatcmpl-123" {
		t.Errorf("id field: got %v", raw["id"])
	}

	var decoded ChatResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: got %q", decoded.Choices[0].FinishReason)
	}
}

func TestErrorResponseMarshal(t *testing.T) {
	errResp := ErrorResponse{
		Error: APIError{
			Message: "model not found",
			Type:    "error",
			Code:    "model_not_found",
		},
	}

	data, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	json.Unmarshal(data, &raw)

	errObj, ok := raw["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error field")
	}
	if errObj["code"] != "model_not_found" {
		t.Errorf("code: got %v", errObj["code"])
	}
}

func TestProviderRequestMarshal(t *testing.T) {
	req := ProviderRequest{
		UpstreamModel: "gpt-4o-2024-08-06",
		Request: ChatRequest{
			Model:    "gpt-4o",
			Messages: []Message{{Role: "user", Content: "Hi"}},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ProviderRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.UpstreamModel != "gpt-4o-2024-08-06" {
		t.Errorf("upstream_model: got %q", decoded.UpstreamModel)
	}
	if decoded.Request.Model != "gpt-4o" {
		t.Errorf("request.model: got %q", decoded.Request.Model)
	}
}

// TestOpenAIJSONCompatibility verifies our types can roundtrip with real OpenAI JSON.
func TestOpenAIJSONCompatibility(t *testing.T) {
	openaiJSON := `{
		"id": "chatcmpl-abc123",
		"object": "chat.completion",
		"created": 1709900000,
		"model": "gpt-4o-2024-08-06",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "Hello there!"},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 12,
			"completion_tokens": 8,
			"total_tokens": 20
		}
	}`

	var resp ChatResponse
	if err := json.Unmarshal([]byte(openaiJSON), &resp); err != nil {
		t.Fatalf("unmarshal OpenAI JSON: %v", err)
	}

	if resp.ID != "chatcmpl-abc123" {
		t.Errorf("id: got %q", resp.ID)
	}
	if resp.Choices[0].Message.Content != "Hello there!" {
		t.Errorf("content: got %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 20 {
		t.Errorf("total_tokens: got %d", resp.Usage.TotalTokens)
	}
}
