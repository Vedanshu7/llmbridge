// Package anthropic provides a base.LLM backed by the Anthropic Messages API.
package anthropic

import (
	"bytes"
	"io"
	"net/http"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/anthropic/chat"
)

const (
	apiURL     = "https://api.anthropic.com/v1/messages"
	apiVersion = "2023-06-01"
)

// doStream opens a streaming HTTP connection without a read deadline.
func (p *Provider) doStream(body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("Accept", "text/event-stream")

	streamClient := &http.Client{Transport: p.client.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, err.Error(), err)
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, exceptions.ClassifyHTTPError("anthropic", resp.StatusCode, raw)
	}
	return resp, nil
}

// makeCall wraps do(), reads the body, and returns a parsed AntResponse.
func (p *Provider) makeCall(body []byte) (*chat.AntResponse, error) {
	return chat.MakeCall(p.client, p.apiKey, body)
}
