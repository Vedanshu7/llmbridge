package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
