// Package callbacks provides an event-driven observability system for llmbridge.
// Register handlers to be notified of every LLM request, response, and error.
//
// Usage:
//
//	m := callbacks.NewManager()
//	m.Register(callbacks.LogHandler(os.Stderr))
//	p := callbacks.Instrument(openai.New("gpt-4o", key), m)
//	resp, err := p.Complete(ctx, req)
package callbacks

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

// EventType identifies what happened in an Event.
type EventType string

const (
	// EventRequest fires before a provider call is made.
	EventRequest EventType = "request"
	// EventResponse fires after a successful provider call.
	EventResponse EventType = "response"
	// EventError fires when a provider call returns an error.
	EventError EventType = "error"
	// EventStreamChunk fires for each chunk received during streaming.
	EventStreamChunk EventType = "stream_chunk"
)

// Event carries all information about a single LLM operation.
type Event struct {
	Type     EventType
	Provider string
	Model    string
	Request  *types.Request
	Response *types.Response
	Error    error
	Duration time.Duration
	Metadata map[string]string
}

// Handler is a function called on each Event. Handlers must not block.
type Handler func(ctx context.Context, event Event)

// Manager holds a set of registered Handlers and fires them on each event.
// All methods are safe for concurrent use.
type Manager struct {
	mu       sync.RWMutex
	handlers []Handler
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{}
}

// Register adds a Handler to the manager.
func (m *Manager) Register(h Handler) {
	m.mu.Lock()
	m.handlers = append(m.handlers, h)
	m.mu.Unlock()
}

// Fire delivers event to all registered handlers in registration order.
// Panics inside handlers are recovered silently to protect the call path.
func (m *Manager) Fire(ctx context.Context, event Event) {
	m.mu.RLock()
	handlers := m.handlers
	m.mu.RUnlock()
	for _, h := range handlers {
		safeCall(ctx, h, event)
	}
}

func safeCall(ctx context.Context, h Handler, event Event) {
	defer func() { recover() }() //nolint:errcheck
	h(ctx, event)
}

// ---- Built-in handlers ----

// LogHandler returns a Handler that writes a one-line summary of each event to w.
func LogHandler(w io.Writer) Handler {
	return func(ctx context.Context, event Event) {
		switch event.Type {
		case EventResponse:
			fmt.Fprintf(w, "[llmbridge] %s provider=%s model=%s duration=%s tokens=%d\n",
				event.Type, event.Provider, event.Model,
				event.Duration.Round(time.Millisecond),
				totalTokens(event.Response),
			)
		case EventError:
			fmt.Fprintf(w, "[llmbridge] %s provider=%s model=%s duration=%s err=%v\n",
				event.Type, event.Provider, event.Model,
				event.Duration.Round(time.Millisecond),
				event.Error,
			)
		}
	}
}

// NoopHandler returns a Handler that does nothing. Useful in tests.
func NoopHandler() Handler {
	return func(_ context.Context, _ Event) {}
}

// JSONLogHandler returns a Handler that writes one JSON line per event to w.
// Output fields: time, event, provider, model, duration_ms, tokens, error.
func JSONLogHandler(w io.Writer) Handler {
	return func(_ context.Context, event Event) {
		rec := map[string]interface{}{
			"time":     time.Now().UTC().Format(time.RFC3339Nano),
			"event":    string(event.Type),
			"provider": event.Provider,
			"model":    event.Model,
		}
		if event.Duration > 0 {
			rec["duration_ms"] = event.Duration.Milliseconds()
		}
		if t := totalTokens(event.Response); t > 0 {
			rec["tokens"] = t
		}
		if event.Error != nil {
			rec["error"] = event.Error.Error()
		}
		b, _ := json.Marshal(rec)
		_, _ = fmt.Fprintf(w, "%s\n", b)
	}
}

