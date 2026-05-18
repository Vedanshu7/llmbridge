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
	"context"
	"fmt"
	"io"
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
