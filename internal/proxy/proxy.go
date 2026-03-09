package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kamalgs/infermesh/api"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nuid"
)

const IdleTimeout = 5 * time.Minute

// Proxy translates OpenAI-compatible HTTP requests to NATS messages.
// Model names use the convention "provider.model" (e.g., "openai.gpt-4o").
// The proxy splits the name and publishes directly to llm.provider.<provider>.
type Proxy struct {
	nc     *nats.Conn
	server *http.Server
	log    *slog.Logger
	router *replyRouter
}

func New(nc *nats.Conn, addr string, log *slog.Logger) *Proxy {
	p := &Proxy{
		nc:     nc,
		log:    log,
		router: newReplyRouter(nc, IdleTimeout, log),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", p.handleChatCompletion)
	mux.HandleFunc("GET /health", p.handleHealth)

	p.server = &http.Server{
		Addr:        addr,
		Handler:     mux,
		ReadTimeout: 10 * time.Second,
	}
	return p
}

func (p *Proxy) Start() error {
	p.log.Info("http proxy listening", "addr", p.server.Addr)
	return p.server.ListenAndServe()
}

func (p *Proxy) Stop(ctx context.Context) error {
	p.router.close()
	return p.server.Shutdown(ctx)
}

// parseModel splits "provider.model" into provider and upstream model name.
// Returns an error if the model name doesn't contain a dot.
func parseModel(model string) (provider, upstream string, err error) {
	i := strings.IndexByte(model, '.')
	if i <= 0 || i == len(model)-1 {
		return "", "", fmt.Errorf("model %q must be in the form provider.model (e.g., openai.gpt-4o)", model)
	}
	return model[:i], model[i+1:], nil
}

func (p *Proxy) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "invalid_request", "failed to read request body")
		return
	}

	var req api.ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		p.writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON: "+err.Error())
		return
	}

	provider, upstream, err := parseModel(req.Model)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Build provider request with the upstream model name
	provReq := api.ProviderRequest{
		UpstreamModel: upstream,
		Request:       req,
	}
	data, err := json.Marshal(provReq)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "internal_error", "failed to marshal request")
		return
	}

	subject := "llm.provider." + provider + "." + upstream
	p.log.Info("proxying request", "provider", provider, "upstream_model", upstream, "subject", subject)

	respData, err := p.router.request(r.Context(), subject, data)
	if err != nil {
		p.log.Error("nats request failed", "error", err)
		if r.Context().Err() != nil {
			return // client disconnected
		}
		p.writeError(w, http.StatusBadGateway, "provider_error", "provider request failed: "+err.Error())
		return
	}

	// Check if the response is an error
	var errResp api.ErrorResponse
	if err := json.Unmarshal(respData, &errResp); err == nil && errResp.Error.Code != "" {
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
		_, _ = w.Write(respData)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respData)
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

// replyRouter manages a long-lived NATS subscription for receiving provider
// responses. It correlates responses to pending requests via sub-subjects
// and cleans up the subscription after a period of inactivity.
type replyRouter struct {
	nc  *nats.Conn
	log *slog.Logger

	mu      sync.Mutex
	prefix  string
	sub     *nats.Subscription
	pending map[string]chan []byte
	idle    *time.Timer
	idleDur time.Duration
}

func newReplyRouter(nc *nats.Conn, idleTimeout time.Duration, log *slog.Logger) *replyRouter {
	return &replyRouter{
		nc:      nc,
		log:     log,
		prefix:  "llm.reply." + nuid.Next(),
		pending: make(map[string]chan []byte),
		idleDur: idleTimeout,
	}
}

// ensureSubscribed creates the wildcard subscription if it doesn't exist.
// Must be called with mu held.
func (r *replyRouter) ensureSubscribed() error {
	if r.sub != nil && r.sub.IsValid() {
		return nil
	}
	sub, err := r.nc.Subscribe(r.prefix+".>", r.dispatch)
	if err != nil {
		return fmt.Errorf("subscribe reply: %w", err)
	}
	r.sub = sub
	r.log.Info("reply subscription started", "subject", r.prefix+".>")
	return nil
}

// dispatch is the NATS callback that routes incoming replies to waiting requests.
func (r *replyRouter) dispatch(msg *nats.Msg) {
	reqID := strings.TrimPrefix(msg.Subject, r.prefix+".")

	r.mu.Lock()
	ch, ok := r.pending[reqID]
	if ok {
		delete(r.pending, reqID)
	}
	r.mu.Unlock()

	if ok {
		ch <- msg.Data
	}
}

// request publishes a message and waits for the reply. It blocks until a
// response arrives or the context is canceled (e.g. HTTP client disconnects).
func (r *replyRouter) request(ctx context.Context, subject string, data []byte) ([]byte, error) {
	reqID := nuid.Next()
	replySubject := r.prefix + "." + reqID
	ch := make(chan []byte, 1)

	r.mu.Lock()
	if err := r.ensureSubscribed(); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.pending[reqID] = ch
	r.resetIdle()
	r.mu.Unlock()

	msg := &nats.Msg{
		Subject: subject,
		Reply:   replySubject,
		Data:    data,
	}
	if err := r.nc.PublishMsg(msg); err != nil {
		r.mu.Lock()
		delete(r.pending, reqID)
		r.mu.Unlock()
		return nil, fmt.Errorf("publish: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		r.mu.Lock()
		delete(r.pending, reqID)
		r.mu.Unlock()
		return nil, ctx.Err()
	}
}

// resetIdle restarts the inactivity timer. Must be called with mu held.
func (r *replyRouter) resetIdle() {
	if r.idle != nil {
		r.idle.Stop()
	}
	r.idle = time.AfterFunc(r.idleDur, r.cleanup)
}

// cleanup tears down the subscription after inactivity.
func (r *replyRouter) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sub != nil {
		r.sub.Unsubscribe()
		r.sub = nil
		r.log.Info("reply subscription cleaned up due to inactivity")
	}
}

// close tears down the router, canceling all pending requests.
func (r *replyRouter) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idle != nil {
		r.idle.Stop()
	}
	if r.sub != nil {
		r.sub.Unsubscribe()
		r.sub = nil
	}
	for id, ch := range r.pending {
		close(ch)
		delete(r.pending, id)
	}
}
