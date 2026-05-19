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
func (c *capturingProvider) Name() string              { return "capturing" }
func (c *capturingProvider) ValidateEnvironment() error { return nil }

// ---- Auth ----

func TestAuthMissingKey(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)

	rec := httptest.NewRecorder()
	req := chatReq("gpt-4o", "hi")
	// No Authorization header.
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", rec.Code)
	}
}

func TestAuthInvalidKey(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)

	rec := httptest.NewRecorder()
	req := chatReq("gpt-4o", "hi")
	req.Header.Set("Authorization", "Bearer not-a-real-key")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid key, got %d", rec.Code)
	}
}

// ---- /v1/responses ----

func responsesReq(input interface{}) *http.Request {
	body := map[string]interface{}{
		"model": "gpt-4o",
		"input": input,
	}
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestResponsesAPIStringInput(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "world"}}
	srv, key := newTestServer(p)

	rec := httptest.NewRecorder()
	req := responsesReq("hello")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&out)
	if out["object"] != "response" {
		t.Fatalf("expected object=response, got %v", out["object"])
	}
	if out["status"] != "completed" {
		t.Fatalf("expected status=completed, got %v", out["status"])
	}
	outputs, _ := out["output"].([]interface{})
	if len(outputs) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(outputs))
	}
}

func TestResponsesAPIArrayInput(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "reply"}}
	srv, key := newTestServer(p)

	rec := httptest.NewRecorder()
	req := responsesReq([]map[string]string{
		{"role": "system", "content": "You are helpful."},
		{"role": "user", "content": "Hello"},
	})
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if p.calls != 1 {
		t.Fatalf("expected 1 provider call, got %d", p.calls)
	}
}

func TestResponsesAPIMissingInput(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)

	body := `{"model":"gpt-4o"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing input, got %d", rec.Code)
	}
}

// ---- Org/team admin endpoints ----

func adminKey(srv *Server) string {
	k, _ := srv.keyStore.GenerateAPIKey([]string{"admin"})
	return k
}

func TestCreateOrg(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	ak := adminKey(srv)

	body := `{"name":"acme","budget":100.0}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/orgs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&out)
	if out["id"] == "" {
		t.Fatal("expected org id in response")
	}
	if out["name"] != "acme" {
		t.Fatalf("expected name=acme, got %v", out["name"])
	}
}

func TestCreateTeam(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	ak := adminKey(srv)

	// Create org first.
	org, _ := srv.orgStore.CreateOrg("testorg", 0)

	body, _ := json.Marshal(map[string]interface{}{
		"org_id": org.ID,
		"name":   "eng",
		"budget": 50.0,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&out)
	if out["org_id"] != org.ID {
		t.Fatalf("team org_id mismatch: got %v", out["org_id"])
	}
}

func TestCreateTeamUnknownOrg(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	ak := adminKey(srv)

	body := `{"org_id":"org_doesnotexist","name":"team1"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/teams", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown org, got %d", rec.Code)
	}
}

func TestListOrgs(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	ak := adminKey(srv)
	srv.orgStore.CreateOrg("a", 0) //nolint:errcheck
	srv.orgStore.CreateOrg("b", 0) //nolint:errcheck

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/orgs", nil)
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var out map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&out)
	orgs, _ := out["orgs"].([]interface{})
	if len(orgs) != 2 {
		t.Fatalf("expected 2 orgs, got %d", len(orgs))
	}
}
