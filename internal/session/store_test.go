package session

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/kamalgs/infermesh/api"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestStore_CreateAndGet(t *testing.T) {
	s := NewStore(time.Minute, noopLogger())
	defer s.Close()

	msgs := []api.Message{{Role: "user", Content: "hello"}}
	sess := s.Create("test-model", msgs)

	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("session not found")
	}
	if got.Model != "test-model" {
		t.Errorf("model: got %q", got.Model)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "hello" {
		t.Errorf("messages: got %v", got.Messages)
	}
}

func TestStore_Append(t *testing.T) {
	s := NewStore(time.Minute, noopLogger())
	defer s.Close()

	sess := s.Create("m", []api.Message{{Role: "user", Content: "a"}})
	s.Append(sess.ID, api.Message{Role: "assistant", Content: "b"})
	s.Append(sess.ID, api.Message{Role: "user", Content: "c"})

	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("session not found")
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages count: got %d, want 3", len(got.Messages))
	}
	if got.Messages[2].Content != "c" {
		t.Errorf("last message: got %q", got.Messages[2].Content)
	}
}

func TestStore_Expiry(t *testing.T) {
	s := NewStore(50*time.Millisecond, noopLogger())
	defer s.Close()

	sess := s.Create("m", []api.Message{{Role: "user", Content: "x"}})
	time.Sleep(100 * time.Millisecond)

	_, ok := s.Get(sess.ID)
	if ok {
		t.Error("session should have expired")
	}
}

func TestStore_GetNotFound(t *testing.T) {
	s := NewStore(time.Minute, noopLogger())
	defer s.Close()

	_, ok := s.Get("nonexistent")
	if ok {
		t.Error("should not find nonexistent session")
	}
}

func TestStore_CreateIsolatesMessages(t *testing.T) {
	s := NewStore(time.Minute, noopLogger())
	defer s.Close()

	original := []api.Message{{Role: "user", Content: "hello"}}
	sess := s.Create("m", original)

	// Mutating the original slice should not affect the session
	original[0].Content = "mutated"

	got, _ := s.Get(sess.ID)
	if got.Messages[0].Content != "hello" {
		t.Error("session messages were mutated via original slice")
	}
}
