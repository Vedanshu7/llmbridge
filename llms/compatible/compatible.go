// Package compatible provides llmbridge Providers for endpoints that speak the
// OpenAI chat completions wire format. This covers self-hosted servers (Ollama,
// LM Studio) as well as cloud APIs that adopted OpenAI compatibility (Groq,
// Together AI, etc.).
package compatible

import (
	"github.com/Vedanshu7/llmbridge/llms/openai"
)

// NewCompatible returns a Provider for any OpenAI-compatible endpoint.
//   - name: label shown in logs and error messages (e.g. "groq", "together").
//   - baseURL: full chat completions URL.
//   - apiKey: Bearer token; may be empty for unauthenticated local servers.
//   - model: model identifier required by the endpoint.
func NewCompatible(name, baseURL, apiKey, model string) *openai.Provider {
	return openai.NewCompatible(name, baseURL, apiKey, model)
}
