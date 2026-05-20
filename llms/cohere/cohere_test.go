package cohere

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
		"id": "test-id",
		"message": map[string]interface{}{
			"role": "assistant",
			"content": []map[string]interface{}{
				{"type": "text", "text": content},
			},
		},
		"usage": map[string]interface{}{
			"billed_units": map[string]int{
				"input_tokens":  10,
				"output_tokens": 5,
			},
		},
	}
}

func newMockServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("command-r-plus-08-2024", "test-key")
	p.baseURL = srv.URL
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

func TestCompleteSystemMessage(t *testing.T) {
	var captured map[string]interface{}
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(okResponse("ok"))
	})

	p.Complete(context.Background(), types.Request{ //nolint:errcheck
		System:   "You are helpful.",
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
	})

	msgs, _ := captured["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system+user), got %d", len(msgs))
	}
	first := msgs[0].(map[string]interface{})
	if first["role"] != "system" {
		t.Fatalf("first message should be system, got %v", first["role"])
	}
}

func TestCompleteToolCall(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "test-id",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": []interface{}{},
				"tool_calls": []map[string]interface{}{
					{
						"id":   "call_1",
						"type": "function",
						"function": map[string]string{
							"name":      "get_weather",
							"arguments": `{"location":"Berlin"}`,
						},
					},
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
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestCompleteAuthHeader(t *testing.T) {
	var authHeader string
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(okResponse("ok"))
	})
	p.Complete(context.Background(), types.Request{ //nolint:errcheck
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if authHeader != "bearer test-key" {
		t.Fatalf("unexpected auth header: %q", authHeader)
	}
}

// ---- Name / ValidateEnvironment ----

func TestName(t *testing.T) {
	p := New("", "key")
	if p.Name() != "cohere" {
		t.Errorf("Name() = %q, want cohere", p.Name())
	}
}

func TestDefaultModel(t *testing.T) {
	p := New("", "key")
	if p.model != defaultModel {
		t.Errorf("model = %q, want %q", p.model, defaultModel)
	}
}

func TestWithRerankModel(t *testing.T) {
	p := New("", "key")
	p.WithRerankModel("rerank-custom-v1")
	if p.rerankModel != "rerank-custom-v1" {
		t.Errorf("rerankModel = %q, want rerank-custom-v1", p.rerankModel)
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
	// Cohere SSE format
	sseBody := `data: {"type":"content-delta","delta":{"message":{"content":[{"type":"text","text":"hello "}]}}}` + "\n" +
		`data: {"type":"content-delta","delta":{"message":{"content":[{"type":"text","text":"world"}]}}}` + "\n" +
		`data: {"type":"message-end"}` + "\n" +
		"data: [DONE]\n"

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

// redirectTransport rewrites every request's host/scheme to a fixed base URL.
type redirectTransport struct {
	base string
}

func (t *redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	parsed, _ := r.URL.Parse(t.base)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = parsed.Scheme
	r2.URL.Host = parsed.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func TestEmbedSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"embeddings": [][]float64{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
		})
	}))
	t.Cleanup(srv.Close)

	p := New("", "key")
	p.client.Transport = &redirectTransport{base: srv.URL}

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	p := New("", "key")
	p.client.Transport = &redirectTransport{base: srv.URL}

	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

// ---- Rerank ----

func TestRerankSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{"index": 1, "relevance_score": 0.9},
				{"index": 0, "relevance_score": 0.4},
			},
		})
	}))
	t.Cleanup(srv.Close)

	p := New("", "key")
	p.client.Transport = &redirectTransport{base: srv.URL}

	resp, err := p.Rerank(context.Background(), types.RerankRequest{
		Query:     "what is AI?",
		Documents: []string{"AI is cool", "The weather is nice"},
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].Score != 0.9 {
		t.Errorf("unexpected score: %v", resp.Results[0].Score)
	}
}

func TestRerankHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	p := New("", "key")
	p.client.Transport = &redirectTransport{base: srv.URL}

	_, err := p.Rerank(context.Background(), types.RerankRequest{
		Query: "q", Documents: []string{"doc"},
	})
	if err == nil {
		t.Fatal("expected error on 400")
	}
}

// ---- CostForResponse ----

func TestCostForResponseKnown(t *testing.T) {
	resp := &types.Response{
		Model:    "command-r-plus-08-2024",
		Provider: "cohere",
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
	resp := &types.Response{Model: "command-r-plus-08-2024", Provider: "cohere"}
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
		Model:    "command-unknown-xyz",
		Provider: "cohere",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	_, err := CostForResponse(resp)
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}
