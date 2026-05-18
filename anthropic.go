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

const anthropicURL     = "https://api.anthropic.com/v1/messages"
const anthropicVersion = "2023-06-01"
const anthropicDefault = "claude-sonnet-4-6"

// AnthropicProvider calls the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey string
	model  string
	http   *http.Client
}

// NewAnthropic returns a provider backed by Anthropic Claude.
// model: e.g. "claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5-20251001".
func NewAnthropic(model, apiKey string) Provider {
	if model == "" {
		model = anthropicDefault
	}
	return &AnthropicProvider{
		apiKey: apiKey,
		model:  model,
		http:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

// Anthropic wire types.
// The Anthropic API uses content blocks instead of a simple string Content field.

type antRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []antMessage `json:"messages"`
	Tools     []antTool    `json:"tools,omitempty"`
}

// antMessage can hold either a plain string or an array of content blocks.
// We always use content blocks for assistant messages that include tool use,
// and a plain string for simple user/assistant turns.
type antMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []antBlock
}

type antBlock struct {
	Type       string      `json:"type"`
	// text block
	Text       string      `json:"text,omitempty"`
	// tool_use block (assistant)
	ID         string      `json:"id,omitempty"`
	Name       string      `json:"name,omitempty"`
	Input      interface{} `json:"input,omitempty"`
	// tool_result block (user)
	ToolUseID  string      `json:"tool_use_id,omitempty"`
	Content    string      `json:"content,omitempty"`
}

type antTool struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	InputSchema antSchema    `json:"input_schema"`
}

type antSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]antSchemaProp  `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type antSchemaProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type antResponse struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"`
	Content    []antBlock `json:"content"`
	StopReason string     `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *AnthropicProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = 1024
	}

	wire := antRequest{
		Model:     model,
		MaxTokens: maxTok,
		System:    req.System,
	}

	// Convert messages, grouping consecutive tool-role messages into a single
	// user message with multiple tool_result blocks (Anthropic requirement).
	wire.Messages = toAntMessages(req.Messages)

	for _, t := range req.Tools {
		wire.Tools = append(wire.Tools, toAntTool(t))
	}

	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal: %w", err)
	}

	var result *antResponse
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

	return fromAntResponse(result), nil
}

func (p *AnthropicProvider) post(ctx context.Context, body []byte) (*antResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read: %w", err)
	}

	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return nil, &retryable{fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, raw)}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, raw)
	}

	var out antResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("anthropic: parse: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("anthropic: API error (%s): %s", out.Error.Type, out.Error.Message)
	}
	return &out, nil
}

// toAntMessages converts provider-agnostic messages to Anthropic wire format.
// Key rule: consecutive "tool" role messages are merged into one "user" message
// containing an array of tool_result blocks.
func toAntMessages(msgs []Message) []antMessage {
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
			// Build an array of content blocks: text (if any) + tool_use blocks.
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

func fromAntResponse(r *antResponse) *Response {
	resp := &Response{}
	for _, block := range r.Content {
		switch block.Type {
		case "text":
			resp.Content += block.Text
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(argsJSON),
			})
		}
	}
	return resp
}

func toAntTool(t Tool) antTool {
	props := make(map[string]antSchemaProp, len(t.Parameters.Properties))
	for k, v := range t.Parameters.Properties {
		props[k] = antSchemaProp{
			Type:        v.Type,
			Description: v.Description,
			Enum:        v.Enum,
		}
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
