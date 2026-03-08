package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/kamalgs/nats-llm-gateway/api"
	"github.com/kamalgs/nats-llm-gateway/internal/config"
	"github.com/nats-io/nats.go"
)

const (
	SubjectChatComplete = "llm.chat.complete"
	QueueGroup          = "gateway"
	ProviderTimeout     = 30 * time.Second
)

type Gateway struct {
	nc     *nats.Conn
	config *config.Config
	sub    *nats.Subscription
	log    *slog.Logger
}

func New(nc *nats.Conn, cfg *config.Config, log *slog.Logger) *Gateway {
	return &Gateway{nc: nc, config: cfg, log: log}
}

func (g *Gateway) Start() error {
	sub, err := g.nc.QueueSubscribe(SubjectChatComplete, QueueGroup, g.handleRequest)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", SubjectChatComplete, err)
	}
	g.sub = sub
	g.log.Info("gateway listening", "subject", SubjectChatComplete, "queue", QueueGroup)
	return nil
}

func (g *Gateway) Stop() {
	if g.sub != nil {
		_ = g.sub.Drain()
	}
}

func (g *Gateway) handleRequest(msg *nats.Msg) {
	var req api.ChatRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		g.replyError(msg, "invalid_request", "failed to parse request: "+err.Error())
		return
	}

	g.log.Info("request received", "model", req.Model)

	// Resolve model → provider
	modelCfg, ok := g.config.Models[req.Model]
	if !ok {
		g.replyError(msg, "model_not_found", fmt.Sprintf("model %q is not configured", req.Model))
		return
	}

	// Build provider request
	provReq := api.ProviderRequest{
		UpstreamModel: modelCfg.UpstreamModel,
		Request:       req,
	}
	data, err := json.Marshal(provReq)
	if err != nil {
		g.replyError(msg, "internal_error", "failed to marshal provider request")
		return
	}

	// Forward to provider via NATS request/reply
	provSubject := "llm.provider." + modelCfg.Provider
	g.log.Info("routing to provider", "subject", provSubject, "upstream_model", modelCfg.UpstreamModel)

	resp, err := g.nc.Request(provSubject, data, ProviderTimeout)
	if err != nil {
		g.replyError(msg, "provider_error", fmt.Sprintf("provider request failed: %v", err))
		return
	}

	// Pass through the provider's response as-is
	_ = msg.Respond(resp.Data)
}

func (g *Gateway) replyError(msg *nats.Msg, code, message string) {
	g.log.Error("request error", "code", code, "message", message)
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
