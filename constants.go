package llmbridge

// Version is the current module version.
const Version = "0.1.0"

// Default HTTP timeout for provider requests.
const DefaultHTTPTimeout = 60 // seconds

// Default model aliases used when no model is specified on a Request.
var DefaultModels = map[string]string{
	"openai":    "gpt-4o-mini",
	"anthropic": "claude-sonnet-4-6",
	"ollama":    "llama3.2",
	"groq":      "llama-3.3-70b-versatile",
	"together":  "meta-llama/Llama-3-8b-chat-hf",
	"lmstudio":  "local-model",
}

// SupportedProviders lists the built-in provider names.
var SupportedProviders = []string{
	"openai",
	"anthropic",
	"ollama",
	"lmstudio",
	"groq",
	"together",
}

// ModelInfoDB is a static registry of known models and their capabilities.
// Sourced from public provider documentation; update as new models are released.
var ModelInfoDB = map[string]ModelInfo{
	// OpenAI
	"gpt-4o": {
		MaxTokens: 128000, MaxInputTokens: 128000,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.0000025, OutputCostPerToken: 0.000010,
	},
	"gpt-4o-mini": {
		MaxTokens: 128000, MaxInputTokens: 128000,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.00000015, OutputCostPerToken: 0.0000006,
	},
	"gpt-4-turbo": {
		MaxTokens: 128000, MaxInputTokens: 128000,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.00001, OutputCostPerToken: 0.00003,
	},
	"gpt-3.5-turbo": {
		MaxTokens: 16385, MaxInputTokens: 16385,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.0000005, OutputCostPerToken: 0.0000015,
	},
	"o1": {
		MaxTokens: 200000, MaxInputTokens: 200000,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.000015, OutputCostPerToken: 0.00006,
	},
	// Anthropic
	"claude-opus-4-7": {
		MaxTokens: 200000, MaxInputTokens: 200000,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.000015, OutputCostPerToken: 0.000075,
	},
	"claude-sonnet-4-6": {
		MaxTokens: 200000, MaxInputTokens: 200000,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.000003, OutputCostPerToken: 0.000015,
	},
	"claude-haiku-4-5-20251001": {
		MaxTokens: 200000, MaxInputTokens: 200000,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.0000008, OutputCostPerToken: 0.000004,
	},
	"claude-3-5-sonnet-20241022": {
		MaxTokens: 200000, MaxInputTokens: 200000,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.000003, OutputCostPerToken: 0.000015,
	},
	"claude-3-haiku-20240307": {
		MaxTokens: 200000, MaxInputTokens: 200000,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.00000025, OutputCostPerToken: 0.00000125,
	},
}
