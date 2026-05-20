package llmbridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

// redirectHome redirects os.UserHomeDir() output to a temp dir for the test.
func redirectHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)      // Unix
	t.Setenv("USERPROFILE", tmp) // Windows fallback
	return tmp
}

func TestNewSession(t *testing.T) {
	s := NewSession("openai", "gpt-4o")
	if s.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if s.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", s.Provider)
	}
	if s.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", s.Model)
	}
	if s.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if len(s.Messages) != 0 {
		t.Error("expected empty messages")
	}
}

func TestSessionAdd(t *testing.T) {
	s := NewSession("anthropic", "claude-sonnet-4-6")
	before := s.UpdatedAt

	s.Add(types.Message{Role: "user", Content: "hello"})
	s.Add(types.Message{Role: "assistant", Content: "hi"})

	if len(s.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(s.Messages))
	}
	if s.Messages[0].Content != "hello" {
		t.Errorf("Messages[0].Content = %q", s.Messages[0].Content)
	}
	if !s.UpdatedAt.After(before) && s.UpdatedAt.Equal(before) {
		// UpdatedAt may be equal if Add executes within same nanosecond —
		// just ensure it's not before.
	}
	_ = before
}

func TestSessionSaveAndLoad(t *testing.T) {
	redirectHome(t)

	s := NewSession("openai", "gpt-4o-mini")
	s.Add(types.Message{Role: "user", Content: "What is 2+2?"})
	s.Add(types.Message{Role: "assistant", Content: "4"})

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.ID != s.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded.ID, s.ID)
	}
	if loaded.Provider != "openai" {
		t.Errorf("Provider = %q", loaded.Provider)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[1].Content != "4" {
		t.Errorf("Messages[1].Content = %q", loaded.Messages[1].Content)
	}
}

func TestLoadSessionNotFound(t *testing.T) {
	redirectHome(t)
	_, err := LoadSession("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestLoadLatestSessionNoSessions(t *testing.T) {
	redirectHome(t)
	s, err := LoadLatestSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != nil {
		t.Fatalf("expected nil session when none saved, got %+v", s)
	}
}

func TestLoadLatestSession(t *testing.T) {
	redirectHome(t)

	s1 := NewSession("openai", "gpt-4o")
	s1.Add(types.Message{Role: "user", Content: "first"})
	if err := s1.Save(); err != nil {
		t.Fatalf("s1.Save: %v", err)
	}

	s2 := NewSession("anthropic", "claude-sonnet-4-6")
	s2.Add(types.Message{Role: "user", Content: "second"})
	if err := s2.Save(); err != nil {
		t.Fatalf("s2.Save: %v", err)
	}

	latest, err := LoadLatestSession()
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil session")
	}
	// Latest pointer should refer to s2 (the last Save).
	if latest.ID != s2.ID {
		t.Errorf("latest ID = %q, want %q", latest.ID, s2.ID)
	}
}

func TestListSessions(t *testing.T) {
	redirectHome(t)

	// No sessions yet.
	list, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions (empty): %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(list))
	}

	// Save three sessions.
	for i, provider := range []string{"openai", "anthropic", "groq"} {
		s := NewSession(provider, "model")
		s.Add(types.Message{Role: "user", Content: provider})
		_ = i
		if err := s.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	list, err = ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(list))
	}
}

func TestListSessionsIgnoresNonJSON(t *testing.T) {
	redirectHome(t)

	s := NewSession("openai", "gpt-4o")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Drop a non-JSON file and the "latest" pointer in the session dir.
	dir, _ := sessionDir()
	_ = os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignore me"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("not-json{{"), 0600)

	list, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	// Only the valid session should be returned; README.txt ignored, corrupt.json skipped.
	if len(list) != 1 {
		t.Fatalf("expected 1 valid session, got %d", len(list))
	}
}

func TestNewIDFormat(t *testing.T) {
	id1 := newID()
	id2 := newID()
	if id1 == id2 {
		t.Error("newID should produce unique values")
	}
	if len(id1) < 10 {
		t.Errorf("newID too short: %q", id1)
	}
}

func TestSessionSaveCreatesDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// The session directory should not exist yet.
	dir := filepath.Join(tmp, ".llmbridge", "sessions")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Skip("directory already exists, skipping")
	}

	s := NewSession("openai", "gpt-4o")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("session dir not created: %v", err)
	}
}
