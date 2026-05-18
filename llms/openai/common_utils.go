// Package openai provides a base.LLM backed by the OpenAI chat completions API.
package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/openai/chat"
)

// post sends a JSON body to url and returns the parsed OAIResponse.
func (p *Provider) post(body []byte) (*chat.OAIResponse, error) {
	req, err := http.NewRequest(http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, err.Error(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "read body: "+err.Error(), err)
	}

	if resp.StatusCode != 200 {
		return nil, exceptions.ClassifyHTTPError(p.name, resp.StatusCode, raw)
	}

	var out chat.OAIResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "parse: "+err.Error(), err)
	}
	if out.Error != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "API error: "+out.Error.Message, nil)
	}
	return &out, nil
}

// newStreamConn opens a streaming HTTP connection and returns the raw response.
func (p *Provider) newStreamConn(body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	streamClient := &http.Client{Transport: p.client.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, err.Error(), err)
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, exceptions.ClassifyHTTPError(p.name, resp.StatusCode, raw)
	}
	return resp, nil
}
