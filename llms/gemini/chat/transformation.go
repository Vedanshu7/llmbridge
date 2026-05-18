// Package chat handles Google Gemini API wire-format transformations.
package chat

import (
	"github.com/Vedanshu7/llmbridge/types"
)

// ---- Wire types ----

// GeminiRequest is the body sent to the Gemini generateContent endpoint.
type GeminiRequest struct {
	Contents          []GeminiContent         `json:"contents"`
	SystemInstruction *GeminiContent          `json:"systemInstruction,omitempty"`
	Tools             []GeminiTool            `json:"tools,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}

// GeminiContent is a single turn in the conversation (user or model).
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart is a text fragment within a content block.
type GeminiPart struct {
	Text         string              `json:"text,omitempty"`
	FunctionCall *GeminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResp *GeminiFunctionResp `json:"functionResponse,omitempty"`
}

// GeminiFunctionCall is a tool invocation requested by the model.
type GeminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// GeminiFunctionResp carries tool output back to the model.
type GeminiFunctionResp struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// GeminiTool wraps function declarations.
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDecl `json:"functionDeclarations"`
}

// GeminiFunctionDecl is a single tool/function definition.
type GeminiFunctionDecl struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  GeminiParamSchema `json:"parameters"`
}

// GeminiParamSchema mirrors JSON Schema for function parameters.
type GeminiParamSchema struct {
	Type       string                       `json:"type"`
	Properties map[string]GeminiParamProp   `json:"properties,omitempty"`
	Required   []string                     `json:"required,omitempty"`
}

// GeminiParamProp is a single parameter within GeminiParamSchema.
type GeminiParamProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// GeminiGenerationConfig holds sampling and output parameters.
type GeminiGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	TopP            float64 `json:"topP,omitempty"`
}

// GeminiResponse is the body returned by the generateContent endpoint.
type GeminiResponse struct {
	Candidates    []GeminiCandidate    `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	Error         *GeminiAPIError      `json:"error,omitempty"`
}

// GeminiCandidate is a single response candidate.
type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

// GeminiUsageMetadata holds token counts.
type GeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// GeminiAPIError is returned in the error field when the API fails.
type GeminiAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// GeminiStreamChunk is one SSE event payload during streamGenerateContent.
type GeminiStreamChunk struct {
	Candidates    []GeminiCandidate    `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
}

// ---- Conversion functions ----

// ToGeminiRequest translates a normalised types.Request into a GeminiRequest.
func ToGeminiRequest(req types.Request, model string, stream bool) GeminiRequest {
	gr := GeminiRequest{}

	if req.System != "" {
		gr.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: req.System}},
		}
	}

	for _, m := range req.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		gc := GeminiContent{Role: role}
		if m.Content != "" {
			gc.Parts = append(gc.Parts, GeminiPart{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			gc.Parts = append(gc.Parts, GeminiPart{
				FunctionCall: &GeminiFunctionCall{Name: tc.Name},
			})
		}
		gr.Contents = append(gr.Contents, gc)
	}

	if len(req.Tools) > 0 {
		decls := make([]GeminiFunctionDecl, len(req.Tools))
		for i, t := range req.Tools {
			props := make(map[string]GeminiParamProp, len(t.Parameters.Properties))
			for k, v := range t.Parameters.Properties {
				props[k] = GeminiParamProp{
					Type:        v.Type,
					Description: v.Description,
					Enum:        v.Enum,
				}
			}
			decls[i] = GeminiFunctionDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters: GeminiParamSchema{
					Type:       t.Parameters.Type,
					Properties: props,
					Required:   t.Parameters.Required,
				},
			}
		}
		gr.Tools = []GeminiTool{{FunctionDeclarations: decls}}
	}

	cfg := &GeminiGenerationConfig{}
	if req.MaxTokens > 0 {
		cfg.MaxOutputTokens = req.MaxTokens
	}
	if req.Temperature > 0 {
		cfg.Temperature = req.Temperature
	}
	if cfg.MaxOutputTokens > 0 || cfg.Temperature > 0 {
		gr.GenerationConfig = cfg
	}

	return gr
}

// FromGeminiResponse translates a GeminiResponse into a types.Response.
func FromGeminiResponse(gr *GeminiResponse, providerName, model string) *types.Response {
	resp := &types.Response{
		Provider: providerName,
		Model:    model,
	}
	if len(gr.Candidates) == 0 {
		return resp
	}
	cand := gr.Candidates[0]
	for _, part := range cand.Content.Parts {
		if part.Text != "" {
			resp.Content += part.Text
		}
		if part.FunctionCall != nil {
			resp.ToolCalls = append(resp.ToolCalls, types.ToolCall{
				Name: part.FunctionCall.Name,
			})
		}
	}
	if gr.UsageMetadata != nil {
		resp.Usage = &types.UsageData{
			PromptTokens:     gr.UsageMetadata.PromptTokenCount,
			CompletionTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gr.UsageMetadata.TotalTokenCount,
		}
	}
	return resp
}
