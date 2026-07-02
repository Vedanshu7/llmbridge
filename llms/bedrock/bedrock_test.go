package bedrock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

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

// newMockServer creates a test server and wires it into a Provider via redirectTransport.
func newMockServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("anthropic.claude-3-5-sonnet-20241022-v2:0", "us-east-1", "AKIATEST", "testsecret")
	p.client.Transport = &redirectTransport{base: srv.URL}
	return srv, p
}

func converseOKResponse(content string) map[string]interface{} {
	return map[string]interface{}{
		"output": map[string]interface{}{
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": []map[string]interface{}{{"type": "text", "text": content}},
			},
		},
		"stopReason": "end_turn",
		"usage":      map[string]int{"inputTokens": 10, "outputTokens": 5, "totalTokens": 15},
	}
}

// ---- Name / ValidateEnvironment ----

func TestName(t *testing.T) {
	p := New("model", "us-east-1", "key", "secret")
	if p.Name() != "bedrock" {
		t.Errorf("Name() = %q, want bedrock", p.Name())
	}
}

func TestValidateEnvironmentMissingKey(t *testing.T) {
	p := New("model", "us-east-1", "", "secret")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing access key")
	}
}

func TestValidateEnvironmentMissingSecret(t *testing.T) {
	p := New("model", "us-east-1", "key", "")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing secret key")
	}
}

func TestValidateEnvironmentMissingRegion(t *testing.T) {
	p := New("model", "", "key", "secret")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing region")
	}
}

func TestValidateEnvironmentMissingModel(t *testing.T) {
	p := New("", "us-east-1", "key", "secret")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing model ID")
	}
}

func TestValidateEnvironmentOK(t *testing.T) {
	p := New("model", "us-east-1", "key", "secret")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---- Complete ----

func TestCompleteBasic(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(converseOKResponse("hello bedrock"))
	})
	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello bedrock" {
		t.Errorf("Content = %q, want %q", resp.Content, "hello bedrock")
	}
}

func TestCompleteUsage(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(converseOKResponse("ok"))
	})
	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage data")
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestCompleteWithRemoteImageURLReturnsError(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not reach the server for an unsupported remote image URL")
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{
			{
				Role: "user",
				Parts: []types.ContentPart{
					{Type: "image_url", ImageURL: "https://example.com/cat.png"},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for remote image URL, got nil")
	}
}

func TestCompleteHTTPError(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Access denied"}`))
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestCompleteSetsAuthHeader(t *testing.T) {
	var authHeader string
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(converseOKResponse("ok"))
	})
	p.Complete(context.Background(), types.Request{ //nolint:errcheck
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	// SigV4 auth header starts with "AWS4-HMAC-SHA256".
	if !strings.HasPrefix(authHeader, "AWS4-HMAC-SHA256") {
		t.Errorf("unexpected auth header: %q", authHeader)
	}
}

// ---- Stream ----

func TestStreamSuccess(t *testing.T) {
	// Bedrock ConverseStream uses chunked JSON events in an event-stream.
	// Each line is a "contentBlockDelta" event with text content.
	event1 := `{"contentBlockDelta":{"delta":{"text":"hello "},"contentBlockIndex":0}}` + "\n"
	event2 := `{"contentBlockDelta":{"delta":{"text":"world"},"contentBlockIndex":0}}` + "\n"
	event3 := `{"messageStop":{"stopReason":"end_turn"}}` + "\n"

	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(event1 + event2 + event3))
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
		t.Errorf("streamed = %q, want %q", got.String(), "hello world")
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
			"embedding": []float64{0.1, 0.2, 0.3},
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
		t.Errorf("embedding[0][0] = %v, want 0.1", result[0][0])
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
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Provider: "bedrock",
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
	resp := &types.Response{Model: "anthropic.claude-3-5-sonnet-20241022-v2:0", Provider: "bedrock"}
	cost, err := CostForResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost != 0 {
		t.Errorf("expected 0 for nil usage, got %f", cost)
	}
}

func TestCostForResponsePrefixMatch(t *testing.T) {
	// Model ID with a suffix like ":0" should match prefix.
	resp := &types.Response{
		Model:    "anthropic.claude-3-5-haiku-20241022:0",
		Provider: "bedrock",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	cost, err := CostForResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost <= 0 {
		t.Errorf("expected positive cost for prefix match, got %f", cost)
	}
}

func TestCostForResponseUnknownModel(t *testing.T) {
	resp := &types.Response{
		Model:    "unknown.model-xyz",
		Provider: "bedrock",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	_, err := CostForResponse(resp)
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}
