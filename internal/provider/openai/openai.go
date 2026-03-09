package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/kamalgs/infermesh/api"
	"github.com/kamalgs/infermesh/internal/config"
	"github.com/kamalgs/infermesh/internal/provider"
)

const QueueGroup = "provider-openai"

// Adapter implements provider.Provider for OpenAI-compatible APIs.
type Adapter struct {
	cfg    config.ProviderConfig
	client *http.Client
	log    *slog.Logger
}

var _ provider.Provider = (*Adapter)(nil)

func NewAdapter(cfg config.ProviderConfig, log *slog.Logger) *Adapter {
	return &Adapter{
		cfg: cfg,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		log: log,
	}
}

func (a *Adapter) Name() string { return "openai" }

// ChatCompletion calls the upstream OpenAI API.
func (a *Adapter) ChatCompletion(ctx context.Context, req *api.ProviderRequest) (*api.ChatResponse, error) {
	// Build the upstream request body — standard OpenAI format
	body := map[string]any{
		"model":    req.UpstreamModel,
		"messages": req.Request.Messages,
	}
	if req.Request.Temperature != nil {
		body["temperature"] = *req.Request.Temperature
	}
	if req.Request.MaxTokens != nil {
		body["max_tokens"] = *req.Request.MaxTokens
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := a.cfg.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	}

	a.log.Info("calling upstream", "url", url, "model", req.UpstreamModel)

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned %d: %s", httpResp.StatusCode, string(respBody))
	}

	var chatResp api.ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &chatResp, nil
}

