// Package azure provides a base.LLM backed by Azure OpenAI Service.
// It uses the same wire format as OpenAI but with Azure-specific URLs and auth.
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/openai/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const defaultAPIVersion = "2024-02-01"

// Provider calls Azure OpenAI Service using the chat completions API.
// Construct with New; do not create the struct directly.
type Provider struct {
	resource        string
	deployment      string
	apiKey          string
	apiVersion      string
	client          *http.Client
	embedDeployment string // optional; falls back to the chat deployment if empty
}

// New returns an Azure OpenAI Provider.
//   - resource:   Azure resource name (subdomain of openai.azure.com).
//   - deployment: Azure deployment name (maps to a specific model version).
//   - apiKey:     Azure OpenAI API key.
//   - apiVersion: API version string; pass "" to use the default (2024-02-01).
func New(resource, deployment, apiKey, apiVersion string) *Provider {
	if apiVersion == "" {
		apiVersion = defaultAPIVersion
	}
	return &Provider{
		resource:   resource,
		deployment: deployment,
		apiKey:     apiKey,
		apiVersion: apiVersion,
		client:     &http.Client{Timeout: 60 * time.Second},
	}
}

// WithEmbedDeployment sets the Azure deployment name used for Embed calls.
// If not called, Embed falls back to the chat completions deployment.
func (p *Provider) WithEmbedDeployment(deployment string) *Provider {
	p.embedDeployment = deployment
	return p
}

// Name implements base.LLM.
func (p *Provider) Name() string { return "azure" }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.apiKey == "" {
		return fmt.Errorf("azure: API key is not set")
	}
	if p.resource == "" {
		return fmt.Errorf("azure: resource name is not set")
	}
	if p.deployment == "" {
		return fmt.Errorf("azure: deployment name is not set")
	}
	return nil
}

// Complete sends a blocking request to Azure OpenAI and returns the full response.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToOAIRequest(req, p.deployment, false)
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
	return chat.FromOAIResponse(raw, p.Name(), p.deployment), nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToOAIRequest(req, p.deployment, true)
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

// Embed implements base.EmbedProvider against an Azure OpenAI embeddings
// deployment. It uses embedDeployment if set via WithEmbedDeployment,
// otherwise falls back to the chat completions deployment.
func (p *Provider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	wireReq := map[string]interface{}{"input": texts}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	raw, err := p.postURL(p.embedURL(), body)
	if err != nil {
		return nil, err
	}

	var wire struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "parse embeddings: "+err.Error(), err)
	}
	if wire.Error != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "API error: "+wire.Error.Message, nil)
	}

	out := make([][]float64, len(wire.Data))
	for _, d := range wire.Data {
		if d.Index >= 0 && d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
}

func (p *Provider) chatURL() string {
	return fmt.Sprintf(
		"https://%s.openai.azure.com/openai/deployments/%s/chat/completions?api-version=%s",
		p.resource, p.deployment, p.apiVersion,
	)
}

func (p *Provider) embedURL() string {
	deployment := p.embedDeployment
	if deployment == "" {
		deployment = p.deployment
	}
	return fmt.Sprintf(
		"https://%s.openai.azure.com/openai/deployments/%s/embeddings?api-version=%s",
		p.resource, deployment, p.apiVersion,
	)
}

func (p *Provider) post(body []byte) (*chat.OAIResponse, error) {
	req, err := http.NewRequest(http.MethodPost, p.chatURL(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", p.apiKey)

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

// postURL is a generic POST helper that returns the raw response body,
// unlike post() which is hardwired to chatURL() and the chat.OAIResponse shape.
func (p *Provider) postURL(url string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", p.apiKey)

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
	return raw, nil
}

func (p *Provider) newStreamConn(body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, p.chatURL(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("api-key", p.apiKey)

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
