package compatible

import "github.com/Vedanshu7/llmbridge/llms/openai"

const (
	ollamaDefaultURL     = "http://localhost:11434/v1/chat/completions"
	ollamaDefaultModel   = "llama3.2"
	lmstudioDefaultURL   = "http://localhost:1234/v1/chat/completions"
	lmstudioDefaultModel = "local-model"
	groqURL              = "https://api.groq.com/openai/v1/chat/completions"
	togetherURL          = "https://api.together.xyz/v1/chat/completions"
	deepseekURL          = "https://api.deepseek.com/v1/chat/completions"
	perplexityURL        = "https://api.perplexity.ai/chat/completions"
	fireworksURL         = "https://api.fireworks.ai/inference/v1/chat/completions"
	cerebrasURL          = "https://api.cerebras.ai/v1/chat/completions"
	sambanovaURL         = "https://api.sambanova.ai/v1/chat/completions"
	mistralURL           = "https://api.mistral.ai/v1/chat/completions"
	hyperbolicURL        = "https://api.hyperbolic.xyz/v1/chat/completions"
	novitaURL            = "https://api.novita.ai/v3/openai/chat/completions"
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

// NewDeepSeek returns a Provider backed by DeepSeek.
// Supports deepseek-chat and deepseek-coder models.
// Get an API key at https://platform.deepseek.com
func NewDeepSeek(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("deepseek", deepseekURL, apiKey, model)
}

// NewPerplexity returns a Provider backed by Perplexity AI.
// Supports llama-3.1-sonar-* models with real-time web search.
// Get an API key at https://www.perplexity.ai/settings/api
func NewPerplexity(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("perplexity", perplexityURL, apiKey, model)
}

// NewFireworks returns a Provider backed by Fireworks AI, which hosts
// many open-source models with fast inference.
// Get an API key at https://fireworks.ai
func NewFireworks(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("fireworks", fireworksURL, apiKey, model)
}

// NewCerebras returns a Provider backed by Cerebras, offering ultra-fast
// Llama inference on dedicated silicon.
// Get an API key at https://cloud.cerebras.ai
func NewCerebras(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("cerebras", cerebrasURL, apiKey, model)
}

// NewSambaNova returns a Provider backed by SambaNova Cloud, optimized
// for enterprise Llama deployments.
// Get an API key at https://cloud.sambanova.ai
func NewSambaNova(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("sambanova", sambanovaURL, apiKey, model)
}

// NewMistral returns a Provider backed by Mistral AI.
// Supports mistral-large, mistral-small, codestral, and open models.
// Get an API key at https://console.mistral.ai
func NewMistral(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("mistral", mistralURL, apiKey, model)
}

// NewHyperbolic returns a Provider backed by Hyperbolic, hosting
// open-source models including Llama, Qwen, and Mistral variants.
// Get an API key at https://app.hyperbolic.xyz
func NewHyperbolic(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("hyperbolic", hyperbolicURL, apiKey, model)
}

// NewNovitaAI returns a Provider backed by Novita AI, which hosts
// hundreds of open-source models.
// Get an API key at https://novita.ai
func NewNovitaAI(model, apiKey string) *openai.Provider {
	return openai.NewCompatible("novita", novitaURL, apiKey, model)
}
