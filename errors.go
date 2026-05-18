package llmbridge

import "fmt"

// ErrAuth indicates an authentication or authorization failure (HTTP 401/403).
// The request should not be retried without correcting the credentials.
type ErrAuth struct {
	Provider string
	Cause    error
}

func (e *ErrAuth) Error() string {
	return fmt.Sprintf("%s: authentication failed: %v", e.Provider, e.Cause)
}
func (e *ErrAuth) Unwrap() error { return e.Cause }

// ErrRateLimit indicates the provider throttled the request (HTTP 429).
// Callers may retry after an appropriate backoff; the Router does this automatically.
type ErrRateLimit struct {
	Provider string
	Cause    error
}

func (e *ErrRateLimit) Error() string {
	return fmt.Sprintf("%s: rate limited: %v", e.Provider, e.Cause)
}
func (e *ErrRateLimit) Unwrap() error { return e.Cause }

// ErrTimeout indicates the request exceeded the HTTP deadline.
type ErrTimeout struct {
	Provider string
	Cause    error
}

func (e *ErrTimeout) Error() string {
	return fmt.Sprintf("%s: timeout: %v", e.Provider, e.Cause)
}
func (e *ErrTimeout) Unwrap() error { return e.Cause }

// ErrProvider wraps a provider-level failure not covered by the specific types above.
// Code is the HTTP status code when the error originated from an HTTP response (0 otherwise).
type ErrProvider struct {
	Provider string
	Code     int
	Cause    error
}

func (e *ErrProvider) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s: HTTP %d: %v", e.Provider, e.Code, e.Cause)
	}
	return fmt.Sprintf("%s: %v", e.Provider, e.Cause)
}
func (e *ErrProvider) Unwrap() error { return e.Cause }
