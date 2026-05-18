package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Vedanshu7/llmbridge"
)

// Stream implements llmbridge.Streamer for token-by-token output via SSE.
// The returned channel is closed after the final Delta is sent.
// A Delta with Done == true signals clean end-of-stream.
// A Delta with Err != nil signals a stream failure.
func (p *Provider) Stream(ctx context.Context, req llmbridge.Request) (<-chan llmbridge.Delta, error) {
	body, err := p.marshal(req, true)
	if err != nil {
		return nil, err
	}

	resp, err := p.doStream(ctx, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan llmbridge.Delta, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		p.readSSE(ctx, resp.Body, ch)
	}()
	return ch, nil
}

// Anthropic SSE event names relevant to streaming.
const (
	evContentBlockDelta = "content_block_delta"
	evMessageStop       = "message_stop"
)

// antChunk is the data payload of a content_block_delta SSE event.
type antChunk struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type      string `json:"type"`
		Text      string `json:"text"`        // for text_delta
		PartialJSON string `json:"partial_json"` // for input_json_delta
	} `json:"delta"`
}

func (p *Provider) readSSE(ctx context.Context, body interface{ Read([]byte) (int, error) }, ch chan<- llmbridge.Delta) {
	scanner := bufio.NewScanner(body)

	var (
		currentEvent string
		toolCallID   string
		toolName     string
	)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- llmbridge.Delta{Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")

		switch currentEvent {
		case "content_block_start":
			// Capture tool_use block metadata (id, name) for later deltas.
			var start struct {
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(payload), &start); err == nil {
				if start.ContentBlock.Type == "tool_use" {
					toolCallID = start.ContentBlock.ID
					toolName = start.ContentBlock.Name
				}
			}

		case evContentBlockDelta:
			var chunk antChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				ch <- llmbridge.Delta{Err: fmt.Errorf("anthropic stream: parse delta: %w", err)}
				return
			}
			switch chunk.Delta.Type {
			case "text_delta":
				if chunk.Delta.Text != "" {
					ch <- llmbridge.Delta{Content: chunk.Delta.Text}
				}
			case "input_json_delta":
				if chunk.Delta.PartialJSON != "" {
					ch <- llmbridge.Delta{
						ToolCall: &llmbridge.ToolCall{
							ID:        toolCallID,
							Name:      toolName,
							Arguments: chunk.Delta.PartialJSON,
						},
					}
				}
			}

		case evMessageStop:
			ch <- llmbridge.Delta{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- llmbridge.Delta{Err: fmt.Errorf("anthropic stream: read: %w", err)}
	}
}
