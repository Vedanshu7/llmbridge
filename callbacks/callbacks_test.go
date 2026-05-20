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

// ---- LangfuseHandler ----

func TestLangfuseHandlerFiresOnResponse(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Basic auth header is set.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			t.Errorf("expected Basic auth, got %q", auth)
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		received = buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := LangfuseHandler("pk-test", "sk-test", srv.URL, nil)
	event := Event{
		Type:     EventResponse,
		Provider: "openai",
		Model:    "gpt-4o",
		Duration: 200 * time.Millisecond,
		Request: &types.Request{
			Messages: []types.Message{{Role: "user", Content: "hello"}},
		},
		Response: &types.Response{
			Content: "world",
			Usage:   &types.UsageData{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		},
	}
	h(context.Background(), event)

	// The handler fires in a goroutine — wait briefly.
	time.Sleep(80 * time.Millisecond)

	if len(received) == 0 {
		t.Fatal("expected Langfuse endpoint to receive payload")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(received, &payload); err != nil {
		t.Fatalf("invalid JSON: %v — raw: %s", err, received)
	}
	batch, ok := payload["batch"].([]interface{})
	if !ok || len(batch) < 2 {
		t.Fatalf("expected batch with ≥2 items, got: %v", payload["batch"])
	}
}

func TestLangfuseHandlerFiresOnError(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		received = buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := LangfuseHandler("pk", "sk", srv.URL, nil)
	h(context.Background(), Event{
		Type:     EventError,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Error:    context.DeadlineExceeded,
	})
	time.Sleep(80 * time.Millisecond)

	if len(received) == 0 {
		t.Fatal("expected Langfuse endpoint to receive error event payload")
	}
}

func TestLangfuseHandlerSkipsNonResponseEvents(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := LangfuseHandler("pk", "sk", srv.URL, nil)
	h(context.Background(), makeEvent(EventRequest, "openai", "gpt-4o"))
	time.Sleep(50 * time.Millisecond)

	if called {
		t.Fatal("Langfuse should not fire on EventRequest events")
	}
}

func TestLangfuseHandlerHTTPError(t *testing.T) {
	// Server returns 500 — handler should not panic; failure is silently dropped.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := LangfuseHandler("pk", "sk", srv.URL, nil)
	h(context.Background(), makeEvent(EventResponse, "openai", "gpt-4o"))
	time.Sleep(50 * time.Millisecond)
	// No assertion needed — just ensure no panic.
}

func TestLangfuseHandlerNilUsage(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := LangfuseHandler("pk", "sk", srv.URL, nil)
	h(context.Background(), Event{
		Type:     EventResponse,
		Provider: "openai",
		Model:    "gpt-4o",
		Response: &types.Response{Content: "ok"}, // nil Usage
	})
	time.Sleep(80 * time.Millisecond)
	if !called {
		t.Fatal("expected Langfuse endpoint to be called even with nil usage")
	}
}

// ---- Instrument / InstrumentedProvider ----

type stubLLM struct {
	name string
	resp *types.Response
	err  error
}

func (s *stubLLM) Complete(_ context.Context, _ types.Request) (*types.Response, error) {
	return s.resp, s.err
}
func (s *stubLLM) Name() string                { return s.name }
func (s *stubLLM) ValidateEnvironment() error  { return nil }

func TestInstrumentName(t *testing.T) {
	inner := &stubLLM{name: "openai"}
	ip := Instrument(inner, NewManager())
	if ip.Name() != "openai" {
		t.Errorf("Name() = %q, want openai", ip.Name())
	}
}

func TestInstrumentValidateEnvironment(t *testing.T) {
	inner := &stubLLM{name: "openai"}
	ip := Instrument(inner, NewManager())
	if err := ip.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstrumentCompleteFiresEvents(t *testing.T) {
	inner := &stubLLM{
		name: "openai",
		resp: &types.Response{Content: "instrumented", Provider: "openai"},
	}
	m := NewManager()
	var events []Event
	m.Register(func(_ context.Context, e Event) { events = append(events, e) })

	ip := Instrument(inner, m)
	resp, err := ip.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "instrumented" {
		t.Errorf("Content = %q", resp.Content)
	}
	// Expect EventRequest + EventResponse.
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != EventRequest {
		t.Errorf("events[0].Type = %v, want EventRequest", events[0].Type)
	}
	if events[1].Type != EventResponse {
		t.Errorf("events[1].Type = %v, want EventResponse", events[1].Type)
	}
}

func TestInstrumentCompleteErrorFiresErrorEvent(t *testing.T) {
	inner := &stubLLM{name: "openai", err: context.DeadlineExceeded}
	m := NewManager()
	var events []Event
	m.Register(func(_ context.Context, e Event) { events = append(events, e) })

	ip := Instrument(inner, m)
	_, err := ip.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from inner provider")
	}
	// Expect EventRequest + EventError.
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[1].Type != EventError {
		t.Errorf("events[1].Type = %v, want EventError", events[1].Type)
	}
	if events[1].Error == nil {
		t.Error("expected non-nil error in event")
	}
}
