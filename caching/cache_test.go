package caching

import (
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

func resp(content string) *types.Response {
	return &types.Response{Content: content}
}

func TestSetAndGet(t *testing.T) {
	c := NewInMemoryCache()
	c.Set("k", resp("hello"), 0)
	got, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit")
	}
	if got.Content != "hello" {
		t.Fatalf("got %q want %q", got.Content, "hello")
	}
}

func TestMiss(t *testing.T) {
	c := NewInMemoryCache()
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestDelete(t *testing.T) {
	c := NewInMemoryCache()
	c.Set("k", resp("v"), 0)
	c.Delete("k")
	_, ok := c.Get("k")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestFlush(t *testing.T) {
	c := NewInMemoryCache()
	c.Set("a", resp("1"), 0)
	c.Set("b", resp("2"), 0)
	c.Flush()
	if c.Len() != 0 {
		t.Fatalf("expected 0 entries after flush, got %d", c.Len())
	}
}

func TestTTLExpiry(t *testing.T) {
	c := NewInMemoryCache()
	c.Set("k", resp("v"), 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	_, ok := c.Get("k")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestTTLNotExpired(t *testing.T) {
	c := NewInMemoryCache()
	c.Set("k", resp("v"), time.Minute)
	_, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit before TTL expiry")
	}
}

func TestLen(t *testing.T) {
	c := NewInMemoryCache()
	c.Set("a", resp("1"), 0)
	c.Set("b", resp("2"), 0)
	if c.Len() != 2 {
		t.Fatalf("expected 2, got %d", c.Len())
	}
}

func TestOverwrite(t *testing.T) {
	c := NewInMemoryCache()
	c.Set("k", resp("first"), 0)
	c.Set("k", resp("second"), 0)
	got, ok := c.Get("k")
	if !ok || got.Content != "second" {
		t.Fatalf("expected overwrite: got %v ok=%v", got, ok)
	}
}
