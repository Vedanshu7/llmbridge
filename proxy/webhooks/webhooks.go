// Package webhooks provides configurable outbound webhook delivery for llmbridge
// proxy events. Each webhook registration is scoped to an org and can filter
// by event type. Payloads are signed with HMAC-SHA256 when a secret is provided.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Vedanshu7/llmbridge/callbacks"
)

// EventFilter selects which callback events trigger a webhook delivery.
type EventFilter string

const (
	// FilterAll fires for every event type.
	FilterAll EventFilter = "all"
	// FilterCompletion fires only on successful completions (EventResponse).
	FilterCompletion EventFilter = "completion"
	// FilterError fires only on errors (EventError).
	FilterError EventFilter = "error"
	// FilterRequest fires only on outgoing requests (EventRequest).
	FilterRequest EventFilter = "request"
)

// Config is a stored webhook registration.
type Config struct {
	ID        string        `json:"id"`
	OrgID     string        `json:"org_id"`
	URL       string        `json:"url"`
	Events    []EventFilter `json:"events"` // empty == FilterAll
	Secret    string        `json:"secret,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

// Store holds webhook registrations.
type Store struct {
	mu       sync.RWMutex
	webhooks map[string]*Config // id → config
	client   *http.Client
}

// NewStore returns an empty Store with a 5-second HTTP client timeout.
func NewStore() *Store {
	return &Store{
		webhooks: make(map[string]*Config),
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// SetClient replaces the HTTP client used for delivery (useful in tests).
func (s *Store) SetClient(c *http.Client) { s.client = c }

// Register adds a new webhook and returns it.
// orgID may be empty to receive events from all orgs.
// events may be nil or empty to receive all event types.
func (s *Store) Register(orgID, url string, events []EventFilter, secret string) *Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := &Config{
		ID:        generateID(),
		OrgID:     orgID,
		URL:       url,
		Events:    events,
		Secret:    secret,
		CreatedAt: time.Now().UTC(),
	}
	if len(cfg.Events) == 0 {
		cfg.Events = []EventFilter{FilterAll}
	}
	s.webhooks[cfg.ID] = cfg
	return cfg
}

// Get retrieves a webhook by ID.
func (s *Store) Get(id string) (*Config, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.webhooks[id]
	return c, ok
}

// Delete removes a webhook by ID. Returns false if not found.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.webhooks[id]; !ok {
		return false
	}
	delete(s.webhooks, id)
	return true
}

// List returns all stored webhook configs.
func (s *Store) List() []*Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Config, 0, len(s.webhooks))
	for _, c := range s.webhooks {
		out = append(out, c)
	}
	return out
}

// Handler returns a callbacks.Handler that delivers events to all matching
// webhooks in the store. Delivery is best-effort — failures are silently
// dropped to avoid blocking the call path. Each delivery fires in its own
// goroutine.
func (s *Store) Handler() callbacks.Handler {
	return func(_ context.Context, event callbacks.Event) {
		s.mu.RLock()
		configs := make([]*Config, 0, len(s.webhooks))
		for _, c := range s.webhooks {
			configs = append(configs, c)
		}
		s.mu.RUnlock()

		orgID := ""
		if event.Metadata != nil {
			orgID = event.Metadata["org_id"]
		}

		for _, cfg := range configs {
			if !matchesOrg(cfg, orgID) {
				continue
			}
			if !matchesEvent(cfg, event) {
				continue
			}
			go s.deliver(cfg, event)
		}
	}
}

func matchesOrg(cfg *Config, orgID string) bool {
	return cfg.OrgID == "" || cfg.OrgID == orgID
}

func matchesEvent(cfg *Config, event callbacks.Event) bool {
	for _, f := range cfg.Events {
		switch f {
		case FilterAll:
			return true
		case FilterCompletion:
			if event.Type == callbacks.EventResponse {
				return true
			}
		case FilterError:
			if event.Type == callbacks.EventError {
				return true
			}
		case FilterRequest:
			if event.Type == callbacks.EventRequest {
				return true
			}
		}
	}
	return false
}

type payload struct {
	Event    string            `json:"event"`
	Provider string            `json:"provider"`
	Model    string            `json:"model"`
	Time     string            `json:"time"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Error    string            `json:"error,omitempty"`
	Tokens   int               `json:"tokens,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
}

func (s *Store) deliver(cfg *Config, event callbacks.Event) {
	p := payload{
		Event:    string(event.Type),
		Provider: event.Provider,
		Model:    event.Model,
		Time:     time.Now().UTC().Format(time.RFC3339Nano),
		Metadata: event.Metadata,
	}
	if event.Duration > 0 {
		p.DurationMS = event.Duration.Milliseconds()
	}
	if event.Response != nil && event.Response.Usage != nil {
		p.Tokens = event.Response.Usage.TotalTokens
	}
	if event.Error != nil {
		p.Error = event.Error.Error()
	}

	body, err := json.Marshal(p)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LLMBridge-Event", string(event.Type))
	req.Header.Set("X-LLMBridge-Webhook-ID", cfg.ID)
	if cfg.Secret != "" {
		sig := sign(body, cfg.Secret)
		req.Header.Set("X-LLMBridge-Signature", "sha256="+sig)
	}

	resp, err := s.client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// sign computes HMAC-SHA256(payload, secret) and returns the hex digest.
func sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- HTTP handlers ----

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]interface{}{
		"error": map[string]string{"message": msg},
	})
}

// HandleRegister handles POST /admin/webhooks.
func (s *Store) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrgID  string        `json:"org_id"`
		URL    string        `json:"url"`
		Events []EventFilter `json:"events"`
		Secret string        `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		writeError(w, http.StatusBadRequest, "url field required")
		return
	}
	cfg := s.Register(body.OrgID, body.URL, body.Events, body.Secret)
	writeJSON(w, http.StatusCreated, cfg)
}

