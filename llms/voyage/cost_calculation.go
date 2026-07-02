package voyage

import "fmt"

// pricingTable maps Voyage model name to cost per token in USD.
var pricingTable = map[string]float64{
	"voyage-3":         0.00000006,
	"voyage-3-lite":    0.00000002,
	"voyage-3-large":   0.00000018,
	"voyage-code-3":    0.00000006,
	"voyage-finance-2": 0.00000012,
	"voyage-law-2":     0.00000012,
}

// CostForEmbedding returns the estimated USD cost for embedding `tokens`
// tokens with the given Voyage model.
func CostForEmbedding(model string, tokens int) (float64, error) {
	price, ok := pricingTable[model]
	if !ok {
		return 0, fmt.Errorf("voyage: unknown embedding model %q — pricing not available", model)
	}
	return float64(tokens) * price, nil
}
