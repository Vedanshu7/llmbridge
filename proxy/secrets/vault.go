package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type vaultLoader struct {
	addr   string
	client *http.Client
}

func newVaultLoader(addr string) *vaultLoader {
	return &vaultLoader{addr: addr, client: &http.Client{Timeout: 10 * time.Second}}
}

// Load retrieves a secret from HashiCorp Vault KV v2 at the given path.
// The token is read from the VAULT_TOKEN environment variable.
func (l *vaultLoader) Load(ctx context.Context, path string) (string, error) {
	token := os.Getenv("VAULT_TOKEN")
	if token == "" {
		return "", fmt.Errorf("secrets/vault: VAULT_TOKEN is not set")
	}

	apiURL := l.addr + "/v1/secret/data/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("secrets/vault: build request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := l.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("secrets/vault: request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("secrets/vault: HTTP %d: %s", resp.StatusCode, raw)
	}

	// KV v2 response: {"data": {"data": {"value": "..."}}}
	var result struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("secrets/vault: parse response: %w", err)
	}
	// Return the "value" field by convention; callers may also use the full data map.
	if v, ok := result.Data.Data["value"]; ok {
		return v, nil
	}
	// If no "value" key, return the JSON of all keys.
	all, _ := json.Marshal(result.Data.Data)
	return string(all), nil
}
