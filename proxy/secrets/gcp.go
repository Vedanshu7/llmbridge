package secrets

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type gcpLoader struct {
	projectID string
	client    *http.Client
}

func newGCPLoader(projectID string) *gcpLoader {
	return &gcpLoader{projectID: projectID, client: &http.Client{Timeout: 10 * time.Second}}
}

// Load retrieves a secret version from GCP Secret Manager.
// name should be the secret name (not the full resource path).
func (l *gcpLoader) Load(ctx context.Context, name string) (string, error) {
	token, err := l.getAccessToken(ctx)
	if err != nil {
		return "", err
	}
	resourceName := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", l.projectID, name)
	apiURL := "https://secretmanager.googleapis.com/v1/" + resourceName + ":access"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("secrets/gcp: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := l.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("secrets/gcp: request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("secrets/gcp: HTTP %d: %s", resp.StatusCode, raw)
	}
	var result struct {
		Payload struct {
			Data string `json:"data"` // base64-encoded secret value
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("secrets/gcp: parse response: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Payload.Data)
	if err != nil {
		return "", fmt.Errorf("secrets/gcp: decode payload: %w", err)
	}
	return string(decoded), nil
}

// getAccessToken exchanges a service account JSON key for an OAuth2 access token.
func (l *gcpLoader) getAccessToken(ctx context.Context) (string, error) {
	saFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if saFile == "" {
		return "", fmt.Errorf("secrets/gcp: GOOGLE_APPLICATION_CREDENTIALS is not set")
	}
	data, err := os.ReadFile(saFile)
	if err != nil {
		return "", fmt.Errorf("secrets/gcp: read credentials: %w", err)
	}
	var sa struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(data, &sa); err != nil {
		return "", fmt.Errorf("secrets/gcp: parse credentials: %w", err)
	}

	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claimsJSON, _ := json.Marshal(map[string]interface{}{
		"iss":   sa.ClientEmail,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   sa.TokenURI,
		"iat":   now,
		"exp":   now + 3600,
	})
	claims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	sigInput := header + "." + claims

	// Parse RSA private key.
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("secrets/gcp: invalid private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("secrets/gcp: parse private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("secrets/gcp: expected RSA key")
	}
	h := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, h[:])
	if err != nil {
		return "", fmt.Errorf("secrets/gcp: sign JWT: %w", err)
	}
	jwt := sigInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	// Exchange JWT for access token.
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwt},
	}
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sa.TokenURI,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("secrets/gcp: build token request: %w", err)
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	tokenResp, err := l.client.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("secrets/gcp: token request: %w", err)
	}
	defer tokenResp.Body.Close()
	raw, _ := io.ReadAll(tokenResp.Body)
	if tokenResp.StatusCode != 200 {
		return "", fmt.Errorf("secrets/gcp: token HTTP %d: %s", tokenResp.StatusCode, raw)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", fmt.Errorf("secrets/gcp: parse token: %w", err)
	}
	return tok.AccessToken, nil
}
