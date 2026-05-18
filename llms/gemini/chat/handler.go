package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/Vedanshu7/llmbridge/types"
)

// ReadSSE reads Gemini SSE events from r and sends Deltas to ch.
// Gemini streams newline-delimited JSON objects (not classic SSE lines).
func ReadSSE(ctx context.Context, providerName string, r io.Reader, ch chan<- types.Delta) {
	scanner := bufio.NewScanner(r)
	// Gemini wraps the stream in a JSON array: first line "[", then chunks, then "]"
	// but each event is also sent as SSE "data: <json>\n\n" lines.
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- types.Delta{Err: ctx.Err()}
			return
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "[" || line == "]" || line == "," {
			continue
		}
		line = strings.TrimPrefix(line, "data: ")
		line = strings.TrimRight(line, ",")
		if line == "[DONE]" || line == "" {
			break
		}
		var chunk GeminiStreamChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}
		for _, cand := range chunk.Candidates {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					ch <- types.Delta{Content: part.Text}
				}
			}
			if cand.FinishReason == "STOP" || cand.FinishReason == "MAX_TOKENS" {
				ch <- types.Delta{Done: true}
				return
			}
		}
	}
	ch <- types.Delta{Done: true}
}
