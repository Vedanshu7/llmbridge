// Package auth provides API key authentication for the llmbridge proxy server.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// KeyInfo holds metadata about an API key.
type KeyInfo struct {
	Key          string
	CreatedAt    time.Time
	LastUsed     time.Time
	ExpiresAt    time.Time // zero = never expires
	Scopes       []string
	SpendLimit   float64  // 0 = unlimited
	CurrentSpend float64  // accumulated spend in USD
	AllowedCIDRs []string // empty = allow all IPs; otherwise restrict to these CIDR ranges
	OrgID        string   // empty = no org association
	TeamID       string   // empty = no team association
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

// ValidateAPIKey returns the KeyInfo and true if the key exists and has not expired.
// Expired keys are deleted on access.
func (s *APIKeyStore) ValidateAPIKey(key string) (*KeyInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.keys[key]
	if !ok {
		return nil, false
	}
	if !info.ExpiresAt.IsZero() && time.Now().After(info.ExpiresAt) {
		delete(s.keys, key)
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

// ImportKey stores an existing API key (e.g. loaded from a config file).
func (s *APIKeyStore) ImportKey(key string, scopes []string) {
	s.mu.Lock()
	s.keys[key] = &KeyInfo{
		Key:       key,
		CreatedAt: time.Now(),
		Scopes:    scopes,
	}
	s.mu.Unlock()
}

// SetExpiry sets a TTL on a key. After ttl elapses the key is invalidated on next use.
func (s *APIKeyStore) SetExpiry(key string, ttl time.Duration) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.ExpiresAt = time.Now().Add(ttl)
	}
	s.mu.Unlock()
}

// SetAllowedIPs restricts key to requests originating from the given CIDR ranges.
// Pass an empty slice to remove IP restrictions.
// Example: SetAllowedIPs("llmb-abc", []string{"10.0.0.0/8", "192.168.1.0/24"})
func (s *APIKeyStore) SetAllowedIPs(key string, cidrs []string) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.AllowedCIDRs = cidrs
	}
	s.mu.Unlock()
}

// SetSpendLimit sets the maximum USD spend allowed for key.
func (s *APIKeyStore) SetSpendLimit(key string, limit float64) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.SpendLimit = limit
	}
	s.mu.Unlock()
}

// RecordSpend adds cost to the key's current spend.
// Returns an error string if the key's spend limit is exceeded.
func (s *APIKeyStore) RecordSpend(key string, cost float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.keys[key]
	if !ok {
		return nil
	}
	info.CurrentSpend += cost
	if info.SpendLimit > 0 && info.CurrentSpend > info.SpendLimit {
		return fmt.Errorf("key %s exceeded spend limit: $%.6f of $%.6f",
			key, info.CurrentSpend, info.SpendLimit)
	}
	return nil
}

// SetKeyOrg associates a key with an org.
func (s *APIKeyStore) SetKeyOrg(key, orgID string) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.OrgID = orgID
	}
	s.mu.Unlock()
}

// SetKeyTeam associates a key with a team.
func (s *APIKeyStore) SetKeyTeam(key, teamID string) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.TeamID = teamID
	}
	s.mu.Unlock()
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
