package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ---- OIDCStateStore ----

func TestOIDCStateIssueConsume(t *testing.T) {
	s := NewOIDCStateStore()
	tok, err := s.Issue("google")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty state token")
	}

	provider, ok := s.Consume(tok)
	if !ok {
		t.Fatal("Consume returned false for valid state")
	}
	if provider != "google" {
		t.Fatalf("got provider %q, want %q", provider, "google")
	}
}

func TestOIDCStateConsumeOnce(t *testing.T) {
	s := NewOIDCStateStore()
	tok, _ := s.Issue("github")

	s.Consume(tok) // first consume
	_, ok := s.Consume(tok)
	if ok {
		t.Fatal("second Consume should return false (token already consumed)")
	}
}

func TestOIDCStateConsumeUnknown(t *testing.T) {
	s := NewOIDCStateStore()
	_, ok := s.Consume("nonexistent")
	if ok {
		t.Fatal("expected false for unknown state")
	}
}

func TestOIDCStateConsumeExpired(t *testing.T) {
	s := NewOIDCStateStore()
	// Manually insert an already-expired entry.
	s.mu.Lock()
	s.states["expired_tok"] = stateEntry{provider: "microsoft", expiry: time.Now().Add(-time.Minute)}
	s.mu.Unlock()

	_, ok := s.Consume("expired_tok")
	if ok {
		t.Fatal("expected false for expired state")
	}
}

func TestOIDCStateTokensAreUnique(t *testing.T) {
	s := NewOIDCStateStore()
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		tok, err := s.Issue("google")
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if seen[tok] {
			t.Fatalf("duplicate state token: %s", tok)
		}
		seen[tok] = true
	}
}

// ---- OIDCProvider.AuthURL ----

func TestGoogleAuthURL(t *testing.T) {
	p := NewGoogleProvider("client123", "secret", "http://localhost/callback")
	u := p.AuthURL("mystate")

	if !strings.HasPrefix(u, "https://accounts.google.com/o/oauth2/v2/auth?") {
		t.Fatalf("unexpected base URL: %s", u)
	}
	for _, param := range []string{"client_id=client123", "state=mystate", "response_type=code", "scope=openid"} {
		if !strings.Contains(u, param) {
			t.Errorf("AuthURL missing %q: %s", param, u)
		}
	}
}

func TestGitHubAuthURL(t *testing.T) {
	p := NewGitHubProvider("ghclient", "ghsecret", "http://localhost/callback")
	u := p.AuthURL("ghstate")

	if !strings.HasPrefix(u, "https://github.com/login/oauth/authorize?") {
		t.Fatalf("unexpected base URL: %s", u)
	}
	for _, param := range []string{"client_id=ghclient", "state=ghstate", "scope=user%3Aemail"} {
		if !strings.Contains(u, param) {
			t.Errorf("AuthURL missing %q: %s", param, u)
		}
	}
}

func TestMicrosoftAuthURL(t *testing.T) {
	p := NewMicrosoftProvider("msclient", "mssecret", "http://localhost/callback", "mytenant")
	u := p.AuthURL("msstate")

	if !strings.Contains(u, "login.microsoftonline.com/mytenant") {
		t.Fatalf("unexpected URL for tenant: %s", u)
	}
	if !strings.Contains(u, "state=msstate") {
		t.Errorf("AuthURL missing state: %s", u)
	}
}

func TestMicrosoftAuthURLDefaultTenant(t *testing.T) {
	p := NewMicrosoftProvider("c", "s", "http://cb", "")
	u := p.AuthURL("x")
	if !strings.Contains(u, "login.microsoftonline.com/common") {
		t.Fatalf("expected 'common' tenant in URL: %s", u)
	}
}

// ---- SetKeyName / Name field ----

func TestSetKeyName(t *testing.T) {
	store := NewAPIKeyStore()
	key, err := store.GenerateAPIKey([]string{"completion"})
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	store.SetKeyName(key, "sso:alice@example.com")

	keys := store.ListKeys()
	var found bool
	for _, k := range keys {
		if k.Key == key {
			found = true
			if k.Name != "sso:alice@example.com" {
				t.Fatalf("Name = %q, want %q", k.Name, "sso:alice@example.com")
			}
		}
	}
	if !found {
		t.Fatal("key not found in ListKeys")
	}
}

