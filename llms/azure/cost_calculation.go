package azure

import (
	"fmt"

	"github.com/Vedanshu7/llmbridge/types"
)

// azurePricing maps Azure deployment model family prefixes to per-token costs.
// Azure OpenAI pricing matches OpenAI pricing for the same model generation.
var azurePricing = map[string]types.CostPerToken{
	"gpt-4o-mini":          {InputCostPerToken: 0.00000015, OutputCostPerToken: 0.00000060},
	"gpt-4o":               {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.000010},
	"gpt-4-turbo":          {InputCostPerToken: 0.000010, OutputCostPerToken: 0.000030},
	"gpt-4":                {InputCostPerToken: 0.000030, OutputCostPerToken: 0.000060},
	"gpt-35-turbo":         {InputCostPerToken: 0.0000005, OutputCostPerToken: 0.0000015},
	"gpt-35-turbo-16k":     {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000004},
	"o1-mini":              {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000012},
	"o1":                   {InputCostPerToken: 0.000015, OutputCostPerToken: 0.000060},
	"o3-mini":              {InputCostPerToken: 0.0000011, OutputCostPerToken: 0.0000044},
	"text-embedding-3-small": {InputCostPerToken: 0.00000002, OutputCostPerToken: 0.0},
	"text-embedding-3-large": {InputCostPerToken: 0.00000013, OutputCostPerToken: 0.0},
}

// CostForResponse returns the estimated USD cost for an Azure OpenAI response.
// Deployment names in Azure typically match or contain the model family name.
func CostForResponse(resp *types.Response) (float64, error) {
	if resp.Usage == nil {
		return 0, nil
	}
	// Try exact match first, then prefix match on deployment name.
	if p, ok := azurePricing[resp.Model]; ok {
		in := float64(resp.Usage.PromptTokens) * p.InputCostPerToken
		out := float64(resp.Usage.CompletionTokens) * p.OutputCostPerToken
		return in + out, nil
	}
	// Unknown deployment — cannot calculate cost.
	return 0, fmt.Errorf("azure: no pricing data for deployment %q", resp.Model)
}
