package guardrails

import (
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func req(system string, msgs ...string) *types.Request {
	r := &types.Request{System: system}
	for _, m := range msgs {
		r.Messages = append(r.Messages, types.Message{Role: "user", Content: m})
	}
	return r
}

func resp(content string, tokens int) *types.Response {
	return &types.Response{
		Content: content,
		Usage:   &types.UsageData{CompletionTokens: tokens},
	}
}

// ---- MaxInputLength ----

func TestMaxInputLengthAllow(t *testing.T) {
	e := New(MaxInputLength(100))
	if err := e.CheckRequest(req("short", "message")); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestMaxInputLengthBlock(t *testing.T) {
	e := New(MaxInputLength(5))
	if err := e.CheckRequest(req("this is too long")); err == nil {
		t.Fatal("expected block")
	}
}

// ---- MaxOutputTokens ----

func TestMaxOutputTokensAllow(t *testing.T) {
	e := New(MaxOutputTokens(100))
	if err := e.CheckResponse(resp("hello", 50)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestMaxOutputTokensBlock(t *testing.T) {
	e := New(MaxOutputTokens(10))
	if err := e.CheckResponse(resp("hello", 50)); err == nil {
		t.Fatal("expected block")
	}
}

func TestMaxOutputTokensNilUsage(t *testing.T) {
	e := New(MaxOutputTokens(10))
	if err := e.CheckResponse(&types.Response{Content: "x"}); err != nil {
		t.Fatalf("nil usage should not error: %v", err)
	}
}

// ---- MaxOutputLength ----

func TestMaxOutputLengthBlock(t *testing.T) {
	e := New(MaxOutputLength(3))
	if err := e.CheckResponse(resp("toolong", 0)); err == nil {
		t.Fatal("expected block")
	}
}

// ---- BlockKeywords ----

func TestBlockKeywordsRequest(t *testing.T) {
	e := New(BlockKeywords([]string{"CONFIDENTIAL"}))
	if err := e.CheckRequest(req("", "this is confidential data")); err == nil {
		t.Fatal("expected keyword block")
	}
}

func TestBlockKeywordsCaseInsensitive(t *testing.T) {
	e := New(BlockKeywords([]string{"password"}))
	if err := e.CheckRequest(req("", "my PASSWORD is 1234")); err == nil {
		t.Fatal("expected case-insensitive block")
	}
}

func TestBlockKeywordsResponseClear(t *testing.T) {
	e := New(BlockKeywords([]string{"forbidden"}))
	if err := e.CheckResponse(resp("clean text", 0)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestBlockKeywordsResponseBlock(t *testing.T) {
	e := New(BlockKeywords([]string{"forbidden"}))
	if err := e.CheckResponse(resp("forbidden word", 0)); err == nil {
		t.Fatal("expected keyword block in response")
	}
}

// ---- BlockPIIPatterns ----

func TestBlockPIIEmail(t *testing.T) {
	e := New(BlockPIIPatterns())
	if err := e.CheckRequest(req("", "email me at test@example.com")); err == nil {
		t.Fatal("expected PII block for email")
	}
}

func TestBlockPIISSN(t *testing.T) {
	e := New(BlockPIIPatterns())
	if err := e.CheckRequest(req("", "ssn: 123-45-6789")); err == nil {
		t.Fatal("expected PII block for SSN")
	}
}

func TestBlockPIIClean(t *testing.T) {
	e := New(BlockPIIPatterns())
	if err := e.CheckRequest(req("", "hello world")); err != nil {
		t.Fatalf("unexpected PII block: %v", err)
	}
}

// ---- BlockPIIPatternsCustom ----

func TestBlockPIIPatternsCustomValid(t *testing.T) {
	r, err := BlockPIIPatternsCustom([]string{`\btest\b`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := New(r)
	if err := e.CheckRequest(req("", "this is a test")); err == nil {
		t.Fatal("expected custom pattern block")
	}
}

func TestBlockPIIPatternsCustomInvalid(t *testing.T) {
	_, err := BlockPIIPatternsCustom([]string{`[invalid`})
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

// ---- BlockRegex ----

func TestBlockRegex(t *testing.T) {
	r, err := BlockRegex(`\bsecret\b`, "contains secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := New(r)
	if err := e.CheckRequest(req("", "the secret is here")); err == nil {
		t.Fatal("expected regex block")
	}
}

func TestBlockRegexInvalid(t *testing.T) {
	_, err := BlockRegex(`[bad`, "msg")
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

// ---- Engine first-error ordering ----

func TestEngineFirstError(t *testing.T) {
	e := New(
		MaxInputLength(3),     // fires first
		MaxInputLength(10000), // would pass
	)
	err := e.CheckRequest(req("", "long enough to fail first rule"))
	if err == nil {
		t.Fatal("expected first rule to fire")
	}
	if !strings.Contains(err.Error(), "input length") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---- CheckResponse with nil ----

func TestCheckResponseNilSafe(t *testing.T) {
	e := New(BlockKeywords([]string{"x"}), MaxOutputTokens(1), BlockPIIPatterns())
	if err := e.CheckResponse(nil); err != nil {
		t.Fatalf("nil response should be safe: %v", err)
	}
}
