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
	"fmt"
	"net/http"
	"sync/atomic"
)

// Collector holds all proxy-level counters.
type Collector struct {
	Requests2xx    atomic.Int64
	Requests4xx    atomic.Int64
	Requests5xx    atomic.Int64
	PromptTokens   atomic.Int64
	CompletionTokens atomic.Int64
	Errors         atomic.Int64
	ActiveRequests atomic.Int64
}

// NewCollector returns a zero-valued Collector.
func NewCollector() *Collector { return &Collector{} }

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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
