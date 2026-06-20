// Package mistral provides a base.LLM backed by the Mistral AI chat completions API.
// It uses the same wire format as OpenAI; auth is a Bearer token.
package mistral

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/openai/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const (
	defaultURL   = "https://api.mistral.ai/v1/chat/completions"
	defaultModel = "mistral-small-latest"
)

// Provider calls the Mistral AI chat completions API.
// Construct with New; do not create the struct directly.
type Provider struct {
	model   string
	apiKey  string
	client  *http.Client
	baseURL string // empty = use defaultURL; overridden in tests
}

// New returns a Mistral Provider.
// If model is empty, "mistral-small-latest" is used.
// If apiKey is empty, MISTRAL_API_KEY is read from the environment at call time.
func New(model, apiKey string) *Provider {
	if model == "" {
		model = defaultModel
	}
	if apiKey == "" {
		apiKey = os.Getenv("MISTRAL_API_KEY")
	}
	return &Provider{
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements base.LLM.
func (p *Provider) Name() string { return "mistral" }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.apiKey == "" && os.Getenv("MISTRAL_API_KEY") == "" {
		return fmt.Errorf("mistral: MISTRAL_API_KEY is not set")
	}
	return nil
}

// Complete sends a blocking chat completions request and returns the full response.
// On rate-limit or server errors it retries once after a 2-second backoff.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToOAIRequest(req, p.model, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
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
		return nil, exceptions.NewProviderError(p.Name(), 0, "empty choices in response", nil)
	}
	return chat.FromOAIResponse(raw, p.Name(), p.model), nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToOAIRequest(req, p.model, true)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	resp, err := p.newStreamConn(body)
	if err != nil {
		return nil, err
	}

	ch := make(chan types.Delta, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		chat.ReadSSE(ctx, p.Name(), resp.Body, ch)
	}()
	return ch, nil
}

func (p *Provider) url() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return defaultURL
}

func (p *Provider) post(body []byte) (*chat.OAIResponse, error) {
	req, err := http.NewRequest(http.MethodPost, p.url(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "read body: "+err.Error(), err)
	}
	if resp.StatusCode != 200 {
		return nil, exceptions.ClassifyHTTPError(p.Name(), resp.StatusCode, raw)
	}

	var out chat.OAIResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "parse: "+err.Error(), err)
	}
	if out.Error != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "API error: "+out.Error.Message, nil)
	}
	return &out, nil
}

func (p *Provider) newStreamConn(body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, p.url(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	streamClient := &http.Client{Transport: p.client.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, exceptions.ClassifyHTTPError(p.Name(), resp.StatusCode, raw)
	}
	return resp, nil
}
