package llmbridge

import (
	"strings"

	"github.com/Vedanshu7/llmbridge/types"
)

// GetModelInfo looks up metadata for a known model.
// Returns (ModelInfo{}, false) for unrecognized model names.
func GetModelInfo(modelName string) (ModelInfo, bool) {
	info, ok := ModelInfoDB[modelName]
	return info, ok
}

// ValidateModel returns true if modelName is in the built-in registry.
func ValidateModel(modelName string) bool {
	_, ok := ModelInfoDB[modelName]
	return ok
}

// SanitizeRequest applies provider-safe defaults to req and trims whitespace.
// It does not mutate the original; it returns a copy.
func SanitizeRequest(req types.Request) types.Request {
	req.System = strings.TrimSpace(req.System)
	req.Model = strings.TrimSpace(req.Model)
	msgs := make([]types.Message, len(req.Messages))
	for i, m := range req.Messages {
		m.Content = strings.TrimSpace(m.Content)
		msgs[i] = m
	}
	req.Messages = msgs
	return req
}

// ResolveModel returns req.Model if non-empty, otherwise the provider's default.
func ResolveModel(req types.Request, providerName string) string {
	if req.Model != "" {
		return req.Model
	}
	if def, ok := DefaultModels[providerName]; ok {
		return def
	}
	return ""
}
