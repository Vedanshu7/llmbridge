package chat

import (
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestToCohereRequestBasic(t *testing.T) {
	req := types.Request{
		Model:    "command-r",
		Messages: []types.Message{{Role: "user", Content: "hello"}},
	}
	cr := ToCohereRequest(req, "command-r", false)
	if cr.Model != "command-r" {
		t.Fatalf("expected command-r, got %s", cr.Model)
	}
	if len(cr.Messages) != 1 || cr.Messages[0].Content != "hello" {
		t.Fatalf("unexpected messages: %+v", cr.Messages)
	}
}

func TestToCohereRequestSystemPrompt(t *testing.T) {
	req := types.Request{
		System:   "be helpful",
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}
	cr := ToCohereRequest(req, "command-r", false)
	if len(cr.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(cr.Messages))
	}
	if cr.Messages[0].Role != "system" || cr.Messages[0].Content != "be helpful" {
		t.Fatalf("system message mismatch: %+v", cr.Messages[0])
	}
}

func TestToCohereRequestStream(t *testing.T) {
	req := types.Request{Messages: []types.Message{{Role: "user", Content: "x"}}}
	cr := ToCohereRequest(req, "command-r", true)
	if !cr.Stream {
		t.Fatal("expected stream=true")
	}
}

func TestToCohereRequestTools(t *testing.T) {
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
			},
		}},
	}
	cr := ToCohereRequest(req, "command-r", false)
	if len(cr.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(cr.Tools))
	}
	if cr.Tools[0].Function.Name != "search" {
		t.Fatalf("tool name mismatch")
	}
}

func TestFromCohereResponseText(t *testing.T) {
	cr := &CohereResponse{
		ID: "resp1",
		Message: CohereRespMsg{
			Role:    "assistant",
			Content: []CohereContent{{Type: "text", Text: "hello"}},
		},
		Usage: &CohereUsage{
			BilledUnits: &CohereTokenUnits{InputTokens: 10, OutputTokens: 5},
		},
	}
	resp := FromCohereResponse(cr, "cohere", "command-r")
	if resp.Content != "hello" {
		t.Fatalf("expected hello, got %q", resp.Content)
	}
	if resp.Provider != "cohere" || resp.Model != "command-r" {
		t.Fatalf("provider/model mismatch")
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Fatalf("usage mismatch: %+v", resp.Usage)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("expected TotalTokens=15, got %d", resp.Usage.TotalTokens)
	}
}

func TestFromCohereResponseUsageFallbackToTokens(t *testing.T) {
	cr := &CohereResponse{
		Message: CohereRespMsg{Content: []CohereContent{{Type: "text", Text: "ok"}}},
		Usage: &CohereUsage{
			Tokens: &CohereTokenUnits{InputTokens: 20, OutputTokens: 10},
		},
	}
	resp := FromCohereResponse(cr, "cohere", "command-r")
	if resp.Usage == nil || resp.Usage.PromptTokens != 20 {
		t.Fatalf("expected fallback to Tokens usage: %+v", resp.Usage)
	}
}

func TestFromCohereResponseToolCalls(t *testing.T) {
	cr := &CohereResponse{
		Message: CohereRespMsg{
			Content: []CohereContent{{Type: "text", Text: ""}},
			ToolCalls: []CohereToolCall{{
				ID:   "tc1",
				Type: "function",
				Function: CohereToolCallFunc{
					Name:      "search",
					Arguments: `{"query":"Go lang"}`,
				},
			}},
		},
	}
	resp := FromCohereResponse(cr, "cohere", "command-r")
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Fatalf("tool name mismatch")
	}
}

func TestFromCohereResponseMultipleTextBlocks(t *testing.T) {
	cr := &CohereResponse{
		Message: CohereRespMsg{
			Content: []CohereContent{
				{Type: "text", Text: "hello "},
				{Type: "text", Text: "world"},
			},
		},
	}
	resp := FromCohereResponse(cr, "cohere", "command-r")
	if resp.Content != "hello world" {
		t.Fatalf("expected concatenated text, got %q", resp.Content)
	}
}

func TestMarshalSSELineValid(t *testing.T) {
	line := `data: {"type":"content-delta","delta":{"message":{"content":[{"type":"text","text":"hi"}]}}}`
	b, ok := MarshalSSELine(line)
	if !ok {
		t.Fatal("expected valid SSE line to parse")
	}
	if len(b) == 0 {
		t.Fatal("expected non-empty payload")
	}
}

func TestMarshalSSELineInvalidJSON(t *testing.T) {
	line := "data: not valid json{"
	_, ok := MarshalSSELine(line)
	if ok {
		t.Fatal("expected invalid JSON to return false")
	}
}

func TestMarshalSSELineTooShort(t *testing.T) {
	_, ok := MarshalSSELine("data:")
	if ok {
		t.Fatal("expected short line to return false")
	}
}
