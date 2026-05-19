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

// AlertFunc is called when a key's spend crosses an alert threshold.
// spend is the current total; limit is the configured budget ceiling.
type AlertFunc func(keyID string, spend, limit float64)

// Manager tracks per-key spend and enforces optional limits.
// All methods are safe for concurrent use.
type Manager struct {
	mu         sync.RWMutex
	limits     map[string]float64 // 0 = no limit
	current    map[string]float64
	thresholds map[string]float64 // fraction of limit at which to fire alert (0.0–1.0)
	alerted    map[string]bool    // true once alert has fired for this key in this window
	onAlert    AlertFunc          // nil = no alerts
}

// NewManager returns an empty Manager with no limits.
func NewManager() *Manager {
	return &Manager{
		limits:     make(map[string]float64),
		current:    make(map[string]float64),
		thresholds: make(map[string]float64),
		alerted:    make(map[string]bool),
	}
}

// OnAlert registers a function to call when spend crosses an alert threshold.
// Only one callback is supported; calling OnAlert again replaces the previous one.
func (m *Manager) OnAlert(fn AlertFunc) {
	m.mu.Lock()
	m.onAlert = fn
	m.mu.Unlock()
}

// SetAlertThreshold configures an alert for keyID that fires the first time
// spend reaches fraction of the limit (e.g. 0.8 = 80%). Requires a limit to
// be set for the key. Calling Reset clears the fired state so the alert can
// fire again.
func (m *Manager) SetAlertThreshold(keyID string, fraction float64) {
	m.mu.Lock()
	m.thresholds[keyID] = fraction
	m.mu.Unlock()
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
// Fires the alert callback (if set) the first time spend crosses the alert threshold.
func (m *Manager) RecordSpend(keyID string, cost float64) error {
	m.mu.Lock()
	m.current[keyID] += cost
	spend := m.current[keyID]
	limit, hasLimit := m.limits[keyID]

	// Check alert threshold (fire once per reset cycle).
	var alertFn AlertFunc
	var alertSpend, alertLimit float64
	if hasLimit && limit > 0 && m.onAlert != nil && !m.alerted[keyID] {
		if frac, ok := m.thresholds[keyID]; ok && frac > 0 && spend >= frac*limit {
			m.alerted[keyID] = true
			alertFn = m.onAlert
			alertSpend, alertLimit = spend, limit
		}
	}

	var budgetErr error
	if hasLimit && limit > 0 && spend > limit {
		budgetErr = &exceptions.BudgetExceededError{
			APIError: exceptions.APIError{
				LLMProvider: keyID,
				StatusCode:  429,
				Message:     fmt.Sprintf("budget exceeded for %s", keyID),
			},
			Budget:  limit,
			Current: spend,
		}
	}
	m.mu.Unlock()

	// Fire alert outside the lock to avoid deadlock if the callback calls back into Manager.
	if alertFn != nil {
		alertFn(keyID, alertSpend, alertLimit)
	}
	return budgetErr
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

// Reset clears the current spend and alert-fired state for keyID without removing its limit.
func (m *Manager) Reset(keyID string) {
	m.mu.Lock()
	m.current[keyID] = 0
	delete(m.alerted, keyID)
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
