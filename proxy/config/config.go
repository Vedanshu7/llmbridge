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
	CacheTTLSeconds int `json:"cache_ttl_seconds,omitempty"`
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

// RouterConfig sets routing strategy and retry policy.
type RouterConfig struct {
	// Strategy is one of: "priority", "round_robin", "least_latency",
	// "least_busy", "cost_based".
	Strategy string `json:"strategy,omitempty"`
	// Retries is the maximum retry attempts per provider.
	Retries int `json:"retries,omitempty"`
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
