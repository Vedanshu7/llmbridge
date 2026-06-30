package anthropic

import (
	"strings"

	"github.com/Vedanshu7/llmbridge/types"
)

// HasReasoningContent returns true if the response contains a <think> block,
// indicating the model produced explicit extended-thinking output.
func HasReasoningContent(resp *types.Response) bool {
	return strings.HasPrefix(resp.Content, "<think>")
}

// ExtractReasoning splits a response that contains a <think> block into the
// reasoning text and the final answer. If no <think> block is present, it
// returns ("", content).
func ExtractReasoning(content string) (reasoning, answer string) {
	const open, close = "<think>\n", "\n</think>\n"
	if !strings.HasPrefix(content, open) {
		return "", content
	}
	rest := strings.TrimPrefix(content, open)
	idx := strings.Index(rest, close)
	if idx < 0 {
		return rest, ""
	}
	return rest[:idx], rest[idx+len(close):]
}
