package caching

import (
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

func diskResp(s string) *types.Response { return &types.Response{Content: s} }

func TestDiskSetAndGet(t *testing.T) {
	c, err := NewDiskCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskCache: %v", err)
	}
	c.Set("k", diskResp("hello"), 0)
	got, ok := c.Get("k")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Content != "hello" {
		t.Fatalf("unexpected content: %q", got.Content)
	}
}

func TestDiskMiss(t *testing.T) {
	c, _ := NewDiskCache(t.TempDir())
	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestDiskDelete(t *testing.T) {
	c, _ := NewDiskCache(t.TempDir())
	c.Set("k", diskResp("v"), 0)
	c.Delete("k")
	_, ok := c.Get("k")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestDiskFlush(t *testing.T) {
	c, _ := NewDiskCache(t.TempDir())
	c.Set("a", diskResp("1"), 0)
	c.Set("b", diskResp("2"), 0)
	c.Flush()
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected miss after flush")
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected miss after flush")
	}
}

func TestDiskTTLExpiry(t *testing.T) {
	c, _ := NewDiskCache(t.TempDir())
	c.Set("k", diskResp("v"), 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	_, ok := c.Get("k")
	if ok {
		t.Fatal("expected expiry")
	}
}

func TestDiskTTLNotExpired(t *testing.T) {
	c, _ := NewDiskCache(t.TempDir())
	c.Set("k", diskResp("v"), time.Minute)
	_, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit before TTL expires")
	}
}

func TestDiskOverwrite(t *testing.T) {
	c, _ := NewDiskCache(t.TempDir())
	c.Set("k", diskResp("first"), 0)
	c.Set("k", diskResp("second"), 0)
	got, ok := c.Get("k")
	if !ok || got.Content != "second" {
		t.Fatalf("expected second value, got %v %v", ok, got)
	}
}

func TestDiskPersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	c1, _ := NewDiskCache(dir)
	c1.Set("persist", diskResp("data"), 0)

	// New cache instance pointing at the same dir should see the entry.
	c2, _ := NewDiskCache(dir)
	got, ok := c2.Get("persist")
	if !ok {
		t.Fatal("expected hit from second cache instance")
	}
	if got.Content != "data" {
		t.Fatalf("unexpected content: %q", got.Content)
	}
}
