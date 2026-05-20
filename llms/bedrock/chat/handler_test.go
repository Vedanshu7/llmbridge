package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestReadSSEText(t *testing.T) {
	event1 := `{"contentBlockDelta":{"delta":{"text":"hello "},"contentBlockIndex":0}}` + "\n"
	event2 := `{"contentBlockDelta":{"delta":{"text":"world"},"contentBlockIndex":0}}` + "\n"
	event3 := `{"messageStop":{"stopReason":"end_turn"}}` + "\n"

	ch := make(chan types.Delta, 16)
	ReadSSE(context.Background(), "bedrock", strings.NewReader(event1+event2+event3), ch)
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

func TestReadSSEContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan types.Delta, 4)
	ReadSSE(ctx, "bedrock", strings.NewReader(`{"contentBlockDelta":{"delta":{"text":"hi"}}}`+"\n"), ch)
	close(ch)
}

func TestReadSSEEmptyLines(t *testing.T) {
	body := "\n\n" + `{"messageStop":{"stopReason":"end_turn"}}` + "\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "bedrock", strings.NewReader(body), ch)
	close(ch)

	for d := range ch {
		if d.Done {
			return
		}
	}
	t.Error("expected Done delta")
}

func TestReadSSEInvalidJSON(t *testing.T) {
	// Invalid JSON should be skipped (no error sent).
	body := "not-json\n" + `{"messageStop":{"stopReason":"end_turn"}}` + "\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "bedrock", strings.NewReader(body), ch)
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

func TestReadSSENoStop(t *testing.T) {
	// Stream ends without messageStop — should still close with Done.
	body := `{"contentBlockDelta":{"delta":{"text":"hi"}}}` + "\n"
	ch := make(chan types.Delta, 4)
	ReadSSE(context.Background(), "bedrock", strings.NewReader(body), ch)
	close(ch)

	var sawDone bool
	for d := range ch {
		if d.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("expected Done delta even when stream ends without messageStop")
	}
}
