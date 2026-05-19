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

const (
	apiURL     = "https://api.anthropic.com/v1/messages"
	apiVersion = "2023-06-01"
)

// MakeCall executes a blocking Anthropic Messages API request.
func MakeCall(client *http.Client, apiKey string, body []byte) (*AntResponse, error) {
	return MakeCallURL(client, apiKey, apiURL, body)
}

// MakeCallURL is like MakeCall but uses the provided URL instead of the default.
func MakeCallURL(client *http.Client, apiKey, url string, body []byte) (*AntResponse, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	resp, err := client.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, err.Error(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, "read body: "+err.Error(), err)
	}

	if resp.StatusCode != 200 {
		return nil, exceptions.ClassifyHTTPError("anthropic", resp.StatusCode, raw)
	}

	var out AntResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, "parse: "+err.Error(), err)
	}
	if out.Error != nil {
		return nil, exceptions.NewProviderError("anthropic", 0,
			fmt.Sprintf("API error (%s): %s", out.Error.Type, out.Error.Message), nil)
	}
	return &out, nil
}

// MakeStreamCall opens a streaming Anthropic Messages API connection.
// The caller is responsible for closing resp.Body.
func MakeStreamCall(client *http.Client, apiKey string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError("anthropic", 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("Accept", "text/event-stream")

	streamClient := &http.Client{Transport: client.Transport}
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

// Anthropic SSE event names.
const (
	evContentBlockStart = "content_block_start"
	evContentBlockDelta = "content_block_delta"
	evMessageStop       = "message_stop"
)

// ReadSSE reads an Anthropic SSE stream from body and emits Deltas on ch.
// ch must be closed by the caller after ReadSSE returns.
func ReadSSE(ctx context.Context, body io.Reader, ch chan<- types.Delta) {
	scanner := bufio.NewScanner(body)

	var (
		currentEvent string
		toolCallID   string
		toolName     string
	)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- types.Delta{Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")

		switch currentEvent {
		case evContentBlockStart:
			var start struct {
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(payload), &start); err == nil {
				if start.ContentBlock.Type == "tool_use" {
					toolCallID = start.ContentBlock.ID
					toolName = start.ContentBlock.Name
				}
			}

		case evContentBlockDelta:
			var chunk AntChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				ch <- types.Delta{Err: fmt.Errorf("anthropic stream: parse delta: %w", err)}
				return
			}
			switch chunk.Delta.Type {
			case "text_delta":
				if chunk.Delta.Text != "" {
					ch <- types.Delta{Content: chunk.Delta.Text}
				}
			case "input_json_delta":
				if chunk.Delta.PartialJSON != "" {
					ch <- types.Delta{
						ToolCall: &types.ToolCall{
							ID:        toolCallID,
							Name:      toolName,
							Arguments: chunk.Delta.PartialJSON,
						},
					}
				}
			}

		case evMessageStop:
			ch <- types.Delta{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- types.Delta{Err: fmt.Errorf("anthropic stream: read: %w", err)}
	}
}
