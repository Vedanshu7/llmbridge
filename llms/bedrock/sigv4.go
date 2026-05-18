package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	awsDateFormat     = "20060102"
	awsDateTimeFormat = "20060102T150405Z"
	awsAlgorithm      = "AWS4-HMAC-SHA256"
)

// signRequest adds AWS Signature v4 authentication headers to req.
// body must be the raw request body bytes (used to compute the payload hash).
func signRequest(req *http.Request, accessKeyID, secretKey, region, service string, body []byte, t time.Time) {
	dateTime := t.UTC().Format(awsDateTimeFormat)
	date := t.UTC().Format(awsDateFormat)

	payloadHash := sha256Hex(body)

	req.Header.Set("x-amz-date", dateTime)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// Canonical headers (sorted, lowercase).
	signedHeaders, canonicalHeaders := buildCanonicalHeaders(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req),
		canonicalQueryString(req),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := strings.Join([]string{date, region, service, "aws4_request"}, "/")

	stringToSign := strings.Join([]string{
		awsAlgorithm,
		dateTime,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(secretKey, date, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	authHeader := fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		awsAlgorithm, accessKeyID, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authHeader)
}

func buildCanonicalHeaders(req *http.Request) (signedHeaders, canonicalHeaders string) {
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte(':')
		// Use the original (mixed-case) key to look up header values.
		for _, v := range req.Header[http.CanonicalHeaderKey(k)] {
			sb.WriteString(strings.TrimSpace(v))
		}
		sb.WriteByte('\n')
	}
	return strings.Join(keys, ";"), sb.String()
}

func canonicalURI(req *http.Request) string {
	path := req.URL.Path
	if path == "" {
		return "/"
	}
	// URI-encode each path segment without double-encoding slashes.
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = uriEncode(seg)
	}
	return strings.Join(segments, "/")
}

func canonicalQueryString(req *http.Request) string {
	q := req.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, uriEncode(k)+"="+uriEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

func deriveSigningKey(secretKey, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// uriEncode percent-encodes a string per RFC 3986 (used by SigV4).
func uriEncode(s string) string {
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