func TestSetKeyNameMissingKey(t *testing.T) {
	store := NewAPIKeyStore()
	// Should be a no-op for a non-existent key — must not panic.
	store.SetKeyName("nonexistent", "label")
}

// ---- Exchange (token endpoint) ----

func TestExchangeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"at123","id_token":"idt456","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	p := NewGoogleProvider("c", "s", "http://cb")
	p.tokenURL = srv.URL
	p.client = srv.Client()

	tok, err := p.Exchange(context.Background(), "code123")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tok.AccessToken != "at123" || tok.IDToken != "idt456" {
		t.Errorf("unexpected token: %+v", tok)
	}
}

func TestExchangeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad_request", http.StatusBadRequest)
	}))
	defer srv.Close()

	p := NewGoogleProvider("c", "s", "http://cb")
	p.tokenURL = srv.URL
	p.client = srv.Client()

	_, err := p.Exchange(context.Background(), "badcode")
	if err == nil {
		t.Error("expected error for HTTP 400")
	}
}

func TestExchangeGitHubSetsAcceptHeader(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"ghat","token_type":"bearer"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("c", "s", "http://cb")
	p.tokenURL = srv.URL
	p.client = srv.Client()

	_, err := p.Exchange(context.Background(), "code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
}

// ---- UserInfo ----

func TestUserInfoGoogle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mytoken" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sub":"123","email":"alice@google.com","name":"Alice"}`))
	}))
	defer srv.Close()

	p := NewGoogleProvider("c", "s", "http://cb")
	p.userInfoURL = srv.URL
	p.client = srv.Client()

	u, err := p.UserInfo(context.Background(), &OIDCToken{AccessToken: "mytoken"})
	if err != nil {
		t.Fatalf("UserInfo: %v", err)
	}
	if u.Email != "alice@google.com" || u.Name != "Alice" {
		t.Errorf("unexpected user: %+v", u)
	}
}

func TestUserInfoGitHub(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		// GitHub returns email in profile only when not private.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login":"bob","name":"Bob","email":"bob@gh.com"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewGitHubProvider("c", "s", "http://cb")
	p.userInfoURL = srv.URL + "/user"
	p.client = srv.Client()

	u, err := p.UserInfo(context.Background(), &OIDCToken{AccessToken: "ghtoken"})
	if err != nil {
		t.Fatalf("UserInfo: %v", err)
	}
	if u.Email != "bob@gh.com" || u.Name != "Bob" {
		t.Errorf("unexpected user: %+v", u)
	}
}

func TestUserInfoGitHubPrivateEmail(t *testing.T) {
	// GitHub profile returns empty email — falls back to /user/emails.
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login":"carol","name":"Carol","email":""}`))
	})
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"email":"carol@work.com","primary":true}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewGitHubProvider("c", "s", "http://cb")
	p.userInfoURL = srv.URL + "/user"
	// Override the hardcoded /user/emails URL via client transport.
	p.client = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	u, err := p.UserInfo(context.Background(), &OIDCToken{AccessToken: "token"})
	if err != nil {
		t.Fatalf("UserInfo: %v", err)
	}
	if u.Email != "carol@work.com" {
		t.Errorf("email = %q, want carol@work.com", u.Email)
	}
}

func TestUserInfoHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	p := NewGoogleProvider("c", "s", "http://cb")
	p.userInfoURL = srv.URL
	p.client = srv.Client()

	_, err := p.UserInfo(context.Background(), &OIDCToken{AccessToken: "bad"})
	if err == nil {
		t.Error("expected error for HTTP 403")
	}
}

// ---- fetchJWKS ----

