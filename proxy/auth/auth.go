// Package auth provides API key authentication for the llmbridge proxy server.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// KeyInfo holds metadata about an API key.
type KeyInfo struct {
	Key       string
	CreatedAt time.Time
	LastUsed  time.Time
	Scopes    []string
}

// APIKeyStore is a thread-safe in-memory store of API keys.
type APIKeyStore struct {
	mu   sync.RWMutex
	keys map[string]*KeyInfo
}

// NewAPIKeyStore returns an empty APIKeyStore.
func NewAPIKeyStore() *APIKeyStore {
	return &APIKeyStore{keys: make(map[string]*KeyInfo)}
}

// GenerateAPIKey creates and stores a new random API key with the given scopes.
func (s *APIKeyStore) GenerateAPIKey(scopes []string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	key := "llmb-" + hex.EncodeToString(b)
	info := &KeyInfo{
		Key:       key,
		CreatedAt: time.Now(),
		Scopes:    scopes,
	}
	s.mu.Lock()
	s.keys[key] = info
	s.mu.Unlock()
	return key, nil
}

// ValidateAPIKey returns true and updates LastUsed if the key exists.
func (s *APIKeyStore) ValidateAPIKey(key string) (*KeyInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.keys[key]
	if !ok {
		return nil, false
	}
	info.LastUsed = time.Now()
	return info, true
}

// DeleteKey removes a key from the store.
func (s *APIKeyStore) DeleteKey(key string) {
	s.mu.Lock()
	delete(s.keys, key)
	s.mu.Unlock()
}

// ListKeys returns all stored KeyInfo values.
func (s *APIKeyStore) ListKeys() []*KeyInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*KeyInfo, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, k)
	}
	return out
}

// HasScope returns true if key has the given scope or the "admin" scope.
func (s *APIKeyStore) HasScope(key, scope string) bool {
	s.mu.RLock()
	info, ok := s.keys[key]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	for _, sc := range info.Scopes {
		if sc == "admin" || sc == scope {
			return true
		}
	}
	return false
}
