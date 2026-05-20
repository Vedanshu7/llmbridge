package management

import (
	"encoding/json"
	"net/http"
	"sync"
)

// ModelInfo describes a registered model in the proxy model registry.
type ModelInfo struct {
	// Provider is the llmbridge provider name (e.g. "openai", "anthropic").
	Provider string `json:"provider"`
	// Model is the backend model identifier passed to the provider.
	Model string `json:"model"`
	// MaxTokens is the context window size; 0 means unknown.
	MaxTokens int `json:"max_tokens,omitempty"`
	// SupportsFunctionCalling indicates tool/function call support.
	SupportsFunctionCalling bool `json:"supports_function_calling,omitempty"`
	// SupportsVision indicates image input support.
	SupportsVision bool `json:"supports_vision,omitempty"`
}

// ModelRegistry stores registered model definitions for the proxy.
type ModelRegistry struct {
	mu     sync.RWMutex
	models map[string]ModelInfo
}

// NewModelRegistry returns an empty ModelRegistry.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{models: make(map[string]ModelInfo)}
}

// RegisterModel adds or updates a model in the registry.
func (mr *ModelRegistry) RegisterModel(name string, info ModelInfo) {
	mr.mu.Lock()
	mr.models[name] = info
	mr.mu.Unlock()
}

// GetModel looks up a model by name.
func (mr *ModelRegistry) GetModel(name string) (ModelInfo, bool) {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	info, ok := mr.models[name]
	return info, ok
}

// ListModels returns all registered models.
func (mr *ModelRegistry) ListModels() map[string]ModelInfo {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	out := make(map[string]ModelInfo, len(mr.models))
	for k, v := range mr.models {
		out[k] = v
	}
	return out
}

// HandleList handles GET /admin/models — returns all registered models.
func (mr *ModelRegistry) HandleList(w http.ResponseWriter, r *http.Request) {
	models := mr.ListModels()
	type modelObj struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	data := make([]modelObj, 0, len(models))
	for name, info := range models {
		data = append(data, modelObj{Name: name, Provider: info.Provider, Model: info.Model})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"models": data})
}

// HandleRegister handles POST /admin/models — registers a new model.
// Accepts a flat body: {"name":"gpt-4o","provider":"openai","model":"gpt-4o-2024-08-06"}.
func (mr *ModelRegistry) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name field required"})
		return
	}
	mr.RegisterModel(body.Name, ModelInfo{Provider: body.Provider, Model: body.Model})
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered", "name": body.Name})
}