func TestFetchJWKS(t *testing.T) {
	jwksBody := `{"keys":[{"kid":"key1","kty":"RSA","n":"AQAB","e":"AQAB","alg":"RS256"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jwksBody))
	}))
	defer srv.Close()

	p := NewGoogleProvider("c", "s", "http://cb")
	p.jwksURL = srv.URL
	p.client = srv.Client()

	set, err := p.fetchJWKS(context.Background())
	if err != nil {
		t.Fatalf("fetchJWKS: %v", err)
	}
	if len(set.Keys) != 1 || set.Keys[0].Kid != "key1" {
		t.Errorf("unexpected JWKS: %+v", set)
	}
}

func TestFetchJWKSCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"keys":[]}`))
	}))
	defer srv.Close()

	p := NewGoogleProvider("c", "s", "http://cb")
	p.jwksURL = srv.URL
	p.client = srv.Client()

	p.fetchJWKS(context.Background())
	p.fetchJWKS(context.Background()) // should use cache
	if calls != 1 {
		t.Errorf("JWKS fetched %d times, want 1 (second call should use cache)", calls)
	}
}

// ---- VerifyIDToken ----

// b64url encodes bytes as base64url without padding.
func b64url(b []byte) string {
	return strings.TrimRight(
		strings.NewReplacer("+", "-", "/", "_").Replace(
			base64.StdEncoding.EncodeToString(b),
		), "=")
}

// mintRS256JWT creates a signed RS256 JWT using key. Returns (token, jwksJSON).
func mintRS256JWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]interface{}) (string, string) {
	t.Helper()
	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	payloadJSON, _ := json.Marshal(claims)
	sigInput := b64url(headerJSON) + "." + b64url(payloadJSON)
	h := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	token := sigInput + "." + b64url(sig)

	// Build minimal JWKS.
	nB := key.PublicKey.N.Bytes()
	eB := big.NewInt(int64(key.PublicKey.E)).Bytes()
	jwksJSON, _ := json.Marshal(map[string]interface{}{
		"keys": []map[string]string{
			{"kid": kid, "kty": "RSA", "alg": "RS256",
				"n": b64url(nB), "e": b64url(eB)},
		},
	})
	return token, string(jwksJSON)
}

func TestVerifyIDTokenInvalidFormat(t *testing.T) {
	p := NewGoogleProvider("c", "s", "http://cb")
	_, err := p.VerifyIDToken(context.Background(), "not-a-jwt")
	if err == nil {
		t.Error("expected error for non-JWT input")
	}
}

func TestVerifyIDTokenNoJWKSURL(t *testing.T) {
	p := NewGitHubProvider("c", "s", "http://cb") // GitHub has no jwksURL
	_, err := p.VerifyIDToken(context.Background(), "a.b.c")
	if err == nil {
		t.Error("expected error when jwksURL is empty")
	}
}

func TestVerifyIDTokenRS256Valid(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]interface{}{"sub": "user42", "email": "user@example.com"}
	token, jwksJSON := mintRS256JWT(t, key, "testkey", claims)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jwksJSON))
	}))
	defer srv.Close()

	p := NewGoogleProvider("c", "s", "http://cb")
	p.jwksURL = srv.URL
	p.client = srv.Client()

	got, err := p.VerifyIDToken(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyIDToken: %v", err)
	}
	if got["sub"] != "user42" {
		t.Errorf("sub = %v, want user42", got["sub"])
	}
}

func TestVerifyIDTokenWrongSignature(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	token, jwksJSON := mintRS256JWT(t, key, "testkey", map[string]interface{}{"sub": "u1"})

	// Tamper with the payload.
	parts := strings.Split(token, ".")
	parts[1] = b64url([]byte(`{"sub":"attacker"}`))
	tampered := strings.Join(parts, ".")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(jwksJSON))
	}))
	defer srv.Close()

	p := NewGoogleProvider("c", "s", "http://cb")
	p.jwksURL = srv.URL
	p.client = srv.Client()

	_, err := p.VerifyIDToken(context.Background(), tampered)
	if err == nil {
		t.Error("expected signature verification failure for tampered payload")
	}
}

// redirectTransport rewrites all requests to targetBase (for GitHub private email test).
type redirectTransport struct {
	base string
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	target, _ := url.Parse(rt.base)
	req2.URL.Host = target.Host
	req2.URL.Scheme = target.Scheme
	return http.DefaultTransport.RoundTrip(req2)
}
