// Package middleware provides HTTP middleware for the llmbridge proxy server.
package middleware

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// statusRecorder wraps http.ResponseWriter to capture the status code and bytes written.
type statusRecorder struct {
	http.ResponseWriter
	status int
	size   int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.size += n
	return n, err
}

// RequestLogger returns middleware that appends one JSON line per request to w.
// Fields: time, method, path, status, latency_ms, key (last 6 chars masked), bytes_out.
func RequestLogger(w io.Writer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: rw, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			latency := time.Since(start)

			key := maskKey(extractBearer(r))
			entry := map[string]interface{}{
				"time":       start.UTC().Format(time.RFC3339Nano),
				"method":     r.Method,
				"path":       r.URL.Path,
				"status":     rec.status,
				"latency_ms": latency.Milliseconds(),
				"bytes_out":  rec.size,
			}
			if key != "" {
				entry["key"] = key
			}
			b, _ := json.Marshal(entry)
			_, _ = fmt.Fprintf(w, "%s\n", b)
		})
	}
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func maskKey(key string) string {
	if len(key) <= 6 {
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", len(key)-6) + key[len(key)-6:]
}
