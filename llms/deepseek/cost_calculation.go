package deepseek

import (
	"fmt"

	"github.com/Vedanshu7/llmbridge/types"
)

// pricingTable maps DeepSeek model name to cost per token in USD.
var pricingTable = map[string]types.CostPerToken{
	// deepseek-chat (V3) — cache hit / cache miss pricing averaged
	"deepseek-chat": {InputCostPerToken: 0.00000027, OutputCostPerToken: 0.0000011},
	// deepseek-reasoner (R1) — cache hit / cache miss pricing averaged
	"deepseek-reasoner": {InputCostPerToken: 0.00000055, OutputCostPerToken: 0.0000022},
}

// CostForResponse calculates the estimated cost in USD for a completed DeepSeek response.
func CostForResponse(resp *types.Response) (float64, error) {
	if resp.Usage == nil {
		return 0, fmt.Errorf("deepseek: usage data not available in response")
	}
	pricing, ok := pricingTable[resp.Model]
	if !ok {
		return 0, fmt.Errorf("deepseek: unknown model %q — pricing not available", resp.Model)
	}
	inputCost := float64(resp.Usage.PromptTokens) * pricing.InputCostPerToken
	outputCost := float64(resp.Usage.CompletionTokens) * pricing.OutputCostPerToken
	return inputCost + outputCost, nil
}
