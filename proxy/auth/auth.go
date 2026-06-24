// Package auth provides API key authentication for the llmbridge proxy server.
package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// KeyInfo holds metadata about an API key.
type KeyInfo struct {
	Key          string    `json:"key"`
	Name         string    `json:"name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsed     time.Time `json:"last_used"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scopes       []string  `json:"scopes"`
	SpendLimit   float64   `json:"spend_limit"`
	CurrentSpend float64   `json:"current_spend"`
	AllowedCIDRs []string  `json:"allowed_cidrs,omitempty"`
	OrgID        string    `json:"org_id,omitempty"`
	TeamID       string    `json:"team_id,omitempty"`
	// ModelAliases maps model names to backend model names for this specific key.
	// Resolution order: per-key alias → global alias → identity.
	ModelAliases map[string]string `json:"model_aliases,omitempty"`
	// ResetPeriod controls when current_spend is automatically reset to zero.
	// Valid values: "daily", "weekly", "monthly". Empty = no automatic reset.
	ResetPeriod string    `json:"reset_period,omitempty"`
	LastReset   time.Time `json:"last_reset,omitempty"`
}

// IsPeriodElapsed returns true if the given reset period has elapsed since lastReset.
// An empty period always returns false.
func IsPeriodElapsed(period string, lastReset time.Time) bool {
	if period == "" || lastReset.IsZero() {
		return false
	}
	now := time.Now()
	switch period {
	case "daily":
		return now.Sub(lastReset) >= 24*time.Hour
	case "weekly":
		return now.Sub(lastReset) >= 7*24*time.Hour
	case "monthly":
		// Consider a month as elapsed when we've crossed into a different calendar month.
		return now.Year() > lastReset.Year() || now.Month() > lastReset.Month()
	}
	return false
}

// ResolveModel returns the canonical model name for model using the key's own
// alias table, then the global alias table, and finally the name itself.
func (k *KeyInfo) ResolveModel(model string, globalAliases map[string]string) string {
	if k != nil {
		if resolved, ok := k.ModelAliases[model]; ok {
			return resolved
		}
	}
	if resolved, ok := globalAliases[model]; ok {
		return resolved
	}
	return model
}

// APIKeyStore is a thread-safe store of API keys backed by an optional SQLite database.
// When db is nil the store operates entirely in memory (suitable for tests or embedded use).
type APIKeyStore struct {
	mu   sync.RWMutex
	keys map[string]*KeyInfo
	db   *sql.DB // nil = in-memory only
}

// NewAPIKeyStore returns an in-memory-only APIKeyStore.
func NewAPIKeyStore() *APIKeyStore {
	return &APIKeyStore{keys: make(map[string]*KeyInfo)}
}

