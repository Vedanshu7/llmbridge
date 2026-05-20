// Package auth — OIDC/OAuth2 SSO for Google, GitHub, and Microsoft (Entra).
// Implemented with stdlib only: net/http, crypto/*, encoding/json.
package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OIDCProvider encapsulates the OAuth2/OIDC configuration and runtime state
// for a single identity provider.
type OIDCProvider struct {
	Name         string // "google", "github", "microsoft"
	ClientID     string
	ClientSecret string
	RedirectURL  string

	authURL     string
	tokenURL    string
	userInfoURL string
	jwksURL     string // empty for GitHub (OAuth2 only, no JWKS)

	client *http.Client

	// JWKS cache
	jwksMu  sync.RWMutex
	jwksSet *jwkSet
	jwksTTL time.Time
}

// OIDCToken holds the tokens returned from a provider token exchange.
type OIDCToken struct {
	AccessToken string
	IDToken     string // empty for GitHub
	TokenType   string
}

// OIDCUser holds the authenticated user's identity.
type OIDCUser struct {
	Email    string
	Name     string
	Sub      string // provider-specific subject identifier
	Provider string
}

// NewGoogleProvider returns an OIDCProvider for Google.
func NewGoogleProvider(clientID, clientSecret, redirectURL string) *OIDCProvider {
	return &OIDCProvider{
		Name:         "google",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		authURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		tokenURL:     "https://oauth2.googleapis.com/token",
		userInfoURL:  "https://www.googleapis.com/oauth2/v3/userinfo",
		jwksURL:      "https://www.googleapis.com/oauth2/v3/certs",
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

// NewGitHubProvider returns an OIDCProvider for GitHub (OAuth2 only).
func NewGitHubProvider(clientID, clientSecret, redirectURL string) *OIDCProvider {
	return &OIDCProvider{
		Name:         "github",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		authURL:      "https://github.com/login/oauth/authorize",
		tokenURL:     "https://github.com/login/oauth/access_token",
		userInfoURL:  "https://api.github.com/user",
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

// NewMicrosoftProvider returns an OIDCProvider for Microsoft Entra / Azure AD.
// tenantID is the Azure tenant GUID or "common".
func NewMicrosoftProvider(clientID, clientSecret, redirectURL, tenantID string) *OIDCProvider {
	if tenantID == "" {
		tenantID = "common"
	}
	base := "https://login.microsoftonline.com/" + tenantID
	return &OIDCProvider{
		Name:         "microsoft",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		authURL:      base + "/oauth2/v2.0/authorize",
		tokenURL:     base + "/oauth2/v2.0/token",
		userInfoURL:  "https://graph.microsoft.com/oidc/userinfo",
		jwksURL:      base + "/discovery/v2.0/keys",
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

// AuthURL builds the provider's authorization redirect URL with the given state token.
func (p *OIDCProvider) AuthURL(state string) string {
	params := url.Values{
		"client_id":     {p.ClientID},
		"redirect_uri":  {p.RedirectURL},
		"response_type": {"code"},
		"state":         {state},
	}
	switch p.Name {
	case "google":
		params.Set("scope", "openid email profile")
		params.Set("access_type", "offline")
	case "github":
		params.Set("scope", "user:email")
	case "microsoft":
		params.Set("scope", "openid email profile")
	}
	return p.authURL + "?" + params.Encode()
}

// Exchange swaps an authorization code for tokens.
func (p *OIDCProvider) Exchange(ctx context.Context, code string) (*OIDCToken, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.RedirectURL},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oidc/%s: build token request: %w", p.Name, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.Name == "github" {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc/%s: token exchange: %w", p.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("oidc/%s: token exchange HTTP %d: %s", p.Name, resp.StatusCode, raw)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("oidc/%s: parse token: %w", p.Name, err)
	}
	return &OIDCToken{
		AccessToken: tok.AccessToken,
		IDToken:     tok.IDToken,
		TokenType:   tok.TokenType,
	}, nil
}

// UserInfo fetches the authenticated user's profile from the provider.
func (p *OIDCProvider) UserInfo(ctx context.Context, tok *OIDCToken) (*OIDCUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.userInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc/%s: build userinfo request: %w", p.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	if p.Name == "github" {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc/%s: userinfo request: %w", p.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("oidc/%s: userinfo HTTP %d: %s", p.Name, resp.StatusCode, raw)
	}

	var profile struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Login string `json:"login"` // GitHub
	}
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, fmt.Errorf("oidc/%s: parse userinfo: %w", p.Name, err)
	}

	email := profile.Email
	name := profile.Name
	if p.Name == "github" {
		if name == "" {
			name = profile.Login
		}
		// GitHub may require a separate /user/emails call for private emails.
		if email == "" {
			email, _ = p.fetchGitHubPrimaryEmail(ctx, tok.AccessToken)
		}
	}
	return &OIDCUser{
		Email:    email,
		Name:     name,
		Sub:      profile.Sub,
		Provider: p.Name,
	}, nil
}

func (p *OIDCProvider) fetchGitHubPrimaryEmail(ctx context.Context, token string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var emails []struct {
		Email   string `json:"email"`
		Primary bool   `json:"primary"`
	}
	_ = json.Unmarshal(raw, &emails)
	for _, e := range emails {
		if e.Primary {
			return e.Email, nil
		}
	}
	return "", nil
}

// ---- JWKS verification ----

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
}

