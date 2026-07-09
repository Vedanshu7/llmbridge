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
	"github.com/Vedanshu7/llmbridge/proxy/auth"
	"github.com/Vedanshu7/llmbridge/proxy/config"
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

// ---- Admin stats ----

func TestAdminStats(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	// newTestServer creates 1 completion key; adminKey creates 1 admin key → 2 total.
	srv, _ := newTestServer(p)
	ak := adminKey(srv)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, field := range []string{"total_requests", "total_tokens", "active_keys", "orgs_count"} {
		if _, ok := out[field]; !ok {
			t.Errorf("missing field %q in /admin/stats response", field)
		}
	}
	if got := out["active_keys"].(float64); got != 2 {
		t.Errorf("active_keys = %.0f, want 2", got)
	}
}

func TestAdminStatsRequiresAdmin(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p) // key has "completion" scope, not "admin"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin key, got %d", rec.Code)
	}
}

// ---- OIDC/SSO auth endpoints ----

func TestAuthLoginNotConfigured(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/login?provider=google", nil)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when OIDC not configured, got %d", rec.Code)
	}
}

func TestAuthLoginBadProvider(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)
	// Register one provider to enable the OIDC flow.
	srv.oidcProviders["google"] = &auth.OIDCProvider{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/login?provider=unknown", nil)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d", rec.Code)
	}
}

func TestAuthCallbackMissingParams(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)

	rec := httptest.NewRecorder()
	// No state or code params — should be 400.
	req := httptest.NewRequest(http.MethodGet, "/auth/callback", nil)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing params, got %d", rec.Code)
	}
}

func TestAuthCallbackInvalidState(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?state=bogus&code=abc", nil)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid state, got %d", rec.Code)
	}
}

func TestAuthLogoutRedirects(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect from /auth/logout, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/auth/login" {
		t.Fatalf("expected redirect to /auth/login, got %q", loc)
	}
}

// ---- Semantic cache via proxy ----

func TestSemanticCacheHitViaProxy(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "semantic answer", Model: "m"}}
	srv, key := newTestServer(p)

	embedder := &fixedEmbedder{vec: []float64{1, 0, 0}}
	sc := caching.NewSemanticCache(caching.NewInMemoryCache(), embedder, 0.99)
	srv.SetCache(sc, time.Minute)

	// First request — cache miss, provider called.
	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := chatReq("m", "hello world")
		r.Header.Set("Authorization", "Bearer "+key)
		srv.ServeHTTP(rec, r)
		return rec
	}

	r1 := do()
	if r1.Code != http.StatusOK {
		t.Fatalf("first request: %d %s", r1.Code, r1.Body.String())
	}
	if p.calls != 1 {
		t.Fatalf("expected 1 provider call, got %d", p.calls)
	}

	// Second identical request — should hit semantic cache.
	r2 := do()
	if r2.Code != http.StatusOK {
		t.Fatalf("second request: %d %s", r2.Code, r2.Body.String())
	}
	if p.calls != 1 {
		t.Fatalf("expected cache hit (still 1 provider call), got %d", p.calls)
	}
}

// fixedEmbedder always returns the same vector regardless of input.
type fixedEmbedder struct {
	vec []float64
}

func (f *fixedEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = f.vec
	}
	return out, nil
}
func (f *fixedEmbedder) Name() string { return "fixed" }

// ---- SQLite persistence round-trip ----

func TestFromConfigWithDB_KeyPersistence(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	cfg := &config.Config{
		AdminKeys: []string{"llmb-admin-test"},
	}

	// Build server with in-memory SQLite.
	srv, err := FromConfigWithDB(cfg, p, ":memory:")
	if err != nil {
		t.Fatalf("FromConfigWithDB: %v", err)
	}

	// Generate a key via the server.
	generatedKey, err := srv.keyStore.GenerateAPIKey([]string{"completion"})
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	// Verify the admin key from config and the generated key both validate.
	if _, ok := srv.keyStore.ValidateAPIKey("llmb-admin-test"); !ok {
		t.Error("admin key from config should be valid")
	}
	if _, ok := srv.keyStore.ValidateAPIKey(generatedKey); !ok {
		t.Error("generated key should be valid")
	}
}

func TestFromConfigWithDB_Error(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	cfg := &config.Config{}

	// An invalid path that can't be created should return an error.
	_, err := FromConfigWithDB(cfg, p, "/nonexistent/dir/llmbridge.db")
	if err == nil {
		t.Fatal("expected error for invalid db path, got nil")
	}
}

// ---- Accessor methods ----

func TestAccessors(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)

	if srv.RateLimiter() == nil {
		t.Error("RateLimiter() should return non-nil")
	}
	if srv.Metrics() == nil {
		t.Error("Metrics() should return non-nil")
	}
	if srv.KeyStore() == nil {
		t.Error("KeyStore() should return non-nil")
	}
}

func TestSetJWTSecret(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)
	srv.SetJWTSecret([]byte("mysecret"))
	// Mux is rebuilt; verify the server still handles requests.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after SetJWTSecret, got %d", rec.Code)
	}
}

