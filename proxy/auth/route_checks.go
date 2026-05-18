package auth

// RoutePolicy maps URL path prefixes to the minimum required scope.
// The empty string means the route requires only a valid API key (no scope).
var RoutePolicy = map[string]string{
	"/v1/chat/completions": "",
	"/v1/embeddings":       "",
	"/v1/models":           "",
	"/health":              "", // public
	"/admin/":              "admin",
}

// RequiredScope returns the scope required for the given request path.
// Returns "" if only a valid key (no specific scope) is needed.
// Returns "public" if the route is completely open.
func RequiredScope(path string) string {
	// Exact match first.
	if s, ok := RoutePolicy[path]; ok {
		return s
	}
	// Prefix match.
	for prefix, scope := range RoutePolicy {
		if len(prefix) > 0 && prefix[len(prefix)-1] == '/' {
			if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
				return scope
			}
		}
	}
	return ""
}

// CheckRouteAuth returns true if the key held by info satisfies the route policy
// for path.
func CheckRouteAuth(store *APIKeyStore, key, path string) bool {
	scope := RequiredScope(path)
	if scope == "" {
		_, ok := store.ValidateAPIKey(key)
		return ok
	}
	return store.HasScope(key, scope)
}
