package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("expected :8080, got %q", cfg.ListenAddr)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("expected info, got %q", cfg.LogLevel)
	}
}

func TestLoadValidConfig(t *testing.T) {
	raw := map[string]interface{}{
		"listen_addr": ":9090",
		"jwt_secret":  "supersecret",
		"admin_keys":  []string{"llmb-abc"},
		"log_level":   "debug",
		"models": []map[string]interface{}{
			{"name": "gpt4", "provider": "openai", "model": "gpt-4o"},
		},
	}
	b, _ := json.Marshal(raw)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Fatalf("expected :9090, got %q", cfg.ListenAddr)
	}
	if cfg.JWTSecret != "supersecret" {
		t.Fatalf("jwt_secret not loaded")
	}
	if len(cfg.AdminKeys) != 1 || cfg.AdminKeys[0] != "llmb-abc" {
		t.Fatalf("admin_keys not loaded: %v", cfg.AdminKeys)
	}
	if len(cfg.Models) != 1 || cfg.Models[0].Name != "gpt4" {
		t.Fatalf("models not loaded: %v", cfg.Models)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/tmp/this-file-does-not-exist-llmbridge.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{invalid json}"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadDefaultsFilled(t *testing.T) {
	// Config file only sets listen_addr; other defaults should be preserved.
	raw := map[string]interface{}{"listen_addr": ":7777"}
	b, _ := json.Marshal(raw)
	path := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(path, b, 0600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("expected default log_level=info, got %q", cfg.LogLevel)
	}
	if cfg.ListenAddr != ":7777" {
		t.Fatalf("expected :7777, got %q", cfg.ListenAddr)
	}
}

func TestLoadGuardrailsConfig(t *testing.T) {
	raw := map[string]interface{}{
		"guardrails": map[string]interface{}{
			"max_input_length":      1000,
			"block_keywords":        []string{"spam"},
			"block_prompt_injection": true,
		},
	}
	b, _ := json.Marshal(raw)
	path := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(path, b, 0600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Guardrails == nil {
		t.Fatal("expected guardrails config")
	}
	if cfg.Guardrails.MaxInputLength != 1000 {
		t.Fatalf("expected max_input_length=1000, got %d", cfg.Guardrails.MaxInputLength)
	}
	if !cfg.Guardrails.BlockPromptInjection {
		t.Fatal("expected block_prompt_injection=true")
	}
}

func TestLoadOrgConfig(t *testing.T) {
	raw := map[string]interface{}{
		"orgs": []map[string]interface{}{
			{
				"name":   "acme",
				"budget": 100.0,
				"teams": []map[string]interface{}{
					{"name": "eng", "budget": 50.0},
				},
			},
		},
	}
	b, _ := json.Marshal(raw)
	path := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(path, b, 0600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Orgs) != 1 || cfg.Orgs[0].Name != "acme" {
		t.Fatalf("org not loaded: %v", cfg.Orgs)
	}
	if len(cfg.Orgs[0].Teams) != 1 || cfg.Orgs[0].Teams[0].Name != "eng" {
		t.Fatalf("team not loaded: %v", cfg.Orgs[0].Teams)
	}
}
