package auth

import (
	"testing"
	"time"
)

func TestAllowNoLimit(t *testing.T) {
	rl := NewRateLimiter()
	allowed, info := rl.Allow("k")
	if !allowed {
		t.Fatal("expected allow when no limit set")
	}
	if info.LimitRequests != 0 {
		t.Fatal("expected zero info for unlimited key")
	}
}

func TestAllowWithinLimit(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{RequestsPerMin: 10})
	allowed, _ := rl.Allow("k")
	if !allowed {
		t.Fatal("first request should be allowed")
	}
}

func TestAllowExceedsLimit(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{RequestsPerMin: 2})

	// Drain the bucket.
	rl.Allow("k") //nolint:errcheck
	rl.Allow("k") //nolint:errcheck

	allowed, info := rl.Allow("k")
	if allowed {
		t.Fatal("expected rate limit to be triggered")
	}
	if info.LimitRequests != 2 {
		t.Fatalf("expected limit 2, got %d", info.LimitRequests)
	}
}

func TestRemoveLimit(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{RequestsPerMin: 1})
	rl.Allow("k") //nolint:errcheck — drain
	rl.RemoveLimit("k")
	// After removing the limit every request should pass.
	for i := 0; i < 5; i++ {
		allowed, _ := rl.Allow("k")
		if !allowed {
			t.Fatalf("expected allow after limit removed (attempt %d)", i)
		}
	}
}

func TestRecordTokensNoLimit(t *testing.T) {
	rl := NewRateLimiter()
	ok, _ := rl.RecordTokens("k", 500)
	if !ok {
		t.Fatal("expected ok when no token limit")
	}
}

func TestRecordTokensWithinLimit(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{TokensPerMin: 1000})
	ok, info := rl.RecordTokens("k", 400)
	if !ok {
		t.Fatal("expected ok within token limit")
	}
	if info.RemainingTokens != 600 {
		t.Fatalf("expected 600 remaining, got %d", info.RemainingTokens)
	}
}

func TestRecordTokensExceedsLimit(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{TokensPerMin: 100})
	rl.RecordTokens("k", 80) //nolint:errcheck
	ok, _ := rl.RecordTokens("k", 30) // total 110 > 100
	if ok {
		t.Fatal("expected token limit to be triggered")
	}
}

func TestTokenWindowReset(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{TokensPerMin: 100})

	b := rl.getOrCreate("k")
	b.mu.Lock()
	b.tokUsed = 90
	b.tokWindowEnd = time.Now().Add(-1 * time.Second) // already expired
	b.mu.Unlock()

	ok, _ := rl.RecordTokens("k", 50)
	if !ok {
		t.Fatal("expected ok after window reset")
	}
}