// HandleList handles GET /admin/webhooks.
func (s *Store) HandleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"webhooks": s.List()})
}

// HandleGet handles GET /admin/webhooks/{id}.
func (s *Store) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg, ok := s.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// HandleDelete handles DELETE /admin/webhooks/{id}.
func (s *Store) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.Delete(id) {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// Verify checks that the X-LLMBridge-Signature header on r matches the
// expected HMAC-SHA256 of body using secret. Returns false if the secret
// is empty (no verification configured).
func Verify(body []byte, secret, sigHeader string) bool {
	if secret == "" {
		return false
	}
	expected := "sha256=" + sign(body, secret)
	return hmac.Equal([]byte(sigHeader), []byte(expected))
}

// SpendThresholdPayload is a convenience payload for spend threshold alerts.
// Fire it manually from the proxy when org/key spend crosses a threshold.
type SpendThresholdPayload struct {
	OrgID        string  `json:"org_id"`
	CurrentSpend float64 `json:"current_spend"`
	Budget       float64 `json:"budget"`
	PercentUsed  float64 `json:"percent_used"`
}

// DeliverSpendAlert sends a spend-threshold alert payload to all webhooks
// registered for orgID (or all-org webhooks) that include FilterAll or FilterError.
// This is called directly from the proxy when spend thresholds are crossed.
func (s *Store) DeliverSpendAlert(orgID string, p SpendThresholdPayload) {
	s.mu.RLock()
	configs := make([]*Config, 0, len(s.webhooks))
	for _, c := range s.webhooks {
		configs = append(configs, c)
	}
	s.mu.RUnlock()

	body, err := json.Marshal(map[string]interface{}{
		"event":         "spend_threshold",
		"org_id":        p.OrgID,
		"current_spend": p.CurrentSpend,
		"budget":        p.Budget,
		"percent_used":  fmt.Sprintf("%.1f%%", p.PercentUsed),
		"time":          time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}

	for _, cfg := range configs {
		if cfg.OrgID != "" && cfg.OrgID != orgID {
			continue
		}
		// Spend alerts go to webhooks configured for all events or errors.
		fires := false
		for _, f := range cfg.Events {
			if f == FilterAll || f == FilterError {
				fires = true
				break
			}
		}
		if !fires {
			continue
		}
		go func(c *Config) {
			req, err := http.NewRequest(http.MethodPost, c.URL, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-LLMBridge-Event", "spend_threshold")
			req.Header.Set("X-LLMBridge-Webhook-ID", c.ID)
			if c.Secret != "" {
				req.Header.Set("X-LLMBridge-Signature", "sha256="+sign(body, c.Secret))
			}
			resp, err := s.client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}(cfg)
	}
}
