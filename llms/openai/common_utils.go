// Package openai provides a base.LLM backed by the OpenAI chat completions API.
package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/openai/chat"
)

// getURL sends a GET to url with Bearer auth and returns the raw response bytes.
func (p *Provider) getURL(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, err.Error(), err)
	}
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
	return raw, nil
}

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

// postURL sends a JSON body to the specified URL and returns the raw response bytes.
func (p *Provider) postURL(url string, body []byte) ([]byte, error) {
	return p.postURLContentType(url, body, "application/json")
}

// postURLContentType sends body with the given Content-Type and returns the raw response bytes.
func (p *Provider) postURLContentType(url string, body []byte, contentType string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", contentType)
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
	return raw, nil
}

// buildTranscribeForm builds a multipart/form-data body for the Whisper endpoint.
func buildTranscribeForm(audio []byte, model, language, format string) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("file", "audio.mp3")
	if err != nil {
		return nil, "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(audio); err != nil {
		return nil, "", fmt.Errorf("write audio: %w", err)
	}
	_ = w.WriteField("model", model)
	if language != "" {
		_ = w.WriteField("language", language)
	}
	if format != "" && format != "json" {
		_ = w.WriteField("response_format", format)
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
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
