package idempotency

import (
	"testing"
	"time"
)

func TestGetReturnsNilForMissingKey(t *testing.T) {
	s := NewStore()
	if s.Get("nonexistent") != nil {
		t.Fatal("expected nil for unknown key")
	}
}

func TestSetAndGetRoundTrip(t *testing.T) {
	s := NewStore()
	e := &Entry{
		StatusCode: 200,
		Body:       []byte(`{"ok":true}`),
		Headers:    map[string]string{"Content-Type": "application/json"},
		StoredAt:   time.Now(),
	}
	s.Set("key-1", e)

	got := s.Get("key-1")
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", got.StatusCode)
	}
	if string(got.Body) != `{"ok":true}` {
		t.Errorf("Body = %q, unexpected", got.Body)
	}
}

func TestGetReturnsNilAfterTTL(t *testing.T) {
	s := &Store{
		entries: make(map[string]*Entry),
		ttl:     50 * time.Millisecond,
	}
	s.Set("expiring", &Entry{StatusCode: 200, StoredAt: time.Now()})
	time.Sleep(100 * time.Millisecond)
	if s.Get("expiring") != nil {
		t.Fatal("expected nil after TTL expiry")
	}
}

func TestOverwriteWithSet(t *testing.T) {
	s := NewStore()
	s.Set("k", &Entry{StatusCode: 200, Body: []byte("first"), StoredAt: time.Now()})
	s.Set("k", &Entry{StatusCode: 201, Body: []byte("second"), StoredAt: time.Now()})
	e := s.Get("k")
	if e == nil || e.StatusCode != 201 {
		t.Errorf("expected second entry (201), got %+v", e)
	}
}
