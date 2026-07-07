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
	rl    *auth.RateLimiter // may be nil
}

// NewKeyManagement returns a KeyManagement backed by the given store.
func NewKeyManagement(store *auth.APIKeyStore) *KeyManagement {
	return &KeyManagement{store: store}
}

// NewKeyManagementWithRateLimiter returns a KeyManagement that can also configure
// per-key rate limits during key generation.
func NewKeyManagementWithRateLimiter(store *auth.APIKeyStore, rl *auth.RateLimiter) *KeyManagement {
	return &KeyManagement{store: store, rl: rl}
}

// HandleGenerate handles POST /admin/key/generate.
// Optional JSON fields: "scopes" ([]string), "ttl_seconds" (int), "spend_limit" (float64),
// "org_id" (string), "team_id" (string), "rpm" (int), "tpm" (int),
// "model_aliases" (map[string]string), "spend_alert_threshold" (float64, e.g. 0.8).
func (km *KeyManagement) HandleGenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scopes              []string          `json:"scopes"`
		TTLSeconds          int               `json:"ttl_seconds"`
		SpendLimit          float64           `json:"spend_limit"`
		OrgID               string            `json:"org_id"`
		TeamID              string            `json:"team_id"`
		RPM                 int               `json:"rpm"` // requests per minute; 0 = unlimited
		TPM                 int               `json:"tpm"` // tokens per minute; 0 = unlimited
		ModelAliases        map[string]string `json:"model_aliases"`
		ResetPeriod         string            `json:"reset_period"`         // "daily", "weekly", "monthly"
		SpendAlertThreshold float64           `json:"spend_alert_threshold"` // fraction, e.g. 0.8
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
	if body.OrgID != "" {
		km.store.SetKeyOrg(key, body.OrgID)
	}
	if body.TeamID != "" {
		km.store.SetKeyTeam(key, body.TeamID)
	}
	if km.rl != nil && (body.RPM > 0 || body.TPM > 0) {
		km.rl.SetLimit(key, auth.RateLimit{
			RequestsPerMin: body.RPM,
			TokensPerMin:   body.TPM,
		})
	}
	if len(body.ModelAliases) > 0 {
		km.store.SetModelAliases(key, body.ModelAliases)
	}
	if body.ResetPeriod != "" {
		km.store.SetResetPeriod(key, body.ResetPeriod)
	}
	if body.SpendAlertThreshold > 0 {
		km.store.SetSpendAlertThreshold(key, body.SpendAlertThreshold)
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

// HandleSetRateLimit handles PUT /admin/key/rate-limit.
// Body: {"key": "...", "rpm": 60, "tpm": 100000}. 0 removes the limit.
func (km *KeyManagement) HandleSetRateLimit(w http.ResponseWriter, r *http.Request) {
	if km.rl == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limiter not configured"})
		return
	}
	var body struct {
		Key string `json:"key"`
		RPM int    `json:"rpm"`
		TPM int    `json:"tpm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key field required"})
		return
	}
	if body.RPM == 0 && body.TPM == 0 {
		km.rl.RemoveLimit(body.Key)
	} else {
		km.rl.SetLimit(body.Key, auth.RateLimit{RequestsPerMin: body.RPM, TokensPerMin: body.TPM})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleRotate handles POST /admin/key/rotate.
// Body: {"key": "llmb-xxx", "grace_period_seconds": 3600}.
// Issues a new key inheriting all settings from the old key. The old key
// remains valid for grace_period_seconds (default: 3600) to allow callers
// to transition without hard failures.
func (km *KeyManagement) HandleRotate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key                string `json:"key"`
		GracePeriodSeconds int    `json:"grace_period_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key field required"})
		return
	}
	grace := time.Duration(body.GracePeriodSeconds) * time.Second
	if grace <= 0 {
		grace = time.Hour
	}
	newKey, err := km.store.RotateKey(body.Key, grace)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"new_key":      newKey,
		"old_key":      body.Key,
		"grace_expires": time.Now().Add(grace).UTC().Format(time.RFC3339),
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
