package callbacks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

func makeEvent(t EventType, provider, model string) Event {
	return Event{
		Type:     t,
		Provider: provider,
		Model:    model,
		Duration: 100 * time.Millisecond,
		Response: &types.Response{
			Usage: &types.UsageData{TotalTokens: 42},
		},
	}
}

func TestManagerRegisterAndFire(t *testing.T) {
	m := NewManager()
	var got []Event
	m.Register(func(_ context.Context, e Event) { got = append(got, e) })
	m.Fire(context.Background(), makeEvent(EventResponse, "openai", "gpt-4o"))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
}

func TestManagerMultipleHandlers(t *testing.T) {
	m := NewManager()
	count := 0
	for i := 0; i < 3; i++ {
		m.Register(func(_ context.Context, _ Event) { count++ })
	}
	m.Fire(context.Background(), makeEvent(EventRequest, "x", "y"))
	if count != 3 {
		t.Fatalf("expected 3, got %d", count)
	}
}

func TestSafeCallRecoversPanic(t *testing.T) {
	m := NewManager()
	m.Register(func(_ context.Context, _ Event) { panic("boom") })
	// Should not panic the test.
	m.Fire(context.Background(), makeEvent(EventError, "x", "y"))
}

func TestLogHandler(t *testing.T) {
	var buf bytes.Buffer
	h := LogHandler(&buf)
	h(context.Background(), makeEvent(EventResponse, "openai", "gpt-4o"))
	if !strings.Contains(buf.String(), "provider=openai") {
		t.Fatalf("log missing provider: %s", buf.String())
	}
}

func TestJSONLogHandler(t *testing.T) {
	var buf bytes.Buffer
	h := JSONLogHandler(&buf)
	h(context.Background(), makeEvent(EventResponse, "openai", "gpt-4o"))
	var rec map[string]interface{}
	if err := json.NewDecoder(&buf).Decode(&rec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if rec["provider"] != "openai" {
		t.Fatalf("wrong provider: %v", rec["provider"])
	}
	if rec["tokens"].(float64) != 42 {
		t.Fatalf("wrong tokens: %v", rec["tokens"])
	}
}

func TestWebhookHandler(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		received = buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := WebhookHandler(srv.URL, nil)
	h(context.Background(), makeEvent(EventResponse, "openai", "gpt-4o"))

	// Give the goroutine time to POST.
	time.Sleep(50 * time.Millisecond)

	if len(received) == 0 {
		t.Fatal("expected webhook to receive payload")
	}
	var rec map[string]interface{}
	if err := json.Unmarshal(received, &rec); err != nil {
		t.Fatalf("invalid JSON payload: %v — raw: %s", err, received)
	}
	if rec["provider"] != "openai" {
		t.Fatalf("wrong provider in webhook payload: %v", rec["provider"])
	}
}

func TestNoopHandler(t *testing.T) {
	h := NoopHandler()
	h(context.Background(), makeEvent(EventRequest, "x", "y")) // should not panic
}
