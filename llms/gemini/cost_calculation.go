package gemini

import (
	"fmt"

	"github.com/Vedanshu7/llmbridge/types"
)

// geminiPricing maps model identifiers to per-token costs (USD).
// Prices from Google AI Studio pricing page, May 2025.
var geminiPricing = map[string]types.CostPerToken{
	"gemini-2.0-flash":          {InputCostPerToken: 0.00000010, OutputCostPerToken: 0.00000040},
	"gemini-2.0-flash-lite":     {InputCostPerToken: 0.000000075, OutputCostPerToken: 0.00000030},
	"gemini-1.5-pro":            {InputCostPerToken: 0.00000125, OutputCostPerToken: 0.000005},
	"gemini-1.5-pro-latest":     {InputCostPerToken: 0.00000125, OutputCostPerToken: 0.000005},
	"gemini-1.5-flash":          {InputCostPerToken: 0.000000075, OutputCostPerToken: 0.00000030},
	"gemini-1.5-flash-latest":   {InputCostPerToken: 0.000000075, OutputCostPerToken: 0.00000030},
	"gemini-1.5-flash-8b":       {InputCostPerToken: 0.0000000375, OutputCostPerToken: 0.00000015},
	"gemini-1.0-pro":            {InputCostPerToken: 0.0000005, OutputCostPerToken: 0.0000015},
	"gemini-1.0-pro-latest":     {InputCostPerToken: 0.0000005, OutputCostPerToken: 0.0000015},
}

// CostForResponse returns the estimated USD cost for a Gemini response.
func CostForResponse(resp *types.Response) (float64, error) {
	if resp.Usage == nil {
		return 0, nil
	}
	p, ok := geminiPricing[resp.Model]
	if !ok {
		return 0, fmt.Errorf("gemini: no pricing data for model %q", resp.Model)
	}
	in := float64(resp.Usage.PromptTokens) * p.InputCostPerToken
	out := float64(resp.Usage.CompletionTokens) * p.OutputCostPerToken
	return in + out, nil
}
