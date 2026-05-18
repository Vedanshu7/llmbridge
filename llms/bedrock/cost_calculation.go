package bedrock

import (
	"fmt"
	"strings"

	"github.com/Vedanshu7/llmbridge/types"
)

// bedrockPricing maps Bedrock model ID prefixes to per-token costs (USD).
// Prices from AWS Bedrock pricing page, May 2025.
var bedrockPricing = map[string]types.CostPerToken{
	// Anthropic Claude on Bedrock
	"anthropic.claude-3-5-sonnet-20241022": {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000015},
	"anthropic.claude-3-5-haiku-20241022":  {InputCostPerToken: 0.0000008, OutputCostPerToken: 0.000004},
	"anthropic.claude-3-opus-20240229":     {InputCostPerToken: 0.000015, OutputCostPerToken: 0.000075},
	"anthropic.claude-3-sonnet-20240229":   {InputCostPerToken: 0.000003, OutputCostPerToken: 0.000015},
	"anthropic.claude-3-haiku-20240307":    {InputCostPerToken: 0.00000025, OutputCostPerToken: 0.00000125},
	"anthropic.claude-instant-v1":          {InputCostPerToken: 0.0000008, OutputCostPerToken: 0.0000024},
	// Amazon Titan
	"amazon.titan-text-express-v1":         {InputCostPerToken: 0.0000002, OutputCostPerToken: 0.0000006},
	"amazon.titan-text-lite-v1":            {InputCostPerToken: 0.00000015, OutputCostPerToken: 0.0000002},
	"amazon.titan-text-premier-v1:0":       {InputCostPerToken: 0.0000005, OutputCostPerToken: 0.0000015},
	// Meta Llama on Bedrock
	"meta.llama3-8b-instruct-v1:0":         {InputCostPerToken: 0.0000003, OutputCostPerToken: 0.0000006},
	"meta.llama3-70b-instruct-v1:0":        {InputCostPerToken: 0.00000265, OutputCostPerToken: 0.0000035},
	"meta.llama3-1-8b-instruct-v1:0":       {InputCostPerToken: 0.00000022, OutputCostPerToken: 0.00000022},
	"meta.llama3-1-70b-instruct-v1:0":      {InputCostPerToken: 0.00000099, OutputCostPerToken: 0.00000099},
	"meta.llama3-2-1b-instruct-v1:0":       {InputCostPerToken: 0.0000001, OutputCostPerToken: 0.0000001},
	"meta.llama3-2-3b-instruct-v1:0":       {InputCostPerToken: 0.00000015, OutputCostPerToken: 0.00000015},
	// Mistral on Bedrock
	"mistral.mistral-7b-instruct-v0:2":     {InputCostPerToken: 0.00000015, OutputCostPerToken: 0.0000002},
	"mistral.mixtral-8x7b-instruct-v0:1":   {InputCostPerToken: 0.00000045, OutputCostPerToken: 0.0000007},
	"mistral.mistral-large-2402-v1:0":      {InputCostPerToken: 0.000004, OutputCostPerToken: 0.000012},
}

// CostForResponse returns the estimated USD cost for a Bedrock Converse response.
// Model IDs are matched by prefix to handle version suffixes.
func CostForResponse(resp *types.Response) (float64, error) {
	if resp.Usage == nil {
		return 0, nil
	}
	// Exact match first.
	if p, ok := bedrockPricing[resp.Model]; ok {
		return calcCost(resp, p), nil
	}
	// Prefix match (handles version suffixes like ":0", ":1").
	for prefix, p := range bedrockPricing {
		if strings.HasPrefix(resp.Model, prefix) {
			return calcCost(resp, p), nil
		}
	}
	return 0, fmt.Errorf("bedrock: no pricing data for model %q", resp.Model)
}

func calcCost(resp *types.Response, p types.CostPerToken) float64 {
	in := float64(resp.Usage.PromptTokens) * p.InputCostPerToken
	out := float64(resp.Usage.CompletionTokens) * p.OutputCostPerToken
	return in + out
}
