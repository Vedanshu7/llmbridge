package caching

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/Vedanshu7/llmbridge/llms/base"
	"github.com/Vedanshu7/llmbridge/types"
)

// vectorEntry links a stored embedding to its cache key and original query text.
type vectorEntry struct {
	embedding []float64
	cacheKey  string
	query     string
}

// SemanticCache wraps any Cache with embedding-based approximate lookup.
// On Set, the query text is embedded and the vector is stored alongside the
// exact-match cache entry. On Get, the query text is embedded and compared
// against all stored vectors; if the best cosine similarity exceeds Threshold,
// the matching inner cache entry is returned.
//
// The "key" passed to Get/Set must be the raw query text (use QueryText to
// extract it from a types.Request), NOT a hash.
type SemanticCache struct {
	inner     Cache
	embedder  base.EmbedProvider
	threshold float64

	mu      sync.RWMutex
	vectors []vectorEntry
}

// NewSemanticCache creates a SemanticCache wrapping inner, using embedder to
// produce vectors and accepting matches with cosine similarity >= threshold
// (e.g. 0.95).
func NewSemanticCache(inner Cache, embedder base.EmbedProvider, threshold float64) *SemanticCache {
	return &SemanticCache{inner: inner, embedder: embedder, threshold: threshold}
}

// Get embeds queryText and returns the stored response whose embedding is
// closest to queryText if the similarity meets the threshold.
func (c *SemanticCache) Get(queryText string) (*types.Response, bool) {
	if queryText == "" {
		return nil, false
	}
	vecs, err := c.embedder.Embed(context.Background(), []string{queryText})
	if err != nil || len(vecs) == 0 {
		return nil, false
	}
	qVec := vecs[0]

	c.mu.RLock()
	best, bestKey := -1.0, ""
	for _, e := range c.vectors {
		if sim := cosineSim(qVec, e.embedding); sim > best {
			best = sim
			bestKey = e.cacheKey
		}
	}
	c.mu.RUnlock()

	if best < c.threshold || bestKey == "" {
		return nil, false
	}
	return c.inner.Get(bestKey)
}

// Set embeds queryText and stores the vector alongside the inner cache entry.
func (c *SemanticCache) Set(queryText string, resp *types.Response, ttl time.Duration) {
	if queryText == "" {
		return
	}
	vecs, err := c.embedder.Embed(context.Background(), []string{queryText})
	if err != nil || len(vecs) == 0 {
		return
	}
	cacheKey := "sem:" + hashText(queryText)
	c.inner.Set(cacheKey, resp, ttl)

	c.mu.Lock()
	c.vectors = append(c.vectors, vectorEntry{
		embedding: vecs[0],
		cacheKey:  cacheKey,
		query:     queryText,
	})
	c.mu.Unlock()
}

// Delete removes the entry matching queryText (exact query match only).
func (c *SemanticCache) Delete(queryText string) {
	cacheKey := "sem:" + hashText(queryText)
	c.inner.Delete(cacheKey)

	c.mu.Lock()
	filtered := c.vectors[:0]
	for _, e := range c.vectors {
		if e.query != queryText {
			filtered = append(filtered, e)
		}
	}
	c.vectors = filtered
	c.mu.Unlock()
}

// Flush removes all entries from both the vector index and the inner cache.
func (c *SemanticCache) Flush() {
	c.mu.Lock()
	c.vectors = nil
	c.mu.Unlock()
	c.inner.Flush()
}

// Threshold returns the configured similarity threshold.
func (c *SemanticCache) Threshold() float64 { return c.threshold }

func cosineSim(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// hashText returns a short hex string for use as an inner cache key.
func hashText(s string) string {
	// Reuse the sha256-based key generator from key.go.
	return GenerateCacheKey(types.Request{
		Messages: []types.Message{{Role: "user", Content: s}},
	})
}
