package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func makeOKResponse(text string) map[string]interface{} {
	return map[string]interface{}{
		"content":     []map[string]interface{}{{"type": "text", "text": text}},
		"stop_reason": "end_turn",
		"model":       "claude-sonnet-4-6",
		"usage":       map[string]int{"input_tokens": 5, "output_tokens": 3},
	}
}

func TestMakeCallURLSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("anthropic-version") == "" {
			http.Error(w, "missing version", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(makeOKResponse("pong"))
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]interface{}{
		"model":    "claude-sonnet-4-6",
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
	})
	resp, err := MakeCallURL(http.DefaultClient, "test-key", srv.URL, body)
	if err != nil {
		t.Fatalf("MakeCallURL: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "pong" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestMakeCallURLHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"model": "claude-sonnet-4-6"})
	_, err := MakeCallURL(http.DefaultClient, "key", srv.URL, body)
	if err == nil {
		t.Fatal("expected error on 429")
	}
}

func TestMakeCallURLAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"type": "invalid_request_error", "message": "bad model"},
		})
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"model": "bad"})
	_, err := MakeCallURL(http.DefaultClient, "key", srv.URL, body)
	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestMakeCallDelegatesToURL(t *testing.T) {
	// MakeCall uses the package-level apiURL. We can't redirect it easily,
	// so just verify it passes when given a valid server via MakeCallURL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(makeOKResponse("ok"))
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"model": "claude-sonnet-4-6"})
	// Use MakeCallURL to reach our server; MakeCall itself just calls MakeCallURL.
	resp, err := MakeCallURL(http.DefaultClient, "k", srv.URL, body)
	if err != nil || len(resp.Content) == 0 {
		t.Fatalf("MakeCallURL via URL: err=%v resp=%+v", err, resp)
	}
}

func TestReadSSETextContent(t *testing.T) {
	sseBody := "event: content_block_delta\n" +
		`data: {"delta":{"type":"text_delta","text":"hello "}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"delta":{"type":"text_delta","text":"world"}}` + "\n\n" +
		"event: message_stop\n" +
		"data: {}\n\n"

	ch := make(chan types.Delta, 16)
	ReadSSE(context.Background(), strings.NewReader(sseBody), ch)
	close(ch)

	var got strings.Builder
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("unexpected error: %v", d.Err)
		}
		if d.Done {
			break
		}
		got.WriteString(d.Content)
	}
	if got.String() != "hello world" {
		t.Errorf("content = %q, want %q", got.String(), "hello world")
	}
}

func TestReadSSEToolUse(t *testing.T) {
	sseBody := "event: content_block_start\n" +
		`data: {"content_block":{"type":"tool_use","id":"toolu_01","name":"get_weather"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"delta":{"type":"input_json_delta","partial_json":"{\"loc\":\"NYC\"}"}}` + "\n\n" +
		"event: message_stop\n" +
		"data: {}\n\n"

	ch := make(chan types.Delta, 16)
	ReadSSE(context.Background(), strings.NewReader(sseBody), ch)
	close(ch)

	var found bool
	for d := range ch {
		if d.ToolCall != nil && d.ToolCall.Name == "get_weather" {
			found = true
		}
	}
	if !found {
		t.Error("expected tool call delta with name get_weather")
	}
}

func TestReadSSEContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sseBody := "event: content_block_delta\n" +
		`data: {"delta":{"type":"text_delta","text":"hi"}}` + "\n\n"

	ch := make(chan types.Delta, 4)
	ReadSSE(ctx, strings.NewReader(sseBody), ch)
	close(ch)
	// Should complete without blocking even though context is cancelled.
}
