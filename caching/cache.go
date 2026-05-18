package caching

import (
	"sync"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

// Cache is the interface for request/response caching.
type Cache interface {
	// Get retrieves a cached response by key.
	Get(key string) (*types.Response, bool)

	// Set stores a response under key with the given TTL.
	// A zero TTL means the entry never expires.
	Set(key string, resp *types.Response, ttl time.Duration)

	// Delete removes a single entry.
	Delete(key string)

	// Flush removes all entries.
	Flush()
}

type entry struct {
	resp      *types.Response
	expiresAt time.Time // zero means no expiry
}

// InMemoryCache is a thread-safe in-memory implementation of Cache.
type InMemoryCache struct {
	mu    sync.RWMutex
	store map[string]entry
}

// NewInMemoryCache returns an empty InMemoryCache.
func NewInMemoryCache() *InMemoryCache {
	return &InMemoryCache{store: make(map[string]entry)}
}

// Get implements Cache.
func (c *InMemoryCache) Get(key string) (*types.Response, bool) {
	c.mu.RLock()
	e, ok := c.store[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		c.Delete(key)
		return nil, false
	}
	return e.resp, true
}

// Set implements Cache.
func (c *InMemoryCache) Set(key string, resp *types.Response, ttl time.Duration) {
	e := entry{resp: resp}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.store[key] = e
	c.mu.Unlock()
}

// Delete implements Cache.
func (c *InMemoryCache) Delete(key string) {
	c.mu.Lock()
	delete(c.store, key)
	c.mu.Unlock()
}

// Flush implements Cache.
func (c *InMemoryCache) Flush() {
	c.mu.Lock()
	c.store = make(map[string]entry)
	c.mu.Unlock()
}

// Len returns the number of entries currently in the cache (including expired).
func (c *InMemoryCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.store)
}
