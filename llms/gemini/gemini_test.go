package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func okResponse(content string) map[string]interface{} {
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
		"usageMetadata": map[string]int{
			"promptTokenCount":     10,
			"candidatesTokenCount": 5,
			"totalTokenCount":      15,
		},
	}
}

func newMockServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("gemini-2.0-flash", "test-key")
	p.apiBaseURL = srv.URL
	return srv, p
}

func TestCompleteBasic(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okResponse("hello world"))
	})

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello world" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
}

func TestCompleteUsage(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(okResponse("reply"))
	})
	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage data")
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestCompleteToolCall(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"candidates": []map[string]interface{}{
				{
					"content": map[string]interface{}{
						"role": "model",
						"parts": []map[string]interface{}{
							{
								"functionCall": map[string]interface{}{
									"name": "get_weather",
									"args": map[string]string{"location": "Tokyo"},
								},
							},
						},
					},
					"finishReason": "STOP",
				},
			},
		})
	})

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "weather?"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("unexpected tool name: %s", resp.ToolCalls[0].Name)
	}
}

func TestCompleteHTTPError(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"API key not valid","status":"UNAUTHENTICATED"}}`))
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestCompleteEmptyCandidates(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"candidates": []interface{}{},
		})
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestCompleteURLContainsModel(t *testing.T) {
	var requestPath string
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(okResponse("ok"))
	})
	p.Complete(context.Background(), types.Request{ //nolint:errcheck
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if !strings.Contains(requestPath, "gemini-2.0-flash") {
		t.Fatalf("request path should contain model name, got %q", requestPath)
	}
}

// ---- Name / ValidateEnvironment ----

func TestName(t *testing.T) {
	p := New("", "key")
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want gemini", p.Name())
	}
}

func TestDefaultModel(t *testing.T) {
	p := New("", "key")
	if p.model != defaultModel {
		t.Errorf("model = %q, want %q", p.model, defaultModel)
	}
}

func TestValidateEnvironmentMissingKey(t *testing.T) {
	p := New("", "")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestValidateEnvironmentWithKey(t *testing.T) {
	p := New("", "my-key")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---- Stream ----

func TestStreamSuccess(t *testing.T) {
	// Gemini SSE format: each event is "data: <json>\n\n"
	chunk := `{"candidates":[{"content":{"parts":[{"text":"hello "}]},"finishReason":""}]}`
	chunk2 := `{"candidates":[{"content":{"parts":[{"text":"world"}]},"finishReason":"STOP"}]}`
	sseBody := "data: " + chunk + "\n\n" + "data: " + chunk2 + "\n\n"

	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
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

func TestStreamHTTPError(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	_, err := p.Stream(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from Stream on 403")
	}
}

// ---- Embed ----

func TestEmbedSuccess(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"embeddings": []map[string]interface{}{
				{"values": []float64{0.1, 0.2, 0.3}},
				{"values": []float64{0.4, 0.5, 0.6}},
			},
		})
	})

	result, err := p.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(result))
	}
	if result[0][0] != 0.1 {
		t.Errorf("unexpected embedding[0][0]: %v", result[0][0])
	}
}

func TestEmbedHTTPError(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

// ---- CostForResponse ----

func TestCostForResponseKnown(t *testing.T) {
	resp := &types.Response{
		Model:    "gemini-2.0-flash",
		Provider: "gemini",
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
	resp := &types.Response{Model: "gemini-2.0-flash", Provider: "gemini"}
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
		Model:    "gemini-unknown-xyz",
		Provider: "gemini",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	_, err := CostForResponse(resp)
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}
