package chat

import (
	"encoding/json"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestToAntRequestDefaults(t *testing.T) {
	req := types.Request{Messages: []types.Message{{Role: "user", Content: "hi"}}}
	ant := ToAntRequest(req, "claude-sonnet-4-6", false)
	if ant.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected default model, got %s", ant.Model)
	}
	if ant.MaxTokens != defaultMaxToks {
		t.Fatalf("expected defaultMaxToks=%d, got %d", defaultMaxToks, ant.MaxTokens)
	}
	if ant.Stream {
		t.Fatal("stream should be false")
	}
}

func TestToAntRequestExplicitModel(t *testing.T) {
	req := types.Request{Model: "my-model", MaxTokens: 512}
	ant := ToAntRequest(req, "default-model", true)
	if ant.Model != "my-model" {
		t.Fatalf("expected my-model, got %s", ant.Model)
	}
	if ant.MaxTokens != 512 {
		t.Fatalf("expected 512, got %d", ant.MaxTokens)
	}
	if !ant.Stream {
		t.Fatal("expected stream=true")
	}
}

func TestToAntRequestSystemAndTools(t *testing.T) {
	req := types.Request{
		System: "be helpful",
		Messages: []types.Message{{Role: "user", Content: "hello"}},
		Tools: []types.Tool{{
			Name:        "weather",
			Description: "get weather",
			Parameters: types.Schema{
				Type: "object",
				Properties: map[string]types.Property{
					"location": {Type: "string", Description: "city name"},
				},
				Required: []string{"location"},
			},
		}},
	}
	ant := ToAntRequest(req, "m", false)
	if ant.System != "be helpful" {
		t.Fatalf("expected system prompt, got %q", ant.System)
	}
	if len(ant.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(ant.Tools))
	}
	if ant.Tools[0].Name != "weather" {
		t.Fatalf("tool name mismatch")
	}
}

func TestFromAntResponseText(t *testing.T) {
	r := &AntResponse{
		Content:    []AntBlock{{Type: "text", Text: "hello world"}},
		StopReason: "end_turn",
		Model:      "claude-3",
		Usage:      &struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}{100, 50},
	}
	resp := FromAntResponse(r, "anthropic")
	if resp.Content != "hello world" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if resp.Provider != "anthropic" {
		t.Fatalf("unexpected provider: %q", resp.Provider)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 100 {
		t.Fatalf("unexpected usage: %v", resp.Usage)
	}
	if resp.Usage.TotalTokens != 150 {
		t.Fatalf("expected TotalTokens=150, got %d", resp.Usage.TotalTokens)
	}
}

func TestFromAntResponseToolCall(t *testing.T) {
	args := map[string]interface{}{"location": "NYC"}
	r := &AntResponse{
		Content: []AntBlock{{
			Type:  "tool_use",
			ID:    "tc1",
			Name:  "weather",
			Input: args,
		}},
		Model: "claude-3",
	}
	resp := FromAntResponse(r, "anthropic")
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "tc1" || tc.Name != "weather" {
		t.Fatalf("wrong tool call: %+v", tc)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Arguments), &got); err != nil {
		t.Fatalf("args not valid JSON: %v", err)
	}
}

func TestFromAntResponseNoUsage(t *testing.T) {
	r := &AntResponse{Content: []AntBlock{{Type: "text", Text: "ok"}}, Model: "m"}
	resp := FromAntResponse(r, "anthropic")
	if resp.Usage != nil {
		t.Fatal("expected nil usage")
	}
}

func TestToAntMessagesUserAssistant(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "a"},
	}
	out := ToAntMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[0].Role != "user" || out[1].Role != "assistant" {
		t.Fatal("role mismatch")
	}
}

func TestToAntMessagesToolResultMerging(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "call tool"},
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "tc1", Name: "fn", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "tc1", Content: "result1"},
		{Role: "tool", ToolCallID: "tc2", Content: "result2"},
		{Role: "user", Content: "next"},
	}
	out := ToAntMessages(msgs)
	// user, assistant with tool_use blocks, user with tool_result blocks, user
	if len(out) != 4 {
		t.Fatalf("expected 4 output messages, got %d", len(out))
	}
	// third message should be user with tool_result blocks
	toolMsg := out[2]
	if toolMsg.Role != "user" {
		t.Fatalf("expected user role for tool results, got %q", toolMsg.Role)
	}
	blocks, ok := toolMsg.Content.([]AntBlock)
	if !ok {
		t.Fatalf("expected []AntBlock content, got %T", toolMsg.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 tool_result blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "tool_result" {
		t.Fatalf("expected tool_result type, got %q", blocks[0].Type)
	}
}

func TestToAntMessagesAssistantWithToolCalls(t *testing.T) {
	msgs := []types.Message{
		{
			Role:    "assistant",
			Content: "thinking",
			ToolCalls: []types.ToolCall{
				{ID: "tc1", Name: "fn", Arguments: `{"x":1}`},
			},
		},
	}
	out := ToAntMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	blocks, ok := out[0].Content.([]AntBlock)
	if !ok {
		t.Fatalf("expected []AntBlock, got %T", out[0].Content)
	}
	// text block + tool_use block
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks (text + tool_use), got %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[1].Type != "tool_use" {
		t.Fatalf("unexpected block types: %s, %s", blocks[0].Type, blocks[1].Type)
	}
}

func TestFromToolConversion(t *testing.T) {
	tool := types.Tool{
		Name:        "calc",
		Description: "do math",
		Parameters: types.Schema{
			Type: "object",
			Properties: map[string]types.Property{
				"expr": {Type: "string", Description: "expression", Enum: []string{"add", "sub"}},
			},
			Required: []string{"expr"},
		},
	}
	ant := FromTool(tool)
	if ant.Name != "calc" || ant.Description != "do math" {
		t.Fatalf("name/description mismatch")
	}
	if ant.InputSchema.Type != "object" {
		t.Fatalf("expected object schema type")
	}
	prop, ok := ant.InputSchema.Properties["expr"]
	if !ok {
		t.Fatal("expected expr property")
	}
	if prop.Type != "string" || len(prop.Enum) != 2 {
		t.Fatalf("property mismatch: %+v", prop)
	}
	if len(ant.InputSchema.Required) != 1 || ant.InputSchema.Required[0] != "expr" {
		t.Fatalf("required mismatch")
	}
}
