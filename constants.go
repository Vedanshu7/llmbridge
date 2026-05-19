package llmbridge

// Version is the current module version.
const Version = "0.3.0"

// Default HTTP timeout for provider requests.
const DefaultHTTPTimeout = 60 // seconds

// Default model aliases used when no model is specified on a Request.
var DefaultModels = map[string]string{
	"openai":     "gpt-4o-mini",
	"anthropic":  "claude-sonnet-4-6",
	"gemini":     "gemini-2.0-flash",
	"azure":      "gpt-4o",
	"cohere":     "command-r-plus-08-2024",
	"bedrock":    "anthropic.claude-3-5-sonnet-20241022-v2:0",
	"ollama":     "llama3.2",
	"groq":       "llama-3.3-70b-versatile",
	"together":   "meta-llama/Llama-3-8b-chat-hf",
	"lmstudio":   "local-model",
	"deepseek":   "deepseek-chat",
	"perplexity": "llama-3.1-sonar-large-128k-online",
	"fireworks":  "accounts/fireworks/models/llama-v3p1-70b-instruct",
	"cerebras":   "llama3.1-70b",
	"sambanova":  "Meta-Llama-3.1-70B-Instruct",
	"mistral":    "mistral-large-latest",
	"hyperbolic": "meta-llama/Meta-Llama-3.1-70B-Instruct",
	"novita":     "meta-llama/llama-3.1-70b-instruct",
	"xai":        "grok-2-latest",
}

// SupportedProviders lists the built-in provider names.
var SupportedProviders = []string{
	"openai",
	"anthropic",
	"gemini",
	"azure",
	"cohere",
	"bedrock",
	"ollama",
	"lmstudio",
	"groq",
	"together",
	"deepseek",
	"perplexity",
	"fireworks",
	"cerebras",
	"sambanova",
	"mistral",
	"hyperbolic",
	"novita",
	"xai",
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
	// Google Gemini
	"gemini-2.0-flash": {
		MaxTokens: 1048576, MaxInputTokens: 1048576,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.0000001, OutputCostPerToken: 0.0000004,
	},
	"gemini-1.5-pro": {
		MaxTokens: 2097152, MaxInputTokens: 2097152,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.00000125, OutputCostPerToken: 0.000005,
	},
	"gemini-1.5-flash": {
		MaxTokens: 1048576, MaxInputTokens: 1048576,
		SupportsFunctionCalling: true, SupportsVision: true, SupportsStreaming: true,
		InputCostPerToken: 0.000000075, OutputCostPerToken: 0.0000003,
	},
	// Cohere
	"command-r-plus-08-2024": {
		MaxTokens: 128000, MaxInputTokens: 128000,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.0000025, OutputCostPerToken: 0.00001,
	},
	"command-r-08-2024": {
		MaxTokens: 128000, MaxInputTokens: 128000,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.00000015, OutputCostPerToken: 0.0000006,
	},
	// DeepSeek
	"deepseek-chat": {
		MaxTokens: 65536, MaxInputTokens: 65536,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.00000027, OutputCostPerToken: 0.0000011,
	},
	"deepseek-coder": {
		MaxTokens: 65536, MaxInputTokens: 65536,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.00000014, OutputCostPerToken: 0.00000028,
	},
	// Mistral
	"mistral-large-latest": {
		MaxTokens: 131072, MaxInputTokens: 131072,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.000003, OutputCostPerToken: 0.000009,
	},
	"mistral-small-latest": {
		MaxTokens: 131072, MaxInputTokens: 131072,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.000001, OutputCostPerToken: 0.000003,
	},
	// Groq (fast inference)
	"llama-3.3-70b-versatile": {
		MaxTokens: 128000, MaxInputTokens: 128000,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.00000059, OutputCostPerToken: 0.00000079,
	},
	"llama-3.1-8b-instant": {
		MaxTokens: 128000, MaxInputTokens: 128000,
		SupportsFunctionCalling: true, SupportsStreaming: true,
		InputCostPerToken: 0.00000005, OutputCostPerToken: 0.00000008,
	},
}
