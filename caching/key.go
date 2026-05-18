// Package caching provides request/response caching for llmbridge providers.
package caching

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Vedanshu7/llmbridge/types"
)

// GenerateCacheKey produces a deterministic hex string from the fields of req
// that affect the LLM output. Two requests with the same key will produce the
// same response (assuming a deterministic provider).
func GenerateCacheKey(req types.Request) string {
	type keyFields struct {
		System      string         `json:"system"`
		Messages    []types.Message `json:"messages"`
		Tools       []types.Tool   `json:"tools,omitempty"`
		Model       string         `json:"model"`
		MaxTokens   int            `json:"max_tokens,omitempty"`
		Temperature float64        `json:"temperature"`
	}
	kf := keyFields{
		System:      req.System,
		Messages:    req.Messages,
		Tools:       req.Tools,
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	b, err := json.Marshal(kf)
	if err != nil {
		// Fallback: use a non-deterministic placeholder that will never match.
		return fmt.Sprintf("err-%x", sha256.Sum256([]byte(err.Error())))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
