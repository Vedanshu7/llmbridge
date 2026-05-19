package auth

import (
	"context"
	"fmt"
	"net"
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
			if info, ok := store.ValidateAPIKey(token); ok {
				if !ipAllowed(r, info.AllowedCIDRs) {
					writeAuthError(w, "IP address not allowed for this key")
					return
				}
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

// ipAllowed returns true when cidrs is empty (allow all) or the request's
// remote address falls within one of the provided CIDR ranges.
func ipAllowed(r *http.Request, cidrs []string) bool {
	if len(cidrs) == 0 {
		return true
	}
	// Prefer X-Forwarded-For when behind a proxy; fall back to RemoteAddr.
	ipStr := r.Header.Get("X-Forwarded-For")
	if ipStr == "" {
		ipStr, _, _ = net.SplitHostPort(r.RemoteAddr)
	} else {
		// X-Forwarded-For may be a comma-separated list; use the first entry.
		if idx := strings.Index(ipStr, ","); idx >= 0 {
			ipStr = strings.TrimSpace(ipStr[:idx])
		}
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
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

// RequireRateLimit returns middleware that enforces per-key request rate limits.
// Attaches X-RateLimit-* headers to every response. Returns 429 when limited.
func RequireRateLimit(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractBearerToken(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			allowed, info := rl.Allow(key)
			if info.LimitRequests > 0 {
				w.Header().Set("X-RateLimit-Limit-Requests", fmt.Sprintf("%d", info.LimitRequests))
				w.Header().Set("X-RateLimit-Remaining-Requests", fmt.Sprintf("%d", info.RemainingRequests))
				w.Header().Set("X-RateLimit-Reset-Requests", info.ResetAt.UTC().Format("2006-01-02T15:04:05Z"))
			}
			if info.LimitTokens > 0 {
				w.Header().Set("X-RateLimit-Limit-Tokens", fmt.Sprintf("%d", info.LimitTokens))
				w.Header().Set("X-RateLimit-Remaining-Tokens", fmt.Sprintf("%d", info.RemainingTokens))
				w.Header().Set("X-RateLimit-Reset-Tokens", info.ResetAt.UTC().Format("2006-01-02T15:04:05Z"))
			}
			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
