// Package gemini provides a base.LLM backed by the Google Gemini API.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/gemini/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const (
	baseURL      = "https://generativelanguage.googleapis.com/v1beta/models"
	defaultModel = "gemini-2.0-flash"
)

// Provider calls the Google Gemini generateContent API.
// Construct with New; do not create the struct directly.
type Provider struct {
	model  string
	apiKey string
	client *http.Client
}

// New returns a Provider backed by Google Gemini.
// If model is empty, "gemini-2.0-flash" is used.
func New(model, apiKey string) *Provider {
	if model == "" {
		model = defaultModel
	}
	return &Provider{
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements base.LLM.
func (p *Provider) Name() string { return "gemini" }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.apiKey == "" {
		return fmt.Errorf("gemini: API key is not set")
	}
	return nil
}

// Complete sends a blocking request to Gemini and returns the full response.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToGeminiRequest(req, p.model, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	raw, err := p.post(body)
	if err != nil {
		return nil, err
	}
	if len(raw.Candidates) == 0 {
		return nil, exceptions.NewProviderError(p.Name(), 0, "empty candidates in response", nil)
	}
	return chat.FromGeminiResponse(raw, p.Name(), p.model), nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToGeminiRequest(req, p.model, true)
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

func (p *Provider) generateContentURL() string {
	return fmt.Sprintf("%s/%s:generateContent?key=%s", baseURL, p.model, p.apiKey)
}

func (p *Provider) streamGenerateContentURL() string {
	return fmt.Sprintf("%s/%s:streamGenerateContent?key=%s&alt=sse", baseURL, p.model, p.apiKey)
}
