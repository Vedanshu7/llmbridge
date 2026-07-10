// Package idempotency provides request deduplication for the llmbridge proxy.
// When a client sends X-Idempotency-Key on a request, the proxy caches the
// response and replays it for any duplicate request carrying the same key
// within the TTL window (default 24 hours).
package idempotency

import (
	"sync"
	"time"
)

const defaultTTL = 24 * time.Hour

// Entry is a cached response for one idempotency key.
type Entry struct {
	StatusCode int
	Body       []byte
	Headers    map[string]string
	StoredAt   time.Time
}

// Store holds idempotency entries in memory. A background goroutine
// evicts expired entries every hour.
type Store struct {
	mu      sync.Mutex
	entries map[string]*Entry
	ttl     time.Duration
}

// NewStore returns a Store with a 24-hour TTL.
func NewStore() *Store {
	s := &Store{
		entries: make(map[string]*Entry),
		ttl:     defaultTTL,
	}
	go s.gc()
	return s
}

// Get retrieves the entry for key. Returns nil if not found or expired.
func (s *Store) Get(key string) *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok || time.Since(e.StoredAt) > s.ttl {
		return nil
	}
	return e
}

// Set stores e under key, overwriting any prior entry.
func (s *Store) Set(key string, e *Entry) {
	s.mu.Lock()
	s.entries[key] = e
	s.mu.Unlock()
}

// Len returns the number of stored (possibly expired) entries.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *Store) gc() {
	ticker := time.NewTicker(time.Hour)
	for range ticker.C {
		s.mu.Lock()
		for k, e := range s.entries {
			if time.Since(e.StoredAt) > s.ttl {
				delete(s.entries, k)
			}
		}
		s.mu.Unlock()
	}
}
