package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/callbacks"
	"github.com/Vedanshu7/llmbridge/types"
)

func TestRecordRequest2xx(t *testing.T) {
	c := NewCollector()
	c.RecordRequest(200)
	c.RecordRequest(201)
	if c.Requests2xx.Load() != 2 {
		t.Fatalf("expected 2, got %d", c.Requests2xx.Load())
	}
	if c.Requests4xx.Load() != 0 || c.Requests5xx.Load() != 0 {
		t.Fatal("unexpected counts in 4xx/5xx")
	}
}

func TestRecordRequest4xx(t *testing.T) {
	c := NewCollector()
	c.RecordRequest(400)
	c.RecordRequest(404)
	if c.Requests4xx.Load() != 2 {
		t.Fatalf("expected 2, got %d", c.Requests4xx.Load())
	}
}

func TestRecordRequest5xx(t *testing.T) {
	c := NewCollector()
	c.RecordRequest(500)
	c.RecordRequest(503)
	if c.Requests5xx.Load() != 2 {
		t.Fatalf("expected 2, got %d", c.Requests5xx.Load())
	}
}

func TestRecordTokens(t *testing.T) {
	c := NewCollector()
	c.RecordTokens(100, 50)
	c.RecordTokens(200, 75)
	if c.PromptTokens.Load() != 300 {
		t.Fatalf("expected 300 prompt tokens, got %d", c.PromptTokens.Load())
	}
	if c.CompletionTokens.Load() != 125 {
		t.Fatalf("expected 125 completion tokens, got %d", c.CompletionTokens.Load())
	}
}

func TestRecordError(t *testing.T) {
	c := NewCollector()
	c.RecordError()
	c.RecordError()
	if c.Errors.Load() != 2 {
		t.Fatalf("expected 2 errors, got %d", c.Errors.Load())
	}
}

func TestActiveRequests(t *testing.T) {
	c := NewCollector()
	c.IncActive()
	c.IncActive()
	if c.ActiveRequests.Load() != 2 {
		t.Fatalf("expected 2, got %d", c.ActiveRequests.Load())
	}
	c.DecActive()
	if c.ActiveRequests.Load() != 1 {
		t.Fatalf("expected 1 after decrement, got %d", c.ActiveRequests.Load())
	}
}

func TestHandlerContainsMetrics(t *testing.T) {
	c := NewCollector()
	c.RecordRequest(200)
	c.RecordRequest(404)
	c.RecordRequest(500)
	c.RecordTokens(10, 5)
	c.RecordError()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.Handler()(w, req)

	body := w.Body.String()
	checks := []string{
		"llmbridge_requests_total{status=\"2xx\"} 1",
		"llmbridge_requests_total{status=\"4xx\"} 1",
		"llmbridge_requests_total{status=\"5xx\"} 1",
		"llmbridge_tokens_total{type=\"prompt\"} 10",
		"llmbridge_tokens_total{type=\"completion\"} 5",
		"llmbridge_errors_total 1",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("expected %q in metrics output:\n%s", check, body)
		}
	}
	ct := w.Result().Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
}

func TestMiddlewareRecordsStatus(t *testing.T) {
	c := NewCollector()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	handler := c.Middleware(inner)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if c.Requests2xx.Load() != 1 {
		t.Fatalf("expected 1 2xx request, got %d", c.Requests2xx.Load())
	}
	if c.ActiveRequests.Load() != 0 {
		t.Fatalf("expected 0 active requests after handler, got %d", c.ActiveRequests.Load())
	}
}

// ---- RecordProviderCall / statsFor ----

func TestRecordProviderCallSuccess(t *testing.T) {
	c := NewCollector()
	c.RecordProviderCall("openai", 120, false)
	c.RecordProviderCall("openai", 80, false)

	s := c.statsFor("openai")
	if s.requests.Load() != 2 {
		t.Errorf("requests = %d, want 2", s.requests.Load())
	}
	if s.errors.Load() != 0 {
		t.Errorf("errors = %d, want 0", s.errors.Load())
	}
	if s.latencyMS.Load() != 200 {
		t.Errorf("latencyMS = %d, want 200", s.latencyMS.Load())
	}
}

