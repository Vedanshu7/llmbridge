package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/proxy/auth"
)

// ---- KeyManagement ----

func newKeyMgmt() (*KeyManagement, *auth.APIKeyStore) {
	store := auth.NewAPIKeyStore()
	return NewKeyManagement(store), store
}

func TestHandleGenerateReturnsKey(t *testing.T) {
	km, _ := newKeyMgmt()
	body := `{"scopes":["completion"]}`
	req := httptest.NewRequest("POST", "/admin/key/generate", strings.NewReader(body))
	w := httptest.NewRecorder()
	km.HandleGenerate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["key"] == "" {
		t.Fatal("expected non-empty key")
	}
}

func TestHandleGenerateDefaultScopes(t *testing.T) {
	km, store := newKeyMgmt()
	req := httptest.NewRequest("POST", "/admin/key/generate", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	km.HandleGenerate(w, req)

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key := resp["key"]
	info, ok := store.ValidateAPIKey(key)
	if !ok {
		t.Fatal("generated key should be valid")
	}
	if len(info.Scopes) == 0 {
		t.Fatal("expected default scopes to be set")
	}
}

func TestHandleDeleteKey(t *testing.T) {
	km, store := newKeyMgmt()
	key, _ := store.GenerateAPIKey([]string{"completion"})

	body, _ := json.Marshal(map[string]string{"key": key})
	req := httptest.NewRequest("DELETE", "/admin/key/delete", bytes.NewReader(body))
	w := httptest.NewRecorder()
	km.HandleDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if _, ok := store.ValidateAPIKey(key); ok {
		t.Fatal("key should be deleted")
	}
}

func TestHandleDeleteMissingKey(t *testing.T) {
	km, _ := newKeyMgmt()
	req := httptest.NewRequest("DELETE", "/admin/key/delete", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	km.HandleDelete(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleListKeys(t *testing.T) {
	km, store := newKeyMgmt()
	_, _ = store.GenerateAPIKey([]string{"completion"})
	_, _ = store.GenerateAPIKey([]string{"admin"})

	req := httptest.NewRequest("GET", "/admin/keys", nil)
	w := httptest.NewRecorder()
	km.HandleList(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	keys, ok := resp["keys"].([]interface{})
	if !ok || len(keys) != 2 {
		t.Fatalf("expected 2 keys, got: %v", resp["keys"])
	}
}

// ---- ModelRegistry ----

func TestModelRegistryRegisterAndGet(t *testing.T) {
	mr := NewModelRegistry()
	mr.RegisterModel("gpt4", ModelInfo{Provider: "openai", Model: "gpt-4o", MaxTokens: 128000})

	info, ok := mr.GetModel("gpt4")
	if !ok {
		t.Fatal("expected model to be found")
	}
	if info.Provider != "openai" || info.MaxTokens != 128000 {
		t.Fatalf("model info mismatch: %+v", info)
	}
}

func TestModelRegistryMiss(t *testing.T) {
	mr := NewModelRegistry()
	_, ok := mr.GetModel("nonexistent")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestHandleRegisterModel(t *testing.T) {
	mr := NewModelRegistry()
	body := `{"name":"sonnet","provider":"anthropic","model":"claude-sonnet-4-6"}`
	req := httptest.NewRequest("POST", "/admin/models", strings.NewReader(body))
	w := httptest.NewRecorder()
	mr.HandleRegister(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	info, ok := mr.GetModel("sonnet")
	if !ok {
		t.Fatal("expected model to be registered")
	}
	if info.Provider != "anthropic" {
		t.Fatalf("provider = %q", info.Provider)
	}
}

func TestHandleRegisterModelMissingName(t *testing.T) {
	mr := NewModelRegistry()
	req := httptest.NewRequest("POST", "/admin/models", strings.NewReader(`{"provider":"openai"}`))
	w := httptest.NewRecorder()
	mr.HandleRegister(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleListModels(t *testing.T) {
	mr := NewModelRegistry()
	mr.RegisterModel("m1", ModelInfo{Provider: "openai", Model: "gpt-4o"})
	mr.RegisterModel("m2", ModelInfo{Provider: "anthropic", Model: "claude-sonnet-4-6"})

	req := httptest.NewRequest("GET", "/admin/models", nil)
	w := httptest.NewRecorder()
	mr.HandleList(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	models, ok := resp["models"].([]interface{})
	if !ok || len(models) != 2 {
		t.Fatalf("expected 2 models under 'models' key, got: %v", resp)
	}
	// Each entry should have name, provider, model fields.
	first := models[0].(map[string]interface{})
	if first["name"] == nil || first["provider"] == nil {
		t.Fatalf("model entry missing fields: %v", first)
	}
}

// ---- RouterConfig ----

func TestRouterConfigDeployAndGet(t *testing.T) {
	rc := NewRouterConfig()
	d := RouterDeployment{Name: "prod", Providers: []string{"openai", "anthropic"}, Strategy: "priority"}
	rc.Deploy(d)

	got, ok := rc.Get("prod")
	if !ok {
		t.Fatal("expected deployment to be found")
	}
	if got.Strategy != "priority" {
		t.Fatalf("strategy mismatch: %s", got.Strategy)
	}
}

func TestRouterConfigList(t *testing.T) {
	rc := NewRouterConfig()
	rc.Deploy(RouterDeployment{Name: "a", Strategy: "priority"})
	rc.Deploy(RouterDeployment{Name: "b", Strategy: "weighted"})

	if len(rc.List()) != 2 {
		t.Fatalf("expected 2 deployments, got %d", len(rc.List()))
	}
}

func TestHandleDeployRouter(t *testing.T) {
	rc := NewRouterConfig()
	body := `{"name":"main","providers":["openai"],"strategy":"least_latency"}`
	req := httptest.NewRequest("POST", "/admin/router", strings.NewReader(body))
	w := httptest.NewRecorder()
	rc.HandleDeploy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_, ok := rc.Get("main")
	if !ok {
		t.Fatal("deployment not stored")
	}
}

func TestHandleDeployMissingName(t *testing.T) {
	rc := NewRouterConfig()
	req := httptest.NewRequest("POST", "/admin/router", strings.NewReader(`{"strategy":"priority"}`))
	w := httptest.NewRecorder()
	rc.HandleDeploy(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleListRouter(t *testing.T) {
	rc := NewRouterConfig()
	rc.Deploy(RouterDeployment{Name: "r1"})

	req := httptest.NewRequest("GET", "/admin/router", nil)
	w := httptest.NewRecorder()
	rc.HandleList(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	deps, ok := resp["deployments"].([]interface{})
	if !ok || len(deps) != 1 {
		t.Fatalf("expected 1 deployment, got: %v", resp["deployments"])
	}
}
