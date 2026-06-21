package deepseek

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

func newTestProvider(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("deepseek-chat", "test-key")
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

func reasonerBody(reasoning, content, model string) map[string]interface{} {
	return map[string]interface{}{
		"choices": []map[string]interface{}{
			{
				"message": map[string]string{
					"role":              "assistant",
					"content":           content,
					"reasoning_content": reasoning,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 50, "total_tokens": 70},
		"model": model,
	}
}

// ---- ValidateEnvironment ----

func TestValidateEnvironmentWithKey(t *testing.T) {
	p := New("deepseek-chat", "test-key")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateEnvironmentMissingKey(t *testing.T) {
	p := &Provider{model: "deepseek-chat", apiKey: ""}
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing api key")
	}
}

// ---- Name / defaults ----

func TestName(t *testing.T) {
	p := New("", "k")
	if p.Name() != "deepseek" {
		t.Errorf("Name() = %q, want deepseek", p.Name())
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
		json.NewEncoder(w).Encode(okBody("hello from deepseek", "deepseek-chat")) //nolint:errcheck
	})

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello from deepseek" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Provider != "deepseek" {
		t.Errorf("Provider = %q", resp.Provider)
	}
}

func TestCompleteWithReasoningContent(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reasonerBody( //nolint:errcheck
			"step 1: think hard\nstep 2: conclude",
			"42 is the answer",
			"deepseek-reasoner",
		))
	})
	p.model = "deepseek-reasoner"

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "what is the answer?"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.HasPrefix(resp.Content, "<think>") {
		t.Errorf("expected <think> block in content, got: %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "step 1: think hard") {
		t.Errorf("expected reasoning in content, got: %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "42 is the answer") {
		t.Errorf("expected answer in content, got: %q", resp.Content)
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
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limited"}}`)) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(okBody("ok", "deepseek-chat")) //nolint:errcheck
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
	_ = resp
}

// ---- ExtractReasoning ----

func TestExtractReasoning(t *testing.T) {
	cases := []struct {
		name          string
		content       string
		wantReasoning string
		wantAnswer    string
	}{
		{
			name:          "with think block",
			content:       "<think>\nI reasoned carefully\n</think>\nThe answer is 42",
			wantReasoning: "I reasoned carefully",
			wantAnswer:    "The answer is 42",
		},
		{
			name:          "no think block",
			content:       "Just the answer",
			wantReasoning: "",
			wantAnswer:    "Just the answer",
		},
		{
			name:          "empty content",
			content:       "",
			wantReasoning: "",
			wantAnswer:    "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, a := ExtractReasoning(c.content)
			if r != c.wantReasoning {
				t.Errorf("reasoning = %q, want %q", r, c.wantReasoning)
			}
			if a != c.wantAnswer {
				t.Errorf("answer = %q, want %q", a, c.wantAnswer)
			}
		})
	}
}

func TestHasReasoningContent(t *testing.T) {
	if !HasReasoningContent(&types.Response{Content: "<think>\nyes\n</think>\nfinal"}) {
		t.Error("expected HasReasoningContent to return true")
	}
	if HasReasoningContent(&types.Response{Content: "no think block"}) {
		t.Error("expected HasReasoningContent to return false")
	}
}

// ---- CostForResponse ----

func TestCostForResponse(t *testing.T) {
	cases := []struct {
		model   string
		wantErr bool
	}{
		{"deepseek-chat", false},
		{"deepseek-reasoner", false},
		{"unknown-model", true},
	}
	for _, c := range cases {
		resp := &types.Response{
			Model: c.model,
			Usage: &types.UsageData{PromptTokens: 1000, CompletionTokens: 500},
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
