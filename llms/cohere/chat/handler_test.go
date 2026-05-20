package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestReadSSEText(t *testing.T) {
	sseBody := `data: {"type":"content-delta","delta":{"message":{"content":[{"type":"text","text":"hello "}]}}}` + "\n" +
		`data: {"type":"content-delta","delta":{"message":{"content":[{"type":"text","text":"world"}]}}}` + "\n" +
		`data: {"type":"message-end"}` + "\n"

	ch := make(chan types.Delta, 16)
	ReadSSE(context.Background(), "cohere", strings.NewReader(sseBody), ch)
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

func TestReadSSEDone(t *testing.T) {
	sseBody := `data: {"type":"message-end"}` + "\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "cohere", strings.NewReader(sseBody), ch)
	close(ch)

	for d := range ch {
		if d.Done {
			return
		}
	}
	t.Error("expected Done delta")
}

func TestReadSSEDONETerminator(t *testing.T) {
	sseBody := `data: {"type":"content-delta","delta":{"message":{"content":[{"type":"text","text":"hi"}]}}}` + "\n" +
		"data: [DONE]\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "cohere", strings.NewReader(sseBody), ch)
	close(ch)

	var sawDone bool
	for d := range ch {
		if d.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("expected Done from [DONE] terminator")
	}
}

func TestReadSSEContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sseBody := `data: {"type":"content-delta","delta":{"message":{"content":[{"type":"text","text":"hi"}]}}}` + "\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(ctx, "cohere", strings.NewReader(sseBody), ch)
	close(ch)
}

func TestReadSSEInvalidJSON(t *testing.T) {
	// Invalid JSON lines are silently skipped.
	sseBody := "data: not-json\n" + `data: {"type":"message-end"}` + "\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "cohere", strings.NewReader(sseBody), ch)
	close(ch)

	for d := range ch {
		if d.Done {
			return
		}
	}
	t.Error("expected Done delta")
}

func TestReadSSENonDataLines(t *testing.T) {
	// Lines without "data: " prefix are skipped.
	sseBody := "event: message\n" +
		`data: {"type":"message-end"}` + "\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "cohere", strings.NewReader(sseBody), ch)
	close(ch)

	for d := range ch {
		if d.Done {
			return
		}
	}
	t.Error("expected Done delta after non-data line is skipped")
}
