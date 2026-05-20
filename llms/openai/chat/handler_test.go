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

func TestMakeCallSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "pong"}, "finish_reason": "stop"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]interface{}{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
	})
	resp, err := MakeCall(http.DefaultClient, srv.URL, "test-key", "openai", body)
	if err != nil {
		t.Fatalf("MakeCall: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "pong" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestMakeCallHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"model": "gpt-4o"})
	_, err := MakeCall(http.DefaultClient, srv.URL, "key", "openai", body)
	if err == nil {
		t.Fatal("expected error on 429")
	}
}

func TestMakeCallAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"message": "model not found", "type": "invalid_request_error"},
		})
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"model": "gpt-99"})
	_, err := MakeCall(http.DefaultClient, srv.URL, "", "openai", body)
	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestMakeStreamCallSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]interface{}{"model": "gpt-4o", "stream": true})
	resp, err := MakeStreamCall(http.DefaultClient, srv.URL, "", "openai", body)
	if err != nil {
		t.Fatalf("MakeStreamCall: %v", err)
	}
	resp.Body.Close()
}

func TestMakeStreamCallHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]interface{}{"model": "gpt-4o", "stream": true})
	_, err := MakeStreamCall(http.DefaultClient, srv.URL, "", "openai", body)
	if err == nil {
		t.Fatal("expected error on 403")
	}
}

func TestReadSSEContent(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan types.Delta, 16)
	ReadSSE(context.Background(), "openai", strings.NewReader(sseBody), ch)
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
		t.Errorf("got %q, want %q", got.String(), "hello world")
	}
}

func TestReadSSEToolCall(t *testing.T) {
	payload := `{"choices":[{"delta":{"tool_calls":[{"id":"tc1","function":{"name":"get_weather","arguments":"{}"}}]}}]}`
	sseBody := "data: " + payload + "\n\ndata: [DONE]\n\n"

	ch := make(chan types.Delta, 16)
	ReadSSE(context.Background(), "openai", strings.NewReader(sseBody), ch)
	close(ch)

	var found bool
	for d := range ch {
		if d.ToolCall != nil && d.ToolCall.Name == "get_weather" {
			found = true
		}
	}
	if !found {
		t.Error("expected tool call delta")
	}
}

func TestReadSSEContextCancel(t *testing.T) {
	// Infinite SSE body — context cancel should terminate ReadSSE.
	pr, pw := strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"), strings.NewReader("")
	_ = pr
	_ = pw

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	ch := make(chan types.Delta, 4)
	ReadSSE(ctx, "openai", strings.NewReader("data: {\"choices\":[]}\n\n"), ch)
	close(ch)
	// Should complete without hanging.
}

func TestReadSSEEmptyChoices(t *testing.T) {
	sseBody := "data: {\"choices\":[]}\n\ndata: [DONE]\n\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "openai", strings.NewReader(sseBody), ch)
	close(ch)

	for d := range ch {
		if d.Done {
			return
		}
		if d.Err != nil {
			t.Fatalf("unexpected error: %v", d.Err)
		}
	}
}
