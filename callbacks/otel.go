package callbacks

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

// OTELHandler returns a Handler that emits OTLP-compatible JSON spans to an
// OpenTelemetry collector HTTP endpoint. No external SDK is required — spans are
// built by hand and POSTed to endpoint/v1/traces.
//
// Span lifecycle:
//   - EventRequest  → creates a span and stores it in memory keyed by a trace ID
//     derived from ctx ("trace_id" value) or a fresh random ID.
//   - EventResponse / EventError → completes the span (end time, status, token
//     attributes) and POSTs it to the collector. Delivery is best-effort;
//     failures are silently dropped to avoid blocking the call path.
//
// An optional http.Client may be supplied; pass nil to use a 5-second default.
func OTELHandler(endpoint, serviceName string, client *http.Client) Handler {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	var mu sync.Mutex
	pending := make(map[string]*pendingSpan)

	return func(ctx context.Context, event Event) {
		switch event.Type {
		case EventRequest:
			traceID := traceIDFromContext(ctx)
			spanID := randomHex(8)
			mu.Lock()
			pending[traceID] = &pendingSpan{
				traceID:   traceID,
				spanID:    spanID,
				startNano: time.Now().UnixNano(),
				provider:  event.Provider,
				model:     event.Model,
			}
			mu.Unlock()

		case EventResponse, EventError:
			traceID := traceIDFromContext(ctx)
			mu.Lock()
			ps, ok := pending[traceID]
			if ok {
				delete(pending, traceID)
			}
			mu.Unlock()

			if !ok {
				// No matching request span; create a synthetic one.
				ps = &pendingSpan{
					traceID:   traceID,
					spanID:    randomHex(8),
					startNano: time.Now().UnixNano() - int64(event.Duration),
					provider:  event.Provider,
					model:     event.Model,
				}
			}

			endNano := time.Now().UnixNano()
			span := buildSpan(ps, event, serviceName, endNano)
			go sendSpan(client, endpoint, serviceName, span) //nolint:errcheck
		}
	}
}

// ---- internal types ----

type pendingSpan struct {
	traceID   string
	spanID    string
	startNano int64
	provider  string
	model     string
}

// buildSpan constructs an OTLP JSON span object.
func buildSpan(ps *pendingSpan, event Event, serviceName string, endNano int64) map[string]interface{} {
	statusCode := "STATUS_CODE_OK"
	statusMsg := ""
	if event.Type == EventError && event.Error != nil {
		statusCode = "STATUS_CODE_ERROR"
		statusMsg = event.Error.Error()
	}

	attrs := []map[string]interface{}{
		{"key": "llm.provider", "value": map[string]string{"stringValue": ps.provider}},
		{"key": "llm.model", "value": map[string]string{"stringValue": ps.model}},
	}
	if event.Response != nil && event.Response.Usage != nil {
		attrs = append(attrs,
			map[string]interface{}{"key": "llm.usage.prompt_tokens", "value": map[string]int{"intValue": event.Response.Usage.PromptTokens}},
			map[string]interface{}{"key": "llm.usage.completion_tokens", "value": map[string]int{"intValue": event.Response.Usage.CompletionTokens}},
		)
	}

	span := map[string]interface{}{
		"traceId":            ps.traceID,
		"spanId":             ps.spanID,
		"name":               fmt.Sprintf("%s/%s", ps.provider, ps.model),
		"startTimeUnixNano":  fmt.Sprintf("%d", ps.startNano),
		"endTimeUnixNano":    fmt.Sprintf("%d", endNano),
		"kind":               3, // SPAN_KIND_CLIENT
		"attributes":         attrs,
		"status": map[string]string{
			"code":    statusCode,
			"message": statusMsg,
		},
	}
	return span
}

func sendSpan(client *http.Client, endpoint, serviceName string, span map[string]interface{}) error {
	payload := map[string]interface{}{
		"resourceSpans": []map[string]interface{}{
			{
				"resource": map[string]interface{}{
					"attributes": []map[string]interface{}{
						{"key": "service.name", "value": map[string]string{"stringValue": serviceName}},
					},
				},
				"scopeSpans": []map[string]interface{}{
					{
						"spans": []interface{}{span},
					},
				},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint+"/v1/traces", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func traceIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(otelTraceIDKey{}).(string); ok && id != "" {
		return id
	}
	return randomHex(16)
}

// otelTraceIDKey is the context key for a caller-supplied trace ID.
type otelTraceIDKey struct{}

// WithTraceID returns a child context carrying id as the OTEL trace ID.
// Handlers created by OTELHandler will correlate request and response spans
// using this ID.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, otelTraceIDKey{}, id)
}

func randomHex(bytes int) string {
	b := make([]byte, bytes)
	for i := range b {
		b[i] = byte(rand.IntN(256))
	}
	return hex.EncodeToString(b)
}
