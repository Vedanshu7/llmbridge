// Package openai provides a llmbridge.Provider backed by the OpenAI chat
// completions API. The same adapter handles any OpenAI-compatible endpoint
// (Groq, Together AI, local proxies, etc.) via NewCompatible.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Vedanshu7/llmbridge"
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
//   - name: label shown in logs and error messages (e.g. "groq", "together").
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

// Name implements llmbridge.Provider.
func (p *Provider) Name() string { return p.name }

// Complete sends a blocking request and returns the full response.
// On rate-limit or server errors it retries once after a 2-second backoff.
func (p *Provider) Complete(ctx context.Context, req llmbridge.Request) (*llmbridge.Response, error) {
	body, err := p.marshal(req, false)
	if err != nil {
		return nil, err
	}

	var raw *oaiResponse
	for attempt := range 2 {
		raw, err = p.post(ctx, body)
		if err == nil {
			break
		}
		var rl *llmbridge.ErrRateLimit
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
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: fmt.Errorf("empty choices in response")}
	}

	msg := raw.Choices[0].Message
	resp := &llmbridge.Response{Content: msg.Content}
	for _, tc := range msg.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, llmbridge.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return resp, nil
}

// marshal builds the JSON request body. streaming=true adds "stream": true.
func (p *Provider) marshal(req llmbridge.Request, streaming bool) ([]byte, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	wire := oaiRequest{
		Model:       model,
		Temperature: req.Temperature,
		Stream:      streaming,
	}
	if req.MaxTokens > 0 {
		wire.MaxTokens = req.MaxTokens
	}

	if req.System != "" {
		wire.Messages = append(wire.Messages, oaiMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		wire.Messages = append(wire.Messages, fromMessage(m))
	}
	for _, t := range req.Tools {
		wire.Tools = append(wire.Tools, fromTool(t))
	}

	b, err := json.Marshal(wire)
	if err != nil {
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: fmt.Errorf("marshal: %w", err)}
	}
	return b, nil
}

func (p *Provider) post(ctx context.Context, body []byte) (*oaiResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &llmbridge.ErrTimeout{Provider: p.name, Cause: err}
		}
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: fmt.Errorf("read body: %w", err)}
	}

	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, &llmbridge.ErrAuth{Provider: p.name, Cause: fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)}
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		return nil, &llmbridge.ErrRateLimit{Provider: p.name, Cause: fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)}
	case resp.StatusCode >= 400:
		return nil, &llmbridge.ErrProvider{Provider: p.name, Code: resp.StatusCode, Cause: fmt.Errorf("%s", raw)}
	}

	var out oaiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: fmt.Errorf("parse: %w", err)}
	}
	if out.Error != nil {
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: fmt.Errorf("API error: %s", out.Error.Message)}
	}
	return &out, nil
}

// newStreamRequest creates an HTTP request with streaming enabled.
func (p *Provider) newStreamRequest(ctx context.Context, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	// Use a client without a read deadline for streaming.
	streamClient := &http.Client{Transport: p.client.Transport}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &llmbridge.ErrTimeout{Provider: p.name, Cause: err}
		}
		return nil, &llmbridge.ErrProvider{Provider: p.name, Cause: err}
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		_ = resp.Body.Close()
		return nil, &llmbridge.ErrAuth{Provider: p.name, Cause: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		_ = resp.Body.Close()
		return nil, &llmbridge.ErrRateLimit{Provider: p.name, Cause: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, &llmbridge.ErrProvider{Provider: p.name, Code: resp.StatusCode, Cause: fmt.Errorf("%s", body)}
	}
	return resp, nil
}

// ---- Wire types ----

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	Temperature float64      `json:"temperature"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Parameters  oaiParams   `json:"parameters"`
	} `json:"function"`
}

type oaiParams struct {
	Type       string              `json:"type"`
	Properties map[string]oaiProp `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

type oaiProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type oaiResponse struct {
	Choices []struct {
		Message      oaiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---- Conversion helpers ----

func fromMessage(m llmbridge.Message) oaiMessage {
	out := oaiMessage{
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
	}
	for _, tc := range m.ToolCalls {
		var otc oaiToolCall
		otc.ID = tc.ID
		otc.Type = "function"
		otc.Function.Name = tc.Name
		otc.Function.Arguments = tc.Arguments
		out.ToolCalls = append(out.ToolCalls, otc)
	}
	return out
}

func fromTool(t llmbridge.Tool) oaiTool {
	props := make(map[string]oaiProp, len(t.Parameters.Properties))
	for k, v := range t.Parameters.Properties {
		props[k] = oaiProp{Type: v.Type, Description: v.Description, Enum: v.Enum}
	}
	var out oaiTool
	out.Type = "function"
	out.Function.Name = t.Name
	out.Function.Description = t.Description
	out.Function.Parameters = oaiParams{
		Type:       t.Parameters.Type,
		Properties: props,
		Required:   t.Parameters.Required,
	}
	return out
}
