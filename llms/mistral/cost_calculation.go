package mistral

import (
	"fmt"

	"github.com/Vedanshu7/llmbridge/types"
)

// pricingTable maps Mistral model name to cost per token in USD.
var pricingTable = map[string]types.CostPerToken{
	"mistral-small-latest":  {InputCostPerToken: 0.0000002, OutputCostPerToken: 0.0000006},
	"mistral-small-2503":    {InputCostPerToken: 0.0000002, OutputCostPerToken: 0.0000006},
	"mistral-medium-latest": {InputCostPerToken: 0.0000004, OutputCostPerToken: 0.0000012},
	"mistral-large-latest":  {InputCostPerToken: 0.000002, OutputCostPerToken: 0.000006},
	"mistral-large-2411":    {InputCostPerToken: 0.000002, OutputCostPerToken: 0.000006},
	"codestral-latest":      {InputCostPerToken: 0.000001, OutputCostPerToken: 0.000003},
	"codestral-2501":        {InputCostPerToken: 0.000001, OutputCostPerToken: 0.000003},
	"ministral-3b-latest":   {InputCostPerToken: 0.00000004, OutputCostPerToken: 0.00000004},
	"ministral-8b-latest":   {InputCostPerToken: 0.0000001, OutputCostPerToken: 0.0000001},
}

// CostForResponse calculates the estimated cost in USD for a completed Mistral response.
func CostForResponse(resp *types.Response) (float64, error) {
	if resp.Usage == nil {
		return 0, fmt.Errorf("mistral: usage data not available in response")
	}
	pricing, ok := pricingTable[resp.Model]
	if !ok {
		return 0, fmt.Errorf("mistral: unknown model %q — pricing not available", resp.Model)
	}
	inputCost := float64(resp.Usage.PromptTokens) * pricing.InputCostPerToken
	outputCost := float64(resp.Usage.CompletionTokens) * pricing.OutputCostPerToken
	return inputCost + outputCost, nil
}
