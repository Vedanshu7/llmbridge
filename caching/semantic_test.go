package caching

import (
	"context"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

// mockEmbedder returns predetermined embeddings for testing.
type mockEmbedder struct {
	vecs map[string][]float64
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		if v, ok := m.vecs[t]; ok {
			out[i] = v
		} else {
			// Default: unit vector in first dimension.
			out[i] = []float64{1, 0, 0}
		}
	}
	return out, nil
}
func (m *mockEmbedder) Name() string { return "mock" }

var (
	vecA = []float64{1, 0, 0}         // query A
	vecB = []float64{0.99, 0.1, 0.05} // very similar to A (cos ≈ 0.994)
	vecC = []float64{0, 1, 0}         // orthogonal to A (cos = 0)
)

func newTestSemanticCache(threshold float64) *SemanticCache {
	embedder := &mockEmbedder{vecs: map[string][]float64{
		"queryA": vecA,
		"queryB": vecB,
		"queryC": vecC,
	}}
	return NewSemanticCache(NewInMemoryCache(), embedder, threshold)
}

func TestSemanticCacheExactMatch(t *testing.T) {
	sc := newTestSemanticCache(0.95)
	resp := &types.Response{Content: "answer"}
	sc.Set("queryA", resp, time.Minute)

	got, ok := sc.Get("queryA")
	if !ok {
		t.Fatal("expected cache hit on exact query")
	}
	if got.Content != resp.Content {
		t.Fatalf("content mismatch: got %q", got.Content)
	}
}

func TestSemanticCacheSimilarMatch(t *testing.T) {
	sc := newTestSemanticCache(0.95)
	resp := &types.Response{Content: "answer"}
	sc.Set("queryA", resp, time.Minute)

	// queryB is very similar to queryA (cos ≈ 0.994 > 0.95 threshold)
	got, ok := sc.Get("queryB")
	if !ok {
		t.Fatal("expected cache hit for similar query")
	}
	if got.Content != resp.Content {
		t.Fatalf("content mismatch: got %q", got.Content)
	}
}

func TestSemanticCacheThresholdMiss(t *testing.T) {
	sc := newTestSemanticCache(0.95)
	resp := &types.Response{Content: "answer"}
	sc.Set("queryA", resp, time.Minute)

	// queryC is orthogonal to queryA (cos = 0 < 0.95)
	_, ok := sc.Get("queryC")
	if ok {
		t.Fatal("expected cache miss for dissimilar query")
	}
}

func TestSemanticCacheDelete(t *testing.T) {
	sc := newTestSemanticCache(0.95)
	resp := &types.Response{Content: "answer"}
	sc.Set("queryA", resp, time.Minute)

	sc.Delete("queryA")
	_, ok := sc.Get("queryA")
	if ok {
		t.Fatal("expected cache miss after delete")
	}
}

func TestSemanticCacheFlush(t *testing.T) {
	sc := newTestSemanticCache(0.95)
	sc.Set("queryA", &types.Response{Content: "a"}, time.Minute)
	sc.Set("queryC", &types.Response{Content: "c"}, time.Minute)

	sc.Flush()
	if _, ok := sc.Get("queryA"); ok {
		t.Fatal("expected miss after flush")
	}
}

func TestSemanticCacheEmptyKey(t *testing.T) {
	sc := newTestSemanticCache(0.95)
	sc.Set("", &types.Response{Content: "x"}, time.Minute) // should be no-op
	if _, ok := sc.Get(""); ok {
		t.Fatal("empty key should never hit")
	}
}

func TestCosineSim(t *testing.T) {
	tests := []struct {
		a, b []float64
		want float64
	}{
		{[]float64{1, 0}, []float64{1, 0}, 1.0},
		{[]float64{1, 0}, []float64{0, 1}, 0.0},
		{[]float64{1, 1}, []float64{1, 1}, 1.0},
		{[]float64{}, []float64{}, 0.0},
	}
	for _, tc := range tests {
		got := cosineSim(tc.a, tc.b)
		if got < tc.want-0.001 || got > tc.want+0.001 {
			t.Errorf("cosineSim(%v, %v) = %.4f, want %.4f", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestQueryText(t *testing.T) {
	req := types.Request{
		Messages: []types.Message{
			{Role: "system", Content: "you are helpful"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "what time is it"},
		},
	}
	got := QueryText(req)
	if got != "what time is it" {
		t.Fatalf("QueryText returned %q, want last user message", got)
	}
}

func TestQueryTextFallback(t *testing.T) {
	req := types.Request{System: "sys", Messages: []types.Message{{Role: "assistant", Content: "hi"}}}
	got := QueryText(req)
	if got != "hi" {
		t.Fatalf("expected first message fallback, got %q", got)
	}
}

func TestQueryTextSystem(t *testing.T) {
	req := types.Request{System: "only system", Messages: nil}
	got := QueryText(req)
	if got != "only system" {
		t.Fatalf("expected system fallback, got %q", got)
	}
}

func TestThreshold(t *testing.T) {
	inner := NewInMemoryCache()
	e := &mockEmbedder{vecs: map[string][]float64{}}
	c := NewSemanticCache(inner, e, 0.85)
	if c.Threshold() != 0.85 {
		t.Errorf("Threshold() = %f, want 0.85", c.Threshold())
	}
}