// NewAPIKeyStoreWithDB returns an APIKeyStore backed by db.
// All existing rows in the api_keys table are loaded into memory on construction.
func NewAPIKeyStoreWithDB(db *sql.DB) (*APIKeyStore, error) {
	s := &APIKeyStore{keys: make(map[string]*KeyInfo), db: db}
	if err := s.loadFromDB(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *APIKeyStore) loadFromDB() error {
	// Migrate runs before loadFromDB so all columns are guaranteed to exist.
	rows, err := s.db.Query(
		`SELECT key, name, created_at, last_used, expires_at, scopes, spend_limit, current_spend,
		        allowed_cidrs, org_id, team_id, model_aliases, reset_period, last_reset
		 FROM api_keys`,
	)
	if err != nil {
		return fmt.Errorf("auth: load keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			info                           KeyInfo
			createdAt, lastUsed, expiresAt int64
			lastReset                      int64
			scopesJSON, cidrsJSON          string
			aliasesJSON                    string
		)
		if err := rows.Scan(
			&info.Key, &info.Name, &createdAt, &lastUsed, &expiresAt,
			&scopesJSON, &info.SpendLimit, &info.CurrentSpend,
			&cidrsJSON, &info.OrgID, &info.TeamID, &aliasesJSON,
			&info.ResetPeriod, &lastReset,
		); err != nil {
			return fmt.Errorf("auth: scan key: %w", err)
		}
		info.CreatedAt = time.Unix(createdAt, 0)
		info.LastUsed = time.Unix(lastUsed, 0)
		if expiresAt > 0 {
			info.ExpiresAt = time.Unix(expiresAt, 0)
		}
		if lastReset > 0 {
			info.LastReset = time.Unix(lastReset, 0)
		}
		_ = json.Unmarshal([]byte(scopesJSON), &info.Scopes)
		_ = json.Unmarshal([]byte(cidrsJSON), &info.AllowedCIDRs)
		_ = json.Unmarshal([]byte(aliasesJSON), &info.ModelAliases)
		s.keys[info.Key] = &info
	}
	return rows.Err()
}

func (s *APIKeyStore) persistKey(info *KeyInfo) {
	if s.db == nil {
		return
	}
	scopesJSON, _ := json.Marshal(info.Scopes)
	cidrsJSON, _ := json.Marshal(info.AllowedCIDRs)
	aliasesJSON, _ := json.Marshal(info.ModelAliases)
	expiresAt := int64(0)
	if !info.ExpiresAt.IsZero() {
		expiresAt = info.ExpiresAt.Unix()
	}
	lastReset := int64(0)
	if !info.LastReset.IsZero() {
		lastReset = info.LastReset.Unix()
	}
	_, _ = s.db.Exec(
		`INSERT OR REPLACE INTO api_keys
		 (key, name, created_at, last_used, expires_at, scopes, spend_limit, current_spend,
		  allowed_cidrs, org_id, team_id, model_aliases, reset_period, last_reset)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		info.Key, info.Name, info.CreatedAt.Unix(), info.LastUsed.Unix(), expiresAt,
		string(scopesJSON), info.SpendLimit, info.CurrentSpend,
		string(cidrsJSON), info.OrgID, info.TeamID, string(aliasesJSON),
		info.ResetPeriod, lastReset,
	)
}

func (s *APIKeyStore) deleteFromDB(key string) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(`DELETE FROM api_keys WHERE key = ?`, key)
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
	s.persistKey(info)
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
	s.deleteFromDB(key)
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

// SetKeyName sets a human-readable label on an existing key.
func (s *APIKeyStore) SetKeyName(key, name string) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.Name = name
		s.persistKey(info)
	}
	s.mu.Unlock()
}

// ImportKey stores an existing API key (e.g. loaded from a config file).
func (s *APIKeyStore) ImportKey(key string, scopes []string) {
	info := &KeyInfo{
		Key:       key,
		CreatedAt: time.Now(),
		Scopes:    scopes,
	}
	s.mu.Lock()
	s.keys[key] = info
	s.persistKey(info)
	s.mu.Unlock()
}

// SetExpiry sets a TTL on a key. After ttl elapses the key is invalidated on next use.
func (s *APIKeyStore) SetExpiry(key string, ttl time.Duration) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.ExpiresAt = time.Now().Add(ttl)
		s.persistKey(info)
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
		s.persistKey(info)
	}
	s.mu.Unlock()
}

// SetResetPeriod configures the automatic spend-reset period for a key.
// Valid values: "daily", "weekly", "monthly". Pass "" to disable.
func (s *APIKeyStore) SetResetPeriod(key, period string) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.ResetPeriod = period
		if info.LastReset.IsZero() {
			info.LastReset = time.Now()
		}
		s.persistKey(info)
	}
	s.mu.Unlock()
}

// ZeroKeySpend resets current_spend to 0 and updates LastReset for key.
// Called by the background reset ticker when the reset period has elapsed.
func (s *APIKeyStore) ZeroKeySpend(key string) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.CurrentSpend = 0
		info.LastReset = time.Now()
		s.persistKey(info)
	}
	s.mu.Unlock()
}

// SetModelAliases sets per-key model alias overrides.
// Aliases are resolved before global aliases: if a key has "gpt-4" → "my-deploy",
// requests from this key for "gpt-4" will be routed to "my-deploy".
func (s *APIKeyStore) SetModelAliases(key string, aliases map[string]string) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.ModelAliases = aliases
		s.persistKey(info)
	}
	s.mu.Unlock()
}

// SetSpendLimit sets the maximum USD spend allowed for key.
func (s *APIKeyStore) SetSpendLimit(key string, limit float64) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.SpendLimit = limit
		s.persistKey(info)
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
	s.persistKey(info)
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
		s.persistKey(info)
	}
	s.mu.Unlock()
}

// SetKeyTeam associates a key with a team.
func (s *APIKeyStore) SetKeyTeam(key, teamID string) {
	s.mu.Lock()
	if info, ok := s.keys[key]; ok {
		info.TeamID = teamID
		s.persistKey(info)
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
