package chat

import (
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestToGeminiRequestBasic(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{{Role: "user", Content: "hello"}},
	}
	gr := ToGeminiRequest(req, "gemini-pro", false)
	if len(gr.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(gr.Contents))
	}
	if gr.Contents[0].Role != "user" || gr.Contents[0].Parts[0].Text != "hello" {
		t.Fatalf("content mismatch: %+v", gr.Contents[0])
	}
}

func TestToGeminiRequestSystemInstruction(t *testing.T) {
	req := types.Request{
		System:   "be helpful",
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}
	gr := ToGeminiRequest(req, "gemini-pro", false)
	if gr.SystemInstruction == nil {
		t.Fatal("expected SystemInstruction to be set")
	}
	if gr.SystemInstruction.Parts[0].Text != "be helpful" {
		t.Fatalf("system instruction mismatch: %+v", gr.SystemInstruction)
	}
}

func TestToGeminiRequestAssistantRoleMapping(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: "answer"},
		},
	}
	gr := ToGeminiRequest(req, "gemini-pro", false)
	if gr.Contents[1].Role != "model" {
		t.Fatalf("expected role=model for assistant, got %q", gr.Contents[1].Role)
	}
}

func TestToGeminiRequestGenerationConfig(t *testing.T) {
	req := types.Request{
		MaxTokens:   512,
		Temperature: 0.7,
		Messages:    []types.Message{{Role: "user", Content: "x"}},
	}
	gr := ToGeminiRequest(req, "gemini-pro", false)
	if gr.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig to be set")
	}
	if gr.GenerationConfig.MaxOutputTokens != 512 {
		t.Fatalf("expected 512, got %d", gr.GenerationConfig.MaxOutputTokens)
	}
	if gr.GenerationConfig.Temperature != 0.7 {
		t.Fatalf("expected 0.7, got %f", gr.GenerationConfig.Temperature)
	}
}

func TestToGeminiRequestNoGenerationConfig(t *testing.T) {
	req := types.Request{Messages: []types.Message{{Role: "user", Content: "x"}}}
	gr := ToGeminiRequest(req, "gemini-pro", false)
	if gr.GenerationConfig != nil {
		t.Fatal("expected nil GenerationConfig when no tokens/temperature set")
	}
}

func TestToGeminiRequestTools(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{{Role: "user", Content: "x"}},
		Tools: []types.Tool{{
			Name:        "lookup",
			Description: "lookup data",
			Parameters: types.Schema{
				Type: "object",
				Properties: map[string]types.Property{
					"key": {Type: "string", Description: "lookup key"},
				},
				Required: []string{"key"},
			},
		}},
	}
	gr := ToGeminiRequest(req, "gemini-pro", false)
	if len(gr.Tools) != 1 {
		t.Fatalf("expected 1 tool group, got %d", len(gr.Tools))
	}
	decls := gr.Tools[0].FunctionDeclarations
	if len(decls) != 1 || decls[0].Name != "lookup" {
		t.Fatalf("function declaration mismatch: %+v", decls)
	}
	prop, ok := decls[0].Parameters.Properties["key"]
	if !ok || prop.Type != "string" {
		t.Fatalf("property mismatch")
	}
}

func TestFromGeminiResponseText(t *testing.T) {
	gr := &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content:      GeminiContent{Parts: []GeminiPart{{Text: "hello"}}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &GeminiUsageMetadata{
			PromptTokenCount:     100,
			CandidatesTokenCount: 50,
			TotalTokenCount:      150,
		},
	}
	resp := FromGeminiResponse(gr, "gemini", "gemini-pro")
	if resp.Content != "hello" {
		t.Fatalf("expected hello, got %q", resp.Content)
	}
	if resp.Provider != "gemini" || resp.Model != "gemini-pro" {
		t.Fatalf("provider/model mismatch")
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 100 || resp.Usage.TotalTokens != 150 {
		t.Fatalf("usage mismatch: %+v", resp.Usage)
	}
}

func TestFromGeminiResponseFunctionCall(t *testing.T) {
	gr := &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content: GeminiContent{
				Parts: []GeminiPart{{
					FunctionCall: &GeminiFunctionCall{
						Name: "weather",
						Args: map[string]interface{}{"location": "NYC"},
					},
				}},
			},
		}},
	}
	resp := FromGeminiResponse(gr, "gemini", "gemini-pro")
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "weather" {
		t.Fatalf("expected weather, got %q", resp.ToolCalls[0].Name)
	}
}

func TestFromGeminiResponseEmptyCandidates(t *testing.T) {
	gr := &GeminiResponse{Candidates: nil}
	resp := FromGeminiResponse(gr, "gemini", "gemini-pro")
	if resp.Content != "" {
		t.Fatal("expected empty content for no candidates")
	}
	if resp.Usage != nil {
		t.Fatal("expected nil usage for no candidates")
	}
}

func TestFromGeminiResponseNoUsage(t *testing.T) {
	gr := &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content: GeminiContent{Parts: []GeminiPart{{Text: "ok"}}},
		}},
	}
	resp := FromGeminiResponse(gr, "gemini", "gemini-pro")
	if resp.Usage != nil {
		t.Fatal("expected nil usage when UsageMetadata is absent")
	}
}
