package cohere

import (
	"fmt"

	"github.com/Vedanshu7/llmbridge/types"
)

// coherePricing maps model identifiers to per-token costs (USD).
// Prices from Cohere pricing page, May 2025.
var coherePricing = map[string]types.CostPerToken{
	"command-r-plus-08-2024": {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.000010},
	"command-r-plus":         {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.000010},
	"command-r-08-2024":      {InputCostPerToken: 0.00000015, OutputCostPerToken: 0.00000060},
	"command-r":              {InputCostPerToken: 0.00000015, OutputCostPerToken: 0.00000060},
	"command":                {InputCostPerToken: 0.000001, OutputCostPerToken: 0.000002},
	"command-light":          {InputCostPerToken: 0.0000003, OutputCostPerToken: 0.0000006},
	"command-nightly":        {InputCostPerToken: 0.000001, OutputCostPerToken: 0.000002},
	"command-light-nightly":  {InputCostPerToken: 0.0000003, OutputCostPerToken: 0.0000006},
}

// CostForResponse returns the estimated USD cost for a Cohere response.
func CostForResponse(resp *types.Response) (float64, error) {
	if resp.Usage == nil {
		return 0, nil
	}
	p, ok := coherePricing[resp.Model]
	if !ok {
		return 0, fmt.Errorf("cohere: no pricing data for model %q", resp.Model)
	}
	in := float64(resp.Usage.PromptTokens) * p.InputCostPerToken
	out := float64(resp.Usage.CompletionTokens) * p.OutputCostPerToken
	return in + out, nil
}