func TestRecordProviderCallFailure(t *testing.T) {
	c := NewCollector()
	c.RecordProviderCall("anthropic", 500, true)

	s := c.statsFor("anthropic")
	if s.requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", s.requests.Load())
	}
	if s.errors.Load() != 1 {
		t.Errorf("errors = %d, want 1", s.errors.Load())
	}
}

func TestStatsForCreatesEntry(t *testing.T) {
	c := NewCollector()
	s1 := c.statsFor("groq")
	s2 := c.statsFor("groq") // should return same pointer
	if s1 != s2 {
		t.Error("statsFor should return the same *providerStats on repeated calls")
	}
}

func TestRecordProviderCallAppearsInMetrics(t *testing.T) {
	c := NewCollector()
	c.RecordProviderCall("openai", 100, false)
	c.RecordProviderCall("openai", 50, true)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.Handler()(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `llmbridge_provider_requests_total{provider="openai"} 2`) {
		t.Errorf("expected provider request count in metrics:\n%s", body)
	}
	if !strings.Contains(body, `llmbridge_provider_errors_total{provider="openai"} 1`) {
		t.Errorf("expected provider error count in metrics:\n%s", body)
	}
	if !strings.Contains(body, `llmbridge_provider_latency_ms_total{provider="openai"} 150`) {
		t.Errorf("expected provider latency in metrics:\n%s", body)
	}
}

// ---- CallbackHandler ----

func TestCallbackHandlerResponse(t *testing.T) {
	c := NewCollector()
	h := c.CallbackHandler()

	h(context.Background(), callbacks.Event{
		Type:     callbacks.EventResponse,
		Provider: "openai",
		Duration: 200 * time.Millisecond,
		Response: &types.Response{
			Usage: &types.UsageData{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	})

	s := c.statsFor("openai")
	if s.requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", s.requests.Load())
	}
	if s.errors.Load() != 0 {
		t.Errorf("errors = %d, want 0", s.errors.Load())
	}
	if c.PromptTokens.Load() != 10 {
		t.Errorf("PromptTokens = %d, want 10", c.PromptTokens.Load())
	}
	if c.CompletionTokens.Load() != 5 {
		t.Errorf("CompletionTokens = %d, want 5", c.CompletionTokens.Load())
	}
}

func TestCallbackHandlerError(t *testing.T) {
	c := NewCollector()
	h := c.CallbackHandler()

	h(context.Background(), callbacks.Event{
		Type:     callbacks.EventError,
		Provider: "anthropic",
		Duration: 100 * time.Millisecond,
		Error:    context.DeadlineExceeded,
	})

	s := c.statsFor("anthropic")
	if s.errors.Load() != 1 {
		t.Errorf("errors = %d, want 1", s.errors.Load())
	}
	if c.Errors.Load() != 1 {
		t.Errorf("Errors = %d, want 1", c.Errors.Load())
	}
}

func TestCallbackHandlerIgnoresNonResponseEvents(t *testing.T) {
	c := NewCollector()
	h := c.CallbackHandler()

	h(context.Background(), callbacks.Event{
		Type:     callbacks.EventRequest,
		Provider: "openai",
	})

	c.providerMu.RLock()
	_, exists := c.providerStats["openai"]
	c.providerMu.RUnlock()

	if exists {
		t.Error("CallbackHandler should not create provider entry for EventRequest")
	}
}

func TestCallbackHandlerNilUsage(t *testing.T) {
	c := NewCollector()
	h := c.CallbackHandler()

	// Response with nil Usage — should not panic, tokens stay 0.
	h(context.Background(), callbacks.Event{
		Type:     callbacks.EventResponse,
		Provider: "openai",
		Response: &types.Response{}, // nil Usage
	})

	if c.PromptTokens.Load() != 0 {
		t.Errorf("PromptTokens should be 0 with nil usage, got %d", c.PromptTokens.Load())
	}
}
