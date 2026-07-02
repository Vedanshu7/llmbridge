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
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

func TestToConverseRequestWithImageDataURI(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{
			{
				Role: "user",
				Parts: []types.ContentPart{
					{Type: "text", Text: "what is in this image?"},
					{Type: "image_url", ImageURL: "data:image/png;base64,aGVsbG8="},
				},
			},
		},
	}
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := cr.Messages[0].Content
	if len(content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(content))
	}
	if content[0].Text != "what is in this image?" {
		t.Fatalf("unexpected text block: %+v", content[0])
	}
	if content[1].Image == nil {
		t.Fatalf("expected image block: %+v", content[1])
	}
	if content[1].Image.Format != "png" {
		t.Fatalf("expected format=png, got %q", content[1].Image.Format)
	}
	if content[1].Image.Source.Bytes != "aGVsbG8=" {
		t.Fatalf("unexpected image bytes: %q", content[1].Image.Source.Bytes)
	}
}

func TestToConverseRequestImageFormatNormalizesJPG(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{
			{
				Role: "user",
				Parts: []types.ContentPart{
					{Type: "image_url", ImageURL: "data:image/jpg;base64,aGVsbG8="},
				},
			},
		},
	}
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cr.Messages[0].Content[0].Image.Format != "jpeg" {
		t.Fatalf("expected jpg to normalize to jpeg, got %q", cr.Messages[0].Content[0].Image.Format)
	}
}

func TestToConverseRequestRemoteImageURLErrors(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{
			{
				Role: "user",
				Parts: []types.ContentPart{
					{Type: "image_url", ImageURL: "https://example.com/cat.png"},
				},
			},
		},
	}
	if _, err := ToConverseRequest(req); err == nil {
		t.Fatal("expected error for remote image URL, got nil")
	}
}

func TestToConverseRequestMixedTextAndImageParts(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{
			{
				Role: "user",
				Parts: []types.ContentPart{
					{Type: "text", Text: "describe this"},
					{Type: "image_url", ImageURL: "data:image/webp;base64,d2VicA=="},
				},
			},
			{Role: "assistant", Content: "a description"},
		},
	}
	cr, err := ToConverseRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cr.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(cr.Messages))
	}
	if len(cr.Messages[0].Content) != 2 {
		t.Fatalf("expected 2 blocks in first message, got %d", len(cr.Messages[0].Content))
	}
	if cr.Messages[1].Content[0].Text != "a description" {
		t.Fatalf("unexpected second message content: %+v", cr.Messages[1].Content)
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
