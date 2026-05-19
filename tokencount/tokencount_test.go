package tokencount

import (
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestEstimateTextEmpty(t *testing.T) {
	if n := EstimateText(""); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestEstimateTextBasic(t *testing.T) {
	// "Hello" = 5 chars → ceil(5/4) = 2
	if n := EstimateText("Hello"); n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

func TestEstimateTextMultiples(t *testing.T) {
	// Exactly 8 chars = 2 tokens.
	if n := EstimateText("12345678"); n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

func TestEstimateMessages(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "Hello"},       // 4 overhead + 2 = 6
		{Role: "assistant", Content: "Hi there"}, // 4 overhead + 2 = 6
	}
	got := EstimateMessages(msgs)
	if got <= 0 {
		t.Fatal("expected positive estimate")
	}
}

func TestEstimateRequest(t *testing.T) {
	req := &types.Request{
		System:   "You are helpful.",
		Messages: []types.Message{{Role: "user", Content: "Hi"}},
	}
	n := EstimateRequest(req)
	if n <= 0 {
		t.Fatal("expected positive estimate")
	}
}

func TestEstimateRequestNil(t *testing.T) {
	if n := EstimateRequest(nil); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestEstimateResponseNil(t *testing.T) {
	if n := EstimateResponse(nil); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestEstimateResponse(t *testing.T) {
	resp := &types.Response{Content: "Some output text here."}
	n := EstimateResponse(resp)
	if n <= 0 {
		t.Fatal("expected positive estimate")
	}
}

func TestModelMaxTokensKnown(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"gpt-4o", 128000},
		{"gpt-4o-mini", 128000},
		{"gpt-3.5-turbo", 16385},
		{"claude-3-5-sonnet", 200000},
		{"claude-3-opus", 200000},
		{"gemini-1.5-pro", 1000000},
	}
	for _, c := range cases {
		if got := ModelMaxTokens(c.model); got != c.want {
			t.Errorf("ModelMaxTokens(%q) = %d, want %d", c.model, got, c.want)
		}
	}
}

func TestModelMaxTokensUnknown(t *testing.T) {
	if got := ModelMaxTokens("unknown-model-xyz"); got != 0 {
		t.Fatalf("expected 0 for unknown model, got %d", got)
	}
}

func TestRemainingTokens(t *testing.T) {
	req := &types.Request{
		System:   "short",
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}
	rem := RemainingTokens("gpt-4o", req)
	if rem <= 0 || rem >= 128000 {
		t.Fatalf("unexpected remaining: %d", rem)
	}
}

func TestRemainingTokensUnknownModel(t *testing.T) {
	if n := RemainingTokens("no-such-model", &types.Request{}); n != -1 {
		t.Fatalf("expected -1 for unknown model, got %d", n)
	}
}
