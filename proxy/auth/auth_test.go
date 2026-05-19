package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- APIKeyStore ----

func TestGenerateAndValidate(t *testing.T) {
	s := NewAPIKeyStore()
	key, err := s.GenerateAPIKey([]string{"completion"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(key, "llmb-") {
		t.Fatalf("unexpected key prefix: %s", key)
	}
	info, ok := s.ValidateAPIKey(key)
	if !ok || info == nil {
		t.Fatal("expected valid key")
	}
}

func TestValidateMissing(t *testing.T) {
	s := NewAPIKeyStore()
	_, ok := s.ValidateAPIKey("no-such-key")
	if ok {
		t.Fatal("expected invalid for unknown key")
	}
}

func TestDeleteKey(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.DeleteKey(key)
	_, ok := s.ValidateAPIKey(key)
	if ok {
		t.Fatal("expected invalid after delete")
	}
}

func TestKeyExpiry(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.SetExpiry(key, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	_, ok := s.ValidateAPIKey(key)
	if ok {
		t.Fatal("expected key to be expired")
	}
}

func TestKeyNotExpired(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.SetExpiry(key, time.Minute)
	_, ok := s.ValidateAPIKey(key)
	if !ok {
		t.Fatal("expected key to be valid before expiry")
	}
}

func TestHasScope(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey([]string{"completion"})
	if !s.HasScope(key, "completion") {
		t.Fatal("expected completion scope")
	}
	if s.HasScope(key, "admin") {
		t.Fatal("expected no admin scope")
	}
}

func TestAdminGrantsAll(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey([]string{"admin"})
	if !s.HasScope(key, "completion") {
		t.Fatal("admin should grant completion")
	}
	if !s.HasScope(key, "anything") {
		t.Fatal("admin should grant arbitrary scope")
	}
}

func TestSpendLimit(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.SetSpendLimit(key, 1.00)
	if err := s.RecordSpend(key, 0.50); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.RecordSpend(key, 0.60); err == nil {
		t.Fatal("expected spend limit error")
	}
}

// ---- RequireAuth middleware ----

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func TestRequireAuthMissingHeader(t *testing.T) {
	s := NewAPIKeyStore()
	h := RequireAuth(s)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuthInvalidKey(t *testing.T) {
	s := NewAPIKeyStore()
	h := RequireAuth(s)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bad-key")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuthValidKey(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	h := RequireAuth(s)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---- RequireRateLimit middleware ----

func TestRateLimitAllows(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{RequestsPerMin: 10})
	s := NewAPIKeyStore()
	s.ImportKey("k", nil)

	h := RequireRateLimit(rl)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-RateLimit-Limit-Requests") == "" {
		t.Fatal("expected X-RateLimit-Limit-Requests header")
	}
}

func TestRateLimitBlocks(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{RequestsPerMin: 1})

	// Drain.
	rl.Allow("k") //nolint:errcheck

	h := RequireRateLimit(rl)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
}
