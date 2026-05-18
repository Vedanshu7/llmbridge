package openai

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

	resp, err := p.newStreamRequest(ctx, body)
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

// oaiChunk is a single SSE event from the OpenAI streaming API.
type oaiChunk struct {
	Choices []struct {
		Delta struct {
			Content   string        `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func (p *Provider) readSSE(ctx context.Context, body interface{ Read([]byte) (int, error) }, ch chan<- llmbridge.Delta) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- llmbridge.Delta{Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			ch <- llmbridge.Delta{Done: true}
			return
		}

		var chunk oaiChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			ch <- llmbridge.Delta{Err: fmt.Errorf("openai stream: parse: %w", err)}
			return
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta
		d := llmbridge.Delta{Content: delta.Content}
		for _, tc := range delta.ToolCalls {
			d.ToolCall = &llmbridge.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			}
		}
		if d.Content != "" || d.ToolCall != nil {
			ch <- d
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- llmbridge.Delta{Err: fmt.Errorf("openai stream: read: %w", err)}
	}
}
