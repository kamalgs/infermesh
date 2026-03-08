package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/kamalgs/infermesh/api"
	"github.com/nats-io/nats.go"
)

const RequestTimeout = 30 * time.Second

// Proxy translates OpenAI-compatible HTTP requests to NATS messages.
type Proxy struct {
	nc     *nats.Conn
	server *http.Server
	log    *slog.Logger
}

func New(nc *nats.Conn, addr string, log *slog.Logger) *Proxy {
	p := &Proxy{nc: nc, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", p.handleChatCompletion)
	mux.HandleFunc("GET /v1/models", p.handleListModels)
	mux.HandleFunc("GET /health", p.handleHealth)

	p.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	return p
}

func (p *Proxy) Start() error {
	p.log.Info("http proxy listening", "addr", p.server.Addr)
	return p.server.ListenAndServe()
}

func (p *Proxy) Stop(ctx context.Context) error {
	return p.server.Shutdown(ctx)
}

func (p *Proxy) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "invalid_request", "failed to read request body")
		return
	}

	// Validate it's a valid ChatRequest
	var req api.ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		p.writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON: "+err.Error())
		return
	}

	p.log.Info("proxying request", "model", req.Model)

	// Forward to gateway via NATS request/reply
	msg, err := p.nc.Request("llm.chat.complete", body, RequestTimeout)
	if err != nil {
		p.log.Error("nats request failed", "error", err)
		p.writeError(w, http.StatusBadGateway, "gateway_error", "gateway request failed: "+err.Error())
		return
	}

	// Check if the response is an error
	var errResp api.ErrorResponse
	if err := json.Unmarshal(msg.Data, &errResp); err == nil && errResp.Error.Code != "" {
		status := http.StatusInternalServerError
		switch errResp.Error.Code {
		case "model_not_found":
			status = http.StatusNotFound
		case "rate_limit_exceeded":
			status = http.StatusTooManyRequests
		case "invalid_request":
			status = http.StatusBadRequest
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(msg.Data)
		return
	}

	// Pass through the response
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(msg.Data)
}

func (p *Proxy) handleListModels(w http.ResponseWriter, r *http.Request) {
	msg, err := p.nc.Request("llm.models", nil, 5*time.Second)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "gateway_error", "models request failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(msg.Data)
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (p *Proxy) writeError(w http.ResponseWriter, status int, code, message string) {
	p.log.Error("proxy error", "status", status, "code", code, "message", message)
	errResp := api.ErrorResponse{
		Error: api.APIError{
			Message: message,
			Type:    "error",
			Code:    code,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errResp)
}
