package openai

import (
	"fmt"

	"github.com/Vedanshu7/llmbridge/types"
)

// pricingTable maps model name to input/output cost per token in USD.
// Prices sourced from https://openai.com/pricing
var pricingTable = map[string]types.CostPerToken{
	// GPT-4o
	"gpt-4o":                  {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.000010},
	"gpt-4o-2024-11-20":       {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.000010},
	"gpt-4o-2024-08-06":       {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.000010},
	"gpt-4o-mini":             {InputCostPerToken: 0.00000015, OutputCostPerToken: 0.0000006},
	"gpt-4o-mini-2024-07-18":  {InputCostPerToken: 0.00000015, OutputCostPerToken: 0.0000006},
	// GPT-4 Turbo
	"gpt-4-turbo":             {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00003},
	"gpt-4-turbo-2024-04-09":  {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00003},
	// GPT-4
	"gpt-4":                   {InputCostPerToken: 0.00003, OutputCostPerToken: 0.00006},
	// GPT-3.5 Turbo
	"gpt-3.5-turbo":           {InputCostPerToken: 0.0000005, OutputCostPerToken: 0.0000015},
	"gpt-3.5-turbo-0125":      {InputCostPerToken: 0.0000005, OutputCostPerToken: 0.0000015},
	// o1 series
	"o1":                      {InputCostPerToken: 0.000015, OutputCostPerToken: 0.00006},
	"o1-mini":                 {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000012},
	"o1-preview":              {InputCostPerToken: 0.000015, OutputCostPerToken: 0.00006},
	// o3 series
	"o3-mini":                 {InputCostPerToken: 0.0000011, OutputCostPerToken: 0.0000044},
}

// CostForResponse calculates the estimated cost in USD for a completed OpenAI response.
func CostForResponse(resp *types.Response) (float64, error) {
	if resp.Usage == nil {
		return 0, fmt.Errorf("openai: usage data not available in response")
	}
	pricing, ok := pricingTable[resp.Model]
	if !ok {
		return 0, fmt.Errorf("openai: unknown model %q — pricing not available", resp.Model)
	}
	inputCost := float64(resp.Usage.PromptTokens) * pricing.InputCostPerToken
	outputCost := float64(resp.Usage.CompletionTokens) * pricing.OutputCostPerToken
	return inputCost + outputCost, nil
}
