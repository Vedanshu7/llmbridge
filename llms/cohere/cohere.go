// Package cohere provides a base.LLM backed by the Cohere API.
// It also implements base.Reranker via the /v1/rerank endpoint.
package cohere

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/cohere/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const (
	chatURL   = "https://api.cohere.com/v2/chat"
	rerankURL = "https://api.cohere.com/v1/rerank"

	defaultModel       = "command-r-plus-08-2024"
	defaultRerankModel = "rerank-v3.5"
)

// Provider calls the Cohere chat and rerank APIs.
// Construct with New; do not create the struct directly.
type Provider struct {
	model       string
	rerankModel string
	apiKey      string
	client      *http.Client
}

// New returns a Cohere Provider.
// If model is empty, "command-r-plus-08-2024" is used.
func New(model, apiKey string) *Provider {
	if model == "" {
		model = defaultModel
	}
	return &Provider{
		model:       model,
		rerankModel: defaultRerankModel,
		apiKey:      apiKey,
		client:      &http.Client{Timeout: 60 * time.Second},
	}
}

// WithRerankModel sets the model used for Rerank calls.
func (p *Provider) WithRerankModel(model string) *Provider {
	p.rerankModel = model
	return p
}

// Name implements base.LLM.
func (p *Provider) Name() string { return "cohere" }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.apiKey == "" {
		return fmt.Errorf("cohere: API key is not set")
	}
	return nil
}

// Complete sends a blocking request to Cohere and returns the full response.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToCohereRequest(req, p.model, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	raw, err := p.post(body)
	if err != nil {
		return nil, err
	}
	return chat.FromCohereResponse(raw, p.Name(), p.model), nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToCohereRequest(req, p.model, true)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	resp, err := p.newStreamConn(body)
	if err != nil {
		return nil, err
	}

	ch := make(chan types.Delta, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		chat.ReadSSE(ctx, p.Name(), resp.Body, ch)
	}()
	return ch, nil
}

// Rerank implements base.Reranker using the Cohere /v1/rerank endpoint.
func (p *Provider) Rerank(ctx context.Context, req types.RerankRequest) (*types.RerankResponse, error) {
	model := req.Model
	if model == "" {
		model = p.rerankModel
	}
	wireReq := chat.CohereRerankRequest{
		Model:           model,
		Query:           req.Query,
		Documents:       req.Documents,
		TopN:            req.TopN,
		ReturnDocuments: req.ReturnDocuments,
	}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	raw, err := p.postRaw(rerankURL, body)
	if err != nil {
		return nil, err
	}

	var cohereResp chat.CohereRerankResponse
	if err := json.Unmarshal(raw, &cohereResp); err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "parse rerank: "+err.Error(), err)
	}

	out := &types.RerankResponse{
		Provider: p.Name(),
		Model:    model,
		Results:  make([]types.RerankResult, len(cohereResp.Results)),
	}
	for i, r := range cohereResp.Results {
		rr := types.RerankResult{
			Index: r.Index,
			Score: r.RelevanceScore,
		}
		if r.Document != nil {
			rr.Document = r.Document.Text
		} else if r.Index < len(req.Documents) {
			rr.Document = req.Documents[r.Index]
		}
		out.Results[i] = rr
	}
	return out, nil
}
