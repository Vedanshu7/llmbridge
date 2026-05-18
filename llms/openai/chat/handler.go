package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/types"
)

// MakeCall executes a blocking OpenAI chat completion HTTP request.
func MakeCall(client *http.Client, url, apiKey, providerName string, body []byte) (*OAIResponse, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(providerName, 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(providerName, 0, err.Error(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, exceptions.NewProviderError(providerName, 0, "read body: "+err.Error(), err)
	}

	if resp.StatusCode != 200 {
		return nil, exceptions.ClassifyHTTPError(providerName, resp.StatusCode, raw)
	}

	var out OAIResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, exceptions.NewProviderError(providerName, 0, "parse: "+err.Error(), err)
	}
	if out.Error != nil {
		return nil, exceptions.NewProviderError(providerName, 0, "API error: "+out.Error.Message, nil)
	}
	return &out, nil
}

// MakeStreamCall opens a streaming SSE connection and returns the raw http.Response.
// The caller is responsible for closing resp.Body.
func MakeStreamCall(client *http.Client, url, apiKey, providerName string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(providerName, 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// Use a transport-only client so there's no read deadline on the stream.
	streamClient := &http.Client{Transport: client.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(providerName, 0, err.Error(), err)
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, exceptions.ClassifyHTTPError(providerName, resp.StatusCode, raw)
	}
	return resp, nil
}

// ReadSSE reads an OpenAI SSE stream from body and emits Deltas on ch.
// ch must be closed by the caller after ReadSSE returns.
func ReadSSE(ctx context.Context, providerName string, body io.Reader, ch chan<- types.Delta) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- types.Delta{Err: ctx.Err()}
			return
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			ch <- types.Delta{Done: true}
			return
		}

		var chunk OAIChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			ch <- types.Delta{Err: fmt.Errorf("%s stream: parse: %w", providerName, err)}
			return
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta
		d := types.Delta{Content: delta.Content}
		for _, tc := range delta.ToolCalls {
			d.ToolCall = &types.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			}
		}
		if d.Content != "" || d.ToolCall != nil {
			ch <- d
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- types.Delta{Err: fmt.Errorf("%s stream: read: %w", providerName, err)}
	}
}
