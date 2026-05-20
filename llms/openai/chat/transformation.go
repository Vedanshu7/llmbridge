// Package chat implements OpenAI chat completions request/response transformation.
package chat

import (
	"github.com/Vedanshu7/llmbridge/types"
)

// ---- Wire types ----

type OAIRequest struct {
	Model       string       `json:"model"`
	Messages    []OAIMessage `json:"messages"`
	Tools       []OAITool    `json:"tools,omitempty"`
	Temperature float64      `json:"temperature"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
}

// OAIContentPart is one element of a multi-modal content array.
type OAIContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

type OAIMessage struct {
	Role       string          `json:"role"`
	Content    interface{}     `json:"content,omitempty"` // string or []OAIContentPart
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []OAIToolCall   `json:"tool_calls,omitempty"`
}

type OAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type OAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string    `json:"name"`
		Description string    `json:"description"`
		Parameters  OAIParams `json:"parameters"`
	} `json:"function"`
}

type OAIParams struct {
	Type       string              `json:"type"`
	Properties map[string]OAIProp  `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

type OAIProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type OAIResponse struct {
	Choices []struct {
		Message      OAIMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// OAIChunk is a single SSE event from the OpenAI streaming API.
type OAIChunk struct {
	Choices []struct {
		Delta struct {
			Content   string        `json:"content"`
			ToolCalls []OAIToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// ---- Conversion helpers ----

// ToOAIRequest converts a types.Request into the OpenAI wire format.
func ToOAIRequest(req types.Request, defaultModel string, stream bool) OAIRequest {
	model := req.Model
	if model == "" {
		model = defaultModel
	}

	wire := OAIRequest{
		Model:       model,
		Temperature: req.Temperature,
		Stream:      stream,
	}
	if req.MaxTokens > 0 {
		wire.MaxTokens = req.MaxTokens
	}
	if req.System != "" {
		wire.Messages = append(wire.Messages, OAIMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		wire.Messages = append(wire.Messages, FromMessage(m))
	}
	for _, t := range req.Tools {
		wire.Tools = append(wire.Tools, FromTool(t))
	}
	return wire
}

// FromOAIResponse converts an OpenAI wire response into types.Response.
func FromOAIResponse(raw *OAIResponse, providerName, model string) *types.Response {
	msg := raw.Choices[0].Message
	var content string
	switch v := msg.Content.(type) {
	case string:
		content = v
	case []interface{}:
		// Multi-part response — concatenate text parts.
		for _, part := range v {
			if pm, ok := part.(map[string]interface{}); ok {
				if t, ok := pm["text"].(string); ok {
					content += t
				}
			}
		}
	}
	resp := &types.Response{
		Content:  content,
		Provider: providerName,
		Model:    model,
	}
	for _, tc := range msg.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, types.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	if raw.Usage != nil {
		resp.Usage = &types.UsageData{
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		}
	}
	return resp
}

// FromMessage converts a types.Message to OAIMessage.
func FromMessage(m types.Message) OAIMessage {
	out := OAIMessage{
		Role:       m.Role,
		ToolCallID: m.ToolCallID,
	}
	// Use multi-part content when image parts are present.
	if len(m.Parts) > 0 {
		parts := make([]OAIContentPart, 0, len(m.Parts))
		for _, p := range m.Parts {
			cp := OAIContentPart{Type: p.Type, Text: p.Text}
			if p.ImageURL != "" {
				cp.ImageURL = &struct{ URL string `json:"url"` }{URL: p.ImageURL}
			}
			parts = append(parts, cp)
		}
		out.Content = parts
	} else {
		out.Content = m.Content
	}
	for _, tc := range m.ToolCalls {
		var otc OAIToolCall
		otc.ID = tc.ID
		otc.Type = "function"
		otc.Function.Name = tc.Name
		otc.Function.Arguments = tc.Arguments
		out.ToolCalls = append(out.ToolCalls, otc)
	}
	return out
}

// FromTool converts a types.Tool to OAITool.
func FromTool(t types.Tool) OAITool {
	props := make(map[string]OAIProp, len(t.Parameters.Properties))
	for k, v := range t.Parameters.Properties {
		props[k] = OAIProp{Type: v.Type, Description: v.Description, Enum: v.Enum}
	}
	var out OAITool
	out.Type = "function"
	out.Function.Name = t.Name
	out.Function.Description = t.Description
	out.Function.Parameters = OAIParams{
		Type:       t.Parameters.Type,
		Properties: props,
		Required:   t.Parameters.Required,
	}
	return out
}
