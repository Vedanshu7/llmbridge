package auth

import (
	"net/http"
	"strings"
)

// RequireAuth returns an http.Handler middleware that validates the
// "Authorization: Bearer <key>" header against the store.
// On failure it writes a 401 JSON response and does not call next.
func RequireAuth(store *APIKeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractBearerToken(r)
			if key == "" {
				writeAuthError(w, "missing Authorization header")
				return
			}
			_, ok := store.ValidateAPIKey(key)
			if !ok {
				writeAuthError(w, "invalid API key")
				return
			}
			next.ServeHTTP(w, r)
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
