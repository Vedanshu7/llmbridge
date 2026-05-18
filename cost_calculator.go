package llmbridge

import (
	"fmt"

	openaiCost "github.com/Vedanshu7/llmbridge/llms/openai"
	anthropicCost "github.com/Vedanshu7/llmbridge/llms/anthropic"
	"github.com/Vedanshu7/llmbridge/types"
)

// CompletionCost calculates the estimated cost in USD for a completed response.
// It dispatches to the provider-specific pricing table based on resp.Provider.
// Returns 0 and an error if the provider or model is not in the pricing tables.
func CompletionCost(resp *types.Response) (float64, error) {
	if resp == nil {
		return 0, fmt.Errorf("cost_calculator: nil response")
	}
	switch resp.Provider {
	case "openai":
		return openaiCost.CostForResponse(resp)
	case "anthropic":
		return anthropicCost.CostForResponse(resp)
	default:
		// For OpenAI-compatible providers (groq, together, etc.) fall back to
		// the model info DB if available.
		if info, ok := ModelInfoDB[resp.Model]; ok && resp.Usage != nil {
			input := float64(resp.Usage.PromptTokens) * info.InputCostPerToken
			output := float64(resp.Usage.CompletionTokens) * info.OutputCostPerToken
			return input + output, nil
		}
		return 0, fmt.Errorf("cost_calculator: unknown provider %q or model %q", resp.Provider, resp.Model)
	}
}

// EmbeddingCost calculates the cost for an embedding request.
// provider is the provider name; tokens is the input token count.
func EmbeddingCost(provider, model string, tokens int) (float64, error) {
	embeddingPrices := map[string]float64{
		"text-embedding-3-small": 0.00000002,
		"text-embedding-3-large": 0.00000013,
		"text-embedding-ada-002": 0.0000001,
	}
	price, ok := embeddingPrices[model]
	if !ok {
		return 0, fmt.Errorf("cost_calculator: unknown embedding model %q", model)
	}
	return float64(tokens) * price, nil
}
