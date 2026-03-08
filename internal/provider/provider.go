package provider

import (
	"context"

	"github.com/kamalgs/nats-llm-gateway/api"
)

// Provider sends a chat completion request to an upstream LLM API.
type Provider interface {
	// Name returns the provider identifier (e.g. "openai", "anthropic").
	Name() string
	// ChatCompletion sends a request to the upstream API and returns the response.
	ChatCompletion(ctx context.Context, req *api.ProviderRequest) (*api.ChatResponse, error)
}
