package vertexai

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

// generateTestKey creates a 2048-bit RSA key and returns its PKCS8 PEM block.
func generateTestKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal PKCS8: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
}

// newTestProvider builds a Provider whose token exchange and generateContent
// calls are both routed to a single mock server, distinguished by the
// handler inspecting r.URL.Path / r.Method.
func newTestProvider(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	sa := map[string]string{
		"client_email": "test@test-project.iam.gserviceaccount.com",
		"private_key":  generateTestKey(t),
		"token_uri":    srv.URL + "/token",
	}
	credsJSON, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal service account: %v", err)
	}
	p, err := New("gemini-2.0-flash", "my-project", "us-central1", credsJSON)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.apiBaseURL = srv.URL
	return srv, p
}

func okCandidate(content string) map[string]interface{} {
	return map[string]interface{}{
		"candidates": []map[string]interface{}{
			{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []map[string]interface{}{{"text": content}},
				},
				"finishReason": "STOP",
			},
		},
	}
}

// ---- NewFromFile / ValidateEnvironment ----

func TestNewFromFileMissingFile(t *testing.T) {
	_, err := NewFromFile("", "proj", "us-central1", "/nonexistent/path/sa.json")
	if err == nil {
		t.Fatal("expected error for missing credentials file")
	}
}

func TestValidateEnvironmentMissingProject(t *testing.T) {
	p := &Provider{location: "us-central1"}
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing project")
	}
}

func TestValidateEnvironmentMissingLocation(t *testing.T) {
	p := &Provider{project: "my-project"}
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing location")
	}
}

// ---- Complete ----

func TestCompleteSuccessExchangesTokenAndCallsGenerateContent(t *testing.T) {
	var gotAuth, gotPath, gotQuery string
	var tokenCalls int
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			tokenCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "test-access-token", "expires_in": 3600,
			})
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okCandidate("hello vertex"))
	})

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello vertex" {
		t.Errorf("Content = %q, want %q", resp.Content, "hello vertex")
	}
	if tokenCalls != 1 {
		t.Errorf("expected 1 token exchange, got %d", tokenCalls)
	}
	if gotAuth != "Bearer test-access-token" {
		t.Errorf("Authorization = %q, want Bearer test-access-token", gotAuth)
	}
	if !strings.Contains(gotPath, "/projects/my-project/locations/us-central1/publishers/google/models/gemini-2.0-flash:generateContent") {
		t.Errorf("unexpected request path: %s", gotPath)
	}
	if strings.Contains(gotQuery, "key=") {
		t.Errorf("expected no API-key query param, got query %q", gotQuery)
	}
}

func TestCompleteReusesCachedToken(t *testing.T) {
	var tokenCalls int
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			tokenCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "cached-token", "expires_in": 3600,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okCandidate("ok"))
	})

	for i := 0; i < 2; i++ {
		if _, err := p.Complete(context.Background(), types.Request{
			Messages: []types.Message{{Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("Complete call %d: %v", i, err)
		}
	}
	if tokenCalls != 1 {
		t.Errorf("expected token endpoint hit exactly once across 2 calls, got %d", tokenCalls)
	}
}

func TestCompleteRefreshesExpiredToken(t *testing.T) {
	var tokenCalls int
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			tokenCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "token", "expires_in": 3600,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okCandidate("ok"))
	})

	if _, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	// Force the cached token to look expired.
	p.tokenMu.Lock()
	p.tokenExp = time.Now().Add(-time.Second)
	p.tokenMu.Unlock()

	if _, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if tokenCalls != 2 {
		t.Errorf("expected token endpoint hit twice after expiry, got %d", tokenCalls)
	}
}

func TestCompleteHTTPError(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "tok", "expires_in": 3600})
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	})

	if _, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}); err == nil {
		t.Fatal("expected error for HTTP 403 response")
	}
}

// ---- Stream ----

func TestStreamSuccess(t *testing.T) {
	chunk := `{"candidates":[{"content":{"parts":[{"text":"hello "}]},"finishReason":""}]}`
	chunk2 := `{"candidates":[{"content":{"parts":[{"text":"world"}]},"finishReason":"STOP"}]}`
	sseBody := "data: " + chunk + "\n\n" + "data: " + chunk2 + "\n\n"

	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "tok", "expires_in": 3600})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	})

	ch, err := p.Stream(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var got strings.Builder
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("stream error: %v", d.Err)
		}
		got.WriteString(d.Content)
		if d.Done {
			break
		}
	}
	if got.String() != "hello world" {
		t.Errorf("streamed content = %q, want %q", got.String(), "hello world")
	}
}
