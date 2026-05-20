package secrets

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- NewLoader factory ----

func TestNewLoaderUnknownBackend(t *testing.T) {
	_, err := NewLoader("bogus", nil)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestNewLoaderAWSMissingRegion(t *testing.T) {
	_, err := NewLoader("aws", map[string]string{})
	if err == nil {
		t.Fatal("expected error when region is missing")
	}
}

func TestNewLoaderGCPMissingProject(t *testing.T) {
	_, err := NewLoader("gcp", map[string]string{})
	if err == nil {
		t.Fatal("expected error when project_id is missing")
	}
}

func TestNewLoaderVaultDefaultAddr(t *testing.T) {
	l, err := NewLoader("vault", map[string]string{})
	if err != nil {
		t.Fatalf("expected no error for vault with empty opts, got %v", err)
	}
	if l == nil {
		t.Fatal("expected non-nil loader")
	}
}

// ---- Vault loader ----

func TestVaultLoad(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "test-token" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"data": map[string]string{"value": "supersecret"},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("VAULT_TOKEN", "test-token")
	l := newVaultLoader(srv.URL)
	val, err := l.Load(context.Background(), "mypath")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if val != "supersecret" {
		t.Fatalf("got %q, want %q", val, "supersecret")
	}
}

func TestVaultLoadNoToken(t *testing.T) {
	os.Unsetenv("VAULT_TOKEN")
	l := newVaultLoader("http://localhost:8200")
	_, err := l.Load(context.Background(), "path")
	if err == nil {
		t.Fatal("expected error when VAULT_TOKEN not set")
	}
}

func TestVaultLoadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("VAULT_TOKEN", "tok")
	l := newVaultLoader(srv.URL)
	_, err := l.Load(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
}

func TestVaultLoadNoValueKey(t *testing.T) {
	// When the data map has no "value" key, all keys are returned as JSON.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"data": map[string]string{"username": "alice", "password": "pw"},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("VAULT_TOKEN", "tok")
	l := newVaultLoader(srv.URL)
	val, err := l.Load(context.Background(), "multi")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Should return JSON of all keys.
	var out map[string]string
	if err := json.Unmarshal([]byte(val), &out); err != nil {
		t.Fatalf("expected JSON result, got %q: %v", val, err)
	}
	if out["username"] != "alice" {
		t.Errorf("username = %q", out["username"])
	}
}

// ---- AWS loader ----

func TestAWSLoadMissingCreds(t *testing.T) {
	os.Unsetenv("AWS_ACCESS_KEY_ID")
	os.Unsetenv("AWS_SECRET_ACCESS_KEY")
	l := newAWSLoader("us-east-1")
	_, err := l.Load(context.Background(), "mysecret")
	if err == nil {
		t.Fatal("expected error when AWS creds not set")
	}
}

func TestAWSLoadSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify correct target header.
		if r.Header.Get("X-Amz-Target") != "secretsmanager.GetSecretValue" {
			http.Error(w, "bad target", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		json.NewEncoder(w).Encode(map[string]string{"SecretString": "aws-secret-value"})
	}))
	defer srv.Close()

	t.Setenv("AWS_ACCESS_KEY_ID", "AKIATEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "testsecretkey")

	l := &awsLoader{region: "us-east-1", client: srv.Client()}
	// Override the endpoint to point at our test server.
	// We can't easily override the URL inside Load(), so we test via a custom client
	// that redirects all requests to the test server.
	l.client = &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}

	val, err := l.Load(context.Background(), "mysecret")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if val != "aws-secret-value" {
		t.Fatalf("got %q, want %q", val, "aws-secret-value")
	}
}

func TestAWSLoadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"ResourceNotFoundException"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	t.Setenv("AWS_ACCESS_KEY_ID", "AKIATEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "testsecretkey")

	l := &awsLoader{region: "us-east-1", client: &http.Client{
		Transport: &redirectTransport{base: srv.URL},
	}}
	_, err := l.Load(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
}

// redirectTransport rewrites every request's host/scheme to a fixed base URL.
type redirectTransport struct {
	base string
}

func (t *redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	parsed, _ := r.URL.Parse(t.base)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = parsed.Scheme
	r2.URL.Host = parsed.Host
	return http.DefaultTransport.RoundTrip(r2)
}

// ---- GCP loader ----

// generateTestKey creates a 2048-bit RSA key and returns its PKCS8 PEM block.
func generateTestKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal PKCS8: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}
	return string(pem.EncodeToMemory(block))
}

// writeServiceAccountFile writes a GCP service account JSON to a temp file and returns its path.
func writeServiceAccountFile(t *testing.T, tokenURI, pemKey string) string {
	t.Helper()
	sa := map[string]string{
		"type":          "service_account",
		"client_email":  "test@test-project.iam.gserviceaccount.com",
		"private_key":   pemKey,
		"token_uri":     tokenURI,
	}
	data, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal service account: %v", err)
	}
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write service account file: %v", err)
	}
	return path
}

func TestGCPLoadNoCredentials(t *testing.T) {
	os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")
	l := newGCPLoader("my-project")
	_, err := l.Load(context.Background(), "my-secret")
	if err == nil {
		t.Fatal("expected error when GOOGLE_APPLICATION_CREDENTIALS is not set")
	}
	if !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Errorf("error should mention env var, got: %v", err)
	}
}

func TestGCPLoadFileNotFound(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/path/sa.json")
	l := newGCPLoader("my-project")
	_, err := l.Load(context.Background(), "my-secret")
	if err == nil {
		t.Fatal("expected error for missing credentials file")
	}
}

func TestGCPLoadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte("not-json{{"), 0600)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	l := newGCPLoader("my-project")
	_, err := l.Load(context.Background(), "my-secret")
	if err == nil {
		t.Fatal("expected error for invalid JSON credentials")
	}
}

func TestGCPLoadSuccess(t *testing.T) {
	secretValue := "super-secret-value"

	// Mock server handles both token exchange and secret manager API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Token exchange endpoint.
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"access_token": "test-gcp-token"}) //nolint:errcheck
			return
		}
		// Secret Manager GET — verify auth header.
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"payload": map[string]string{
				"data": base64.StdEncoding.EncodeToString([]byte(secretValue)),
			},
		})
	}))
	defer srv.Close()

	pemKey := generateTestKey(t)
	saPath := writeServiceAccountFile(t, srv.URL+"/token", pemKey)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", saPath)

	l := newGCPLoader("my-project")
	// Redirect all HTTP calls to the mock server.
	l.client = &http.Client{Transport: &redirectTransport{base: srv.URL}}

	val, err := l.Load(context.Background(), "my-secret")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if val != secretValue {
		t.Errorf("got %q, want %q", val, secretValue)
	}
}

func TestGCPLoadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"}) //nolint:errcheck
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	pemKey := generateTestKey(t)
	saPath := writeServiceAccountFile(t, srv.URL+"/token", pemKey)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", saPath)

	l := newGCPLoader("my-project")
	l.client = &http.Client{Transport: &redirectTransport{base: srv.URL}}

	_, err := l.Load(context.Background(), "missing-secret")
	if err == nil {
		t.Fatal("expected error for HTTP 404 from secret manager")
	}
}

func TestNewLoaderGCP(t *testing.T) {
	l, err := NewLoader("gcp", map[string]string{"project_id": "my-project"})
	if err != nil {
		t.Fatalf("NewLoader gcp: %v", err)
	}
	if l == nil {
		t.Fatal("expected non-nil loader")
	}
}
