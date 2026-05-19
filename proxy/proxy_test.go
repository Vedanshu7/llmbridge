package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/caching"
	"github.com/Vedanshu7/llmbridge/guardrails"
	"github.com/Vedanshu7/llmbridge/types"
)

// stubProvider is a minimal Provider that returns a fixed response.
type stubProvider struct {
	resp  *types.Response
	calls int
}

func (s *stubProvider) Complete(_ context.Context, _ types.Request) (*types.Response, error) {
	s.calls++
	return s.resp, nil
}
func (s *stubProvider) Name() string             { return "stub" }
func (s *stubProvider) ValidateEnvironment() error { return nil }

func newTestServer(p *stubProvider) (*Server, string) {
	srv := NewServer(p)
	key, _ := srv.keyStore.GenerateAPIKey([]string{"completion"})
	return srv, key
}

func chatReq(model, userMsg string) *http.Request {
	body := map[string]interface{}{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": userMsg}},
	}
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// ---- Caching ----

func TestCachingHit(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "cached response"}}
	srv, key := newTestServer(p)
	srv.SetCache(caching.NewInMemoryCache(), time.Minute)

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := chatReq("gpt-4o", "hello")
		req.Header.Set("Authorization", "Bearer "+key)
		srv.ServeHTTP(rec, req)
		return rec
	}

	r1 := do()
	if r1.Code != http.StatusOK {
		t.Fatalf("first request: %d", r1.Code)
	}
	if p.calls != 1 {
		t.Fatalf("expected 1 provider call, got %d", p.calls)
	}

	r2 := do()
	if r2.Code != http.StatusOK {
		t.Fatalf("second request: %d", r2.Code)
	}
	if p.calls != 1 {
		t.Fatalf("expected cache hit (still 1 provider call), got %d", p.calls)
	}
	if r1.Body.String() != r2.Body.String() {
		t.Fatal("cached response body differs from original")
	}
}

func TestCachingDisabled(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "live"}}
	srv, key := newTestServer(p)
	// No SetCache call — caching is off.

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := chatReq("gpt-4o", "hello")
		req.Header.Set("Authorization", "Bearer "+key)
		srv.ServeHTTP(rec, req)
	}
	if p.calls != 3 {
		t.Fatalf("expected 3 provider calls without cache, got %d", p.calls)
	}
}

// ---- Guardrails ----

func TestGuardrailsBlockRequest(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)
	srv.SetGuardrails(guardrails.New(guardrails.MaxInputLength(5)))

	rec := httptest.NewRecorder()
	req := chatReq("gpt-4o", "this message is way too long for the limit")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 from guardrail, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "guardrail_violation") {
		t.Fatalf("expected guardrail_violation error type: %s", rec.Body.String())
	}
	if p.calls != 0 {
		t.Fatal("provider should not be called when guardrail blocks request")
	}
}

func TestGuardrailsBlockResponse(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "this response contains a forbidden word"}}
	srv, key := newTestServer(p)
	srv.SetGuardrails(guardrails.New(guardrails.BlockKeywords([]string{"forbidden"})))

	rec := httptest.NewRecorder()
	req := chatReq("gpt-4o", "hello")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 from response guardrail, got %d", rec.Code)
	}
}

func TestGuardrailsAllowCleanRequest(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "clean reply"}}
	srv, key := newTestServer(p)
	srv.SetGuardrails(guardrails.New(guardrails.BlockKeywords([]string{"forbidden"})))

	rec := httptest.NewRecorder()
	req := chatReq("gpt-4o", "hello world")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for clean request, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---- Model aliasing ----

func TestModelAlias(t *testing.T) {
	var capturedModel string
	p := &stubProvider{}
	p.resp = &types.Response{Content: "ok"}

	srv := NewServer(&capturingProvider{inner: p, onComplete: func(req types.Request) {
		capturedModel = req.Model
	}})
	key, _ := srv.keyStore.GenerateAPIKey([]string{"completion"})
	srv.aliases = map[string]string{"gpt4": "gpt-4o-2024-08-06"}

	rec := httptest.NewRecorder()
	req := chatReq("gpt4", "hi")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)

	if capturedModel != "gpt-4o-2024-08-06" {
		t.Fatalf("expected alias resolved to gpt-4o-2024-08-06, got %q", capturedModel)
	}
}

// capturingProvider intercepts Complete calls to record request details.
type capturingProvider struct {
	inner      *stubProvider
	onComplete func(types.Request)
}

func (c *capturingProvider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	c.onComplete(req)
	return c.inner.Complete(ctx, req)
}
func (c *capturingProvider) Name() string             { return "capturing" }
func (c *capturingProvider) ValidateEnvironment() error { return nil }
