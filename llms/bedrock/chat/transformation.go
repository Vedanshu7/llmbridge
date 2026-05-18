// Package chat handles AWS Bedrock Converse API wire-format transformations.
package chat

import (
	"encoding/json"

	"github.com/Vedanshu7/llmbridge/types"
)

// ---- Converse API wire types ----

// ConverseRequest is the body for the Bedrock Converse API.
type ConverseRequest struct {
	Messages        []ConverseMessage        `json:"messages"`
	System          []ConverseSystemBlock    `json:"system,omitempty"`
	InferenceConfig *ConverseInferenceConfig `json:"inferenceConfig,omitempty"`
	ToolConfig      *ConverseToolConfig      `json:"toolConfig,omitempty"`
}

// ConverseMessage is a single turn in the conversation.
type ConverseMessage struct {
	Role    string          `json:"role"`
	Content []ConverseBlock `json:"content"`
}

// ConverseBlock is one content block within a message.
type ConverseBlock struct {
	Text      string              `json:"text,omitempty"`
	ToolUse   *ConverseToolUse   `json:"toolUse,omitempty"`
	ToolResult *ConverseToolResult `json:"toolResult,omitempty"`
}

// ConverseToolUse is a tool invocation block in an assistant message.
type ConverseToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

// ConverseToolResult is the tool output block in a user message.
type ConverseToolResult struct {
	ToolUseID string          `json:"toolUseId"`
	Content   []ConverseBlock `json:"content"`
}

// ConverseSystemBlock is a system prompt block.
type ConverseSystemBlock struct {
	Text string `json:"text"`
}

// ConverseInferenceConfig holds sampling parameters.
type ConverseInferenceConfig struct {
	MaxTokens   int     `json:"maxTokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"topP,omitempty"`
}

// ConverseToolConfig holds tool definitions.
type ConverseToolConfig struct {
	Tools []ConverseTool `json:"tools"`
}

// ConverseTool is a single tool definition.
type ConverseTool struct {
	ToolSpec ConverseToolSpec `json:"toolSpec"`
}

// ConverseToolSpec defines a tool's schema.
type ConverseToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema ConverseSchema  `json:"inputSchema"`
}

// ConverseSchema wraps the JSON Schema object.
type ConverseSchema struct {
	JSON json.RawMessage `json:"json"`
}

// ---- Response types ----

// ConverseResponse is returned by the Converse API.
type ConverseResponse struct {
	Output      ConverseOutput  `json:"output"`
	StopReason  string          `json:"stopReason"`
	Usage       *ConverseUsage  `json:"usage,omitempty"`
}

// ConverseOutput contains the assistant message.
type ConverseOutput struct {
	Message ConverseMessage `json:"message"`
}

// ConverseUsage holds token counts.
type ConverseUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

// ConverseStreamEvent is one event from the ConverseStream response.
type ConverseStreamEvent struct {
	ContentBlockDelta  *ContentBlockDelta  `json:"contentBlockDelta,omitempty"`
	MessageStop        *MessageStop        `json:"messageStop,omitempty"`
	Metadata           *ConverseMetadata   `json:"metadata,omitempty"`
}

// ContentBlockDelta carries a text fragment.
type ContentBlockDelta struct {
	Delta ConverseContentDelta `json:"delta"`
}

// ConverseContentDelta is the content of a ContentBlockDelta.
type ConverseContentDelta struct {
	Text string `json:"text,omitempty"`
}

// MessageStop signals end of stream.
type MessageStop struct {
	StopReason string `json:"stopReason"`
}

// ConverseMetadata carries usage info at the end of a stream.
type ConverseMetadata struct {
	Usage *ConverseUsage `json:"usage,omitempty"`
}

// ---- Conversion functions ----

// ToConverseRequest translates a types.Request to a Bedrock ConverseRequest.
func ToConverseRequest(req types.Request) ConverseRequest {
	cr := ConverseRequest{}

	if req.System != "" {
		cr.System = []ConverseSystemBlock{{Text: req.System}}
	}

	for _, m := range req.Messages {
		var blocks []ConverseBlock
		if m.Content != "" {
			blocks = append(blocks, ConverseBlock{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, ConverseBlock{
				ToolUse: &ConverseToolUse{
					ToolUseID: tc.ID,
					Name:      tc.Name,
					Input:     json.RawMessage(tc.Arguments),
				},
			})
		}
		cr.Messages = append(cr.Messages, ConverseMessage{Role: m.Role, Content: blocks})
	}

	cfg := &ConverseInferenceConfig{}
	if req.MaxTokens > 0 {
		cfg.MaxTokens = req.MaxTokens
	}
	if req.Temperature > 0 {
		cfg.Temperature = req.Temperature
	}
	if cfg.MaxTokens > 0 || cfg.Temperature > 0 {
		cr.InferenceConfig = cfg
	}

	if len(req.Tools) > 0 {
		tools := make([]ConverseTool, len(req.Tools))
		for i, t := range req.Tools {
			schemaMap := map[string]interface{}{
				"type":       t.Parameters.Type,
				"properties": t.Parameters.Properties,
				"required":   t.Parameters.Required,
			}
			schemaJSON, _ := json.Marshal(schemaMap)
			tools[i] = ConverseTool{
				ToolSpec: ConverseToolSpec{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: ConverseSchema{JSON: schemaJSON},
				},
			}
		}
		cr.ToolConfig = &ConverseToolConfig{Tools: tools}
	}
	return cr
}

// FromConverseResponse translates a ConverseResponse to a types.Response.
func FromConverseResponse(cr *ConverseResponse, providerName, modelID string) *types.Response {
	resp := &types.Response{
		Provider: providerName,
		Model:    modelID,
	}
	for _, block := range cr.Output.Message.Content {
		if block.Text != "" {
			resp.Content += block.Text
		}
		if block.ToolUse != nil {
			resp.ToolCalls = append(resp.ToolCalls, types.ToolCall{
				ID:        block.ToolUse.ToolUseID,
				Name:      block.ToolUse.Name,
				Arguments: string(block.ToolUse.Input),
			})
		}
	}
	if cr.Usage != nil {
		resp.Usage = &types.UsageData{
			PromptTokens:     cr.Usage.InputTokens,
			CompletionTokens: cr.Usage.OutputTokens,
			TotalTokens:      cr.Usage.TotalTokens,
		}
	}
	return resp
}
