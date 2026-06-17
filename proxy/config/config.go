// Package config defines the JSON configuration file format for the llmbridge proxy server.
//
// Example config.json:
//
//	{
//	  "listen_addr": ":8080",
//	  "jwt_secret": "change-me-in-production",
//	  "admin_keys": ["llmb-abc123"],
//	  "models": [
//	    {"name": "gpt-4o", "provider": "openai", "model": "gpt-4o"}
//	  ],
//	  "router": {"strategy": "round_robin", "retries": 2}
//	}
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the top-level proxy server configuration.
type Config struct {
	// ListenAddr is the TCP address to listen on (e.g. ":8080").
	ListenAddr string `json:"listen_addr"`

	// JWTSecret is the HMAC-SHA256 key for signing and verifying JWT tokens.
	// Leave empty to disable JWT auth (API-key-only mode).
	JWTSecret string `json:"jwt_secret,omitempty"`

	// AdminKeys is a list of API keys to pre-populate with admin scope.
	AdminKeys []string `json:"admin_keys,omitempty"`

	// Models is the list of model entries to register in the model registry.
	Models []ModelEntry `json:"models,omitempty"`

	// Aliases maps short model names to their canonical registered names.
	// Example: {"gpt4": "gpt-4o", "sonnet": "claude-sonnet-4-6"}
	Aliases map[string]string `json:"aliases,omitempty"`

	// Router configures the multi-provider router, if used.
	Router *RouterConfig `json:"router,omitempty"`

	// LogLevel controls verbosity: "debug", "info", "warn", "error".
	LogLevel string `json:"log_level,omitempty"`

	// LogFile is the path to write access logs (one JSON line per request).
	// Leave empty to disable access logging.
	LogFile string `json:"log_file,omitempty"`

	// CacheTTLSeconds is the default cache TTL in seconds (default: 300).
	// Set to -1 to disable caching entirely.
	CacheTTLSeconds int `json:"cache_ttl_seconds,omitempty"`

	// Guardrails configures the request/response safety engine.
	Guardrails *GuardrailsConfig `json:"guardrails,omitempty"`

	// Orgs pre-defines organizations and their teams.
	Orgs []OrgEntry `json:"orgs,omitempty"`

	// Secrets configures an external secret backend for loading provider API keys.
	// When set, the Mappings field maps env-var names to secret paths in the backend.
	// Example: {"OPENAI_API_KEY": "prod/openai-key"}
	Secrets *SecretConfig `json:"secrets,omitempty"`

	// OIDC configures SSO/OIDC authentication for the admin UI.
	OIDC *OIDCConfig `json:"oidc,omitempty"`
}

// SecretConfig specifies an external secret backend.
type SecretConfig struct {
	// Backend selects the secret store: "aws", "gcp", or "vault".
	Backend string `json:"backend"`

	// Options are backend-specific settings (e.g. region, project_id, vault_addr).
	Options map[string]string `json:"options,omitempty"`

	// Mappings maps environment variable names to secret paths in the backend.
	// On startup each mapping is resolved and the result is set as an env var.
	Mappings map[string]string `json:"mappings,omitempty"`
}

// OIDCConfig configures SSO authentication via an external identity provider.
type OIDCConfig struct {
	// Provider selects the identity provider: "google", "github", or "microsoft".
	Provider string `json:"provider"`

	// ClientID is the OAuth2 application client ID.
	ClientID string `json:"client_id"`

	// ClientSecret is the OAuth2 application client secret.
	ClientSecret string `json:"client_secret"`

	// RedirectURL is the callback URL registered with the identity provider.
	// Example: "http://localhost:8080/auth/callback"
	RedirectURL string `json:"redirect_url"`

	// TenantID is required for Microsoft/Entra (the Azure AD tenant GUID or domain).
	TenantID string `json:"tenant_id,omitempty"`
}

// OrgEntry defines an organization and its teams for the config file.
type OrgEntry struct {
	// ID is an optional fixed org ID. Leave empty to auto-generate.
	ID string `json:"id,omitempty"`

	// Name is the org display name.
	Name string `json:"name"`

	// Budget is the maximum USD spend (0 = unlimited).
	Budget float64 `json:"budget,omitempty"`

	// Teams lists teams within this org.
	Teams []TeamEntry `json:"teams,omitempty"`
}

// TeamEntry defines a team within an org for the config file.
type TeamEntry struct {
	// ID is an optional fixed team ID. Leave empty to auto-generate.
	ID string `json:"id,omitempty"`

	// Name is the team display name.
	Name string `json:"name"`

	// Budget is the maximum USD spend (0 = unlimited).
	Budget float64 `json:"budget,omitempty"`
}

// GuardrailsConfig controls the proxy guardrails engine.
type GuardrailsConfig struct {
	// MaxInputLength rejects requests whose total character count exceeds this.
	// 0 = disabled.
	MaxInputLength int `json:"max_input_length,omitempty"`

	// MaxOutputLength rejects responses whose content character count exceeds this.
	// 0 = disabled.
	MaxOutputLength int `json:"max_output_length,omitempty"`

	// MaxOutputTokens rejects responses that report more completion tokens than this.
	// 0 = disabled.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`

	// BlockKeywords is a list of words (case-insensitive) to reject in any message.
	BlockKeywords []string `json:"block_keywords,omitempty"`

	// BlockPII enables PII detection (email, SSN, credit card).
	BlockPII bool `json:"block_pii,omitempty"`

	// BlockPromptInjection enables detection of common prompt-injection and
	// jailbreak phrases in request messages.
	BlockPromptInjection bool `json:"block_prompt_injection,omitempty"`
}

// ModelEntry maps a user-facing model name to a provider and backend model.
type ModelEntry struct {
	// Name is the model identifier exposed to clients (e.g. "gpt-4o").
	Name string `json:"name"`
	// Provider is the llmbridge provider name (e.g. "openai", "anthropic").
	Provider string `json:"provider"`
	// Model is the backend model identifier (e.g. "gpt-4o-2024-08-06").
	Model string `json:"model"`
}

// RouterConfig sets routing strategy, retry policy, and per-model fallbacks.
type RouterConfig struct {
	// Strategy is one of: "priority", "round_robin", "least_latency",
	// "least_busy", "cost_based".
	Strategy string `json:"strategy,omitempty"`
	// Retries is the maximum retry attempts per provider.
	Retries int `json:"retries,omitempty"`
	// FallbackModels maps a primary model name to an ordered list of fallback
	// model names. When all providers fail a request for the primary model, the
	// router retries with each fallback model in sequence.
	// Example: {"gpt-4o": ["gpt-4o-mini", "gpt-3.5-turbo"]}
	FallbackModels map[string][]string `json:"fallback_models,omitempty"`
}

// Load reads and parses a JSON config file from path.
// Missing optional fields are filled with defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}

// Default returns a Config with sensible defaults.
func Default() *Config {
	return &Config{
		ListenAddr: ":8080",
		LogLevel:   "info",
	}
}
