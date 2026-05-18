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

import (
	"context"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/base"
	"github.com/Vedanshu7/llmbridge/types"
)

// Type aliases — all existing llmbridge.X names remain valid without change.

// Provider is the unified interface every LLM backend must satisfy.
type Provider = base.LLM

// Streamer is the optional interface for token-by-token streaming.
type Streamer = base.Streamer

// EmbedProvider is the optional interface for embedding generation.
type EmbedProvider = base.EmbedProvider

// Request is the normalized, provider-agnostic input to any LLM.
type Request = types.Request

// Response is the normalized output from any provider.
type Response = types.Response

// Message is a single turn in a conversation.
type Message = types.Message

// ToolCall is a single tool invocation requested by the model.
type ToolCall = types.ToolCall

// Tool defines a function the model can invoke.
type Tool = types.Tool

// Schema is the JSON Schema definition of tool parameters.
type Schema = types.Schema

// Property is a single parameter in a Schema.
type Property = types.Property

// Delta is a single token or structured fragment emitted during streaming.
type Delta = types.Delta

// ModelInfo describes the capabilities and pricing of a specific model.
type ModelInfo = types.ModelInfo

// UsageData holds token consumption metrics.
type UsageData = types.UsageData

// CallType identifies the kind of LLM operation.
type CallType = types.CallType

// Error type aliases for backward compatibility.

// ErrAuth indicates an authentication or authorization failure.
// Deprecated: use exceptions.AuthenticationError directly.
type ErrAuth = exceptions.AuthenticationError

// ErrRateLimit indicates the provider throttled the request.
// Deprecated: use exceptions.RateLimitError directly.
type ErrRateLimit = exceptions.RateLimitError

// ErrTimeout indicates the request exceeded the HTTP deadline.
// Deprecated: use exceptions.TimeoutError directly.
type ErrTimeout = exceptions.TimeoutError

// ErrProvider wraps a provider-level failure.
// Deprecated: use exceptions.ProviderError directly.
type ErrProvider = exceptions.ProviderError

// AsyncResult wraps a Response and error for async operations.
type AsyncResult = types.AsyncResult

// Complete sends a blocking completion request using the given provider.
// This is a package-level convenience wrapper around provider.Complete.
func Complete(ctx context.Context, p Provider, req Request) (*Response, error) {
	return p.Complete(ctx, req)
}

// AComplete sends a completion request asynchronously and returns a channel
// that will receive exactly one AsyncResult.
func AComplete(ctx context.Context, p Provider, req Request) <-chan AsyncResult {
	ch := make(chan AsyncResult, 1)
	go func() {
		resp, err := p.Complete(ctx, req)
		ch <- AsyncResult{Response: resp, Err: err}
	}()
	return ch
}

// Embed generates vector embeddings using the given EmbedProvider.
func Embed(ctx context.Context, p EmbedProvider, texts []string) ([][]float64, error) {
	return p.Embed(ctx, texts)
}
