package chat

import (
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestToOAIRequestDefaults(t *testing.T) {
	req := types.Request{Messages: []types.Message{{Role: "user", Content: "hi"}}}
	oai := ToOAIRequest(req, "gpt-4o", false)
	if oai.Model != "gpt-4o" {
		t.Fatalf("expected gpt-4o, got %s", oai.Model)
	}
	if oai.Stream {
		t.Fatal("stream should be false")
	}
	if len(oai.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(oai.Messages))
	}
}

func TestToOAIRequestSystemPrompt(t *testing.T) {
	req := types.Request{
		System:   "you are helpful",
		Messages: []types.Message{{Role: "user", Content: "hello"}},
	}
	oai := ToOAIRequest(req, "gpt-4o", false)
	if len(oai.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(oai.Messages))
	}
	if oai.Messages[0].Role != "system" || oai.Messages[0].Content != "you are helpful" {
		t.Fatalf("system message not prepended correctly: %+v", oai.Messages[0])
	}
}

func TestToOAIRequestMaxTokens(t *testing.T) {
	req := types.Request{MaxTokens: 256, Messages: []types.Message{{Role: "user", Content: "x"}}}
	oai := ToOAIRequest(req, "gpt-4o", false)
	if oai.MaxTokens != 256 {
		t.Fatalf("expected 256, got %d", oai.MaxTokens)
	}
}

func TestToOAIRequestZeroMaxTokensOmitted(t *testing.T) {
	req := types.Request{Messages: []types.Message{{Role: "user", Content: "x"}}}
	oai := ToOAIRequest(req, "gpt-4o", false)
	if oai.MaxTokens != 0 {
		t.Fatalf("expected 0 (omit), got %d", oai.MaxTokens)
	}
}

func TestToOAIRequestTools(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{{Role: "user", Content: "x"}},
		Tools: []types.Tool{{
			Name:        "search",
			Description: "web search",
			Parameters: types.Schema{
				Type: "object",
				Properties: map[string]types.Property{
					"query": {Type: "string"},
				},
				Required: []string{"query"},
			},
		}},
	}
	oai := ToOAIRequest(req, "gpt-4o", false)
	if len(oai.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(oai.Tools))
	}
	fn := oai.Tools[0].Function
	if fn.Name != "search" || oai.Tools[0].Type != "function" {
		t.Fatalf("tool mismatch: %+v", oai.Tools[0])
	}
}

func TestFromOAIResponseText(t *testing.T) {
	raw := &OAIResponse{
		Choices: []struct {
			Message      OAIMessage `json:"message"`
			FinishReason string     `json:"finish_reason"`
		}{
			{Message: OAIMessage{Role: "assistant", Content: "hello"}},
		},
		Usage: &struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{100, 50, 150},
	}
	resp := FromOAIResponse(raw, "openai", "gpt-4o")
	if resp.Content != "hello" {
		t.Fatalf("expected hello, got %q", resp.Content)
	}
	if resp.Provider != "openai" || resp.Model != "gpt-4o" {
		t.Fatalf("provider/model mismatch")
	}
	if resp.Usage.PromptTokens != 100 || resp.Usage.TotalTokens != 150 {
		t.Fatalf("usage mismatch: %+v", resp.Usage)
	}
}

func TestFromOAIResponseToolCalls(t *testing.T) {
	raw := &OAIResponse{
		Choices: []struct {
			Message      OAIMessage `json:"message"`
			FinishReason string     `json:"finish_reason"`
		}{
			{
				Message: OAIMessage{
					Role: "assistant",
					ToolCalls: []OAIToolCall{{
						ID:   "call1",
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: "weather", Arguments: `{"city":"NYC"}`},
					}},
				},
			},
		},
	}
	resp := FromOAIResponse(raw, "openai", "gpt-4o")
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call1" || resp.ToolCalls[0].Name != "weather" {
		t.Fatalf("tool call mismatch: %+v", resp.ToolCalls[0])
	}
}

func TestFromMessageRoundtrip(t *testing.T) {
	m := types.Message{Role: "user", Content: "hello", ToolCallID: "tc1"}
	out := FromMessage(m)
	if out.Role != "user" || out.Content != "hello" || out.ToolCallID != "tc1" {
		t.Fatalf("message roundtrip mismatch: %+v", out)
	}
}

func TestFromMessageWithToolCalls(t *testing.T) {
	m := types.Message{
		Role: "assistant",
		ToolCalls: []types.ToolCall{
			{ID: "id1", Name: "fn", Arguments: `{"x":1}`},
		},
	}
	out := FromMessage(m)
	if len(out.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(out.ToolCalls))
	}
	otc := out.ToolCalls[0]
	if otc.ID != "id1" || otc.Type != "function" || otc.Function.Name != "fn" {
		t.Fatalf("tool call mismatch: %+v", otc)
	}
}

func TestFromToolOAI(t *testing.T) {
	tool := types.Tool{
		Name:        "calc",
		Description: "math",
		Parameters: types.Schema{
			Type: "object",
			Properties: map[string]types.Property{
				"n": {Type: "integer", Description: "number"},
			},
			Required: []string{"n"},
		},
	}
	oai := FromTool(tool)
	if oai.Type != "function" {
		t.Fatalf("expected type=function, got %q", oai.Type)
	}
	if oai.Function.Name != "calc" {
		t.Fatalf("expected calc, got %q", oai.Function.Name)
	}
	prop, ok := oai.Function.Parameters.Properties["n"]
	if !ok || prop.Type != "integer" {
		t.Fatalf("property mismatch")
	}
}
