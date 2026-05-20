// Package audit provides a fixed-size ring buffer of request audit entries
// for the llmbridge proxy. Entries are written after every LLM call and can
// be retrieved via the admin API at GET /admin/audit-log.
package audit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Entry records metadata about a single proxied LLM request.
type Entry struct {
	Timestamp        time.Time `json:"timestamp"`
	APIKey           string    `json:"api_key"`            // masked: "llmb-xxxx…xxxx"
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	Cost             float64   `json:"cost"`
	LatencyMS        int64     `json:"latency_ms"`
	Status           int       `json:"status"` // HTTP status written to client
	UserIP           string    `json:"user_ip,omitempty"`
	OrgID            string    `json:"org_id,omitempty"`
	TeamID           string    `json:"team_id,omitempty"`
}

// Log is a thread-safe ring buffer of audit entries.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	head    int // next write position
	count   int // number of valid entries (≤ max)
	max     int
}

// New returns a Log with capacity for maxEntries entries.
// If maxEntries ≤ 0, the default of 1000 is used.
func New(maxEntries int) *Log {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	return &Log{entries: make([]Entry, maxEntries), max: maxEntries}
}

// Record appends an entry, overwriting the oldest entry if the buffer is full.
func (l *Log) Record(e Entry) {
	l.mu.Lock()
	l.entries[l.head] = e
	l.head = (l.head + 1) % l.max
	if l.count < l.max {
		l.count++
	}
	l.mu.Unlock()
}

// Recent returns the n most-recent entries, newest first.
// If n ≤ 0 or n > the number of recorded entries, all recorded entries are returned.
func (l *Log) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > l.count {
		n = l.count
	}
	out := make([]Entry, n)
	for i := range n {
		idx := (l.head - 1 - i + l.max) % l.max
		out[i] = l.entries[idx]
	}
	return out
}

// MaskKey returns a privacy-safe version of an API key: "llmb-xxxx…abcd".
func MaskKey(key string) string {
	if len(key) <= 12 {
		return "***"
	}
	return key[:8] + "…" + key[len(key)-4:]
}

// HandleList handles GET /admin/audit-log.
// Optional query param: limit (default 100, max 1000).
func (l *Log) HandleList(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	entries := l.Recent(limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
	})
}
