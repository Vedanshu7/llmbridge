package mistral

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/types"
)

func newTestProvider(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("mistral-small-latest", "test-key")
	p.baseURL = srv.URL
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

func TestValidateEnvironmentWithKey(t *testing.T) {
	p := New("mistral-small-latest", "test-key")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateEnvironmentMissingKey(t *testing.T) {
	p := &Provider{model: "mistral-small-latest", apiKey: ""}
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing api key")
	}
}

// ---- Name ----

func TestName(t *testing.T) {
	p := New("", "k")
	if p.Name() != "mistral" {
		t.Errorf("Name() = %q, want mistral", p.Name())
	}
}

func TestDefaultModel(t *testing.T) {
	p := New("", "k")
	if p.model != defaultModel {
		t.Errorf("model = %q, want %q", p.model, defaultModel)
	}
}

// ---- Complete ----

func TestCompleteSuccess(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(okBody("hello from mistral", "mistral-small-latest")) //nolint:errcheck
	})

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello from mistral" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Provider != "mistral" {
		t.Errorf("Provider = %q", resp.Provider)
	}
}

func TestCompleteHTTP401(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"unauthorized","type":"auth_error"}}`)) //nolint:errcheck
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected auth error")
	}
	var authErr *exceptions.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Errorf("expected AuthenticationError, got %T: %v", err, err)
	}
}

func TestCompleteHTTP429Retry(t *testing.T) {
	calls := 0
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limited"}}`)) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(okBody("ok", "mistral-small-latest")) //nolint:errcheck
	})
	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (retry), got %d", calls)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q", resp.Content)
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

// ---- CostForResponse ----

func TestCostForResponse(t *testing.T) {
	cases := []struct {
		model    string
		prompt   int
		complete int
		wantErr  bool
	}{
		{"mistral-small-latest", 1000, 500, false},
		{"mistral-large-latest", 1000, 500, false},
		{"unknown-model", 1000, 500, true},
	}
	for _, c := range cases {
		resp := &types.Response{
			Model: c.model,
			Usage: &types.UsageData{PromptTokens: c.prompt, CompletionTokens: c.complete},
		}
		cost, err := CostForResponse(resp)
		if c.wantErr {
			if err == nil {
				t.Errorf("[%s] expected error", c.model)
			}
		} else {
			if err != nil {
				t.Errorf("[%s] unexpected error: %v", c.model, err)
			}
			if cost <= 0 {
				t.Errorf("[%s] expected positive cost, got %f", c.model, cost)
			}
		}
	}
}
