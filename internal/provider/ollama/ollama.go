package ollama

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
	"github.com/nats-io/nats.go"
)

const QueueGroup = "provider-ollama"

// Adapter implements provider.Provider using Ollama's native /api/chat endpoint.
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
			Timeout: 120 * time.Second, // longer timeout for local inference
		},
		log: log,
	}
}

func (a *Adapter) Name() string { return "ollama" }

// Ollama native /api/chat request and response types.

type ollamaRequest struct {
	Model    string        `json:"model"`
	Messages []api.Message `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  *ollamaOptions `json:"options,omitempty"`
}

type ollamaOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	NumPredict  *int     `json:"num_predict,omitempty"`
}

type ollamaResponse struct {
	Model           string      `json:"model"`
	CreatedAt       string      `json:"created_at"`
	Message         api.Message `json:"message"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason"`
	TotalDuration   int64       `json:"total_duration"`
	LoadDuration    int64       `json:"load_duration"`
	PromptEvalCount int         `json:"prompt_eval_count"`
	EvalCount       int         `json:"eval_count"`
}

// ChatCompletion calls Ollama's native /api/chat endpoint and translates
// the response into the unified ChatResponse format.
func (a *Adapter) ChatCompletion(ctx context.Context, req *api.ProviderRequest) (*api.ChatResponse, error) {
	ollamaReq := ollamaRequest{
		Model:    req.UpstreamModel,
		Messages: req.Request.Messages,
		Stream:   false,
	}
	if req.Request.Temperature != nil || req.Request.MaxTokens != nil {
		opts := &ollamaOptions{}
		if req.Request.Temperature != nil {
			opts.Temperature = req.Request.Temperature
		}
		if req.Request.MaxTokens != nil {
			opts.NumPredict = req.Request.MaxTokens
		}
		ollamaReq.Options = opts
	}

	data, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := a.cfg.BaseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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

	var ollamaResp ollamaResponse
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	finishReason := "stop"
	if ollamaResp.DoneReason == "length" {
		finishReason = "length"
	}

	totalTokens := ollamaResp.PromptEvalCount + ollamaResp.EvalCount

	return &api.ChatResponse{
		ID:      fmt.Sprintf("ollama-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   ollamaResp.Model,
		Choices: []api.Choice{{
			Index:        0,
			Message:      &api.Message{Role: ollamaResp.Message.Role, Content: ollamaResp.Message.Content},
			FinishReason: finishReason,
		}},
		Usage: &api.Usage{
			PromptTokens:     ollamaResp.PromptEvalCount,
			CompletionTokens: ollamaResp.EvalCount,
			TotalTokens:      totalTokens,
		},
	}, nil
}

// Subscribe registers this adapter as a NATS subscriber on llm.provider.ollama.
func (a *Adapter) Subscribe(nc *nats.Conn) (*nats.Subscription, error) {
	subject := "llm.provider." + a.Name() + ".>"
	sub, err := nc.QueueSubscribe(subject, QueueGroup, func(msg *nats.Msg) {
		a.handleMessage(msg)
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", subject, err)
	}
	a.log.Info("provider adapter listening", "subject", subject, "queue", QueueGroup)
	return sub, nil
}

func (a *Adapter) handleMessage(msg *nats.Msg) {
	var req api.ProviderRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		a.replyError(msg, "invalid_request", "failed to parse provider request: "+err.Error())
		return
	}

	resp, err := a.ChatCompletion(context.Background(), &req)
	if err != nil {
		a.replyError(msg, "provider_error", err.Error())
		return
	}

	data, err := json.Marshal(resp)
	if err != nil {
		a.replyError(msg, "internal_error", "failed to marshal response")
		return
	}
	_ = msg.Respond(data)
}

func (a *Adapter) replyError(msg *nats.Msg, code, message string) {
	a.log.Error("provider error", "code", code, "message", message)
	errResp := api.ErrorResponse{
		Error: api.APIError{
			Message: message,
			Type:    "error",
			Code:    code,
		},
	}
	data, _ := json.Marshal(errResp)
	_ = msg.Respond(data)
}
