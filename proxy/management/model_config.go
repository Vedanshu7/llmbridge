package management

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/Vedanshu7/llmbridge/types"
)

// ModelRegistry stores registered model definitions for the proxy.
type ModelRegistry struct {
	mu     sync.RWMutex
	models map[string]types.ModelInfo
}

// NewModelRegistry returns an empty ModelRegistry.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{models: make(map[string]types.ModelInfo)}
}

// RegisterModel adds or updates a model in the registry.
func (mr *ModelRegistry) RegisterModel(name string, info types.ModelInfo) {
	mr.mu.Lock()
	mr.models[name] = info
	mr.mu.Unlock()
}

// GetModel looks up a model by name.
func (mr *ModelRegistry) GetModel(name string) (types.ModelInfo, bool) {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	info, ok := mr.models[name]
	return info, ok
}

// ListModels returns all registered models.
func (mr *ModelRegistry) ListModels() map[string]types.ModelInfo {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	out := make(map[string]types.ModelInfo, len(mr.models))
	for k, v := range mr.models {
		out[k] = v
	}
	return out
}

// HandleList handles GET /admin/models — returns all registered models.
func (mr *ModelRegistry) HandleList(w http.ResponseWriter, r *http.Request) {
	models := mr.ListModels()

	// Return in OpenAI /v1/models format.
	type modelObj struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	var data []modelObj
	for name := range models {
		data = append(data, modelObj{ID: name, Object: "model"})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

// HandleRegister handles POST /admin/models — registers a new model.
func (mr *ModelRegistry) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string          `json:"name"`
		Info types.ModelInfo `json:"info"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name field required"})
		return
	}
	mr.RegisterModel(body.Name, body.Info)
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered", "name": body.Name})
}
