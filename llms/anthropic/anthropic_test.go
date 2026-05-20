package anthropic

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

func okResponse(content string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": content},
		},
		"stop_reason": "end_turn",
		"model":       "claude-sonnet-4-6",
		"usage": map[string]int{
			"input_tokens":  10,
			"output_tokens": 5,
		},
	}
}

func newMockServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("claude-sonnet-4-6", "test-key")
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

func TestCompleteToolCall(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type":  "tool_use",
					"id":    "toolu_01",
					"name":  "get_weather",
					"input": map[string]string{"location": "Paris"},
				},
			},
			"stop_reason": "tool_use",
			"model":       "claude-sonnet-4-6",
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
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid api key"}}`))
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestCompleteAPIVersionHeader(t *testing.T) {
	var capturedVersion string
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedVersion = r.Header.Get("anthropic-version")
		_ = json.NewEncoder(w).Encode(okResponse("ok"))
	})
	p.Complete(context.Background(), types.Request{ //nolint:errcheck
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if capturedVersion != apiVersion {
		t.Fatalf("unexpected anthropic-version header: %q", capturedVersion)
	}
}

func TestCompleteToolResultMerging(t *testing.T) {
	var captured map[string]interface{}
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(okResponse("done"))
	})

	p.Complete(context.Background(), types.Request{ //nolint:errcheck
		Messages: []types.Message{
			{Role: "user", Content: "use a tool"},
			{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "t1", Name: "fn", Arguments: `{}`}}},
			{Role: "tool", ToolCallID: "t1", Content: "result1"},
			{Role: "tool", ToolCallID: "t1", Content: "result2"},
		},
	})

	msgs, _ := captured["messages"].([]interface{})
	// 4 source messages → 3 wire messages: user, assistant(tool_use), user(merged tool_results)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 wire messages after tool merging, got %d", len(msgs))
	}
	last := msgs[2].(map[string]interface{})
	if last["role"] != "user" {
		t.Fatalf("merged tool results should have role=user, got %v", last["role"])
	}
	content, _ := last["content"].([]interface{})
	if len(content) != 2 {
		t.Fatalf("expected 2 tool_result blocks, got %d", len(content))
	}
}

// ---- Name / ValidateEnvironment ----

func TestName(t *testing.T) {
	p := New("", "key")
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q, want anthropic", p.Name())
	}
}

func TestDefaultModel(t *testing.T) {
	p := New("", "key")
	if p.model != defaultModel {
		t.Errorf("model = %q, want %q", p.model, defaultModel)
	}
}

func TestValidateEnvironmentMissingKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	p := New("claude-sonnet-4-6", "")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error when key is empty and env not set")
	}
}

func TestValidateEnvironmentFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	p := New("claude-sonnet-4-6", "")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error when key is in env: %v", err)
	}
}

// ---- Stream ----

func TestStreamSuccess(t *testing.T) {
	// Anthropic SSE format
	sseBody := "event: content_block_delta\n" +
		`data: {"delta":{"type":"text_delta","text":"hello "}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"delta":{"type":"text_delta","text":"world"}}` + "\n\n" +
		"event: message_stop\n" +
		"data: {}\n\n"

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

func TestCostForResponseKnown(t *testing.T) {
	resp := &types.Response{
		Model:    "claude-sonnet-4-6",
		Provider: "anthropic",
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
	resp := &types.Response{Model: "claude-sonnet-4-6", Provider: "anthropic"}
	_, err := CostForResponse(resp)
	if err == nil {
		t.Fatal("expected error for nil usage")
	}
}

func TestCostForResponseUnknownModel(t *testing.T) {
	resp := &types.Response{
		Model:    "claude-unknown-xyz",
		Provider: "anthropic",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	_, err := CostForResponse(resp)
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}