func (p *OIDCProvider) fetchJWKS(ctx context.Context) (*jwkSet, error) {
	p.jwksMu.RLock()
	if p.jwksSet != nil && time.Now().Before(p.jwksTTL) {
		set := p.jwksSet
		p.jwksMu.RUnlock()
		return set, nil
	}
	p.jwksMu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var set jwkSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, err
	}
	p.jwksMu.Lock()
	p.jwksSet = &set
	p.jwksTTL = time.Now().Add(1 * time.Hour)
	p.jwksMu.Unlock()
	return &set, nil
}

// VerifyIDToken validates the id_token's signature using the provider's JWKS.
// Returns the parsed claims or an error.
func (p *OIDCProvider) VerifyIDToken(ctx context.Context, idToken string) (map[string]interface{}, error) {
	if p.jwksURL == "" {
		return nil, fmt.Errorf("oidc/%s: provider does not support JWKS verification", p.Name)
	}
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("oidc: invalid id_token format")
	}

	// Decode header to get kid.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode header: %w", err)
	}
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("oidc: parse header: %w", err)
	}

	set, err := p.fetchJWKS(ctx)
	if err != nil {
		return nil, fmt.Errorf("oidc: fetch JWKS: %w", err)
	}

	var key *jwk
	for i := range set.Keys {
		if set.Keys[i].Kid == header.Kid {
			key = &set.Keys[i]
			break
		}
	}
	if key == nil {
		return nil, fmt.Errorf("oidc: no key found for kid %q", header.Kid)
	}

	// Build RSA public key from JWK n and e.
	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("oidc: decode JWK n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("oidc: decode JWK e: %w", err)
	}
	pub := &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}

	// Verify signature.
	sigInput := []byte(parts[0] + "." + parts[1])
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode signature: %w", err)
	}
	var hashed []byte
	var hashAlg crypto.Hash
	switch header.Alg {
	case "RS256":
		h := sha256.Sum256(sigInput)
		hashed = h[:]
		hashAlg = crypto.SHA256
	case "RS384":
		h := sha512.Sum384(sigInput)
		hashed = h[:]
		hashAlg = crypto.SHA384
	case "RS512":
		h := sha512.Sum512(sigInput)
		hashed = h[:]
		hashAlg = crypto.SHA512
	default:
		return nil, fmt.Errorf("oidc: unsupported algorithm %q", header.Alg)
	}
	if err := rsa.VerifyPKCS1v15(pub, hashAlg, hashed, sig); err != nil {
		return nil, fmt.Errorf("oidc: invalid signature: %w", err)
	}

	// Decode claims.
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode claims: %w", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("oidc: parse claims: %w", err)
	}
	return claims, nil
}

// ---- State management for CSRF protection ----

// OIDCStateStore is a short-lived in-memory map of state tokens → provider name.
type OIDCStateStore struct {
	mu     sync.Mutex
	states map[string]stateEntry
}

type stateEntry struct {
	provider string
	expiry   time.Time
}

// NewOIDCStateStore returns an empty state store.
func NewOIDCStateStore() *OIDCStateStore {
	s := &OIDCStateStore{states: make(map[string]stateEntry)}
	go s.purgeLoop()
	return s
}

// Issue creates and stores a new random state token for the given provider.
func (s *OIDCStateStore) Issue(provider string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.states[tok] = stateEntry{provider: provider, expiry: time.Now().Add(10 * time.Minute)}
	s.mu.Unlock()
	return tok, nil
}

// Consume validates and removes a state token, returning the provider name.
func (s *OIDCStateStore) Consume(state string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.states[state]
	if !ok || time.Now().After(entry.expiry) {
		delete(s.states, state)
		return "", false
	}
	delete(s.states, state)
	return entry.provider, true
}

func (s *OIDCStateStore) purgeLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for k, v := range s.states {
			if now.After(v.expiry) {
				delete(s.states, k)
			}
		}
		s.mu.Unlock()
	}
}

// sha256Unused suppresses unused import warning for crypto/sha256.
var _ = sha256.Sum256
