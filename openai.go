package llmbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const openAIDefaultURL = "https://api.openai.com/v1/chat/completions"
const openAIDefaultModel = "gpt-4o-mini"

// OpenAIProvider calls the OpenAI chat completions API (or any compatible endpoint).
type OpenAIProvider struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewOpenAI returns a provider backed by OpenAI.
func NewOpenAI(model, apiKey string) Provider {
	if model == "" {
		model = openAIDefaultModel
	}
	return &OpenAIProvider{
		name:    "openai",
		baseURL: openAIDefaultURL,
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// NewOpenAICompatible returns a provider for any OpenAI-compatible endpoint.
// name is a label shown in logs (e.g. "groq", "together").
// baseURL is the full chat completions URL.
// apiKey may be empty for unauthenticated local endpoints.
func NewOpenAICompatible(name, baseURL, apiKey, model string) Provider {
	return &OpenAIProvider{
		name:    name,
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *OpenAIProvider) Name() string { return p.name }

// openAI wire types

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	Temperature float64      `json:"temperature"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Store       bool         `json:"store"`
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
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  oaiToolParams   `json:"parameters"`
	} `json:"function"`
}

type oaiToolParams struct {
	Type       string                 `json:"type"`
	Properties map[string]oaiToolProp `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

type oaiToolProp struct {
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

func (p *OpenAIProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	wire := oaiRequest{
		Model:       model,
		Temperature: req.Temperature,
		Store:       false,
	}
	if req.MaxTokens > 0 {
		wire.MaxTokens = req.MaxTokens
	}

	// System prompt as first message.
	if req.System != "" {
		wire.Messages = append(wire.Messages, oaiMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		wire.Messages = append(wire.Messages, toOAIMessage(m))
	}

	for _, t := range req.Tools {
		wire.Tools = append(wire.Tools, toOAITool(t))
	}

	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal: %w", err)
	}

	var result *oaiResponse
	for attempt := range 2 {
		result, err = p.post(ctx, body)
		if err == nil {
			break
		}
		if attempt == 0 && isRetryableErr(err) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		return nil, err
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices in response")
	}

	msg := result.Choices[0].Message
	resp := &Response{Content: msg.Content}
	for _, tc := range msg.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return resp, nil
}

func (p *OpenAIProvider) post(ctx context.Context, body []byte) (*oaiResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read: %w", err)
	}

	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return nil, &retryable{fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, raw)}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, raw)
	}

	var out oaiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("openai: parse: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("openai: API error: %s", out.Error.Message)
	}
	return &out, nil
}

func toOAIMessage(m Message) oaiMessage {
	out := oaiMessage{
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
	}
	for _, tc := range m.ToolCalls {
		oTc := oaiToolCall{ID: tc.ID, Type: "function"}
		oTc.Function.Name = tc.Name
		oTc.Function.Arguments = tc.Arguments
		out.ToolCalls = append(out.ToolCalls, oTc)
	}
	return out
}

func toOAITool(t Tool) oaiTool {
	props := make(map[string]oaiToolProp, len(t.Parameters.Properties))
	for k, v := range t.Parameters.Properties {
		props[k] = oaiToolProp{
			Type:        v.Type,
			Description: v.Description,
			Enum:        v.Enum,
		}
	}
	var tool oaiTool
	tool.Type = "function"
	tool.Function.Name = t.Name
	tool.Function.Description = t.Description
	tool.Function.Parameters = oaiToolParams{
		Type:       t.Parameters.Type,
		Properties: props,
		Required:   t.Parameters.Required,
	}
	return tool
}

type retryable struct{ err error }

func (e *retryable) Error() string { return e.err.Error() }

func isRetryableErr(err error) bool {
	_, ok := err.(*retryable)
	return ok
}
