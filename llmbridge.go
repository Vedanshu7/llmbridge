// Package llmbridge provides a unified interface to multiple LLM providers.
//
// Every provider implements the Provider interface, so you can swap between
// OpenAI, Anthropic, Ollama, LM Studio, or any OpenAI-compatible endpoint
// without changing your application code.
//
// Quick start:
//
//	p := llmbridge.NewOpenAI("gpt-4o-mini", os.Getenv("OPENAI_API_KEY"))
//	resp, err := p.Complete(ctx, llmbridge.Request{
//	    System:   "You are a helpful assistant.",
//	    Messages: []llmbridge.Message{{Role: "user", Content: "Hello!"}},
//	})
package llmbridge

import "context"

// Provider is the unified interface every LLM backend must satisfy.
type Provider interface {
	// Complete sends a request and returns the full response.
	Complete(ctx context.Context, req Request) (*Response, error)

	// Name returns the provider identifier (e.g. "openai", "anthropic", "ollama").
	Name() string
}

// Request is the normalized, provider-agnostic input to any LLM.
type Request struct {
	// System is the system prompt. Passed as a top-level field on providers
	// that support it (Anthropic), or as a system-role message on others.
	System string

	// Messages is the conversation history.
	Messages []Message

	// Tools is the set of functions the model can call.
	// Leave nil to disable tool use.
	Tools []Tool

	// Model overrides the provider's default model for this request.
	// Leave empty to use the provider default.
	Model string

	// MaxTokens caps the response length. 0 uses the provider default.
	MaxTokens int

	// Temperature controls randomness. 0 = deterministic.
	Temperature float64
}

// Response is the normalized output from any provider.
type Response struct {
	// Content is the text content of the reply, if any.
	Content string

	// ToolCalls lists the tool invocations the model requested, if any.
	// When non-empty, the caller should execute the tools and send results
	// back as tool-role Messages in the next Request.
	ToolCalls []ToolCall
}

// Message is a single turn in a conversation.
type Message struct {
	// Role is one of: "user", "assistant", "tool".
	Role string

	// Content is the text content of the message.
	Content string

	// ToolCallID is the ID linking a "tool" role message to the ToolCall
	// that requested it. Required when Role == "tool".
	ToolCallID string

	// ToolCalls lists tool invocations requested by an "assistant" message.
	ToolCalls []ToolCall
}

// ToolCall is a single tool invocation requested by the model.
type ToolCall struct {
	// ID is the opaque identifier the model assigned. Must be echoed back
	// in the tool result message's ToolCallID.
	ID string

	// Name is the tool name from the Tools list.
	Name string

	// Arguments is the raw JSON object of tool inputs.
	Arguments string
}

// Tool defines a function the model can invoke.
type Tool struct {
	Name        string
	Description string
	Parameters  Schema
}

// Schema is the JSON Schema definition of tool parameters.
type Schema struct {
	// Type is always "object" for tool parameters.
	Type string

	// Properties maps parameter names to their definitions.
	Properties map[string]Property

	// Required lists the names of mandatory parameters.
	Required []string
}

// Property is a single parameter in a Schema.
type Property struct {
	Type        string
	Description string
	// Enum restricts valid values. Leave nil for free-form input.
	Enum []string
}
