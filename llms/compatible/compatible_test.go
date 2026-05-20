package compatible

import (
	"testing"

	"github.com/Vedanshu7/llmbridge/llms/openai"
)

// All constructors delegate to openai.NewCompatible; tests confirm the correct
// provider label and non-nil result for every public constructor.

func TestNewCompatibleReturnsNonNil(t *testing.T) {
	p := NewCompatible("custom", "http://localhost:9999/v1/chat/completions", "key", "model-x")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "custom" {
		t.Errorf("Name() = %q, want %q", p.Name(), "custom")
	}
}

func TestNewOllamaDefaults(t *testing.T) {
	p := NewOllama("")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q, want ollama", p.Name())
	}
}

func TestNewOllamaCustomModel(t *testing.T) {
	p := NewOllama("mistral:7b")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewOllamaAt(t *testing.T) {
	p := NewOllamaAt("http://192.168.1.5:11434/v1/chat/completions", "phi3")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q, want ollama", p.Name())
	}
}

func TestNewOllamaAtDefaults(t *testing.T) {
	// Empty URL and model should fall back to defaults without panicking.
	p := NewOllamaAt("", "")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewLMStudio(t *testing.T) {
	p := NewLMStudio("gemma-3b")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "lmstudio" {
		t.Errorf("Name() = %q, want lmstudio", p.Name())
	}
}

func TestNewLMStudioDefaults(t *testing.T) {
	p := NewLMStudio("")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewLMStudioAt(t *testing.T) {
	p := NewLMStudioAt("http://host:1234/v1/chat/completions", "model")
	if p.Name() != "lmstudio" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestNewLMStudioAtDefaults(t *testing.T) {
	p := NewLMStudioAt("", "")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewGroq(t *testing.T) {
	p := NewGroq("llama-3.1-70b-versatile", "test-key")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "groq" {
		t.Errorf("Name() = %q, want groq", p.Name())
	}
}

func TestNewTogetherAI(t *testing.T) {
	p := NewTogetherAI("meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo", "key")
	if p.Name() != "together" {
		t.Errorf("Name() = %q, want together", p.Name())
	}
}

func TestNewDeepSeek(t *testing.T) {
	p := NewDeepSeek("deepseek-chat", "key")
	if p.Name() != "deepseek" {
		t.Errorf("Name() = %q, want deepseek", p.Name())
	}
}

func TestNewPerplexity(t *testing.T) {
	p := NewPerplexity("llama-3.1-sonar-large-128k-online", "key")
	if p.Name() != "perplexity" {
		t.Errorf("Name() = %q, want perplexity", p.Name())
	}
}

func TestNewFireworks(t *testing.T) {
	p := NewFireworks("accounts/fireworks/models/llama-v3p1-70b-instruct", "key")
	if p.Name() != "fireworks" {
		t.Errorf("Name() = %q, want fireworks", p.Name())
	}
}

func TestNewCerebras(t *testing.T) {
	p := NewCerebras("llama3.1-70b", "key")
	if p.Name() != "cerebras" {
		t.Errorf("Name() = %q, want cerebras", p.Name())
	}
}

func TestNewSambaNova(t *testing.T) {
	p := NewSambaNova("Meta-Llama-3.1-70B-Instruct", "key")
	if p.Name() != "sambanova" {
		t.Errorf("Name() = %q, want sambanova", p.Name())
	}
}

func TestNewMistral(t *testing.T) {
	p := NewMistral("mistral-large-latest", "key")
	if p.Name() != "mistral" {
		t.Errorf("Name() = %q, want mistral", p.Name())
	}
}

func TestNewHyperbolic(t *testing.T) {
	p := NewHyperbolic("meta-llama/Meta-Llama-3.1-405B-Instruct", "key")
	if p.Name() != "hyperbolic" {
		t.Errorf("Name() = %q, want hyperbolic", p.Name())
	}
}

func TestNewNovitaAI(t *testing.T) {
	p := NewNovitaAI("meta-llama/llama-3.1-70b-instruct", "key")
	if p.Name() != "novita" {
		t.Errorf("Name() = %q, want novita", p.Name())
	}
}

func TestNewXAI(t *testing.T) {
	p := NewXAI("grok-beta", "key")
	if p.Name() != "xai" {
		t.Errorf("Name() = %q, want xai", p.Name())
	}
}

func TestNewXAIDefaultModel(t *testing.T) {
	p := NewXAI("", "key")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	// Verify the default model was applied (can't read private field, but provider is valid)
	if p.Name() != "xai" {
		t.Errorf("Name() = %q, want xai", p.Name())
	}
}

// ValidateEnvironment: compatible providers (groq, together, etc.) do not enforce
// key presence — only the "openai" named provider does. All others return nil.

func TestCompatibleValidateNoKeyReturnsNil(t *testing.T) {
	providers := []*openai.Provider{
		NewGroq("model", ""),
		NewTogetherAI("model", ""),
		NewDeepSeek("model", ""),
		NewPerplexity("model", ""),
		NewMistral("model", ""),
	}
	for _, p := range providers {
		if err := p.ValidateEnvironment(); err != nil {
			t.Errorf("%s.ValidateEnvironment() returned unexpected error: %v", p.Name(), err)
		}
	}
}

func TestOllamaValidateNoKey(t *testing.T) {
	p := NewOllama("llama3.2")
	if err := p.ValidateEnvironment(); err != nil {
		t.Errorf("Ollama ValidateEnvironment returned unexpected error: %v", err)
	}
}
