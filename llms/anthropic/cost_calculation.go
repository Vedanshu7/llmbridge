package anthropic

import (
	"fmt"

	"github.com/Vedanshu7/llmbridge/types"
)

// pricingTable maps model name to input/output cost per token in USD.
// Prices sourced from https://www.anthropic.com/pricing
var pricingTable = map[string]types.CostPerToken{
	// Claude 4 series
	"claude-opus-4-7":              {InputCostPerToken: 0.000015, OutputCostPerToken: 0.000075},
	"claude-sonnet-4-6":            {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000015},
	// Claude 3.5 series
	"claude-3-5-sonnet-20241022":   {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000015},
	"claude-3-5-sonnet-20240620":   {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000015},
	"claude-3-5-haiku-20241022":    {InputCostPerToken: 0.0000008, OutputCostPerToken: 0.000004},
	// Claude 3 series
	"claude-3-opus-20240229":       {InputCostPerToken: 0.000015, OutputCostPerToken: 0.000075},
	"claude-3-sonnet-20240229":     {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000015},
	"claude-3-haiku-20240307":      {InputCostPerToken: 0.00000025, OutputCostPerToken: 0.00000125},
	// Haiku 4.5
	"claude-haiku-4-5-20251001":    {InputCostPerToken: 0.0000008, OutputCostPerToken: 0.000004},
}

// CostForResponse calculates the estimated cost in USD for a completed Anthropic response.
func CostForResponse(resp *types.Response) (float64, error) {
	if resp.Usage == nil {
		return 0, fmt.Errorf("anthropic: usage data not available in response")
	}
	pricing, ok := pricingTable[resp.Model]
	if !ok {
		return 0, fmt.Errorf("anthropic: unknown model %q — pricing not available", resp.Model)
	}
	inputCost := float64(resp.Usage.PromptTokens) * pricing.InputCostPerToken
	outputCost := float64(resp.Usage.CompletionTokens) * pricing.OutputCostPerToken
	return inputCost + outputCost, nil
}
