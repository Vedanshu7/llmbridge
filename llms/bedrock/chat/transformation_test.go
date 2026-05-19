package chat

import (
	"encoding/json"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestToConverseRequestBasic(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{
			{Role: "user", Content: "Hello"},
		},
	}
	cr := ToConverseRequest(req)
	if len(cr.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(cr.Messages))
	}
	if cr.Messages[0].Role != "user" {
		t.Fatalf("expected role=user, got %q", cr.Messages[0].Role)
	}
	if len(cr.Messages[0].Content) != 1 || cr.Messages[0].Content[0].Text != "Hello" {
		t.Fatalf("unexpected content: %+v", cr.Messages[0].Content)
	}
}

func TestToConverseRequestSystemPrompt(t *testing.T) {
	req := types.Request{
		System:   "You are helpful.",
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}
	cr := ToConverseRequest(req)
	if len(cr.System) != 1 || cr.System[0].Text != "You are helpful." {
		t.Fatalf("unexpected system blocks: %+v", cr.System)
	}
}

func TestToConverseRequestInferenceConfig(t *testing.T) {
	req := types.Request{
		Messages:    []types.Message{{Role: "user", Content: "hi"}},
		MaxTokens:   512,
		Temperature: 0.7,
	}
	cr := ToConverseRequest(req)
	if cr.InferenceConfig == nil {
		t.Fatal("expected InferenceConfig to be set")
	}
	if cr.InferenceConfig.MaxTokens != 512 {
		t.Fatalf("expected MaxTokens=512, got %d", cr.InferenceConfig.MaxTokens)
	}
	if cr.InferenceConfig.Temperature != 0.7 {
		t.Fatalf("expected Temperature=0.7, got %f", cr.InferenceConfig.Temperature)
	}
}

func TestToConverseRequestNoInferenceConfig(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}
	cr := ToConverseRequest(req)
	if cr.InferenceConfig != nil {
		t.Fatal("expected no InferenceConfig when MaxTokens=0 and Temperature=0")
	}
}

func TestToConverseRequestToolCall(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{
			{
				Role: "assistant",
				ToolCalls: []types.ToolCall{
					{ID: "tc1", Name: "get_weather", Arguments: `{"location":"NYC"}`},
				},
			},
		},
	}
	cr := ToConverseRequest(req)
	if len(cr.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(cr.Messages))
	}
	content := cr.Messages[0].Content
	if len(content) != 1 || content[0].ToolUse == nil {
		t.Fatalf("expected tool use block: %+v", content)
	}
	if content[0].ToolUse.Name != "get_weather" {
		t.Fatalf("unexpected tool name: %s", content[0].ToolUse.Name)
	}
}

func TestToConverseRequestTools(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
		Tools: []types.Tool{
			{
				Name:        "search",
				Description: "Search the web",
				Parameters: types.Schema{
					Type: "object",
					Properties: map[string]types.Property{
						"query": {Type: "string", Description: "search query"},
					},
					Required: []string{"query"},
				},
			},
		},
	}
	cr := ToConverseRequest(req)
	if cr.ToolConfig == nil || len(cr.ToolConfig.Tools) != 1 {
		t.Fatal("expected 1 tool in ToolConfig")
	}
	spec := cr.ToolConfig.Tools[0].ToolSpec
	if spec.Name != "search" {
		t.Fatalf("unexpected tool name: %s", spec.Name)
	}
	// InputSchema.JSON should be valid JSON with the schema.
	var schemaObj map[string]interface{}
	if err := json.Unmarshal(spec.InputSchema.JSON, &schemaObj); err != nil {
		t.Fatalf("InputSchema.JSON is not valid JSON: %v", err)
	}
}

func TestFromConverseResponseBasic(t *testing.T) {
	cr := &ConverseResponse{
		Output: ConverseOutput{
			Message: ConverseMessage{
				Role:    "assistant",
				Content: []ConverseBlock{{Text: "hello world"}},
			},
		},
		StopReason: "end_turn",
	}
	resp := FromConverseResponse(cr, "bedrock", "amazon.titan-text-v1")
	if resp.Content != "hello world" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if resp.Provider != "bedrock" {
		t.Fatalf("unexpected provider: %q", resp.Provider)
	}
}

func TestFromConverseResponseUsage(t *testing.T) {
	cr := &ConverseResponse{
		Output: ConverseOutput{
			Message: ConverseMessage{
				Content: []ConverseBlock{{Text: "reply"}},
			},
		},
		Usage: &ConverseUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
	resp := FromConverseResponse(cr, "bedrock", "m")
	if resp.Usage == nil {
		t.Fatal("expected usage data")
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestFromConverseResponseToolUse(t *testing.T) {
	cr := &ConverseResponse{
		Output: ConverseOutput{
			Message: ConverseMessage{
				Content: []ConverseBlock{
					{
						ToolUse: &ConverseToolUse{
							ToolUseID: "tu1",
							Name:      "get_weather",
							Input:     json.RawMessage(`{"location":"Paris"}`),
						},
					},
				},
			},
		},
		StopReason: "tool_use",
	}
	resp := FromConverseResponse(cr, "bedrock", "m")
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("unexpected tool name: %s", resp.ToolCalls[0].Name)
	}
	if resp.ToolCalls[0].Arguments != `{"location":"Paris"}` {
		t.Fatalf("unexpected arguments: %s", resp.ToolCalls[0].Arguments)
	}
}

func TestFromConverseResponseMultiBlock(t *testing.T) {
	cr := &ConverseResponse{
		Output: ConverseOutput{
			Message: ConverseMessage{
				Content: []ConverseBlock{
					{Text: "first "},
					{Text: "second"},
				},
			},
		},
	}
	resp := FromConverseResponse(cr, "bedrock", "m")
	if resp.Content != "first second" {
		t.Fatalf("expected concatenated text: %q", resp.Content)
	}
}
