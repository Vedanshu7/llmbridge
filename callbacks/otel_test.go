package callbacks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

// capturedBodies is a goroutine-safe container for HTTP request bodies captured in tests.
type capturedBodies struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *capturedBodies) append(b []byte) {
	c.mu.Lock()
	c.bodies = append(c.bodies, b)
	c.mu.Unlock()
}

func (c *capturedBodies) all() [][]byte {
	c.mu.Lock()
	out := make([][]byte, len(c.bodies))
	copy(out, c.bodies)
	c.mu.Unlock()
	return out
}

// captureServer returns a test HTTP server that safely captures all request bodies.
func captureServer(t *testing.T) (*httptest.Server, *capturedBodies) {
	t.Helper()
	captured := &capturedBodies{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [65536]byte
		n, _ := r.Body.Read(buf[:])
		b := make([]byte, n)
		copy(b, buf[:n])
		captured.append(b)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func TestOTELHandlerEmitsSpanOnResponse(t *testing.T) {
	srv, captured := captureServer(t)

	h := OTELHandler(srv.URL, "test-service", nil)
	ctx := WithTraceID(context.Background(), "aabbccddeeff00112233445566778899")

	h(ctx, Event{Type: EventRequest, Provider: "openai", Model: "gpt-4o"})
	h(ctx, Event{
		Type:     EventResponse,
		Provider: "openai",
		Model:    "gpt-4o",
		Duration: 50 * time.Millisecond,
		Response: &types.Response{
			Content:  "hello",
			Provider: "openai",
			Usage:    &types.UsageData{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	})

	// Give the goroutine time to POST.
	time.Sleep(80 * time.Millisecond)

	bodies := captured.all()
	if len(bodies) == 0 {
		t.Fatal("expected OTLP collector to receive a span")
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(bodies[0], &payload); err != nil {
		t.Fatalf("invalid JSON: %v — raw: %s", err, bodies[0])
	}

	rs, ok := payload["resourceSpans"].([]interface{})
	if !ok || len(rs) == 0 {
		t.Fatalf("expected resourceSpans, got: %v", payload)
	}

	// Verify service.name attribute.
	rsMap := rs[0].(map[string]interface{})
	resource := rsMap["resource"].(map[string]interface{})
	resAttrs := resource["attributes"].([]interface{})
	found := false
	for _, a := range resAttrs {
		attr := a.(map[string]interface{})
		if attr["key"] == "service.name" {
			val := attr["value"].(map[string]interface{})
			if val["stringValue"] == "test-service" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected service.name = test-service in resource attributes")
	}

	// Verify span contains model attribute.
	scopeSpans := rsMap["scopeSpans"].([]interface{})
	spans := scopeSpans[0].(map[string]interface{})["spans"].([]interface{})
	span := spans[0].(map[string]interface{})
	attrs := span["attributes"].([]interface{})
	modelFound := false
	for _, a := range attrs {
		attr := a.(map[string]interface{})
		if attr["key"] == "llm.model" {
			modelFound = true
		}
	}
	if !modelFound {
		t.Error("expected llm.model attribute in span")
	}
}

func TestOTELHandlerEmitsErrorStatus(t *testing.T) {
	srv, captured := captureServer(t)
	h := OTELHandler(srv.URL, "svc", nil)
	ctx := WithTraceID(context.Background(), "deadbeef0011223344556677aabbccdd")

	h(ctx, Event{Type: EventRequest, Provider: "openai", Model: "gpt-4o"})
	h(ctx, Event{
		Type:  EventError,
		Model: "gpt-4o",
		Error: context.DeadlineExceeded,
	})

	time.Sleep(80 * time.Millisecond)

	bodies := captured.all()
	if len(bodies) == 0 {
		t.Fatal("expected span on error event")
	}
	var payload map[string]interface{}
	_ = json.Unmarshal(bodies[0], &payload)
	rs := payload["resourceSpans"].([]interface{})
	rsMap := rs[0].(map[string]interface{})
	spans := rsMap["scopeSpans"].([]interface{})[0].(map[string]interface{})["spans"].([]interface{})
	span := spans[0].(map[string]interface{})
	status := span["status"].(map[string]interface{})
	if status["code"] != "STATUS_CODE_ERROR" {
		t.Errorf("expected STATUS_CODE_ERROR, got %v", status["code"])
	}
}

func TestOTELHandlerSyntheticSpanWhenNoRequest(t *testing.T) {
	srv, captured := captureServer(t)
	h := OTELHandler(srv.URL, "svc", nil)

	// Fire response without a prior request event — should still emit a span.
	h(context.Background(), Event{
		Type:     EventResponse,
		Provider: "anthropic",
		Model:    "claude-3",
		Response: &types.Response{Content: "ok"},
		Duration: 100 * time.Millisecond,
	})

	time.Sleep(80 * time.Millisecond)

	if len(captured.all()) == 0 {
		t.Fatal("expected synthetic span to be emitted")
	}
}

func TestWithTraceID(t *testing.T) {
	ctx := WithTraceID(context.Background(), "my-trace-id")
	id := traceIDFromContext(ctx)
	if id != "my-trace-id" {
		t.Errorf("expected my-trace-id, got %q", id)
	}
}

func TestRandomHexLength(t *testing.T) {
	h := randomHex(8)
	if len(h) != 16 {
		t.Errorf("expected 16 hex chars for 8 bytes, got %d", len(h))
	}
}
