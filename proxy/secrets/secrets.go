// Package secrets provides pluggable secret loading from AWS Secrets Manager,
// GCP Secret Manager, and HashiCorp Vault — all implemented with stdlib only.
package secrets

import (
	"context"
	"fmt"
)

// Loader fetches a secret value by name from an external backend.
type Loader interface {
	Load(ctx context.Context, name string) (string, error)
}

// NewLoader returns a Loader for the given backend.
//
// Supported backends:
//   - "aws"   — AWS Secrets Manager. Required opts: "region". Credentials from
//     AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY env vars.
//   - "gcp"   — GCP Secret Manager. Required opts: "project_id". Credentials from
//     GOOGLE_APPLICATION_CREDENTIALS env var (service account JSON).
//   - "vault" — HashiCorp Vault KV v2. Required opts: "vault_addr". Token from
//     VAULT_TOKEN env var.
func NewLoader(backend string, opts map[string]string) (Loader, error) {
	switch backend {
	case "aws":
		region := opts["region"]
		if region == "" {
			return nil, fmt.Errorf("secrets: aws loader requires 'region'")
		}
		return newAWSLoader(region), nil
	case "gcp":
		project := opts["project_id"]
		if project == "" {
			return nil, fmt.Errorf("secrets: gcp loader requires 'project_id'")
		}
		return newGCPLoader(project), nil
	case "vault":
		addr := opts["vault_addr"]
		if addr == "" {
			addr = "http://127.0.0.1:8200"
		}
		return newVaultLoader(addr), nil
	default:
		return nil, fmt.Errorf("secrets: unknown backend %q (valid: aws, gcp, vault)", backend)
	}
}
