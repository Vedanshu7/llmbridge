package azure

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/types"
)

// redirectTransport rewrites every request's scheme and host to a fixed base URL,
// allowing tests to intercept traffic without changing provider-internal URL logic.
type redirectTransport struct{ base string }

func (rt *redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	parsed, _ := r.URL.Parse(rt.base)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = parsed.Scheme
	r2.URL.Host = parsed.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func newTestProvider(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("myresource", "my-gpt4o", "test-key", "")
	p.client = &http.Client{Transport: &redirectTransport{base: srv.URL}}
	return srv, p
}

func okBody(content, model string) map[string]interface{} {
	return map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"},
		},
		"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		"model": model,
	}
}

// ---- ValidateEnvironment ----

func TestValidateEnvironmentOK(t *testing.T) {
	p := New("res", "dep", "key", "")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateEnvironmentMissingKey(t *testing.T) {
	p := New("res", "dep", "", "")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing api key")
	}
}

func TestValidateEnvironmentMissingResource(t *testing.T) {
	p := New("", "dep", "key", "")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing resource")
	}
}

func TestValidateEnvironmentMissingDeployment(t *testing.T) {
	p := New("res", "", "key", "")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing deployment")
	}
}

// ---- Name & defaults ----

func TestName(t *testing.T) {
	p := New("r", "d", "k", "")
	if p.Name() != "azure" {
		t.Errorf("Name() = %q, want %q", p.Name(), "azure")
	}
}

func TestDefaultAPIVersion(t *testing.T) {
	p := New("r", "d", "k", "")
	if p.apiVersion != defaultAPIVersion {
		t.Errorf("apiVersion = %q, want %q", p.apiVersion, defaultAPIVersion)
	}
}

func TestCustomAPIVersion(t *testing.T) {
	p := New("r", "d", "k", "2025-01-01")
	if p.apiVersion != "2025-01-01" {
		t.Errorf("apiVersion = %q, want 2025-01-01", p.apiVersion)
	}
}

// ---- chatURL ----

func TestChatURL(t *testing.T) {
	p := New("myres", "mydep", "key", "2024-02-01")
	url := p.chatURL()
	if !strings.Contains(url, "myres.openai.azure.com") {
		t.Errorf("URL missing resource: %s", url)
	}
	if !strings.Contains(url, "mydep") {
		t.Errorf("URL missing deployment: %s", url)
	}
	if !strings.Contains(url, "2024-02-01") {
		t.Errorf("URL missing api-version: %s", url)
	}
}

func TestEmbedURLFallsBackToChatDeployment(t *testing.T) {
	p := New("myres", "mydep", "key", "2024-02-01")
	url := p.embedURL()
	if !strings.Contains(url, "mydep") {
		t.Errorf("URL missing chat deployment fallback: %s", url)
	}
	if !strings.Contains(url, "/embeddings") {
		t.Errorf("URL missing /embeddings path: %s", url)
	}
}

func TestEmbedURLUsesOverride(t *testing.T) {
	p := New("myres", "mydep", "key", "2024-02-01")
	p.WithEmbedDeployment("my-embed-dep")
	url := p.embedURL()
	if !strings.Contains(url, "my-embed-dep") {
		t.Errorf("URL missing embed deployment override: %s", url)
	}
	if strings.Contains(url, "/mydep/") {
		t.Errorf("URL should not use chat deployment when override is set: %s", url)
	}
}

// ---- Embed ----

func TestEmbedSuccess(t *testing.T) {
	var gotPath, gotQuery string
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}, "index": 0},
			},
		})
	})

	out, err := p.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(out) != 1 || len(out[0]) != 3 {
		t.Fatalf("unexpected embedding output: %+v", out)
	}
	if out[0][1] != 0.2 {
		t.Errorf("unexpected embedding value: %v", out[0])
	}
	if !strings.Contains(gotPath, "/openai/deployments/my-gpt4o/embeddings") {
		t.Errorf("unexpected request path: %s", gotPath)
	}
	if !strings.Contains(gotQuery, "api-version=") {
		t.Errorf("unexpected request query: %s", gotQuery)
	}
}

func TestEmbedUsesEmbedDeploymentOverride(t *testing.T) {
	var gotPath string
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{{"embedding": []float64{0.5}, "index": 0}},
		})
	})
	p.WithEmbedDeployment("text-embed-dep")

	if _, err := p.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !strings.Contains(gotPath, "/openai/deployments/text-embed-dep/embeddings") {
		t.Errorf("expected embed deployment override in path, got %s", gotPath)
	}
}

func TestEmbedFallsBackToChatDeployment(t *testing.T) {
	var gotPath string
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{{"embedding": []float64{0.5}, "index": 0}},
		})
	})

	if _, err := p.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !strings.Contains(gotPath, "/openai/deployments/my-gpt4o/embeddings") {
		t.Errorf("expected chat deployment fallback in path, got %s", gotPath)
	}
}

func TestEmbedHTTPError(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
	})

	if _, err := p.Embed(context.Background(), []string{"hello"}); err == nil {
		t.Fatal("expected error for HTTP 400 response")
	}
}

// ---- Complete ----

func TestCompleteSuccess(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "test-key" {
			http.Error(w, "missing api-key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(okBody("bonjour", "my-gpt4o")) //nolint:errcheck
	})

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "say bonjour"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "bonjour" {
		t.Errorf("Content = %q, want %q", resp.Content, "bonjour")
	}
	if resp.Provider != "azure" {
		t.Errorf("Provider = %q, want azure", resp.Provider)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestCompleteEmptyChoices(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{}}) //nolint:errcheck
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestCompleteHTTP401(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	var authErr *exceptions.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
}

func TestCompleteHTTP429WithRetry(t *testing.T) {
	calls := 0
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(okBody("ok", "my-gpt4o")) //nolint:errcheck
	})
	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 attempts, got %d", calls)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q", resp.Content)
	}
}

func TestCompleteHTTP429ExhaustedRetries(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	_, err := p.Complete(ctx, types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when context cancelled")
	}
}

// ---- Stream ----

func TestStreamSuccess(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseBody)) //nolint:errcheck
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
	if got.String() != "hello" {
		t.Errorf("streamed content = %q, want %q", got.String(), "hello")
	}
}

func TestStreamHTTP401(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	_, err := p.Stream(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	var authErr *exceptions.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
}

// ---- CostForResponse ----

func TestCostForResponseKnownModel(t *testing.T) {
	resp := &types.Response{
		Model:    "gpt-4o",
		Provider: "azure",
		Usage:    &types.UsageData{PromptTokens: 1000, CompletionTokens: 500},
	}
	cost, err := CostForResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost <= 0 {
		t.Errorf("expected positive cost, got %f", cost)
	}
}

func TestCostForResponseNilUsage(t *testing.T) {
	resp := &types.Response{Model: "gpt-4o", Provider: "azure"}
	cost, err := CostForResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost != 0 {
		t.Errorf("expected 0 cost for nil usage, got %f", cost)
	}
}

func TestCostForResponseUnknownModel(t *testing.T) {
	resp := &types.Response{
		Model:    "unknown-deployment-xyz",
		Provider: "azure",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	_, err := CostForResponse(resp)
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestCostForResponseEmbedding(t *testing.T) {
	resp := &types.Response{
		Model:    "text-embedding-3-small",
		Provider: "azure",
		Usage:    &types.UsageData{PromptTokens: 500, CompletionTokens: 0},
	}
	cost, err := CostForResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// input only, output=0
	if cost <= 0 {
		t.Errorf("expected positive cost for embedding, got %f", cost)
	}
}
