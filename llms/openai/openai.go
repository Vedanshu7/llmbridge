// Package openai provides a base.LLM backed by the OpenAI chat completions API.
// The same adapter handles any OpenAI-compatible endpoint via NewCompatible.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/openai/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const (
	defaultURL   = "https://api.openai.com/v1/chat/completions"
	defaultModel = "gpt-4o-mini"
)

// Provider calls the OpenAI chat completions API.
// Construct with New or NewCompatible; do not create the struct directly.
type Provider struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// New returns a Provider backed by OpenAI.
// If model is empty, "gpt-4o-mini" is used.
func New(model, apiKey string) *Provider {
	if model == "" {
		model = defaultModel
	}
	return &Provider{
		name:    "openai",
		baseURL: defaultURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// NewCompatible returns a Provider for any OpenAI-compatible endpoint.
//   - name: label shown in logs and errors (e.g. "groq", "together").
//   - baseURL: full chat completions URL.
//   - apiKey: Bearer token; may be empty for unauthenticated local servers.
//   - model: model identifier required by the endpoint.
func NewCompatible(name, baseURL, apiKey, model string) *Provider {
	return &Provider{
		name:    name,
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements base.LLM.
func (p *Provider) Name() string { return p.name }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.name == "openai" && p.apiKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
		return fmt.Errorf("openai: OPENAI_API_KEY is not set")
	}
	return nil
}

// Complete sends a blocking request and returns the full response.
// On rate-limit or server errors it retries once after a 2-second backoff.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToOAIRequest(req, p.model, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}

	var raw *chat.OAIResponse
	for attempt := range 2 {
		raw, err = p.post(body)
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

	if len(raw.Choices) == 0 {
		return nil, exceptions.NewProviderError(p.name, 0, "empty choices in response", nil)
	}
	return chat.FromOAIResponse(raw, p.name, wireReq.Model), nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToOAIRequest(req, p.model, true)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}

	resp, err := p.newStreamConn(body)
	if err != nil {
		return nil, err
	}

	ch := make(chan types.Delta, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		chat.ReadSSE(ctx, p.name, resp.Body, ch)
	}()
	return ch, nil
}
