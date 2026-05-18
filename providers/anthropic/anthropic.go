// Package anthropic provides a llmbridge.Provider backed by the Anthropic
// Messages API (Claude Opus, Sonnet, Haiku families).
//
// Key wire-format differences from OpenAI that this package handles:
//   - system prompt is a top-level field, not a "system" role message
//   - response content is an array of typed blocks (text, tool_use)
//   - consecutive "tool" role messages must be merged into a single "user"
//     message containing an array of tool_result blocks
package anthropic

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
	apiURL         = "https://api.anthropic.com/v1/messages"
	apiVersion     = "2023-06-01"
	defaultModel   = "claude-sonnet-4-6"
	defaultMaxToks = 1024
)

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

// Name implements llmbridge.Provider.
func (p *Provider) Name() string { return "anthropic" }

// Complete sends a blocking request and returns the full response.
// On rate-limit or server errors it retries once after a 2-second backoff.
func (p *Provider) Complete(ctx context.Context, req llmbridge.Request) (*llmbridge.Response, error) {
	body, err := p.marshal(req, false)
	if err != nil {
		return nil, err
	}

	var raw *antResponse
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

	return fromAntResponse(raw), nil
}

// marshal builds the JSON request body. stream=true adds "stream": true.
func (p *Provider) marshal(req llmbridge.Request, stream bool) ([]byte, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = defaultMaxToks
	}

	wire := antRequest{
		Model:     model,
		MaxTokens: maxTok,
		System:    req.System,
		Stream:    stream,
		Messages:  toAntMessages(req.Messages),
	}
	for _, t := range req.Tools {
		wire.Tools = append(wire.Tools, fromTool(t))
	}

	b, err := json.Marshal(wire)
	if err != nil {
		return nil, &llmbridge.ErrProvider{Provider: "anthropic", Cause: fmt.Errorf("marshal: %w", err)}
	}
	return b, nil
}

func (p *Provider) post(ctx context.Context, body []byte) (*antResponse, error) {
	resp, err := p.do(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &llmbridge.ErrProvider{Provider: "anthropic", Cause: fmt.Errorf("read body: %w", err)}
	}

	if err := p.checkStatus(resp.StatusCode, raw); err != nil {
		return nil, err
	}

	var out antResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &llmbridge.ErrProvider{Provider: "anthropic", Cause: fmt.Errorf("parse: %w", err)}
	}
	if out.Error != nil {
		return nil, &llmbridge.ErrProvider{Provider: "anthropic", Cause: fmt.Errorf("API error (%s): %s", out.Error.Type, out.Error.Message)}
	}
	return &out, nil
}

// do executes the HTTP request and returns the raw response without reading the body.
func (p *Provider) do(ctx context.Context, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, &llmbridge.ErrProvider{Provider: "anthropic", Cause: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &llmbridge.ErrTimeout{Provider: "anthropic", Cause: err}
		}
		return nil, &llmbridge.ErrProvider{Provider: "anthropic", Cause: err}
	}
	return resp, nil
}

// doStream opens a streaming HTTP connection without a read deadline.
func (p *Provider) doStream(ctx context.Context, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, &llmbridge.ErrProvider{Provider: "anthropic", Cause: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)
	httpReq.Header.Set("Accept", "text/event-stream")

	streamClient := &http.Client{Transport: p.client.Transport}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &llmbridge.ErrTimeout{Provider: "anthropic", Cause: err}
		}
		return nil, &llmbridge.ErrProvider{Provider: "anthropic", Cause: err}
	}

	if err := p.checkStatus(resp.StatusCode, nil); err != nil {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, p.checkStatusRaw(resp.StatusCode, raw)
	}
	return resp, nil
}

func (p *Provider) checkStatus(code int, body []byte) error {
	return p.checkStatusRaw(code, body)
}

func (p *Provider) checkStatusRaw(code int, body []byte) error {
	switch {
	case code == 401 || code == 403:
		return &llmbridge.ErrAuth{Provider: "anthropic", Cause: fmt.Errorf("HTTP %d: %s", code, body)}
	case code == 429 || code >= 500:
		return &llmbridge.ErrRateLimit{Provider: "anthropic", Cause: fmt.Errorf("HTTP %d: %s", code, body)}
	case code >= 400:
		return &llmbridge.ErrProvider{Provider: "anthropic", Code: code, Cause: fmt.Errorf("%s", body)}
	}
	return nil
}

// ---- Wire types ----

type antRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []antMessage `json:"messages"`
	Tools     []antTool    `json:"tools,omitempty"`
	Stream    bool         `json:"stream,omitempty"`
}

// antMessage content can be a plain string or an array of blocks.
type antMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string | []antBlock
}

type antBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   string      `json:"content,omitempty"`
}

type antTool struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	InputSchema antSchema `json:"input_schema"`
}

type antSchema struct {
	Type       string                   `json:"type"`
	Properties map[string]antSchemaProp `json:"properties,omitempty"`
	Required   []string                 `json:"required,omitempty"`
}

type antSchemaProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type antResponse struct {
	Content    []antBlock `json:"content"`
	StopReason string     `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---- Conversion helpers ----

// toAntMessages converts provider-agnostic messages to Anthropic wire format.
// Consecutive "tool" role messages are merged into one "user" message with
// an array of tool_result blocks -- a requirement of the Anthropic API.
func toAntMessages(msgs []llmbridge.Message) []antMessage {
	var out []antMessage
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		switch m.Role {
		case "user":
			out = append(out, antMessage{Role: "user", Content: m.Content})
			i++

		case "assistant":
			if len(m.ToolCalls) == 0 {
				out = append(out, antMessage{Role: "assistant", Content: m.Content})
				i++
				break
			}
			var blocks []antBlock
			if m.Content != "" {
				blocks = append(blocks, antBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input interface{}
				_ = json.Unmarshal([]byte(tc.Arguments), &input)
				blocks = append(blocks, antBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			out = append(out, antMessage{Role: "assistant", Content: blocks})
			i++

		case "tool":
			// Collect all consecutive tool results into one user message.
			var results []antBlock
			for i < len(msgs) && msgs[i].Role == "tool" {
				results = append(results, antBlock{
					Type:      "tool_result",
					ToolUseID: msgs[i].ToolCallID,
					Content:   msgs[i].Content,
				})
				i++
			}
			out = append(out, antMessage{Role: "user", Content: results})

		default:
			i++
		}
	}
	return out
}

func fromAntResponse(r *antResponse) *llmbridge.Response {
	resp := &llmbridge.Response{}
	for _, block := range r.Content {
		switch block.Type {
		case "text":
			resp.Content += block.Text
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			resp.ToolCalls = append(resp.ToolCalls, llmbridge.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(argsJSON),
			})
		}
	}
	return resp
}

func fromTool(t llmbridge.Tool) antTool {
	props := make(map[string]antSchemaProp, len(t.Parameters.Properties))
	for k, v := range t.Parameters.Properties {
		props[k] = antSchemaProp{Type: v.Type, Description: v.Description, Enum: v.Enum}
	}
	return antTool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: antSchema{
			Type:       "object",
			Properties: props,
			Required:   t.Parameters.Required,
		},
	}
}
