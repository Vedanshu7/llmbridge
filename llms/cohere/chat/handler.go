package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/Vedanshu7/llmbridge/types"
)

// ReadSSE reads Cohere SSE events and sends Deltas to ch.
func ReadSSE(ctx context.Context, providerName string, r io.Reader, ch chan<- types.Delta) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- types.Delta{Err: ctx.Err()}
			return
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[len("data: "):]
		if data == "[DONE]" {
			ch <- types.Delta{Done: true}
			return
		}
		var evt CohereStreamEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "content-delta":
			if evt.Delta != nil && evt.Delta.Message != nil {
				for _, block := range evt.Delta.Message.Content {
					if block.Type == "text" && block.Text != "" {
						ch <- types.Delta{Content: block.Text}
					}
				}
			}
		case "message-end":
			ch <- types.Delta{Done: true}
			return
		}
	}
	ch <- types.Delta{Done: true}
}
