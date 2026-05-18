// Package anthropic provides a base.LLM backed by the Anthropic Messages API
// (Claude Opus, Sonnet, Haiku families).
//
// Key wire-format differences from OpenAI that this package handles:
//   - system prompt is a top-level field, not a "system" role message
//   - response content is an array of typed blocks (text, tool_use)
//   - consecutive "tool" role messages must be merged into a single "user"
//     message containing an array of tool_result blocks
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/anthropic/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const defaultModel = "claude-sonnet-4-6"

// Provider calls the Anthropic Messages API.
// Construct with New; do not create the struct directly.
type Provider struct {
	apiKey string
	model  string
	client *http.Client
}

// New returns a Provider backed by Anthropic Claude.
// model may be e.g. "claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5-20251001".
// If model is empty, "claude-sonnet-4-6" is used.
func New(model, apiKey string) *Provider {
	if model == "" {
		model = defaultModel
	}
	return &Provider{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements base.LLM.
func (p *Provider) Name() string { return "anthropic" }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.apiKey == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		return fmt.Errorf("anthropic: ANTHROPIC_API_KEY is not set")
	}
	return nil
}

// Complete sends a blocking request and returns the full response.
// On rate-limit or server errors it retries once after a 2-second backoff.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToAntRequest(req, p.model, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, "marshal: "+err.Error(), err)
	}

	var raw *chat.AntResponse
	for attempt := range 2 {
		raw, err = p.makeCall(body)
		if err == nil {
			break
		}
		var rl *exceptions.RateLimitError
		if attempt == 0 && errors.As(err, &rl) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		return nil, err
	}

	return chat.FromAntResponse(raw, "anthropic"), nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToAntRequest(req, p.model, true)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, "marshal: "+err.Error(), err)
	}

	resp, err := p.doStream(body)
	if err != nil {
		return nil, err
	}

	ch := make(chan types.Delta, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		chat.ReadSSE(ctx, resp.Body, ch)
	}()
	return ch, nil
}
