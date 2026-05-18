package caching

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

type diskEntry struct {
	Resp      *types.Response `json:"resp"`
	ExpiresAt time.Time       `json:"expires_at,omitempty"`
}

// DiskCache stores responses as JSON files in a directory.
// Each file is named by the SHA-256 hash of its cache key.
// All methods are safe for concurrent use.
type DiskCache struct {
	dir string
	mu  sync.RWMutex
}

// NewDiskCache returns a DiskCache that stores files in dir.
// The directory is created if it does not exist.
func NewDiskCache(dir string) (*DiskCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &DiskCache{dir: dir}, nil
}

// Get implements Cache.
func (c *DiskCache) Get(key string) (*types.Response, bool) {
	c.mu.RLock()
	raw, err := os.ReadFile(c.filePath(key))
	c.mu.RUnlock()
	if err != nil {
		return nil, false
	}
	var e diskEntry
	if json.Unmarshal(raw, &e) != nil {
		return nil, false
	}
	if !e.ExpiresAt.IsZero() && time.Now().After(e.ExpiresAt) {
		c.Delete(key)
		return nil, false
	}
	return e.Resp, true
}

// Set implements Cache.
func (c *DiskCache) Set(key string, resp *types.Response, ttl time.Duration) {
	e := diskEntry{Resp: resp}
	if ttl > 0 {
		e.ExpiresAt = time.Now().Add(ttl)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	path := c.filePath(key)
	tmp := path + ".tmp"
	c.mu.Lock()
	_ = os.WriteFile(tmp, raw, 0o644)
	_ = os.Rename(tmp, path)
	c.mu.Unlock()
}

// Delete implements Cache.
func (c *DiskCache) Delete(key string) {
	c.mu.Lock()
	_ = os.Remove(c.filePath(key))
	c.mu.Unlock()
}

// Flush implements Cache.
func (c *DiskCache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			_ = os.Remove(filepath.Join(c.dir, e.Name()))
		}
	}
}

func (c *DiskCache) filePath(key string) string {
	h := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(h[:])+".json")
}
