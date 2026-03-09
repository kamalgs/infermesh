package session

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kamalgs/infermesh/api"
	"github.com/nats-io/nats.go"
)

// Session holds the accumulated conversation context for a sticky session.
type Session struct {
	ID       string
	Model    string
	Messages []api.Message
	LastUsed time.Time
	Sub      *nats.Subscription
}

// Store manages sessions with TTL-based expiry.
type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
	log      *slog.Logger
	stopOnce sync.Once
	stop     chan struct{}
}

func NewStore(ttl time.Duration, log *slog.Logger) *Store {
	s := &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
		log:      log,
		stop:     make(chan struct{}),
	}
	go s.cleanup()
	return s
}

func (s *Store) Create(model string, messages []api.Message) *Session {
	id := newID()
	sess := &Session{
		ID:       id,
		Model:    model,
		Messages: make([]api.Message, len(messages)),
		LastUsed: time.Now(),
	}
	copy(sess.Messages, messages)

	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	s.log.Info("session created", "session_id", id, "messages", len(messages))
	return sess
}

func (s *Store) Get(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	if time.Since(sess.LastUsed) > s.ttl {
		s.removeLocked(id)
		return nil, false
	}
	sess.LastUsed = time.Now()
	return sess, true
}

// Append adds messages to the session and updates LastUsed.
func (s *Store) Append(id string, msgs ...api.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return
	}
	sess.Messages = append(sess.Messages, msgs...)
	sess.LastUsed = time.Now()
}

func (s *Store) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *Store) cleanup() {
	ticker := time.NewTicker(s.ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for id, sess := range s.sessions {
				if now.Sub(sess.LastUsed) > s.ttl {
					s.removeLocked(id)
				}
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

func (s *Store) removeLocked(id string) {
	sess := s.sessions[id]
	if sess == nil {
		return
	}
	if sess.Sub != nil {
		_ = sess.Sub.Drain()
	}
	delete(s.sessions, id)
	s.log.Info("session removed", "session_id", id)
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}