// WebhookHandler returns a Handler that POSTs one JSON payload per event to url.
// The payload matches JSONLogHandler output. Delivery is best-effort — failures
// are silently dropped to avoid blocking the call path.
// An optional http.Client may be supplied; pass nil to use a 5-second default.
func WebhookHandler(url string, client *http.Client) Handler {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return func(_ context.Context, event Event) {
		rec := map[string]interface{}{
			"time":     time.Now().UTC().Format(time.RFC3339Nano),
			"event":    string(event.Type),
			"provider": event.Provider,
			"model":    event.Model,
		}
		if event.Duration > 0 {
			rec["duration_ms"] = event.Duration.Milliseconds()
		}
		if t := totalTokens(event.Response); t > 0 {
			rec["tokens"] = t
		}
		if event.Error != nil {
			rec["error"] = event.Error.Error()
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return
		}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}

// LangfuseHandler returns a Handler that sends trace + generation events to a
// Langfuse instance (cloud or self-hosted) using Basic auth (publicKey:secretKey).
// baseURL should be "https://cloud.langfuse.com" for the managed service.
// Delivery is best-effort — failures are silently dropped.
// An optional http.Client may be supplied; pass nil to use a 5-second default.
func LangfuseHandler(publicKey, secretKey, baseURL string, client *http.Client) Handler {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	creds := base64.StdEncoding.EncodeToString([]byte(publicKey + ":" + secretKey))
	return func(_ context.Context, event Event) {
		if event.Type != EventResponse && event.Type != EventError {
			return
		}
		traceID := fmt.Sprintf("llmbridge-%d", time.Now().UnixNano())
		now := time.Now().UTC().Format(time.RFC3339Nano)

		var inputText, outputText string
		if event.Request != nil && len(event.Request.Messages) > 0 {
			last := event.Request.Messages[len(event.Request.Messages)-1]
			inputText = last.Content
		}
		if event.Response != nil {
			outputText = event.Response.Content
		}
		var errMsg *string
		if event.Error != nil {
			s := event.Error.Error()
			errMsg = &s
		}

		type observationBody struct {
			ID          string      `json:"id"`
			TraceID     string      `json:"traceId"`
			Type        string      `json:"type"`
			Name        string      `json:"name"`
			StartTime   string      `json:"startTime"`
			EndTime     string      `json:"endTime"`
			Model       string      `json:"model"`
			Input       string      `json:"input"`
			Output      string      `json:"output"`
			Level       string      `json:"level"`
			StatusMsg   *string     `json:"statusMessage,omitempty"`
			Usage       interface{} `json:"usage,omitempty"`
		}
		level := "DEFAULT"
		if event.Type == EventError {
			level = "ERROR"
		}
		var usage interface{}
		if event.Response != nil && event.Response.Usage != nil {
			usage = map[string]int{
				"input":  event.Response.Usage.PromptTokens,
				"output": event.Response.Usage.CompletionTokens,
				"total":  event.Response.Usage.TotalTokens,
			}
		}
		endTime := time.Now().UTC().Format(time.RFC3339Nano)
		obs := observationBody{
			ID:        traceID + "-gen",
			TraceID:   traceID,
			Type:      "GENERATION",
			Name:      event.Provider + "/" + event.Model,
			StartTime: now,
			EndTime:   endTime,
			Model:     event.Model,
			Input:     inputText,
			Output:    outputText,
			Level:     level,
			StatusMsg: errMsg,
			Usage:     usage,
		}
		payload := map[string]interface{}{
			"batch": []map[string]interface{}{
				{"id": traceID, "type": "trace-create", "body": map[string]string{
					"id": traceID, "name": "llmbridge",
				}},
				{"id": traceID + "-gen", "type": "observation-create", "body": obs},
			},
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return
		}
		req, err := http.NewRequest(http.MethodPost, baseURL+"/api/public/ingestion", bytes.NewReader(b))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+creds)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}

// VerboseLogHandler returns a Handler that writes full request and response
// bodies as JSON lines to w. Each line carries: time, event, key (from
// Metadata["key"]), and the complete request or response body.
// Intended for debugging and compliance audit trails — never written to stdout.
func VerboseLogHandler(w io.Writer) Handler {
	return func(_ context.Context, event Event) {
		rec := map[string]interface{}{
			"time":  time.Now().UTC().Format(time.RFC3339Nano),
			"event": string(event.Type),
		}
		if k := event.Metadata["key"]; k != "" {
			rec["key"] = k
		}
		if event.Provider != "" {
			rec["provider"] = event.Provider
		}
		if event.Model != "" {
			rec["model"] = event.Model
		}
		switch event.Type {
		case EventRequest:
			if event.Request != nil {
				rec["request"] = event.Request
			}
		case EventResponse:
			if event.Response != nil {
				rec["response"] = event.Response
			}
		case EventError:
			if event.Error != nil {
				rec["error"] = event.Error.Error()
			}
		}
		b, _ := json.Marshal(rec)
		_, _ = fmt.Fprintf(w, "%s\n", b)
	}
}

func totalTokens(resp *types.Response) int {
	if resp == nil || resp.Usage == nil {
		return 0
	}
	return resp.Usage.TotalTokens
}

// ---- InstrumentedProvider ----

// InstrumentedProvider wraps any base.LLM and fires Events on every call.
type InstrumentedProvider struct {
	inner   types.LLM
	manager *Manager
}

// Instrument wraps p so that every Complete call fires callbacks on m.
func Instrument(p types.LLM, m *Manager) *InstrumentedProvider {
	return &InstrumentedProvider{inner: p, manager: m}
}

// Name implements base.LLM.
func (ip *InstrumentedProvider) Name() string { return ip.inner.Name() }

// ValidateEnvironment implements base.LLM.
func (ip *InstrumentedProvider) ValidateEnvironment() error {
	return ip.inner.ValidateEnvironment()
}

// Complete implements base.LLM, firing EventRequest + EventResponse/EventError.
func (ip *InstrumentedProvider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	ip.manager.Fire(ctx, Event{
		Type:     EventRequest,
		Provider: ip.inner.Name(),
		Model:    req.Model,
		Request:  &req,
	})
	start := time.Now()
	resp, err := ip.inner.Complete(ctx, req)
	elapsed := time.Since(start)
	if err != nil {
		ip.manager.Fire(ctx, Event{
			Type:     EventError,
			Provider: ip.inner.Name(),
			Model:    req.Model,
			Request:  &req,
			Error:    err,
			Duration: elapsed,
		})
		return nil, err
	}
	ip.manager.Fire(ctx, Event{
		Type:     EventResponse,
		Provider: ip.inner.Name(),
		Model:    req.Model,
		Request:  &req,
		Response: resp,
		Duration: elapsed,
	})
	return resp, nil
}
