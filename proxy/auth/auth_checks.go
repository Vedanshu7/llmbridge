package auth

import (
	"context"
	"net/http"
	"strings"
)

// ctxKeyAPIKey is the context key for the authenticated API key.
type ctxKeyAPIKey struct{}

// APIKeyFromContext returns the API key stored in the context by RequireAuth.
func APIKeyFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyAPIKey{}).(string)
	return v
}

// RequireAuth returns an http.Handler middleware that validates the
// "Authorization: Bearer <token>" header.
//
// Tokens are accepted if they:
//  1. Match a stored API key in the store, OR
//  2. Are valid HS256 JWTs signed with the optional jwtSecret (pass nil to disable JWT auth).
func RequireAuth(store *APIKeyStore, jwtSecret ...[]byte) func(http.Handler) http.Handler {
	var secret []byte
	if len(jwtSecret) > 0 {
		secret = jwtSecret[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				writeAuthError(w, "missing Authorization header")
				return
			}
			// Try API key first.
			if _, ok := store.ValidateAPIKey(token); ok {
				ctx := context.WithValue(r.Context(), ctxKeyAPIKey{}, token)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// Try JWT if a secret is configured.
			if len(secret) > 0 {
				if _, err := Validate(token, secret); err == nil {
					ctx := context.WithValue(r.Context(), ctxKeyAPIKey{}, token)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
			writeAuthError(w, "invalid API key or token")
		})
	}
}

// RequireScope returns an http.Handler middleware that validates both the key
// and a specific scope (e.g. "admin").
func RequireScope(store *APIKeyStore, scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractBearerToken(r)
			if key == "" {
				writeAuthError(w, "missing Authorization header")
				return
			}
			if !store.HasScope(key, scope) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"message":"insufficient scope","type":"permission_denied"}}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"message":"` + msg + `","type":"authentication_error"}}`))
}
