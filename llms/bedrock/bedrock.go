// Package bedrock provides a base.LLM backed by AWS Bedrock Converse API.
// Authentication uses AWS Signature v4 (stdlib only — no AWS SDK required).
package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/bedrock/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const awsService = "bedrock"

// Provider calls the AWS Bedrock Converse API.
// Construct with New; do not create the struct directly.
type Provider struct {
	modelID     string
	region      string
	accessKeyID string
	secretKey   string
	client      *http.Client
}

// New returns a Bedrock Provider.
//   - modelID:     Bedrock model ID (e.g. "anthropic.claude-3-5-sonnet-20241022-v2:0").
//   - region:      AWS region (e.g. "us-east-1").
//   - accessKeyID: AWS access key ID.
//   - secretKey:   AWS secret access key.
func New(modelID, region, accessKeyID, secretKey string) *Provider {
	return &Provider{
		modelID:     modelID,
		region:      region,
		accessKeyID: accessKeyID,
		secretKey:   secretKey,
		client:      &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements base.LLM.
func (p *Provider) Name() string { return "bedrock" }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.accessKeyID == "" {
		return fmt.Errorf("bedrock: AWS access key ID is not set")
	}
	if p.secretKey == "" {
		return fmt.Errorf("bedrock: AWS secret access key is not set")
	}
	if p.region == "" {
		return fmt.Errorf("bedrock: AWS region is not set")
	}
	if p.modelID == "" {
		return fmt.Errorf("bedrock: model ID is not set")
	}
	return nil
}

// Complete sends a blocking Converse request and returns the full response.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToConverseRequest(req)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	raw, err := p.converse(body)
	if err != nil {
		return nil, err
	}
	return chat.FromConverseResponse(raw, p.Name(), p.modelID), nil
}

// Stream implements base.Streamer via the Bedrock ConverseStream API.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToConverseRequest(req)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	resp, err := p.converseStream(body)
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

func (p *Provider) converseURL() string {
	return fmt.Sprintf(
		"https://bedrock-runtime.%s.amazonaws.com/model/%s/converse",
		p.region, p.modelID,
	)
}

func (p *Provider) converseStreamURL() string {
	return fmt.Sprintf(
		"https://bedrock-runtime.%s.amazonaws.com/model/%s/converse-stream",
		p.region, p.modelID,
	)
}

func (p *Provider) converse(body []byte) (*chat.ConverseResponse, error) {
	req, err := http.NewRequest(http.MethodPost, p.converseURL(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	signRequest(req, p.accessKeyID, p.secretKey, p.region, awsService, body, time.Now().UTC())

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

	var out chat.ConverseResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "parse: "+err.Error(), err)
	}
	return &out, nil
}

func (p *Provider) converseStream(body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, p.converseStreamURL(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.amazon.eventstream")
	signRequest(req, p.accessKeyID, p.secretKey, p.region, awsService, body, time.Now().UTC())

	streamClient := &http.Client{Transport: p.client.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, exceptions.ClassifyHTTPError(p.Name(), resp.StatusCode, raw)
	}
	return resp, nil
}