// ---- Health ----

func TestHealth(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var out map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["status"] != "ok" {
		t.Errorf("status = %q, want ok", out["status"])
	}
}

// ---- Model registry endpoints ----

func TestListModels(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["object"] != "list" {
		t.Errorf("object = %v, want list", out["object"])
	}
}

func TestGetModelFound(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)
	// Register a model first since the registry starts empty.
	ak := adminKey(srv)
	body := `{"name":"my-model","provider":"openai","model":"gpt-4o"}`
	rec0 := httptest.NewRecorder()
	req0 := httptest.NewRequest(http.MethodPost, "/admin/models", strings.NewReader(body))
	req0.Header.Set("Content-Type", "application/json")
	req0.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec0, req0)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models/my-model", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetModelNotFound(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models/no-such-model-xyz", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---- Aliases endpoints ----

func TestListAndSetAlias(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	ak := adminKey(srv)

	// Set an alias.
	body := `{"alias":"fast","model":"gpt-4o-mini"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/aliases", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set alias: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// List aliases and verify.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/admin/aliases", nil)
	req2.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("list aliases: expected 200, got %d", rec2.Code)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(rec2.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	aliases, _ := out["aliases"].(map[string]interface{})
	if aliases["fast"] != "gpt-4o-mini" {
		t.Errorf("alias fast = %v, want gpt-4o-mini", aliases["fast"])
	}
}

func TestSetAliasBadBody(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	ak := adminKey(srv)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/aliases", strings.NewReader(`{"alias":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestListAliasesNoneSet(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	ak := adminKey(srv)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/aliases", nil)
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---- Embeddings endpoint ----

type stubEmbedProvider struct {
	stubProvider
	result [][]float64
}

func (s *stubEmbedProvider) Embed(_ context.Context, _ []string) ([][]float64, error) {
	return s.result, nil
}

func TestEmbeddingsEndpoint(t *testing.T) {
	inner := &stubEmbedProvider{
		stubProvider: stubProvider{resp: &types.Response{Content: "ok"}},
		result:       [][]float64{{0.1, 0.2}, {0.3, 0.4}},
	}
	srv := NewServer(inner)
	key, _ := srv.keyStore.GenerateAPIKey([]string{"completion"})

	body := `{"input":["hello","world"],"model":"text-embedding-3-small"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := out["data"].([]interface{})
	if len(data) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(data))
	}
}

func TestEmbeddingsProviderNotSupported(t *testing.T) {
	// stubProvider does not implement EmbedProvider.
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)

	body := `{"input":["hello"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ---- List teams endpoint ----

func TestListTeams(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	ak := adminKey(srv)

	// Create an org and a team first.
	orgBody := `{"name":"my-org","budget":100.0}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/orgs", strings.NewReader(orgBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec, req)
	var orgOut map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&orgOut) //nolint:errcheck
	orgID := orgOut["id"].(string)

	teamBody, _ := json.Marshal(map[string]interface{}{"name": "alpha", "budget": 50.0, "org_id": orgID})
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/admin/teams", bytes.NewReader(teamBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec2, req2)

	// List teams filtered by org_id.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/admin/teams?org_id="+orgID, nil)
	req3.Header.Set("Authorization", "Bearer "+ak)
	srv.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec3.Code, rec3.Body.String())
	}
	var out map[string]interface{}
	if err := json.NewDecoder(rec3.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	teams, _ := out["teams"].([]interface{})
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}
}

// ---- Batch endpoints ----

func TestBatchCreateAndStatus(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)

	body := `{"requests":[{"messages":[{"role":"user","content":"hello"}]}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch create: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var createOut map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&createOut); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	batchID := createOut["id"].(string)

	// Poll status.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/batches/"+batchID, nil)
	req2.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("batch status: expected 200, got %d", rec2.Code)
	}
}

func TestBatchStatusNotFound(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/batches/no-such-batch", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestBatchCancelInProcess(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)

	// Create a batch first.
	body := `{"requests":[{"messages":[{"role":"user","content":"hi"}]}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec, req)
	var out map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&out) //nolint:errcheck
	batchID := out["id"].(string)

	// Cancel it.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/batches/"+batchID+"/cancel", nil)
	req2.Header.Set("Authorization", "Bearer "+key)
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// ---- Provider health endpoint ----

func TestAdminHealthEndpointSingleProvider(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv := NewServer(p)
	adminKey, _ := srv.keyStore.GenerateAPIKey([]string{"admin"})

	req := httptest.NewRequest(http.MethodGet, "/admin/health", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	providers, ok := out["providers"].([]interface{})
	if !ok || len(providers) < 1 {
		t.Fatalf("expected at least one provider in response, got: %+v", out)
	}
}

func TestAdminHealthRequiresAdminScope(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p) // completion scope only

	req := httptest.NewRequest(http.MethodGet, "/admin/health", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-admin key, got %d", w.Code)
	}
}
