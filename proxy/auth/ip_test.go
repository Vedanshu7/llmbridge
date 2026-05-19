package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPAllowedEmptyCIDRs(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5000"
	if !ipAllowed(req, nil) {
		t.Fatal("empty CIDR list should allow all IPs")
	}
}

func TestIPAllowedMatchesCIDR(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	if !ipAllowed(req, []string{"10.0.0.0/8"}) {
		t.Fatal("10.0.0.5 should match 10.0.0.0/8")
	}
}

func TestIPAllowedNoMatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5000"
	if ipAllowed(req, []string{"10.0.0.0/8"}) {
		t.Fatal("1.2.3.4 should NOT match 10.0.0.0/8")
	}
}

func TestIPAllowedXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234" // proxy loopback
	req.Header.Set("X-Forwarded-For", "192.168.1.50, 10.0.0.1")
	if !ipAllowed(req, []string{"192.168.1.0/24"}) {
		t.Fatal("X-Forwarded-For first entry should be used")
	}
}

func TestIPAllowedMultipleCIDRs(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.16.0.10:80"
	if !ipAllowed(req, []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}) {
		t.Fatal("172.16.0.10 should match 172.16.0.0/12")
	}
}

func TestSetAllowedIPsEnforcedByMiddleware(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.SetAllowedIPs(key, []string{"10.0.0.0/8"})

	h := RequireAuth(s)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request from disallowed IP.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	req.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for disallowed IP, got %d", rec.Code)
	}

	// Request from allowed IP.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:9999"
	req.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for allowed IP, got %d", rec.Code)
	}
}
