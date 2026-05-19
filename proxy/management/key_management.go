// Package management provides admin endpoints for the llmbridge proxy server.
package management

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Vedanshu7/llmbridge/proxy/auth"
)

// KeyManagement handles API key CRUD via HTTP.
type KeyManagement struct {
	store *auth.APIKeyStore
}

// NewKeyManagement returns a KeyManagement backed by the given store.
func NewKeyManagement(store *auth.APIKeyStore) *KeyManagement {
	return &KeyManagement{store: store}
}

// HandleGenerate handles POST /admin/key/generate.
// Optional JSON fields: "scopes" ([]string), "ttl_seconds" (int), "spend_limit" (float64).
func (km *KeyManagement) HandleGenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scopes     []string `json:"scopes"`
		TTLSeconds int      `json:"ttl_seconds"`
		SpendLimit float64  `json:"spend_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Scopes) == 0 {
		body.Scopes = []string{"completion"}
	}
	key, err := km.store.GenerateAPIKey(body.Scopes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if body.TTLSeconds > 0 {
		km.store.SetExpiry(key, time.Duration(body.TTLSeconds)*time.Second)
	}
	if body.SpendLimit > 0 {
		km.store.SetSpendLimit(key, body.SpendLimit)
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key})
}

// HandleDelete handles DELETE /admin/key/delete.
func (km *KeyManagement) HandleDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key field required"})
		return
	}
	km.store.DeleteKey(body.Key)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleList handles GET /admin/keys.
func (km *KeyManagement) HandleList(w http.ResponseWriter, r *http.Request) {
	keys := km.store.ListKeys()
	writeJSON(w, http.StatusOK, map[string]interface{}{"keys": keys})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
