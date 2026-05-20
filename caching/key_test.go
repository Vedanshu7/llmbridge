package caching

import (
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestGenerateCacheKeyDeterministic(t *testing.T) {
	req := types.Request{
		System:   "sys",
		Messages: []types.Message{{Role: "user", Content: "hello"}},
		Model:    "gpt-4o",
	}
	k1 := GenerateCacheKey(req)
	k2 := GenerateCacheKey(req)
	if k1 != k2 {
		t.Fatalf("same request produced different keys: %s vs %s", k1, k2)
	}
}

func TestGenerateCacheKeyDifferentMessages(t *testing.T) {
	base := types.Request{Model: "gpt-4o", Messages: []types.Message{{Role: "user", Content: "hello"}}}
	other := types.Request{Model: "gpt-4o", Messages: []types.Message{{Role: "user", Content: "world"}}}
	if GenerateCacheKey(base) == GenerateCacheKey(other) {
		t.Fatal("different messages should produce different keys")
	}
}

func TestGenerateCacheKeyDifferentModels(t *testing.T) {
	a := types.Request{Model: "gpt-4o", Messages: []types.Message{{Role: "user", Content: "hi"}}}
	b := types.Request{Model: "gpt-4o-mini", Messages: []types.Message{{Role: "user", Content: "hi"}}}
	if GenerateCacheKey(a) == GenerateCacheKey(b) {
		t.Fatal("different models should produce different keys")
	}
}

func TestGenerateCacheKeyIgnoresStream(t *testing.T) {
	// Stream flag does not affect the response content, so it should not affect the key.
	a := types.Request{Model: "gpt-4o", Messages: []types.Message{{Role: "user", Content: "hi"}}, Stream: false}
	b := types.Request{Model: "gpt-4o", Messages: []types.Message{{Role: "user", Content: "hi"}}, Stream: true}
	if GenerateCacheKey(a) != GenerateCacheKey(b) {
		t.Fatal("stream flag should not affect cache key")
	}
}

func TestGenerateCacheKeyIsHex(t *testing.T) {
	k := GenerateCacheKey(types.Request{Model: "m", Messages: []types.Message{{Role: "user", Content: "x"}}})
	if len(k) != 64 {
		t.Fatalf("expected 64-char hex SHA-256, got %d chars: %s", len(k), k)
	}
	for _, c := range k {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Fatalf("non-hex character %q in key %s", c, k)
		}
	}
}
