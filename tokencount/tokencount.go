// Package tokencount provides heuristic token-count estimates for LLM requests
// and responses without requiring any external tokenizer library.
//
// Estimates use the GPT-3/4 approximation of ~4 characters per token, which is
// accurate to within ±15% for English prose. For exact counts, use the actual
// usage data returned by the provider.
//
// Usage:
//
//	n := tokencount.EstimateText("Hello, world!")      // ~3 tokens
//	n  = tokencount.EstimateRequest(&req)              // sum of all message content
//	n  = tokencount.EstimateResponse(&resp)            // completion content
package tokencount

import (
	"strings"

	"github.com/Vedanshu7/llmbridge/types"
)

const charsPerToken = 4

// EstimateText returns the estimated token count for a raw string.
// Uses the 4-chars-per-token heuristic common for English text.
func EstimateText(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + charsPerToken - 1) / charsPerToken
}

// EstimateMessages returns the estimated token count for a slice of Messages.
// Each message adds ~4 tokens of per-message overhead on top of content.
func EstimateMessages(messages []types.Message) int {
	total := 0
	for _, m := range messages {
		total += 4 // role + framing overhead per message
		total += EstimateText(m.Content)
	}
	return total
}

// EstimateRequest returns the estimated prompt token count for a Request.
// Includes the system prompt, all messages, and tool definitions.
func EstimateRequest(req *types.Request) int {
	if req == nil {
		return 0
	}
	total := EstimateText(req.System)
	total += EstimateMessages(req.Messages)
	for _, t := range req.Tools {
		total += EstimateText(t.Name)
		total += EstimateText(t.Description)
		for name, prop := range t.Parameters.Properties {
			total += EstimateText(name)
			total += EstimateText(prop.Description)
		}
	}
	return total
}

// EstimateResponse returns the estimated completion token count for a Response.
func EstimateResponse(resp *types.Response) int {
	if resp == nil {
		return 0
	}
	total := EstimateText(resp.Content)
	for _, tc := range resp.ToolCalls {
		total += EstimateText(tc.Name)
		total += EstimateText(tc.Arguments)
	}
	return total
}

// ModelMaxTokens returns the known context window size for common models.
// Returns 0 if the model is not recognised.
func ModelMaxTokens(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "gpt-4o"):
		return 128000
	case strings.HasPrefix(m, "gpt-4-turbo"):
		return 128000
	case strings.HasPrefix(m, "gpt-4"):
		return 8192
	case strings.HasPrefix(m, "gpt-3.5-turbo"):
		return 16385
	case strings.HasPrefix(m, "claude-3-5"), strings.HasPrefix(m, "claude-3"):
		return 200000
	case strings.HasPrefix(m, "claude-2"):
		return 100000
	case strings.HasPrefix(m, "claude-"):
		return 200000
	case strings.HasPrefix(m, "gemini-1.5"):
		return 1000000
	case strings.HasPrefix(m, "gemini-"):
		return 32768
	case strings.HasPrefix(m, "llama-3"):
		return 128000
	case strings.HasPrefix(m, "mistral-large"), strings.HasPrefix(m, "mixtral"):
		return 32768
	default:
		return 0
	}
}

// RemainingTokens returns the estimated number of tokens left in the context
// window given a request and a known model. Returns -1 if the model is unknown.
func RemainingTokens(model string, req *types.Request) int {
	max := ModelMaxTokens(model)
	if max == 0 {
		return -1
	}
	used := EstimateRequest(req)
	if used >= max {
		return 0
	}
	return max - used
}
