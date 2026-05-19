// Package types defines all core data structures shared across llmbridge packages.
package types

import "context"

// CallType identifies the kind of LLM operation being performed.
type CallType string

const (
	CallTypeCompletion      CallType = "completion"
	CallTypeAsyncCompletion CallType = "acompletion"
	CallTypeStreaming        CallType = "streaming"
	CallTypeEmbedding        CallType = "embedding"
	CallTypeImageGeneration CallType = "image_generation"
	CallTypeTranscription   CallType = "transcription"
	CallTypeReranking       CallType = "reranking"
	CallTypeTextCompletion  CallType = "text_completion"
	CallTypeBatch           CallType = "batch"
)

// Request is the normalized, provider-agnostic input to any LLM.
type Request struct {
	// System is the system prompt.
	System string

	// Messages is the conversation history.
	Messages []Message

	// Tools is the set of functions the model can call.
	Tools []Tool

	// Model overrides the provider's default model for this request.
	Model string

	// MaxTokens caps the response length. 0 uses the provider default.
	MaxTokens int

	// Temperature controls randomness. 0 = deterministic.
	Temperature float64

	// Stream signals whether the caller wants token-by-token output.
	Stream bool
}

// Response is the normalized output from any provider.
type Response struct {
	// Content is the text content of the reply, if any.
	Content string

	// ToolCalls lists the tool invocations the model requested, if any.
	ToolCalls []ToolCall

	// Provider identifies which backend produced this response (e.g. "openai").
	Provider string

	// Model is the specific model that generated this response.
	Model string

	// Usage holds token counts reported by the provider.
	Usage *UsageData
}

// UsageData holds token consumption metrics reported by a provider.
type UsageData struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CacheReadTokens  int
}

// Message is a single turn in a conversation.
type Message struct {
	// Role is one of: "user", "assistant", "tool".
	Role string

	// Content is the text content of the message.
	Content string

	// ToolCallID links a "tool" role message to the ToolCall that requested it.
	ToolCallID string

	// ToolCalls lists tool invocations requested by an "assistant" message.
	ToolCalls []ToolCall
}

// ToolCall is a single tool invocation requested by the model.
type ToolCall struct {
	// ID is the opaque identifier the model assigned.
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

// Delta is a single token or structured fragment emitted during streaming.
type Delta struct {
	// Content is a text fragment to append to the response so far.
	Content string

	// ToolCall carries a partial or complete tool-call update.
	ToolCall *ToolCall

	// Done is true on the final Delta.
	Done bool

	// Err is non-nil if the stream terminated with an error.
	Err error
}

// GenericStreamingChunk is an intermediate representation used internally
// by provider handlers to normalise SSE payloads before emitting Deltas.
type GenericStreamingChunk struct {
	Text            string
	ToolCallID      string
	ToolName        string
	ToolArgsDelta   string
	IsFinished      bool
}

// ModelInfo describes the capabilities and pricing of a specific model.
type ModelInfo struct {
	// MaxTokens is the total context window size.
	MaxTokens int

	// MaxInputTokens is the maximum input context.
	MaxInputTokens int

	// SupportsFunctionCalling indicates tool/function call support.
	SupportsFunctionCalling bool

	// SupportsVision indicates image input support.
	SupportsVision bool

	// SupportsStreaming indicates SSE streaming support.
	SupportsStreaming bool

	// InputCostPerToken is the cost in USD per input token.
	InputCostPerToken float64

	// OutputCostPerToken is the cost in USD per output token.
	OutputCostPerToken float64
}

// CostPerToken holds per-token pricing for a single operation.
type CostPerToken struct {
	InputCostPerToken  float64
	OutputCostPerToken float64
}

// ProviderSpecificModelInfo holds capability flags for provider-side features.
type ProviderSpecificModelInfo struct {
	SupportsFunctionCalling bool
	SupportsVision          bool
	SupportsStreaming        bool
	SupportsParallelFnCalls  bool
}

// ProviderField describes a configuration field specific to a provider.
type ProviderField struct {
	Name        string
	Description string
	Required    bool
}

// AsyncResult wraps a Response and error for asynchronous completion.
type AsyncResult struct {
	Response *Response
	Err      error
}

// BatchResult holds the outcome of one request in a BatchComplete call.
type BatchResult struct {
	Response *Response
	Err      error
	Index    int
}

// ImageRequest is the input to an image generation call.
type ImageRequest struct {
	Prompt   string
	N        int
	Size     string
	Quality  string
	Model    string
	Provider string
}

// ImageResponse is the output from an image generation call.
type ImageResponse struct {
	Images   []GeneratedImage
	Provider string
	Model    string
}

// GeneratedImage is a single image returned by an image generation call.
type GeneratedImage struct {
	URL           string
	B64JSON       string
	RevisedPrompt string
}

// TranscriptionRequest is the input to an audio transcription call.
type TranscriptionRequest struct {
	AudioData []byte
	Language  string
	Model     string
	Provider  string
	Format    string // "json", "text", "srt", "vtt"
}

// TranscriptionResponse is the output from an audio transcription call.
type TranscriptionResponse struct {
	Text     string
	Provider string
	Model    string
}

// RerankRequest is the input to a document reranking call.
type RerankRequest struct {
	Query           string
	Documents       []string
	TopN            int
	Model           string
	Provider        string
	ReturnDocuments bool
}

// RerankResponse is the output from a document reranking call.
type RerankResponse struct {
	Results  []RerankResult
	Provider string
	Model    string
}

// RerankResult is a single ranked document returned by a reranking call.
type RerankResult struct {
	Index    int
	Document string
	Score    float64
}

// TextRequest is the input to a legacy text completion call (non-chat).
type TextRequest struct {
	Prompt      string
	MaxTokens   int
	Temperature float64
	Model       string
	Provider    string
}

// TextResponse is the output from a legacy text completion call.
type TextResponse struct {
	Text     string
	Provider string
	Model    string
	Usage    *UsageData
}

// SpeechRequest is the input to a text-to-speech call.
type SpeechRequest struct {
	// Input is the text to synthesize.
	Input string

	// Model is the TTS model (e.g. "tts-1", "tts-1-hd").
	Model string

	// Voice selects the voice (e.g. "alloy", "echo", "fable", "onyx", "nova", "shimmer").
	Voice string

	// ResponseFormat is the audio format: "mp3", "opus", "aac", "flac", "wav", "pcm".
	ResponseFormat string

	// Speed adjusts the playback rate (0.25–4.0). 0 uses the provider default (1.0).
	Speed float64
}

// SpeechResponse is the output from a text-to-speech call.
type SpeechResponse struct {
	// Audio contains the raw audio bytes in the requested format.
	Audio []byte

	// Format is the audio format of the returned bytes.
	Format string

	// Provider identifies which backend produced this response.
	Provider string

	// Model is the specific model that generated this response.
	Model string
}

// LLM is the base interface every provider must satisfy.
// Defined here so types and base can share it without a circular import.
type LLM interface {
	Complete(ctx context.Context, req Request) (*Response, error)
	Name() string
	ValidateEnvironment() error
}

// Streamer is the optional interface for token-by-token streaming.
type Streamer interface {
	Stream(ctx context.Context, req Request) (<-chan Delta, error)
}

// EmbedProvider is the optional interface for embedding generation.
type EmbedProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	Name() string
}
