package compatible

import "github.com/Vedanshu7/llmbridge/llms/openai"

const (
	ollamaDefaultURL     = "http://localhost:11434/v1/chat/completions"
	ollamaDefaultModel   = "llama3.2"
	lmstudioDefaultURL   = "http://localhost:1234/v1/chat/completions"
	lmstudioDefaultModel = "local-model"
	groqURL              = "https://api.groq.com/openai/v1/chat/completions"
	togetherURL          = "https://api.together.xyz/v1/chat/completions"
)

// NewOllama returns a Provider for a local Ollama instance (default: localhost:11434).
// Ollama must be running. Pull a model first with: ollama pull llama3.2
//
// Via Docker:
//
//	docker run -d -p 11434:11434 ollama/ollama
//	docker exec <container> ollama pull llama3.2
func NewOllama(model string) *openai.Provider {
	if model == "" {
		model = ollamaDefaultModel
	}
	return openai.NewCompatible("ollama", ollamaDefaultURL, "", model)
}

// NewOllamaAt returns a Provider for an Ollama instance at a custom URL.
func NewOllamaAt(baseURL, model string) *openai.Provider {
	if baseURL == "" {
		baseURL = ollamaDefaultURL
	}
	if model == "" {
		model = ollamaDefaultModel
	}
	return openai.NewCompatible("ollama", baseURL, "", model)
}

// NewLMStudio returns a Provider for a local LM Studio server (default: localhost:1234).
// Start the server from the LM Studio "Local Server" tab.
//
// Via Docker (community image):
//
//	docker run -d -p 1234:1234 ghcr.io/lmstudio-community/lmstudio-server
func NewLMStudio(model string) *openai.Provider {
	if model == "" {
		model = lmstudioDefaultModel
	}
	return openai.NewCompatible("lmstudio", lmstudioDefaultURL, "", model)
}

// NewLMStudioAt returns a Provider for an LM Studio server at a custom URL.
func NewLMStudioAt(baseURL, model string) *openai.Provider {
	if baseURL == "" {
		baseURL = lmstudioDefaultURL
	}
	if model == "" {
		model = lmstudioDefaultModel
	}
	return openai.NewCompatible("lmstudio", baseURL, "", model)
}

// NewGroq returns a Provider backed by Groq's fast inference API.
// Get an API key at https://console.groq.com
func NewGroq(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("groq", groqURL, apiKey, model)
}

// NewTogetherAI returns a Provider backed by Together AI, which hosts many
// open-source models. Get an API key at https://api.together.xyz
func NewTogetherAI(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("together", togetherURL, apiKey, model)
}
