package bedrock

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSHA256Hex(t *testing.T) {
	// Known SHA-256 of empty string.
	got := sha256Hex([]byte{})
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Fatalf("sha256Hex(\"\") = %q, want %q", got, want)
	}
}

func TestSHA256HexKnownValue(t *testing.T) {
	got := sha256Hex([]byte("hello"))
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("sha256Hex(hello) = %q, want %q", got, want)
	}
}

func TestHmacSHA256Length(t *testing.T) {
	out := hmacSHA256([]byte("key"), []byte("data"))
	if len(out) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(out))
	}
}

func TestHmacSHA256Deterministic(t *testing.T) {
	a := hmacSHA256([]byte("key"), []byte("data"))
	b := hmacSHA256([]byte("key"), []byte("data"))
	if !bytes.Equal(a, b) {
		t.Fatal("hmacSHA256 not deterministic")
	}
}

func TestURIEncodePassthrough(t *testing.T) {
	safe := "abcABC0123-._~"
	if got := uriEncode(safe); got != safe {
		t.Fatalf("safe chars should pass through: %q", got)
	}
}

func TestURIEncodeSpecialChars(t *testing.T) {
	got := uriEncode(" /=&")
	if !strings.Contains(got, "%20") {
		t.Fatalf("expected space encoded as %%20, got %q", got)
	}
	if !strings.Contains(got, "%2F") {
		t.Fatalf("expected / encoded as %%2F, got %q", got)
	}
}

func TestCanonicalURIRoot(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/", nil)
	if got := canonicalURI(req); got != "/" {
		t.Fatalf("expected /, got %q", got)
	}
}

func TestCanonicalURIPath(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/a/b/c", nil)
	got := canonicalURI(req)
	if got != "/a/b/c" {
		t.Fatalf("expected /a/b/c, got %q", got)
	}
}

func TestCanonicalURIEmptyPath(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	req.URL.Path = ""
	if got := canonicalURI(req); got != "/" {
		t.Fatalf("expected / for empty path, got %q", got)
	}
}

func TestCanonicalQueryStringSorted(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com?z=1&a=2&m=3", nil)
	got := canonicalQueryString(req)
	if !strings.HasPrefix(got, "a=2") {
		t.Fatalf("expected query to start with a=2 (sorted), got %q", got)
	}
}

func TestCanonicalQueryStringEmpty(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/path", nil)
	if got := canonicalQueryString(req); got != "" {
		t.Fatalf("expected empty query string, got %q", got)
	}
}

func TestBuildCanonicalHeadersSorted(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://bedrock.us-east-1.amazonaws.com/", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-amz-date", "20240101T000000Z")
	signed, canonical := buildCanonicalHeaders(req)
	// signed headers should be sorted and lowercase
	parts := strings.Split(signed, ";")
	for i := 1; i < len(parts); i++ {
		if parts[i-1] >= parts[i] {
			t.Fatalf("signed headers not sorted: %s >= %s", parts[i-1], parts[i])
		}
	}
	// canonical headers must end with newline
	if !strings.HasSuffix(canonical, "\n") {
		t.Fatalf("canonical headers must end with newline: %q", canonical)
	}
}

func TestDeriveSigningKeyLength(t *testing.T) {
	key := deriveSigningKey("secret", "20240101", "us-east-1", "bedrock")
	if len(key) != 32 {
		t.Fatalf("expected 32-byte signing key, got %d", len(key))
	}
}

func TestDeriveSigningKeyDeterministic(t *testing.T) {
	a := deriveSigningKey("secret", "20240101", "us-east-1", "bedrock")
	b := deriveSigningKey("secret", "20240101", "us-east-1", "bedrock")
	if !bytes.Equal(a, b) {
		t.Fatal("signing key not deterministic")
	}
}

func TestSignRequestSetsAuthorizationHeader(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://bedrock.us-east-1.amazonaws.com/model/test/invoke", bytes.NewReader([]byte(`{}`)))
	req.URL = &url.URL{
		Scheme: "https",
		Host:   "bedrock.us-east-1.amazonaws.com",
		Path:   "/model/test/invoke",
	}
	body := []byte(`{"prompt":"hello"}`)
	signRequest(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"us-east-1", "bedrock", body, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))

	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("expected AWS4-HMAC-SHA256 authorization, got %q", auth)
	}
	if !strings.Contains(auth, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("authorization must contain access key ID")
	}
	if req.Header.Get("x-amz-date") == "" {
		t.Fatal("expected x-amz-date header")
	}
	if req.Header.Get("x-amz-content-sha256") == "" {
		t.Fatal("expected x-amz-content-sha256 header")
	}
}
