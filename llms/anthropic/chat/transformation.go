// Package chat implements Anthropic Messages API request/response transformation.
package chat

import (
	"encoding/json"

	"github.com/Vedanshu7/llmbridge/types"
)

// ---- Wire types ----

type AntRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []AntMessage `json:"messages"`
	Tools     []AntTool    `json:"tools,omitempty"`
	Stream    bool         `json:"stream,omitempty"`
}

// AntMessage content can be a plain string or an array of blocks.
type AntMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string | []AntBlock
}

// AntImageSource describes the source of an image block.
type AntImageSource struct {
	Type      string `json:"type"`            // "url" or "base64"
	URL       string `json:"url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

type AntBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     interface{}     `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	Source    *AntImageSource `json:"source,omitempty"` // for type=="image"
}

type AntTool struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	InputSchema AntSchema    `json:"input_schema"`
}

type AntSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]AntSchemaProp  `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type AntSchemaProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type AntResponse struct {
	Content    []AntBlock `json:"content"`
	StopReason string     `json:"stop_reason"`
	Model      string     `json:"model"`
	Usage      *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// AntChunk is the data payload of a content_block_delta SSE event.
type AntChunk struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
}

// ---- Conversion helpers ----

const defaultMaxToks = 1024

// ToAntRequest converts a types.Request to the Anthropic wire format.
func ToAntRequest(req types.Request, defaultModel string, stream bool) AntRequest {
	model := req.Model
	if model == "" {
		model = defaultModel
	}
	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = defaultMaxToks
	}

	wire := AntRequest{
		Model:     model,
		MaxTokens: maxTok,
		System:    req.System,
		Stream:    stream,
		Messages:  ToAntMessages(req.Messages),
	}
	for _, t := range req.Tools {
		wire.Tools = append(wire.Tools, FromTool(t))
	}
	return wire
}

// FromAntResponse converts an Anthropic wire response to types.Response.
func FromAntResponse(r *AntResponse, providerName string) *types.Response {
	resp := &types.Response{
		Provider: providerName,
		Model:    r.Model,
	}
	for _, block := range r.Content {
		switch block.Type {
		case "text":
			resp.Content += block.Text
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			resp.ToolCalls = append(resp.ToolCalls, types.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(argsJSON),
			})
		}
	}
	if r.Usage != nil {
		resp.Usage = &types.UsageData{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
		}
	}
	return resp
}

// ToAntMessages converts provider-agnostic messages to Anthropic wire format.
// Consecutive "tool" role messages are merged into one "user" message with
// an array of tool_result blocks — a requirement of the Anthropic API.
func ToAntMessages(msgs []types.Message) []AntMessage {
	var out []AntMessage
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		switch m.Role {
		case "user":
			if len(m.Parts) > 0 {
				blocks := partsToAntBlocks(m.Parts)
				out = append(out, AntMessage{Role: "user", Content: blocks})
			} else {
				out = append(out, AntMessage{Role: "user", Content: m.Content})
			}
			i++

		case "assistant":
			if len(m.ToolCalls) == 0 {
				out = append(out, AntMessage{Role: "assistant", Content: m.Content})
				i++
				break
			}
			var blocks []AntBlock
			if m.Content != "" {
				blocks = append(blocks, AntBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input interface{}
				_ = json.Unmarshal([]byte(tc.Arguments), &input)
				blocks = append(blocks, AntBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			out = append(out, AntMessage{Role: "assistant", Content: blocks})
			i++

		case "tool":
			var results []AntBlock
			for i < len(msgs) && msgs[i].Role == "tool" {
				results = append(results, AntBlock{
					Type:      "tool_result",
					ToolUseID: msgs[i].ToolCallID,
					Content:   msgs[i].Content,
				})
				i++
			}
			out = append(out, AntMessage{Role: "user", Content: results})

		default:
			i++
		}
	}
	return out
}

// partsToAntBlocks converts ContentParts to Anthropic content blocks.
func partsToAntBlocks(parts []types.ContentPart) []AntBlock {
	out := make([]AntBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, AntBlock{Type: "text", Text: p.Text})
		case "image_url":
			out = append(out, AntBlock{
				Type:   "image",
				Source: &AntImageSource{Type: "url", URL: p.ImageURL},
			})
		}
	}
	return out
}

// FromTool converts a types.Tool to an AntTool.
func FromTool(t types.Tool) AntTool {
	props := make(map[string]AntSchemaProp, len(t.Parameters.Properties))
	for k, v := range t.Parameters.Properties {
		props[k] = AntSchemaProp{Type: v.Type, Description: v.Description, Enum: v.Enum}
	}
	return AntTool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: AntSchema{
			Type:       "object",
			Properties: props,
			Required:   t.Parameters.Required,
		},
	}
}
