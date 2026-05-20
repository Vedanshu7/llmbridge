package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestReadSSEText(t *testing.T) {
	chunk1 := `{"candidates":[{"content":{"parts":[{"text":"hello "}]},"finishReason":""}]}`
	chunk2 := `{"candidates":[{"content":{"parts":[{"text":"world"}]},"finishReason":"STOP"}]}`
	sseBody := "data: " + chunk1 + "\n\n" + "data: " + chunk2 + "\n\n"

	ch := make(chan types.Delta, 16)
	ReadSSE(context.Background(), "gemini", strings.NewReader(sseBody), ch)
	close(ch)

	var got strings.Builder
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("unexpected error: %v", d.Err)
		}
		got.WriteString(d.Content)
		if d.Done {
			break
		}
	}
	if got.String() != "hello world" {
		t.Errorf("content = %q, want %q", got.String(), "hello world")
	}
}

func TestReadSSEMaxTokens(t *testing.T) {
	chunk := `{"candidates":[{"content":{"parts":[{"text":"cut"}]},"finishReason":"MAX_TOKENS"}]}`
	sseBody := "data: " + chunk + "\n\n"

	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "gemini", strings.NewReader(sseBody), ch)
	close(ch)

	var sawDone bool
	for d := range ch {
		if d.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("expected Done delta for MAX_TOKENS finish reason")
	}
}

func TestReadSSEContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sseBody := "data: " + `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}` + "\n\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(ctx, "gemini", strings.NewReader(sseBody), ch)
	close(ch)
}

func TestReadSSESkipsArrayDelimiters(t *testing.T) {
	// Gemini sometimes wraps stream in JSON array brackets.
	chunk := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	sseBody := "[\n" + chunk + ",\n]\n"

	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "gemini", strings.NewReader(sseBody), ch)
	close(ch)

	var sawContent bool
	for d := range ch {
		if d.Content == "ok" {
			sawContent = true
		}
	}
	if !sawContent {
		t.Error("expected content from array-wrapped stream")
	}
}

func TestReadSSEDONETerminator(t *testing.T) {
	sseBody := "data: [DONE]\n\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "gemini", strings.NewReader(sseBody), ch)
	close(ch)
	// Should not block or panic.
}

func TestReadSSEInvalidJSON(t *testing.T) {
	// Invalid JSON lines are skipped.
	sseBody := "data: not-json\n\n" +
		"data: " + `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}` + "\n\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "gemini", strings.NewReader(sseBody), ch)
	close(ch)

	var sawDone bool
	for d := range ch {
		if d.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("expected Done after invalid JSON was skipped")
	}
}
