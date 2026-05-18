package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/Vedanshu7/llmbridge/types"
)

// ReadSSE reads Bedrock ConverseStream SSE events and sends Deltas to ch.
// Bedrock streams newline-delimited JSON event objects.
func ReadSSE(ctx context.Context, providerName string, r io.Reader, ch chan<- types.Delta) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- types.Delta{Err: ctx.Err()}
			return
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Strip SSE "data: " prefix if present.
		line = strings.TrimPrefix(line, "data: ")

		var evt ConverseStreamEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.ContentBlockDelta != nil {
			text := evt.ContentBlockDelta.Delta.Text
			if text != "" {
				ch <- types.Delta{Content: text}
			}
		}
		if evt.MessageStop != nil {
			ch <- types.Delta{Done: true}
			return
		}
	}
	ch <- types.Delta{Done: true}
}
