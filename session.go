package llmbridge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Session stores a conversation history that can be saved to disk and
// resumed in future processes, just like claude --continue.
type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
}

// NewSession creates an empty session for the given provider and model.
func NewSession(providerName, model string) *Session {
	return &Session{
		ID:        newID(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Provider:  providerName,
		Model:     model,
	}
}

// Add appends a message to the session and updates UpdatedAt.
func (s *Session) Add(msg Message) {
	s.Messages = append(s.Messages, msg)
	s.UpdatedAt = time.Now()
}

// Save writes the session to disk and updates the "latest" pointer.
func (s *Session) Save() error {
	dir, err := sessionDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("session dir: %w", err)
	}

	path := filepath.Join(dir, s.ID+".json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("session marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("session write: %w", err)
	}

	// Overwrite the "latest" pointer file with the session ID.
	latestPath := filepath.Join(dir, "latest")
	return os.WriteFile(latestPath, []byte(s.ID), 0600)
}

// LoadSession loads a session by its ID from disk.
func LoadSession(id string) (*Session, error) {
	dir, err := sessionDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", id, err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("session %s: parse: %w", id, err)
	}
	return &s, nil
}

// LoadLatestSession loads the most recently saved session.
// Returns (nil, nil) if no sessions have been saved yet.
func LoadLatestSession() (*Session, error) {
	dir, err := sessionDir()
	if err != nil {
		return nil, err
	}
	latestPath := filepath.Join(dir, "latest")
	idBytes, err := os.ReadFile(latestPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest pointer: %w", err)
	}
	return LoadSession(string(idBytes))
}

// ListSessions returns all saved sessions sorted by creation time (newest first).
func ListSessions() ([]*Session, error) {
	dir, err := sessionDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sessions []*Session
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-5]
		s, err := LoadSession(id)
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}

	// Sort newest first.
	for i := 1; i < len(sessions); i++ {
		for j := i; j > 0 && sessions[j].CreatedAt.After(sessions[j-1].CreatedAt); j-- {
			sessions[j], sessions[j-1] = sessions[j-1], sessions[j]
		}
	}
	return sessions, nil
}

// sessionDir returns the platform-appropriate directory for session files.
//
//	macOS/Linux: ~/.llmbridge/sessions
//	Windows:     %APPDATA%\llmbridge\sessions
func sessionDir() (string, error) {
	var base string
	if runtime.GOOS == "windows" {
		base = os.Getenv("APPDATA")
		if base == "" {
			return "", fmt.Errorf("%%APPDATA%% not set")
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		base = home
	}
	return filepath.Join(base, ".llmbridge", "sessions"), nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
