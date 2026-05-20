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

// ImageGenerator is an optional interface for providers that support
// generating images from text prompts (e.g. DALL-E).
type ImageGenerator interface {
	ImageGenerate(ctx context.Context, req types.ImageRequest) (*types.ImageResponse, error)
	Name() string
}

// Transcriber is an optional interface for providers that support
// converting audio to text (e.g. Whisper).
type Transcriber interface {
	Transcribe(ctx context.Context, req types.TranscriptionRequest) (*types.TranscriptionResponse, error)
	Name() string
}

// Reranker is an optional interface for providers that support
// reranking a list of documents given a query (e.g. Cohere Rerank).
type Reranker interface {
	Rerank(ctx context.Context, req types.RerankRequest) (*types.RerankResponse, error)
	Name() string
}

// TextCompleter is an optional interface for providers that support
// the legacy non-chat text completion endpoint.
type TextCompleter interface {
	TextComplete(ctx context.Context, req types.TextRequest) (*types.TextResponse, error)
	Name() string
}

// SpeechProvider is an optional interface for providers that support
// converting text to audio (e.g. OpenAI TTS).
type SpeechProvider interface {
	Speech(ctx context.Context, req types.SpeechRequest) (*types.SpeechResponse, error)
	Name() string
}

// Moderator is an optional interface for providers that support content
// moderation (e.g. OpenAI /v1/moderations).
type Moderator interface {
	Moderate(ctx context.Context, req types.ModerationRequest) (*types.ModerationResponse, error)
	Name() string
}

// BatchProvider is an optional interface for providers that support the native
// asynchronous batch API (e.g. OpenAI /v1/batches). For providers that do not
// implement this interface the proxy falls back to concurrent in-process batching.
type BatchProvider interface {
	// BatchCreate submits a batch job and returns an opaque batch ID.
	BatchCreate(ctx context.Context, reqs []types.Request) (batchID string, err error)

	// BatchStatus returns the current status ("queued", "in_progress", "completed",
	// "failed", "cancelled") and per-status request counts.
	BatchStatus(ctx context.Context, batchID string) (status string, counts map[string]int, err error)

	// BatchResults returns the completed results for a finished batch.
	// Returns an error if the batch is not yet completed.
	BatchResults(ctx context.Context, batchID string) ([]types.BatchResult, error)

	Name() string
}
