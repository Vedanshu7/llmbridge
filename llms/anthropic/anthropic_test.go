package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		json.NewEncoder(w).Encode(okResponse("hello world"))
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
		json.NewEncoder(w).Encode(okResponse("reply"))
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
		json.NewEncoder(w).Encode(map[string]interface{}{
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
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid api key"}}`))
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
		json.NewEncoder(w).Encode(okResponse("ok"))
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
		json.NewDecoder(r.Body).Decode(&captured)
		json.NewEncoder(w).Encode(okResponse("done"))
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
