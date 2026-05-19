package llmbridge

import (
	"context"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

// ---- CompletionCost ----

func TestCompletionCostOpenAI(t *testing.T) {
	resp := &types.Response{
		Provider: "openai",
		Model:    "gpt-4o",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	cost, err := CompletionCost(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost <= 0 {
		t.Fatalf("expected positive cost, got %f", cost)
	}
}

func TestCompletionCostAnthropic(t *testing.T) {
	resp := &types.Response{
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Usage:    &types.UsageData{PromptTokens: 200, CompletionTokens: 100},
	}
	cost, err := CompletionCost(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost <= 0 {
		t.Fatalf("expected positive cost, got %f", cost)
	}
}

func TestCompletionCostNilResponse(t *testing.T) {
	_, err := CompletionCost(nil)
	if err == nil {
		t.Fatal("expected error for nil response")
	}
}

func TestCompletionCostUnknownProvider(t *testing.T) {
	resp := &types.Response{
		Provider: "unknown-provider",
		Model:    "unknown-model",
		Usage:    &types.UsageData{PromptTokens: 10, CompletionTokens: 5},
	}
	_, err := CompletionCost(resp)
	if err == nil {
		t.Fatal("expected error for unknown provider/model")
	}
}

func TestCompletionCostFallbackToModelDB(t *testing.T) {
	// Compatible providers (e.g. "groq") fall back to ModelInfoDB.
	resp := &types.Response{
		Provider: "groq",
		Model:    "llama-3.3-70b-versatile",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	cost, err := CompletionCost(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost <= 0 {
		t.Fatalf("expected positive cost from model DB, got %f", cost)
	}
}

// ---- EmbeddingCost ----

func TestEmbeddingCostKnownModel(t *testing.T) {
	cost, err := EmbeddingCost("openai", "text-embedding-3-small", 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 1000 * 0.00000002
	if cost != want {
		t.Fatalf("expected %f, got %f", want, cost)
	}
}

func TestEmbeddingCostUnknownModel(t *testing.T) {
	_, err := EmbeddingCost("openai", "no-such-model", 100)
	if err == nil {
		t.Fatal("expected error for unknown embedding model")
	}
}

// ---- Chain (middleware) ----

type stubProvider struct {
	name string
	resp *types.Response
}

func (s *stubProvider) Complete(_ context.Context, req types.Request) (*types.Response, error) {
	return s.resp, nil
}
func (s *stubProvider) Name() string              { return s.name }
func (s *stubProvider) ValidateEnvironment() error { return nil }

func TestChainPassthrough(t *testing.T) {
	inner := &stubProvider{name: "inner", resp: &types.Response{Content: "ok"}}
	p := Chain(inner)
	resp, err := p.Complete(context.Background(), types.Request{})
	if err != nil || resp.Content != "ok" {
		t.Fatalf("unexpected: err=%v content=%s", err, resp.Content)
	}
}

func TestChainMiddlewareOrder(t *testing.T) {
	var order []string
	mw := func(label string) Middleware {
		return func(ctx context.Context, req types.Request, next Handler) (*types.Response, error) {
			order = append(order, label+"-before")
			resp, err := next(ctx, req)
			order = append(order, label+"-after")
			return resp, err
		}
	}
	inner := &stubProvider{name: "inner", resp: &types.Response{Content: "ok"}}
	p := Chain(inner, mw("A"), mw("B"))
	p.Complete(context.Background(), types.Request{}) //nolint:errcheck

	// A is outermost: before-A, before-B, provider, after-B, after-A
	want := []string{"A-before", "B-before", "B-after", "A-after"}
	if len(order) != len(want) {
		t.Fatalf("expected order %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("position %d: want %s got %s", i, want[i], order[i])
		}
	}
}

func TestChainName(t *testing.T) {
	inner := &stubProvider{name: "myprovider"}
	p := Chain(inner)
	if p.Name() != "myprovider" {
		t.Fatalf("expected name myprovider, got %s", p.Name())
	}
}

// ---- GetModelInfo / ValidateModel / SanitizeRequest / ResolveModel ----

func TestGetModelInfoKnown(t *testing.T) {
	info, ok := GetModelInfo("gpt-4o")
	if !ok {
		t.Fatal("expected gpt-4o in registry")
	}
	if info.MaxTokens == 0 {
		t.Fatal("expected non-zero MaxTokens")
	}
}

func TestGetModelInfoUnknown(t *testing.T) {
	_, ok := GetModelInfo("no-such-model-xyz")
	if ok {
		t.Fatal("expected miss for unknown model")
	}
}

func TestValidateModelKnown(t *testing.T) {
	if !ValidateModel("gpt-4o") {
		t.Fatal("expected gpt-4o to be valid")
	}
}

func TestValidateModelUnknown(t *testing.T) {
	if ValidateModel("not-a-real-model") {
		t.Fatal("expected unknown model to be invalid")
	}
}

func TestSanitizeRequestTrimsWhitespace(t *testing.T) {
	req := types.Request{
		System: "  be helpful  ",
		Model:  " gpt-4o ",
		Messages: []types.Message{
			{Role: "user", Content: "  hello  "},
		},
	}
	got := SanitizeRequest(req)
	if got.System != "be helpful" {
		t.Fatalf("system not trimmed: %q", got.System)
	}
	if got.Model != "gpt-4o" {
		t.Fatalf("model not trimmed: %q", got.Model)
	}
	if got.Messages[0].Content != "hello" {
		t.Fatalf("message content not trimmed: %q", got.Messages[0].Content)
	}
}

func TestSanitizeRequestDoesNotMutate(t *testing.T) {
	req := types.Request{System: "  original  "}
	SanitizeRequest(req)
	if req.System != "  original  " {
		t.Fatal("SanitizeRequest must not mutate the original request")
	}
}

func TestResolveModelExplicit(t *testing.T) {
	req := types.Request{Model: "gpt-4o-mini"}
	if got := ResolveModel(req, "openai"); got != "gpt-4o-mini" {
		t.Fatalf("expected explicit model, got %s", got)
	}
}

func TestResolveModelDefault(t *testing.T) {
	req := types.Request{}
	got := ResolveModel(req, "openai")
	if got != DefaultModels["openai"] {
		t.Fatalf("expected default model %s, got %s", DefaultModels["openai"], got)
	}
}

func TestResolveModelUnknownProvider(t *testing.T) {
	req := types.Request{}
	if got := ResolveModel(req, "no-such-provider"); got != "" {
		t.Fatalf("expected empty string for unknown provider, got %s", got)
	}
}
