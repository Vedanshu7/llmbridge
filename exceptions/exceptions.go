// Package exceptions defines the error hierarchy for llmbridge provider failures.
// All errors embed APIError which carries provider, model, and HTTP status context.
package exceptions

import (
	"fmt"
	"strings"
)

// APIError is the base struct embedded by all concrete error types.
// It carries the provider name, model, HTTP status code, and a human-readable message.
type APIError struct {
	LLMProvider string
	Model       string
	StatusCode  int
	Message     string
	Cause       error
}

func (e *APIError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.LLMProvider, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.LLMProvider, e.Message)
}

func (e *APIError) Unwrap() error { return e.Cause }

// AuthenticationError is returned on HTTP 401/403. Do not retry without
// correcting credentials.
type AuthenticationError struct{ APIError }

// PermissionDeniedError is returned when the API key lacks permission for
// the requested resource (HTTP 403).
type PermissionDeniedError struct{ APIError }

// NotFoundError is returned when the requested model or endpoint does not
// exist (HTTP 404).
type NotFoundError struct{ APIError }

// BadRequestError is returned for malformed requests (HTTP 400).
type BadRequestError struct{ APIError }

// UnprocessableEntityError is returned for semantically invalid requests (HTTP 422).
type UnprocessableEntityError struct{ APIError }

// UnsupportedParamsError is returned when a parameter is not supported by
// the target provider or model.
type UnsupportedParamsError struct{ APIError }

// RateLimitError is returned when the provider throttles the request (HTTP 429).
// Callers may retry after an appropriate backoff; the Router does this automatically.
type RateLimitError struct{ APIError }

// ContextWindowExceededError is returned when the request exceeds the model's
// maximum context length.
type ContextWindowExceededError struct{ APIError }

// InternalServerError wraps HTTP 500 responses from the provider.
type InternalServerError struct{ APIError }

// ServiceUnavailableError wraps HTTP 503 provider responses.
type ServiceUnavailableError struct{ APIError }

// BadGatewayError wraps HTTP 502 provider responses.
type BadGatewayError struct{ APIError }

// TimeoutError is returned when the HTTP request exceeds its deadline.
// Retryable.
type TimeoutError struct{ APIError }

// APIConnectionError is returned for network-level failures (DNS, TCP, TLS).
type APIConnectionError struct{ APIError }

// APIResponseValidationError is returned when the provider returns a response
// that cannot be decoded into the expected structure.
type APIResponseValidationError struct{ APIError }

// ContentPolicyViolationError is returned when the provider's safety system
// rejects the request or response.
type ContentPolicyViolationError struct{ APIError }

// BudgetExceededError is returned when a configured spend limit is reached.
type BudgetExceededError struct {
	APIError
	Budget  float64
	Current float64
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("budget exceeded: spent $%.6f of $%.6f limit", e.Current, e.Budget)
}

// ProviderError is a catch-all for provider failures not covered by more
// specific types. Code is the HTTP status code (0 when not from HTTP).
type ProviderError struct {
	APIError
	Code int
}

func (e *ProviderError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s: HTTP %d: %s", e.LLMProvider, e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.LLMProvider, e.Message)
}

// NewAuthError constructs an AuthenticationError.
func NewAuthError(provider string, statusCode int, msg string, cause error) *AuthenticationError {
	return &AuthenticationError{APIError{LLMProvider: provider, StatusCode: statusCode, Message: msg, Cause: cause}}
}

// NewRateLimitError constructs a RateLimitError.
func NewRateLimitError(provider string, statusCode int, msg string, cause error) *RateLimitError {
	return &RateLimitError{APIError{LLMProvider: provider, StatusCode: statusCode, Message: msg, Cause: cause}}
}

// NewTimeoutError constructs a TimeoutError.
func NewTimeoutError(provider string, msg string, cause error) *TimeoutError {
	return &TimeoutError{APIError{LLMProvider: provider, Message: msg, Cause: cause}}
}

// NewProviderError constructs a ProviderError.
func NewProviderError(provider string, code int, msg string, cause error) *ProviderError {
	return &ProviderError{APIError: APIError{LLMProvider: provider, StatusCode: code, Message: msg, Cause: cause}, Code: code}
}

// NewInternalServerError constructs an InternalServerError.
func NewInternalServerError(provider string, msg string, cause error) *InternalServerError {
	return &InternalServerError{APIError{LLMProvider: provider, StatusCode: 500, Message: msg, Cause: cause}}
}

// NewContextWindowExceededError constructs a ContextWindowExceededError.
func NewContextWindowExceededError(provider string, statusCode int, msg string, cause error) *ContextWindowExceededError {
	return &ContextWindowExceededError{APIError{LLMProvider: provider, StatusCode: statusCode, Message: msg, Cause: cause}}
}

// NewContentPolicyViolationError constructs a ContentPolicyViolationError.
func NewContentPolicyViolationError(provider string, statusCode int, msg string, cause error) *ContentPolicyViolationError {
	return &ContentPolicyViolationError{APIError{LLMProvider: provider, StatusCode: statusCode, Message: msg, Cause: cause}}
}

// isContextWindowBody returns true when the response body indicates the request
// exceeded the model's context length.
func isContextWindowBody(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "context window") ||
		strings.Contains(lower, "maximum context") ||
		strings.Contains(lower, "token limit") ||
		strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "reduce the length")
}

// isContentPolicyBody returns true when the response body indicates the provider's
// safety system blocked the request.
func isContentPolicyBody(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "content_policy_violation") ||
		strings.Contains(lower, "content_filter") ||
		strings.Contains(lower, "content policy") ||
		strings.Contains(lower, "moderation") ||
		strings.Contains(lower, "safety") ||
		strings.Contains(lower, "blocked") ||
		strings.Contains(lower, "harmful")
}

// ClassifyHTTPError maps an HTTP status code to the appropriate error type.
// body is the raw response body (may be nil for streaming errors).
// It inspects the body text to distinguish context-window and content-policy errors.
func ClassifyHTTPError(provider string, statusCode int, body []byte) error {
	msg := string(body)
	switch {
	case statusCode == 401 || statusCode == 403:
		return NewAuthError(provider, statusCode, fmt.Sprintf("HTTP %d: %s", statusCode, msg), nil)
	case statusCode == 429:
		return NewRateLimitError(provider, statusCode, fmt.Sprintf("HTTP %d: %s", statusCode, msg), nil)
	case statusCode == 400 || statusCode == 422:
		if isContextWindowBody(msg) {
			return NewContextWindowExceededError(provider, statusCode, msg, nil)
		}
		if isContentPolicyBody(msg) {
			return NewContentPolicyViolationError(provider, statusCode, msg, nil)
		}
		return NewProviderError(provider, statusCode, msg, nil)
	case statusCode >= 500:
		if isContentPolicyBody(msg) {
			return NewContentPolicyViolationError(provider, statusCode, msg, nil)
		}
		return NewInternalServerError(provider, fmt.Sprintf("HTTP %d: %s", statusCode, msg), nil)
	default:
		return NewProviderError(provider, statusCode, msg, nil)
	}
}
