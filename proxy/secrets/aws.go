package secrets

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type awsLoader struct {
	region string
	client *http.Client
}

func newAWSLoader(region string) *awsLoader {
	return &awsLoader{region: region, client: &http.Client{Timeout: 10 * time.Second}}
}

// Load retrieves a secret string from AWS Secrets Manager by name or ARN.
func (l *awsLoader) Load(ctx context.Context, name string) (string, error) {
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if accessKey == "" || secretKey == "" {
		return "", fmt.Errorf("secrets/aws: AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must be set")
	}

	endpoint := fmt.Sprintf("https://secretsmanager.%s.amazonaws.com", l.region)
	payload := map[string]string{"SecretId": name}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("secrets/aws: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")

	awsSign(req, accessKey, secretKey, l.region, "secretsmanager", body, time.Now().UTC())

	resp, err := l.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("secrets/aws: request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("secrets/aws: HTTP %d: %s", resp.StatusCode, raw)
	}
	var result struct {
		SecretString string `json:"SecretString"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("secrets/aws: parse response: %w", err)
	}
	return result.SecretString, nil
}

// awsSign applies AWS Signature v4 to req (minimal implementation for Secrets Manager).
func awsSign(req *http.Request, accessKey, secretKey, region, service string, body []byte, t time.Time) {
	const algorithm = "AWS4-HMAC-SHA256"
	dateTime := t.Format("20060102T150405Z")
	date := t.Format("20060102")

	payloadHash := hexSHA256(body)
	req.Header.Set("x-amz-date", dateTime)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if req.Header.Get("host") == "" {
		req.Header.Set("host", req.Host)
		if req.Host == "" {
			req.Header.Set("host", req.URL.Host)
		}
	}

	// Build sorted signed-headers list.
	var keys []string
	for k := range req.Header {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)

	var canonHeaders strings.Builder
	for _, k := range keys {
		canonHeaders.WriteString(k)
		canonHeaders.WriteByte(':')
		for _, v := range req.Header[http.CanonicalHeaderKey(k)] {
			canonHeaders.WriteString(strings.TrimSpace(v))
		}
		canonHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(keys, ";")

	path := req.URL.Path
	if path == "" {
		path = "/"
	}
	canonReq := strings.Join([]string{
		req.Method, path, "", canonHeaders.String(), signedHeaders, payloadHash,
	}, "\n")

	credScope := strings.Join([]string{date, region, service, "aws4_request"}, "/")
	strToSign := strings.Join([]string{algorithm, dateTime, credScope, hexSHA256([]byte(canonReq))}, "\n")

	kDate := hmacSHA256b([]byte("AWS4"+secretKey), []byte(date))
	kRegion := hmacSHA256b(kDate, []byte(region))
	kService := hmacSHA256b(kRegion, []byte(service))
	kSigning := hmacSHA256b(kService, []byte("aws4_request"))
	sig := hex.EncodeToString(hmacSHA256b(kSigning, []byte(strToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, accessKey, credScope, signedHeaders, sig,
	))
}

func hexSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256b(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
