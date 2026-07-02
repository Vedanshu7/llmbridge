package llmbridge

import (
	"fmt"

	anthropicCost "github.com/Vedanshu7/llmbridge/llms/anthropic"
	azureCost "github.com/Vedanshu7/llmbridge/llms/azure"
	bedrockCost "github.com/Vedanshu7/llmbridge/llms/bedrock"
	cohereCost "github.com/Vedanshu7/llmbridge/llms/cohere"
	deepseekCost "github.com/Vedanshu7/llmbridge/llms/deepseek"
	geminiCost "github.com/Vedanshu7/llmbridge/llms/gemini"
	mistralCost "github.com/Vedanshu7/llmbridge/llms/mistral"
	openaiCost "github.com/Vedanshu7/llmbridge/llms/openai"
	voyageCost "github.com/Vedanshu7/llmbridge/llms/voyage"
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
	case "gemini":
		return geminiCost.CostForResponse(resp)
	case "azure":
		return azureCost.CostForResponse(resp)
	case "cohere":
		return cohereCost.CostForResponse(resp)
	case "bedrock":
		return bedrockCost.CostForResponse(resp)
	case "mistral":
		return mistralCost.CostForResponse(resp)
	case "deepseek":
		return deepseekCost.CostForResponse(resp)
	default:
		// For OpenAI-compatible providers (groq, together, deepseek, etc.)
		// fall back to the model info DB if available.
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
	if provider == "voyage" {
		return voyageCost.CostForEmbedding(model, tokens)
	}
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
