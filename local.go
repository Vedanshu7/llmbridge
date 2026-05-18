package llmbridge

// Local provider constructors for self-hosted / Docker-based LLMs.
// All of these speak the OpenAI chat completions wire format,
// so they reuse OpenAIProvider with a custom base URL and no auth.

const (
	ollamaDefaultURL   = "http://localhost:11434/v1/chat/completions"
	lmstudioDefaultURL = "http://localhost:1234/v1/chat/completions"
	ollamaDefaultModel = "llama3.2"
	lmstudioDefaultModel = "local-model"
)

// NewOllama returns a provider for a local Ollama instance.
// Ollama must be running: https://ollama.com
// Pull a model first: ollama pull llama3.2
//
// Via Docker:
//
//	docker run -d -p 11434:11434 ollama/ollama
//	docker exec <container> ollama pull llama3.2
func NewOllama(model string) Provider {
	if model == "" {
		model = ollamaDefaultModel
	}
	return NewOpenAICompatible("ollama", ollamaDefaultURL, "", model)
}

// NewOllamaAt returns a provider for an Ollama instance at a custom URL.
// Use this when Ollama is running on a remote machine or non-default port.
func NewOllamaAt(baseURL, model string) Provider {
	if baseURL == "" {
		baseURL = ollamaDefaultURL
	}
	if model == "" {
		model = ollamaDefaultModel
	}
	return NewOpenAICompatible("ollama", baseURL, "", model)
}

// NewLMStudio returns a provider for a local LM Studio server.
// Start the server in LM Studio under Local Server tab.
//
// Via Docker (community image):
//
//	docker run -d -p 1234:1234 ghcr.io/lmstudio-community/lmstudio-server
func NewLMStudio(model string) Provider {
	if model == "" {
		model = lmstudioDefaultModel
	}
	return NewOpenAICompatible("lmstudio", lmstudioDefaultURL, "", model)
}

// NewLMStudioAt returns a provider for an LM Studio server at a custom URL.
func NewLMStudioAt(baseURL, model string) Provider {
	if baseURL == "" {
		baseURL = lmstudioDefaultURL
	}
	if model == "" {
		model = lmstudioDefaultModel
	}
	return NewOpenAICompatible("lmstudio", baseURL, "", model)
}

// NewGroq returns a provider backed by Groq's fast inference API.
// Groq is OpenAI-compatible with fast inference for open models.
// Get an API key at https://console.groq.com
func NewGroq(model, apiKey string) Provider {
	return NewOpenAICompatible("groq", "https://api.groq.com/openai/v1/chat/completions", apiKey, model)
}

// NewTogetherAI returns a provider backed by Together AI.
// Together AI hosts many open-source models.
func NewTogetherAI(model, apiKey string) Provider {
	return NewOpenAICompatible("together", "https://api.together.xyz/v1/chat/completions", apiKey, model)
}
