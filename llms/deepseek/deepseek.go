// Package deepseek provides a base.LLM backed by the DeepSeek chat API.
// The wire format is OpenAI-compatible; requests use the standard OpenAI schema.
// Responses from the deepseek-reasoner (R1) model include a reasoning_content
// field alongside the final content; this package surfaces it as a <think> block
// prepended to the response content.
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/openai/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const (
	defaultURL   = "https://api.deepseek.com/v1/chat/completions"
	defaultModel = "deepseek-chat"
)

// Provider calls the DeepSeek chat API.
// Construct with New; do not create the struct directly.
type Provider struct {
	model   string
	apiKey  string
	client  *http.Client
	baseURL string // empty = use defaultURL; overridden in tests
}

// New returns a DeepSeek Provider.
// If model is empty, "deepseek-chat" is used.
// If apiKey is empty, DEEPSEEK_API_KEY is read from the environment at call time.
func New(model, apiKey string) *Provider {
	if model == "" {
		model = defaultModel
	}
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	return &Provider{
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: 120 * time.Second}, // R1 can be slow
	}
}

// Name implements base.LLM.
func (p *Provider) Name() string { return "deepseek" }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.apiKey == "" && os.Getenv("DEEPSEEK_API_KEY") == "" {
		return fmt.Errorf("deepseek: DEEPSEEK_API_KEY is not set")
	}
	return nil
}

// Complete sends a blocking chat completions request and returns the full response.
// On rate-limit or server errors it retries once after a 2-second backoff.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToOAIRequest(req, p.model, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	var raw *dsResponse
	for attempt := range 2 {
		raw, err = p.post(body)
		if err == nil {
			break
		}
		var rl *exceptions.RateLimitError
		if attempt == 0 && errors.As(err, &rl) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		return nil, err
	}

	if len(raw.Choices) == 0 {
		return nil, exceptions.NewProviderError(p.Name(), 0, "empty choices in response", nil)
	}

	oai := raw.toOAIResponse()
	resp := chat.FromOAIResponse(oai, p.Name(), p.model)

	// Surface reasoning content as a <think> block so callers can inspect it.
	if rc := raw.Choices[0].Message.ReasoningContent; rc != "" {
		resp.Content = "<think>\n" + rc + "\n</think>\n" + resp.Content
	}
	return resp, nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
// Note: reasoning_content is not included in streaming output from deepseek-reasoner;
// it arrives before the main content delta begins.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToOAIRequest(req, p.model, true)
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

// ---- DeepSeek-specific response types ----

// dsMessage extends the standard OpenAI message with the reasoning_content field
// returned by deepseek-reasoner (R1).
type dsMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// dsResponse is the DeepSeek completion response wire type.
type dsResponse struct {
	Choices []struct {
		Message      dsMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Model string `json:"model,omitempty"`
}

// toOAIResponse converts dsResponse to the standard chat.OAIResponse so the
// existing FromOAIResponse conversion can be reused.
func (r *dsResponse) toOAIResponse() *chat.OAIResponse {
	oai := &chat.OAIResponse{}
	for _, c := range r.Choices {
		oai.Choices = append(oai.Choices, struct {
			Message      chat.OAIMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		}{
			Message:      chat.OAIMessage{Role: c.Message.Role, Content: c.Message.Content},
			FinishReason: c.FinishReason,
		})
	}
	if r.Usage != nil {
		oai.Usage = &struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens:      r.Usage.TotalTokens,
		}
	}
	if r.Error != nil {
		oai.Error = &struct{ Message string `json:"message"` }{Message: r.Error.Message}
	}
	return oai
}

// ---- HTTP helpers ----

func (p *Provider) url() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return defaultURL
}

func (p *Provider) post(body []byte) (*dsResponse, error) {
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

	var out dsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "parse: "+err.Error(), err)
	}
	if out.Error != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "API error: "+out.Error.Message, nil)
	}
	return &out, nil
}

func (p *Provider) newStreamConn(body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, p.url(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

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

// HasReasoningContent returns true if the response contains a <think> block,
// indicating the model produced explicit reasoning steps.
func HasReasoningContent(resp *types.Response) bool {
	return strings.HasPrefix(resp.Content, "<think>")
}

// ExtractReasoning splits a response that contains a <think> block into the
// reasoning text and the final answer. If no <think> block is present, it
// returns ("", content).
func ExtractReasoning(content string) (reasoning, answer string) {
	const open, close = "<think>\n", "\n</think>\n"
	if !strings.HasPrefix(content, open) {
		return "", content
	}
	rest := strings.TrimPrefix(content, open)
	idx := strings.Index(rest, close)
	if idx < 0 {
		return rest, ""
	}
	return rest[:idx], rest[idx+len(close):]
}
