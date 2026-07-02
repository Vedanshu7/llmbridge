// Package voyage provides a base.EmbedProvider backed by the Voyage AI
// embeddings API. Voyage is embedding-only; it does not implement base.LLM,
// and its wire format is not OpenAI-compatible (Bearer auth, {"input":[...],
// "model":...} request, {"data":[{"embedding":[...],"index":N}]} response).
package voyage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
)

const (
	defaultURL   = "https://api.voyageai.com/v1/embeddings"
	defaultModel = "voyage-3"
)

// Provider calls the Voyage AI embeddings API.
// Construct with New; do not create the struct directly.
type Provider struct {
	model   string
	apiKey  string
	client  *http.Client
	baseURL string // empty = use defaultURL; set in tests
}

// New returns a Voyage Provider. If model is empty, "voyage-3" is used.
// If apiKey is empty, VOYAGE_API_KEY is read from the environment.
func New(model, apiKey string) *Provider {
	if model == "" {
		model = defaultModel
	}
	if apiKey == "" {
		apiKey = os.Getenv("VOYAGE_API_KEY")
	}
	return &Provider{
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements base.EmbedProvider.
func (p *Provider) Name() string { return "voyage" }

// ValidateEnvironment implements base.EmbedProvider.
func (p *Provider) ValidateEnvironment() error {
	if p.apiKey == "" && os.Getenv("VOYAGE_API_KEY") == "" {
		return fmt.Errorf("voyage: VOYAGE_API_KEY is not set")
	}
	return nil
}

// Embed implements base.EmbedProvider using the Voyage /v1/embeddings endpoint.
func (p *Provider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	wireReq := map[string]interface{}{"input": texts, "model": p.model}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	raw, err := p.post(body)
	if err != nil {
		return nil, err
	}

	var wire struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "parse embeddings: "+err.Error(), err)
	}

	out := make([][]float64, len(wire.Data))
	for _, d := range wire.Data {
		if d.Index >= 0 && d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
}

func (p *Provider) url() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return defaultURL
}

func (p *Provider) post(body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, p.url(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "read body: "+err.Error(), err)
	}
	if resp.StatusCode != 200 {
		return nil, exceptions.ClassifyHTTPError(p.Name(), resp.StatusCode, raw)
	}
	return raw, nil
}
