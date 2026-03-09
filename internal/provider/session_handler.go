package provider

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/kamalgs/infermesh/api"
	"github.com/kamalgs/infermesh/internal/session"
	"github.com/nats-io/nats.go"
)

const SessionTTL = 30 * time.Minute

// SessionHandler wraps a Provider with session management.
// It handles NATS message dispatch, session creation, and session-based messaging.
type SessionHandler struct {
	adapter  Provider
	sessions *session.Store
	nc       *nats.Conn
	log      *slog.Logger
}

func NewSessionHandler(adapter Provider, nc *nats.Conn, log *slog.Logger) *SessionHandler {
	return &SessionHandler{
		adapter:  adapter,
		sessions: session.NewStore(SessionTTL, log),
		nc:       nc,
		log:      log,
	}
}

// Subscribe registers the provider on its standard subject with session support.
func (sh *SessionHandler) Subscribe(queueGroup string) (*nats.Subscription, error) {
	subject := "llm.provider." + sh.adapter.Name() + ".>"
	sub, err := sh.nc.QueueSubscribe(subject, queueGroup, func(msg *nats.Msg) {
		sh.handleProviderMessage(msg)
	})
	if err != nil {
		return nil, err
	}
	sh.log.Info("provider adapter listening", "subject", subject, "queue", queueGroup)
	return sub, nil
}

func (sh *SessionHandler) Close() {
	sh.sessions.Close()
}

// handleProviderMessage handles requests on the standard provider subject.
// Creates a new session and returns session info in the response.
func (sh *SessionHandler) handleProviderMessage(msg *nats.Msg) {
	var req api.ProviderRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		sh.replyError(msg, "invalid_request", "failed to parse provider request: "+err.Error())
		return
	}

	resp, err := sh.adapter.ChatCompletion(context.Background(), &req)
	if err != nil {
		sh.replyError(msg, "provider_error", err.Error())
		return
	}

	// Build session context: original messages + assistant reply
	allMessages := make([]api.Message, len(req.Request.Messages))
	copy(allMessages, req.Request.Messages)
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		allMessages = append(allMessages, *resp.Choices[0].Message)
	}

	sess := sh.sessions.Create(req.UpstreamModel, allMessages)

	// Subscribe to session subject for subsequent messages
	sessionSubject := "llm.session." + sess.ID
	sessSub, err := sh.nc.Subscribe(sessionSubject, func(m *nats.Msg) {
		sh.handleSessionMessage(sess.ID, m)
	})
	if err != nil {
		sh.log.Error("failed to subscribe session subject", "error", err)
	} else {
		sess.Sub = sessSub
		resp.SessionID = sess.ID
		resp.SessionSubject = sessionSubject
	}

	data, _ := json.Marshal(resp)
	_ = msg.Respond(data)
}

// handleSessionMessage handles requests on a session-specific subject.
// Appends new message(s) to the session context and sends full context to the model.
func (sh *SessionHandler) handleSessionMessage(sessionID string, msg *nats.Msg) {
	sess, ok := sh.sessions.Get(sessionID)
	if !ok {
		sh.replyError(msg, "session_expired", "session not found or expired")
		return
	}

	var req api.ProviderRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		sh.replyError(msg, "invalid_request", "failed to parse request: "+err.Error())
		return
	}

	if req.Request.SessionID != sessionID {
		sh.replyError(msg, "invalid_request", "session_id mismatch")
		return
	}

	// Append new messages from client to session
	sh.sessions.Append(sessionID, req.Request.Messages...)

	// Refresh session reference after append
	sess, _ = sh.sessions.Get(sessionID)

	// Build full-context request for upstream
	fullReq := &api.ProviderRequest{
		UpstreamModel: req.UpstreamModel,
		Request: api.ChatRequest{
			Model:       req.Request.Model,
			Messages:    sess.Messages,
			Temperature: req.Request.Temperature,
			MaxTokens:   req.Request.MaxTokens,
		},
	}

	resp, err := sh.adapter.ChatCompletion(context.Background(), fullReq)
	if err != nil {
		sh.replyError(msg, "provider_error", err.Error())
		return
	}

	// Append assistant reply to session
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		sh.sessions.Append(sessionID, *resp.Choices[0].Message)
	}

	resp.SessionID = sessionID
	resp.SessionSubject = "llm.session." + sessionID

	data, _ := json.Marshal(resp)
	_ = msg.Respond(data)
}

func (sh *SessionHandler) replyError(msg *nats.Msg, code, message string) {
	sh.log.Error("provider error", "code", code, "message", message)
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
