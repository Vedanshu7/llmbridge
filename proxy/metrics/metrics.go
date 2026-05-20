// Package metrics provides a minimal Prometheus-compatible /metrics endpoint
// for the llmbridge proxy server. No external dependencies — uses stdlib
// encoding/json and sync/atomic for thread-safe counters.
//
// Exposed metrics (Prometheus text format):
//
//	llmbridge_requests_total{status="2xx|4xx|5xx"} N
//	llmbridge_tokens_total{type="prompt|completion"} N
//	llmbridge_errors_total N
//	llmbridge_active_requests N
package metrics

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/Vedanshu7/llmbridge/callbacks"
)

// providerStats holds per-provider LLM call counters.
type providerStats struct {
	requests  atomic.Int64
	errors    atomic.Int64
	latencyMS atomic.Int64 // sum of latencies in ms (divide by requests for avg)
}

// Collector holds all proxy-level counters.
type Collector struct {
	Requests2xx      atomic.Int64
	Requests4xx      atomic.Int64
	Requests5xx      atomic.Int64
	PromptTokens     atomic.Int64
	CompletionTokens atomic.Int64
	Errors           atomic.Int64
	ActiveRequests   atomic.Int64

	providerMu    sync.RWMutex
	providerStats map[string]*providerStats // keyed by provider name
}

// NewCollector returns a zero-valued Collector.
func NewCollector() *Collector {
	return &Collector{providerStats: make(map[string]*providerStats)}
}

func (c *Collector) statsFor(provider string) *providerStats {
	c.providerMu.RLock()
	s, ok := c.providerStats[provider]
	c.providerMu.RUnlock()
	if ok {
		return s
	}
	c.providerMu.Lock()
	if s, ok = c.providerStats[provider]; !ok {
		s = &providerStats{}
		c.providerStats[provider] = s
	}
	c.providerMu.Unlock()
	return s
}

// RecordProviderCall records an LLM provider call result.
// latencyMS is the call duration in milliseconds.
func (c *Collector) RecordProviderCall(provider string, latencyMS int64, failed bool) {
	s := c.statsFor(provider)
	s.requests.Add(1)
	s.latencyMS.Add(latencyMS)
	if failed {
		s.errors.Add(1)
	}
}

// RecordRequest increments the counter for the given HTTP status code.
func (c *Collector) RecordRequest(status int) {
	switch {
	case status >= 500:
		c.Requests5xx.Add(1)
	case status >= 400:
		c.Requests4xx.Add(1)
	default:
		c.Requests2xx.Add(1)
	}
}

// RecordTokens adds prompt and completion token counts.
func (c *Collector) RecordTokens(prompt, completion int) {
	c.PromptTokens.Add(int64(prompt))
	c.CompletionTokens.Add(int64(completion))
}

// RecordError increments the error counter.
func (c *Collector) RecordError() { c.Errors.Add(1) }

// IncActive increments the in-flight request gauge.
func (c *Collector) IncActive() { c.ActiveRequests.Add(1) }

// DecActive decrements the in-flight request gauge.
func (c *Collector) DecActive() { c.ActiveRequests.Add(-1) }

// Handler returns an http.HandlerFunc that serves Prometheus text-format metrics.
func (c *Collector) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# HELP llmbridge_requests_total Total HTTP requests by status class.\n")
		fmt.Fprintf(w, "# TYPE llmbridge_requests_total counter\n")
		fmt.Fprintf(w, "llmbridge_requests_total{status=\"2xx\"} %d\n", c.Requests2xx.Load())
		fmt.Fprintf(w, "llmbridge_requests_total{status=\"4xx\"} %d\n", c.Requests4xx.Load())
		fmt.Fprintf(w, "llmbridge_requests_total{status=\"5xx\"} %d\n", c.Requests5xx.Load())

		fmt.Fprintf(w, "# HELP llmbridge_tokens_total Total LLM tokens processed.\n")
		fmt.Fprintf(w, "# TYPE llmbridge_tokens_total counter\n")
		fmt.Fprintf(w, "llmbridge_tokens_total{type=\"prompt\"} %d\n", c.PromptTokens.Load())
		fmt.Fprintf(w, "llmbridge_tokens_total{type=\"completion\"} %d\n", c.CompletionTokens.Load())

		fmt.Fprintf(w, "# HELP llmbridge_errors_total Total provider errors.\n")
		fmt.Fprintf(w, "# TYPE llmbridge_errors_total counter\n")
		fmt.Fprintf(w, "llmbridge_errors_total %d\n", c.Errors.Load())

		fmt.Fprintf(w, "# HELP llmbridge_active_requests Current in-flight requests.\n")
		fmt.Fprintf(w, "# TYPE llmbridge_active_requests gauge\n")
		fmt.Fprintf(w, "llmbridge_active_requests %d\n", c.ActiveRequests.Load())

		c.providerMu.RLock()
		pstats := make(map[string]*providerStats, len(c.providerStats))
		for k, v := range c.providerStats {
			pstats[k] = v
		}
		c.providerMu.RUnlock()

		if len(pstats) > 0 {
			fmt.Fprintf(w, "# HELP llmbridge_provider_requests_total Total LLM calls per provider.\n")
			fmt.Fprintf(w, "# TYPE llmbridge_provider_requests_total counter\n")
			for p, s := range pstats {
				fmt.Fprintf(w, "llmbridge_provider_requests_total{provider=%q} %d\n", p, s.requests.Load())
			}
			fmt.Fprintf(w, "# HELP llmbridge_provider_errors_total Total LLM errors per provider.\n")
			fmt.Fprintf(w, "# TYPE llmbridge_provider_errors_total counter\n")
			for p, s := range pstats {
				fmt.Fprintf(w, "llmbridge_provider_errors_total{provider=%q} %d\n", p, s.errors.Load())
			}
			fmt.Fprintf(w, "# HELP llmbridge_provider_latency_ms_total Sum of LLM call latencies per provider (ms).\n")
			fmt.Fprintf(w, "# TYPE llmbridge_provider_latency_ms_total counter\n")
			for p, s := range pstats {
				fmt.Fprintf(w, "llmbridge_provider_latency_ms_total{provider=%q} %d\n", p, s.latencyMS.Load())
			}
		}
	}
}

// Middleware wraps an http.Handler, recording request status and active count.
func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.IncActive()
		defer c.DecActive()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		c.RecordRequest(rec.status)
	})
}

// CallbackHandler returns a callbacks.Handler that records per-provider LLM call
// metrics into this Collector. Register it with a callbacks.Manager to keep the
// /metrics endpoint accurate without any extra plumbing.
func (c *Collector) CallbackHandler() callbacks.Handler {
	return func(_ context.Context, event callbacks.Event) {
		switch event.Type {
		case callbacks.EventResponse:
			c.RecordProviderCall(event.Provider, event.Duration.Milliseconds(), false)
			if event.Response != nil && event.Response.Usage != nil {
				c.RecordTokens(event.Response.Usage.PromptTokens, event.Response.Usage.CompletionTokens)
			}
		case callbacks.EventError:
			c.RecordProviderCall(event.Provider, event.Duration.Milliseconds(), true)
			c.RecordError()
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
