// Package budget provides per-key spend tracking and budget enforcement.
//
// Usage:
//
//	m := budget.NewManager()
//	m.SetLimit("llmb-abc123", 5.00)  // $5.00 limit
//	err := m.RecordSpend("llmb-abc123", 0.001)
//	if errors.As(err, new(*exceptions.BudgetExceededError)) {
//	    // key has exceeded its budget
//	}
package budget

import (
	"fmt"
	"sync"

	"github.com/Vedanshu7/llmbridge/exceptions"
)

// Manager tracks per-key spend and enforces optional limits.
// All methods are safe for concurrent use.
type Manager struct {
	mu      sync.RWMutex
	limits  map[string]float64 // 0 = no limit
	current map[string]float64
}

// NewManager returns an empty Manager with no limits.
func NewManager() *Manager {
	return &Manager{
		limits:  make(map[string]float64),
		current: make(map[string]float64),
	}
}

// SetLimit sets the maximum USD spend for keyID.
// Pass 0 to remove the limit.
func (m *Manager) SetLimit(keyID string, limit float64) {
	m.mu.Lock()
	m.limits[keyID] = limit
	m.mu.Unlock()
}

// RemoveLimit removes any spend limit for keyID.
func (m *Manager) RemoveLimit(keyID string) {
	m.mu.Lock()
	delete(m.limits, keyID)
	m.mu.Unlock()
}

// RecordSpend adds cost to keyID's current spend.
// Returns a *exceptions.BudgetExceededError if the result exceeds the configured limit.
func (m *Manager) RecordSpend(keyID string, cost float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current[keyID] += cost
	limit, hasLimit := m.limits[keyID]
	if hasLimit && limit > 0 && m.current[keyID] > limit {
		return &exceptions.BudgetExceededError{
			APIError: exceptions.APIError{
				LLMProvider: keyID,
				StatusCode:  429,
				Message:     fmt.Sprintf("budget exceeded for %s", keyID),
			},
			Budget:  limit,
			Current: m.current[keyID],
		}
	}
	return nil
}

// GetSpend returns the total spend recorded for keyID.
func (m *Manager) GetSpend(keyID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current[keyID]
}

// GetLimit returns the spend limit for keyID and whether one is set.
func (m *Manager) GetLimit(keyID string) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.limits[keyID]
	return l, ok && l > 0
}

// Reset clears the current spend for keyID without removing its limit.
func (m *Manager) Reset(keyID string) {
	m.mu.Lock()
	m.current[keyID] = 0
	m.mu.Unlock()
}

// BudgetSummary describes the spend state of a single key.
type BudgetSummary struct {
	Spend float64
	Limit float64 // 0 = no limit
	Over  bool
}

// Summary returns spend state for all tracked keys.
func (m *Manager) Summary() map[string]BudgetSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]BudgetSummary, len(m.current))
	for k, spend := range m.current {
		limit := m.limits[k]
		out[k] = BudgetSummary{
			Spend: spend,
			Limit: limit,
			Over:  limit > 0 && spend > limit,
		}
	}
	return out
}
