// Package vertexai provides a base.LLM backed by the Google Vertex AI Gemini
// endpoint. Unlike llms/gemini (Google AI Studio, API-key auth), Vertex AI
// uses GCP service-account OAuth2 auth and a project/region-scoped endpoint.
// The generateContent/streamGenerateContent wire format is identical to AI
// Studio's, so request/response transformation is reused from
// llms/gemini/chat.
package vertexai

import (
	"bytes"
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
	"sync"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/gemini/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const defaultModel = "gemini-2.0-flash"

// Provider calls the Google Vertex AI generateContent API.
// Construct with New or NewFromFile; do not create the struct directly.
type Provider struct {
	model    string
	project  string
	location string

	saEmail  string
	saKey    *rsa.PrivateKey
	tokenURI string

	client     *http.Client
	apiBaseURL string // empty = derive from location; set in tests

	tokenMu  sync.Mutex
	token    string
	tokenExp time.Time
}

// serviceAccount is the subset of a GCP service-account JSON key file used
// for JWT-bearer OAuth2 authentication.
type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// New returns a Vertex AI Provider from raw service-account JSON credentials
// (the same format as GOOGLE_APPLICATION_CREDENTIALS files).
// If model is empty, "gemini-2.0-flash" is used.
func New(model, project, location string, credentialsJSON []byte) (*Provider, error) {
	if model == "" {
		model = defaultModel
	}
	var sa serviceAccount
	if err := json.Unmarshal(credentialsJSON, &sa); err != nil {
		return nil, fmt.Errorf("vertexai: parse credentials: %w", err)
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("vertexai: invalid private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("vertexai: parse private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("vertexai: expected RSA private key")
	}
	return &Provider{
		model:    model,
		project:  project,
		location: location,
		saEmail:  sa.ClientEmail,
		saKey:    rsaKey,
		tokenURI: sa.TokenURI,
		client:   &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// NewFromFile loads service-account credentials from a JSON file path
// (typically the value of GOOGLE_APPLICATION_CREDENTIALS).
func NewFromFile(model, project, location, credentialsPath string) (*Provider, error) {
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("vertexai: read credentials: %w", err)
	}
	return New(model, project, location, data)
}

// Name implements base.LLM.
func (p *Provider) Name() string { return "vertexai" }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.project == "" {
		return fmt.Errorf("vertexai: project is not set")
	}
	if p.location == "" {
		return fmt.Errorf("vertexai: location is not set")
	}
	if p.saKey == nil {
		return fmt.Errorf("vertexai: service account credentials are not set")
	}
	return nil
}

// Complete sends a blocking request to Vertex AI and returns the full response.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToGeminiRequest(req, p.model, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	raw, err := p.post(ctx, body)
	if err != nil {
		return nil, err
	}
	if len(raw.Candidates) == 0 {
		return nil, exceptions.NewProviderError(p.Name(), 0, "empty candidates in response", nil)
	}
	return chat.FromGeminiResponse(raw, p.Name(), p.model), nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToGeminiRequest(req, p.model, true)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "marshal: "+err.Error(), err)
	}

	resp, err := p.newStreamConn(ctx, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan types.Delta, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		chat.ReadSSE(ctx, p.Name(), resp.Body, ch)
	}()
	return ch, nil
}

// accessToken returns a cached OAuth2 access token, refreshing via a signed
// JWT-bearer exchange (RS256) when expired or absent. Thread-safe.
func (p *Provider) accessToken(ctx context.Context) (string, error) {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()

	if p.token != "" && time.Now().Before(p.tokenExp) {
		return p.token, nil
	}

	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claimsJSON, err := json.Marshal(map[string]interface{}{
		"iss":   p.saEmail,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   p.tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", exceptions.NewProviderError(p.Name(), 0, "marshal claims: "+err.Error(), err)
	}
	claims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	sigInput := header + "." + claims

	h := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.saKey, crypto.SHA256, h[:])
	if err != nil {
		return "", exceptions.NewProviderError(p.Name(), 0, "sign JWT: "+err.Error(), err)
	}
	jwt := sigInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwt},
	}
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(tokenReq)
	if err != nil {
		return "", exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", exceptions.NewProviderError(p.Name(), 0, "read body: "+err.Error(), err)
	}
	if resp.StatusCode != 200 {
		return "", exceptions.ClassifyHTTPError(p.Name(), resp.StatusCode, raw)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", exceptions.NewProviderError(p.Name(), 0, "parse token: "+err.Error(), err)
	}
	expiresIn := tok.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	p.token = tok.AccessToken
	p.tokenExp = now.Add(time.Duration(expiresIn) * time.Second)
	return p.token, nil
}

func (p *Provider) host() string {
	if p.apiBaseURL != "" {
		return p.apiBaseURL
	}
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com", p.location)
}

func (p *Provider) generateContentURL() string {
	return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		p.host(), p.project, p.location, p.model)
}

func (p *Provider) streamGenerateContentURL() string {
	return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:streamGenerateContent?alt=sse",
		p.host(), p.project, p.location, p.model)
}

func (p *Provider) post(ctx context.Context, body []byte) (*chat.GeminiResponse, error) {
	tok, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.generateContentURL(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "read body: "+err.Error(), err)
	}
	if resp.StatusCode != 200 {
		return nil, exceptions.ClassifyHTTPError(p.Name(), resp.StatusCode, raw)
	}

	var out chat.GeminiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "parse: "+err.Error(), err)
	}
	if out.Error != nil {
		return nil, exceptions.ClassifyHTTPError(p.Name(), out.Error.Code, []byte(out.Error.Message))
	}
	return &out, nil
}

func (p *Provider) newStreamConn(ctx context.Context, body []byte) (*http.Response, error) {
	tok, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.streamGenerateContentURL(), bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok)

	streamClient := &http.Client{Transport: p.client.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, exceptions.ClassifyHTTPError(p.Name(), resp.StatusCode, raw)
	}
	return resp, nil
}
