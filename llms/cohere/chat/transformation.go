// Package chat handles Cohere API wire-format transformations.
package chat

import (
	"encoding/json"

	"github.com/Vedanshu7/llmbridge/types"
)

// ---- Wire types ----

// CohereRequest is the body sent to the Cohere v2 chat endpoint.
type CohereRequest struct {
	Model       string          `json:"model"`
	Messages    []CohereMessage `json:"messages"`
	Tools       []CohereTool    `json:"tools,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

// CohereMessage is a single turn in the Cohere conversation.
type CohereMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CohereTool defines a function available to the model.
type CohereTool struct {
	Type     string             `json:"type"`
	Function CohereToolFunction `json:"function"`
}

// CohereToolFunction is the function definition within a CohereTool.
type CohereToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// CohereResponse is returned by the Cohere v2 chat endpoint.
type CohereResponse struct {
	ID      string          `json:"id"`
	Message CohereRespMsg   `json:"message"`
	Usage   *CohereUsage    `json:"usage,omitempty"`
}

// CohereRespMsg is the assistant message in CohereResponse.
type CohereRespMsg struct {
	Role      string             `json:"role"`
	Content   []CohereContent    `json:"content"`
	ToolCalls []CohereToolCall   `json:"tool_calls,omitempty"`
}

// CohereContent is a text block in the assistant response.
type CohereContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CohereToolCall is a tool invocation in the assistant response.
type CohereToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function CohereToolCallFunc `json:"function"`
}

// CohereToolCallFunc holds the name and arguments of a tool call.
type CohereToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// CohereUsage holds token counts.
type CohereUsage struct {
	BilledUnits *CohereTokenUnits `json:"billed_units,omitempty"`
	Tokens      *CohereTokenUnits `json:"tokens,omitempty"`
}

// CohereTokenUnits holds input/output token counts.
type CohereTokenUnits struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// CohereStreamEvent is one SSE event during a streaming Cohere response.
type CohereStreamEvent struct {
	Type  string          `json:"type"`
	Delta *CohereTextDelta `json:"delta,omitempty"`
	Usage *CohereUsage    `json:"usage,omitempty"`
}

// CohereTextDelta holds a partial text fragment.
type CohereTextDelta struct {
	Message *CohereStreamMessage `json:"message,omitempty"`
}

// CohereStreamMessage is the message delta in a stream event.
type CohereStreamMessage struct {
	Content []CohereContent `json:"content,omitempty"`
}

// ---- Conversion functions ----

// ToCohereRequest translates a types.Request into a CohereRequest.
func ToCohereRequest(req types.Request, model string, stream bool) CohereRequest {
	cr := CohereRequest{
		Model:       model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      stream,
	}

	if req.System != "" {
		cr.Messages = append(cr.Messages, CohereMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		cr.Messages = append(cr.Messages, CohereMessage{Role: m.Role, Content: m.Content})
	}

	for _, t := range req.Tools {
		paramsMap := map[string]interface{}{
			"type":       t.Parameters.Type,
			"required":   t.Parameters.Required,
			"properties": t.Parameters.Properties,
		}
		cr.Tools = append(cr.Tools, CohereTool{
			Type: "function",
			Function: CohereToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  paramsMap,
			},
		})
	}
	return cr
}

// FromCohereResponse translates a CohereResponse into a types.Response.
func FromCohereResponse(cr *CohereResponse, providerName, model string) *types.Response {
	resp := &types.Response{
		Provider: providerName,
		Model:    model,
	}
	for _, block := range cr.Message.Content {
		if block.Type == "text" {
			resp.Content += block.Text
		}
	}
	for _, tc := range cr.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, types.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	if cr.Usage != nil {
		u := cr.Usage
		if u.BilledUnits != nil {
			resp.Usage = &types.UsageData{
				PromptTokens:     u.BilledUnits.InputTokens,
				CompletionTokens: u.BilledUnits.OutputTokens,
				TotalTokens:      u.BilledUnits.InputTokens + u.BilledUnits.OutputTokens,
			}
		} else if u.Tokens != nil {
			resp.Usage = &types.UsageData{
				PromptTokens:     u.Tokens.InputTokens,
				CompletionTokens: u.Tokens.OutputTokens,
				TotalTokens:      u.Tokens.InputTokens + u.Tokens.OutputTokens,
			}
		}
	}
	return resp
}

// ---- Rerank wire types ----

// CohereRerankRequest is the body for the /v1/rerank endpoint.
type CohereRerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents,omitempty"`
}

// CohereRerankResponse is returned by the /v1/rerank endpoint.
type CohereRerankResponse struct {
	ID      string               `json:"id"`
	Results []CohereRerankResult `json:"results"`
	Usage   *CohereUsage         `json:"usage,omitempty"`
}

// CohereRerankResult is a single ranked document.
type CohereRerankResult struct {
	Index          int              `json:"index"`
	RelevanceScore float64          `json:"relevance_score"`
	Document       *CohereRerankDoc `json:"document,omitempty"`
}

// CohereRerankDoc holds the text of a returned document.
type CohereRerankDoc struct {
	Text string `json:"text"`
}

// MarshalSSELine extracts the JSON payload from a Cohere SSE "data:" line.
func MarshalSSELine(line string) ([]byte, bool) {
	const prefix = "data: "
	if len(line) < len(prefix) {
		return nil, false
	}
	b := []byte(line[len(prefix):])
	var check json.RawMessage
	if json.Unmarshal(b, &check) != nil {
		return nil, false
	}
	return b, true
}
