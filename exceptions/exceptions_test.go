package exceptions

import (
	"errors"
	"testing"
)

// ---- ClassifyHTTPError ----

func TestClassify401(t *testing.T) {
	err := ClassifyHTTPError("openai", 401, []byte("invalid api key"))
	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T", err)
	}
	if authErr.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", authErr.StatusCode)
	}
}

func TestClassify403(t *testing.T) {
	err := ClassifyHTTPError("anthropic", 403, []byte("forbidden"))
	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T", err)
	}
	if authErr.LLMProvider != "anthropic" {
		t.Errorf("LLMProvider = %q", authErr.LLMProvider)
	}
}

func TestClassify429(t *testing.T) {
	err := ClassifyHTTPError("openai", 429, []byte("rate limit exceeded"))
	var rlErr *RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected RateLimitError, got %T", err)
	}
	if rlErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", rlErr.StatusCode)
	}
}

func TestClassify400ContextWindow(t *testing.T) {
	bodies := []string{
		`{"error":{"code":"context_length_exceeded"}}`,
		`context window exceeded`,
		`maximum context length reached`,
		`too many tokens in your prompt`,
		`please reduce the length of messages`,
	}
	for _, body := range bodies {
		err := ClassifyHTTPError("openai", 400, []byte(body))
		var ctxErr *ContextWindowExceededError
		if !errors.As(err, &ctxErr) {
			t.Errorf("body %q: expected ContextWindowExceededError, got %T", body, err)
		}
	}
}

func TestClassify422ContextWindow(t *testing.T) {
	err := ClassifyHTTPError("openai", 422, []byte("token limit reached"))
	var ctxErr *ContextWindowExceededError
	if !errors.As(err, &ctxErr) {
		t.Fatalf("expected ContextWindowExceededError, got %T", err)
	}
}

func TestClassify400ContentPolicy(t *testing.T) {
	bodies := []string{
		`content_policy_violation`,
		`content_filter triggered`,
		`content policy blocked this request`,
		`moderation flagged`,
		`safety system blocked`,
		`this request was blocked`,
		`harmful content detected`,
	}
	for _, body := range bodies {
		err := ClassifyHTTPError("openai", 400, []byte(body))
		var cpErr *ContentPolicyViolationError
		if !errors.As(err, &cpErr) {
			t.Errorf("body %q: expected ContentPolicyViolationError, got %T", body, err)
		}
	}
}

func TestClassify400GenericBadRequest(t *testing.T) {
	err := ClassifyHTTPError("openai", 400, []byte("bad request: missing model field"))
	var provErr *ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected ProviderError, got %T", err)
	}
}

func TestClassify500InternalServer(t *testing.T) {
	err := ClassifyHTTPError("anthropic", 500, []byte("internal server error"))
	var isErr *InternalServerError
	if !errors.As(err, &isErr) {
		t.Fatalf("expected InternalServerError, got %T", err)
	}
	if isErr.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", isErr.StatusCode)
	}
}

func TestClassify503InternalServer(t *testing.T) {
	err := ClassifyHTTPError("openai", 503, []byte("service unavailable"))
	var isErr *InternalServerError
	if !errors.As(err, &isErr) {
		t.Fatalf("expected InternalServerError for 503, got %T", err)
	}
}

func TestClassify5xxContentPolicy(t *testing.T) {
	err := ClassifyHTTPError("openai", 500, []byte("safety system blocked this response"))
	var cpErr *ContentPolicyViolationError
	if !errors.As(err, &cpErr) {
		t.Fatalf("expected ContentPolicyViolationError for 500+safety body, got %T", err)
	}
}

func TestClassifyDefault(t *testing.T) {
	err := ClassifyHTTPError("openai", 302, []byte("redirect"))
	var provErr *ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected ProviderError for unexpected status, got %T", err)
	}
}

// ---- isContextWindowBody ----

