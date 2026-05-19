package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLoggerWritesJSON(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestLogger(&buf)(inner)

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	line := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(line, "{") {
		t.Fatalf("expected JSON log line, got %q", line)
	}
	var entry map[string]interface{}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("log line not valid JSON: %v\n%s", err, line)
	}
	if entry["method"] != "GET" {
		t.Fatalf("expected method=GET, got %v", entry["method"])
	}
	if entry["path"] != "/v1/chat/completions" {
		t.Fatalf("expected path=/v1/chat/completions, got %v", entry["path"])
	}
	if entry["status"] == nil {
		t.Fatal("expected status field")
	}
}

func TestRequestLoggerMasksKey(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	handler := RequestLogger(&buf)(inner)

	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer mysecretkey123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	line := buf.String()
	if strings.Contains(line, "mysecretkey123") {
		t.Fatal("full key must not appear in log")
	}
	// last 6 chars should appear
	if !strings.Contains(line, "ey123") {
		t.Fatalf("expected last chars in masked key, got: %s", line)
	}
}

func TestRequestLoggerNoKeyOmitsField(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	handler := RequestLogger(&buf)(inner)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var entry map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("parse log: %v", err)
	}
	if _, ok := entry["key"]; ok {
		t.Fatal("key field should be omitted when no Authorization header present")
	}
}

func TestRequestLoggerRecordsBytesOut(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	})
	handler := RequestLogger(&buf)(inner)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var entry map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("parse log: %v", err)
	}
	bytesOut, ok := entry["bytes_out"].(float64)
	if !ok || bytesOut != 11 {
		t.Fatalf("expected bytes_out=11, got %v", entry["bytes_out"])
	}
}

func TestExtractBearer(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	if got := extractBearer(req); got != "mytoken" {
		t.Fatalf("expected mytoken, got %q", got)
	}
}

func TestExtractBearerEmpty(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if got := extractBearer(req); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestExtractBearerNonBearer(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	if got := extractBearer(req); got != "" {
		t.Fatalf("expected empty for Basic auth, got %q", got)
	}
}

func TestMaskKeyShort(t *testing.T) {
	if got := maskKey("abc"); got != "***" {
		t.Fatalf("expected ***, got %q", got)
	}
}

func TestMaskKeyLong(t *testing.T) {
	key := "mysecretkey123"
	got := maskKey(key)
	// last 6 chars should be visible
	if !strings.HasSuffix(got, "ey123") {
		t.Fatalf("expected last 6 chars visible, got %q", got)
	}
	if strings.Contains(got, "mysec") {
		t.Fatal("prefix should be masked")
	}
}
