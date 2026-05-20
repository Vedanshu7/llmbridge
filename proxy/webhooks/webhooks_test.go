package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/callbacks"
	"github.com/Vedanshu7/llmbridge/types"
)

func TestRegisterAndGet(t *testing.T) {
	s := NewStore()
	cfg := s.Register("org1", "https://example.com/hook", []EventFilter{FilterCompletion}, "")
	if cfg.ID == "" {
		t.Error("expected non-empty ID")
	}
	if cfg.OrgID != "org1" || cfg.URL != "https://example.com/hook" {
		t.Errorf("unexpected config: %+v", cfg)
	}

	got, ok := s.Get(cfg.ID)
	if !ok || got.ID != cfg.ID {
		t.Error("Get returned wrong result")
	}
}

func TestDefaultEventFilter(t *testing.T) {
	s := NewStore()
	cfg := s.Register("", "https://example.com/hook", nil, "")
	if len(cfg.Events) != 1 || cfg.Events[0] != FilterAll {
		t.Errorf("expected [FilterAll], got %v", cfg.Events)
	}
}

func TestDelete(t *testing.T) {
	s := NewStore()
	cfg := s.Register("", "https://example.com/hook", nil, "")
	if !s.Delete(cfg.ID) {
		t.Error("Delete returned false")
	}
	if s.Delete(cfg.ID) {
		t.Error("second Delete should return false")
	}
}

func TestList(t *testing.T) {
	s := NewStore()
	s.Register("", "https://a.com", nil, "")
	s.Register("", "https://b.com", nil, "")
	if len(s.List()) != 2 {
		t.Errorf("expected 2 webhooks, got %d", len(s.List()))
	}
}

func TestHandlerDelivery(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]interface{}
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		received = append(received, p)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewStore()
	s.SetClient(srv.Client())
	s.Register("", srv.URL, []EventFilter{FilterCompletion}, "")

	h := s.Handler()
	h(context.Background(), callbacks.Event{
		Type:     callbacks.EventResponse,
		Provider: "openai",
		Model:    "gpt-4o",
		Response: &types.Response{
			Usage: &types.UsageData{TotalTokens: 42},
		},
	})

	// Wait for async delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(received))
	}
	if received[0]["event"] != "response" {
		t.Errorf("event = %q", received[0]["event"])
	}
	if received[0]["provider"] != "openai" {
		t.Errorf("provider = %q", received[0]["provider"])
	}
}

func TestHandlerFiltersEventType(t *testing.T) {
	var mu sync.Mutex
	var count int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewStore()
	s.SetClient(srv.Client())
	// Only fire on errors, not completions.
	s.Register("", srv.URL, []EventFilter{FilterError}, "")

	h := s.Handler()
	h(context.Background(), callbacks.Event{
		Type:     callbacks.EventResponse, // should NOT fire
		Provider: "openai",
		Model:    "gpt-4o",
	})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := count
	mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 deliveries for completion event filtered to errors, got %d", n)
	}
}

func TestHandlerFiltersOrgID(t *testing.T) {
	var mu sync.Mutex
	var count int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewStore()
	s.SetClient(srv.Client())
	// Only fire for org "target".
	s.Register("target", srv.URL, nil, "")

	h := s.Handler()
	// Event from different org — should NOT fire.
	h(context.Background(), callbacks.Event{
		Type:     callbacks.EventResponse,
		Provider: "openai",
		Metadata: map[string]string{"org_id": "other"},
	})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := count
	mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 deliveries for wrong org, got %d", n)
	}
}

func TestHMACSignature(t *testing.T) {
	var mu sync.Mutex
	var sigHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sigHeader = r.Header.Get("X-LLMBridge-Signature")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewStore()
	s.SetClient(srv.Client())
	s.Register("", srv.URL, nil, "mysecret")

	h := s.Handler()
	h(context.Background(), callbacks.Event{
		Type:     callbacks.EventResponse,
		Provider: "openai",
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := sigHeader
		mu.Unlock()
		if got != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	got := sigHeader
	mu.Unlock()

	if got == "" {
		t.Fatal("expected X-LLMBridge-Signature header")
	}
	if len(got) < 7 || got[:7] != "sha256=" {
		t.Errorf("signature = %q, want sha256=...", got)
	}
}

func TestVerify(t *testing.T) {
	body := []byte(`{"event":"response"}`)
	secret := "topsecret"
	sig := "sha256=" + sign(body, secret)

	if !Verify(body, secret, sig) {
		t.Error("Verify should return true for correct signature")
	}
	if Verify(body, secret, "sha256=badhex") {
		t.Error("Verify should return false for wrong signature")
	}
	if Verify(body, "", sig) {
		t.Error("Verify should return false when secret is empty")
	}
}

func TestSpendAlert(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]interface{}
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		received = append(received, p)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewStore()
	s.SetClient(srv.Client())
	s.Register("org1", srv.URL, []EventFilter{FilterAll}, "")

	s.DeliverSpendAlert("org1", SpendThresholdPayload{
		OrgID:        "org1",
		CurrentSpend: 80.0,
		Budget:       100.0,
		PercentUsed:  80.0,
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 spend alert, got %d", len(received))
	}
	if received[0]["event"] != "spend_threshold" {
		t.Errorf("event = %q", received[0]["event"])
	}
}

// ---- HTTP handler tests ----

func TestHandleRegister(t *testing.T) {
	s := NewStore()
	body, _ := json.Marshal(map[string]interface{}{
		"url":    "https://example.com/hook",
		"org_id": "org1",
		"events": []string{"completion"},
		"secret": "sec",
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/webhooks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleRegister(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", w.Code)
	}
	var cfg Config
	json.NewDecoder(w.Body).Decode(&cfg)
	if cfg.ID == "" || cfg.URL != "https://example.com/hook" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestHandleRegisterMissingURL(t *testing.T) {
	s := NewStore()
	body, _ := json.Marshal(map[string]string{"org_id": "org1"})
	req := httptest.NewRequest(http.MethodPost, "/admin/webhooks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleRegister(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleListAndGetAndDelete(t *testing.T) {
	s := NewStore()
	cfg := s.Register("", "https://a.com", nil, "")

	// List.
	req := httptest.NewRequest(http.MethodGet, "/admin/webhooks", nil)
	w := httptest.NewRecorder()
	s.HandleList(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("list status = %d, want 200", w.Code)
	}

	// Get.
	req2 := httptest.NewRequest(http.MethodGet, "/admin/webhooks/"+cfg.ID, nil)
	req2.SetPathValue("id", cfg.ID)
	w2 := httptest.NewRecorder()
	s.HandleGet(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("get status = %d, want 200", w2.Code)
	}

	// Delete.
	req3 := httptest.NewRequest(http.MethodDelete, "/admin/webhooks/"+cfg.ID, nil)
	req3.SetPathValue("id", cfg.ID)
	w3 := httptest.NewRecorder()
	s.HandleDelete(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("delete status = %d, want 200", w3.Code)
	}

	// Get after delete.
	req4 := httptest.NewRequest(http.MethodGet, "/admin/webhooks/"+cfg.ID, nil)
	req4.SetPathValue("id", cfg.ID)
	w4 := httptest.NewRecorder()
	s.HandleGet(w4, req4)
	if w4.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", w4.Code)
	}
}
