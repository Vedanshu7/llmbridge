package auth

import (
	"sync"
	"time"
)

// RateLimit defines per-key request and token throughput limits.
type RateLimit struct {
	// RequestsPerMin is the maximum number of requests allowed per minute.
	// 0 means unlimited.
	RequestsPerMin int

	// TokensPerMin is the maximum number of LLM tokens (prompt + completion)
	// allowed per minute. 0 means unlimited.
	TokensPerMin int
}

// RateLimitInfo carries the current rate-limit state for a key, used to
// populate X-RateLimit-* response headers.
type RateLimitInfo struct {
	LimitRequests     int
	RemainingRequests int
	LimitTokens       int
	RemainingTokens   int
	ResetAt           time.Time
}

// rateBucket holds the mutable token-bucket state for one API key.
type rateBucket struct {
	mu sync.Mutex

	// request token bucket (continuous refill)
	reqTokens   float64
	reqLastTime time.Time

	// token sliding window (resets each minute)
	tokUsed      int
	tokWindowEnd time.Time
}

// RateLimiter tracks per-key request and token rate limits.
type RateLimiter struct {
	mu      sync.RWMutex
	limits  map[string]RateLimit
	buckets map[string]*rateBucket
}

// NewRateLimiter returns an empty RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		limits:  make(map[string]RateLimit),
		buckets: make(map[string]*rateBucket),
	}
}

// SetLimit configures rate limits for a key. Replaces any existing limit.
func (rl *RateLimiter) SetLimit(key string, limit RateLimit) {
	rl.mu.Lock()
	rl.limits[key] = limit
	rl.mu.Unlock()
}

// RemoveLimit removes rate limits for a key.
func (rl *RateLimiter) RemoveLimit(key string) {
	rl.mu.Lock()
	delete(rl.limits, key)
	delete(rl.buckets, key)
	rl.mu.Unlock()
}

// Allow checks whether the key may make another request right now.
// Returns (true, info) if allowed, (false, info) if rate-limited.
func (rl *RateLimiter) Allow(key string) (bool, RateLimitInfo) {
	rl.mu.RLock()
	limit, hasLimit := rl.limits[key]
	rl.mu.RUnlock()

	if !hasLimit || limit.RequestsPerMin <= 0 {
		return true, RateLimitInfo{}
	}

	b := rl.getOrCreate(key)
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	capacity := float64(limit.RequestsPerMin)
	rate := capacity / 60.0 // tokens per second

	// Refill bucket based on elapsed time.
	if !b.reqLastTime.IsZero() {
		elapsed := now.Sub(b.reqLastTime).Seconds()
		b.reqTokens += elapsed * rate
		if b.reqTokens > capacity {
			b.reqTokens = capacity
		}
	} else {
		b.reqTokens = capacity
	}
	b.reqLastTime = now

	info := RateLimitInfo{
		LimitRequests:     limit.RequestsPerMin,
		RemainingRequests: int(b.reqTokens),
		LimitTokens:       limit.TokensPerMin,
		ResetAt:           now.Add(time.Minute),
	}

	if b.reqTokens < 1.0 {
		return false, info
	}
	b.reqTokens -= 1.0
	info.RemainingRequests = int(b.reqTokens)
	return true, info
}

// RecordTokens records token usage after a completed request.
// Returns (true, info) if within limits, (false, info) if the token budget
// for the current minute was exceeded — the next call to Allow will be blocked
// until the window resets.
func (rl *RateLimiter) RecordTokens(key string, tokens int) (bool, RateLimitInfo) {
	rl.mu.RLock()
	limit, hasLimit := rl.limits[key]
	rl.mu.RUnlock()

	if !hasLimit || limit.TokensPerMin <= 0 {
		return true, RateLimitInfo{}
	}

	b := rl.getOrCreate(key)
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if now.After(b.tokWindowEnd) {
		b.tokUsed = 0
		b.tokWindowEnd = now.Add(time.Minute)
	}
	b.tokUsed += tokens

	info := RateLimitInfo{
		LimitTokens:     limit.TokensPerMin,
		RemainingTokens: limit.TokensPerMin - b.tokUsed,
		ResetAt:         b.tokWindowEnd,
	}
	if info.RemainingTokens < 0 {
		info.RemainingTokens = 0
	}
	return b.tokUsed <= limit.TokensPerMin, info
}

func (rl *RateLimiter) getOrCreate(key string) *rateBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if b, ok := rl.buckets[key]; ok {
		return b
	}
	b := &rateBucket{}
	rl.buckets[key] = b
	return b
}
