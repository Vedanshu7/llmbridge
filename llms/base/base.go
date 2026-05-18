// Package base defines the core interfaces that all LLM provider implementations
// must satisfy. Provider-specific packages (llms/openai, llms/anthropic, etc.)
// implement these interfaces.
package base

import (
	"context"

	"github.com/Vedanshu7/llmbridge/types"
)

// LLM is the unified interface every LLM backend must satisfy.
type LLM interface {
	// Complete sends a request and returns the full response.
	Complete(ctx context.Context, req types.Request) (*types.Response, error)

	// Name returns the provider identifier (e.g. "openai", "anthropic").
	Name() string

	// ValidateEnvironment checks that required credentials and configuration
	// are present (e.g. API key env vars). Returns an error if not satisfied.
	ValidateEnvironment() error
}

// Streamer is an optional interface that providers may implement to support
// token-by-token streaming responses.
//
//	if s, ok := provider.(base.Streamer); ok {
//	    ch, err := s.Stream(ctx, req)
//	}
//
// The returned channel is closed when the stream ends. A final Delta with
// Done == true signals clean completion; Err != nil signals failure.
type Streamer interface {
	Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error)
}

// EmbedProvider is an optional interface for providers that support
// generating vector embeddings from text.
type EmbedProvider interface {
	// Embed returns a vector embedding for each input text.
	Embed(ctx context.Context, texts []string) ([][]float64, error)

	// Name returns the provider identifier.
	Name() string
}