func TestIsContextWindowBodyPositive(t *testing.T) {
	cases := []string{
		"context_length_exceeded",
		"CONTEXT WINDOW limit",
		"Maximum Context length exceeded",
		"Token limit reached",
		"Too many tokens",
		"Please reduce the length",
	}
	for _, c := range cases {
		if !isContextWindowBody(c) {
			t.Errorf("isContextWindowBody(%q) = false, want true", c)
		}
	}
}

func TestIsContextWindowBodyNegative(t *testing.T) {
	cases := []string{
		"bad request",
		"invalid model",
		"missing field",
	}
	for _, c := range cases {
		if isContextWindowBody(c) {
			t.Errorf("isContextWindowBody(%q) = true, want false", c)
		}
	}
}

// ---- isContentPolicyBody ----

func TestIsContentPolicyBodyPositive(t *testing.T) {
	cases := []string{
		"content_policy_violation",
		"Content_Filter triggered",
		"Content Policy blocked",
		"Moderation flagged",
		"Safety system",
		"request was Blocked",
		"Harmful content",
	}
	for _, c := range cases {
		if !isContentPolicyBody(c) {
			t.Errorf("isContentPolicyBody(%q) = false, want true", c)
		}
	}
}

func TestIsContentPolicyBodyNegative(t *testing.T) {
	cases := []string{
		"context_length_exceeded",
		"rate limit",
		"internal server error",
	}
	for _, c := range cases {
		if isContentPolicyBody(c) {
			t.Errorf("isContentPolicyBody(%q) = true, want false", c)
		}
	}
}

// ---- Error constructors & messages ----

func TestAPIErrorMessage(t *testing.T) {
	e := &APIError{LLMProvider: "openai", StatusCode: 429, Message: "too many requests"}
	want := "openai (HTTP 429): too many requests"
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
}

func TestAPIErrorNoStatus(t *testing.T) {
	e := &APIError{LLMProvider: "openai", Message: "timeout"}
	want := "openai: timeout"
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
}

func TestBudgetExceededError(t *testing.T) {
	e := &BudgetExceededError{Budget: 10.0, Current: 12.345678}
	if e.Error() == "" {
		t.Fatal("expected non-empty error string")
	}
	if e.Current != 12.345678 {
		t.Errorf("Current = %f", e.Current)
	}
}

func TestAPIErrorUnwrap(t *testing.T) {
	cause := errors.New("network error")
	e := &APIError{LLMProvider: "openai", Message: "failed", Cause: cause}
	if !errors.Is(e, cause) {
		t.Fatal("expected Unwrap to expose cause")
	}
}

func TestNewAuthError(t *testing.T) {
	err := NewAuthError("openai", 401, "bad key", nil)
	if err.LLMProvider != "openai" || err.StatusCode != 401 {
		t.Errorf("unexpected fields: %+v", err)
	}
}

func TestNewRateLimitError(t *testing.T) {
	err := NewRateLimitError("anthropic", 429, "slow down", nil)
	if err.LLMProvider != "anthropic" {
		t.Errorf("LLMProvider = %q", err.LLMProvider)
	}
}

func TestNewContextWindowExceededError(t *testing.T) {
	err := NewContextWindowExceededError("gemini", 400, "too long", nil)
	if err.StatusCode != 400 {
		t.Errorf("StatusCode = %d", err.StatusCode)
	}
}

func TestNewContentPolicyViolationError(t *testing.T) {
	err := NewContentPolicyViolationError("openai", 400, "blocked", nil)
	if err.Message != "blocked" {
		t.Errorf("Message = %q", err.Message)
	}
}

func TestProviderError(t *testing.T) {
	err := NewProviderError("groq", 418, "I'm a teapot", nil)
	if err.Code != 418 {
		t.Errorf("Code = %d", err.Code)
	}
	msg := err.Error()
	if msg == "" {
		t.Fatal("expected non-empty error string")
	}
}

func TestProviderErrorNoCode(t *testing.T) {
	err := &ProviderError{APIError: APIError{LLMProvider: "groq", Message: "unk"}}
	if err.Error() == "" {
		t.Fatal("expected non-empty error string")
	}
}
