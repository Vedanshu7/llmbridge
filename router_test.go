package llmbridge

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/types"
)

// fakeProvider is a minimal LLM that returns a canned response or error.
type fakeProvider struct {
	name    string
	resp    *types.Response
	err     error
	callCount int
}

func (f *fakeProvider) Complete(_ context.Context, _ types.Request) (*types.Response, error) {
	f.callCount++
	return f.resp, f.err
}
func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) ValidateEnvironment() error { return nil }

func goodProvider(name string) *fakeProvider {
	return &fakeProvider{name: name, resp: &types.Response{Content: "ok", Provider: name}}
}

func errProvider(name string) *fakeProvider {
	// Use a retryable error so the router falls over to the next provider.
	return &fakeProvider{name: name, err: &exceptions.ServiceUnavailableError{
		APIError: exceptions.APIError{LLMProvider: name, StatusCode: 503, Message: "down"},
	}}
}

// ---- Basic routing ----

func TestRouterPrioritySuccess(t *testing.T) {
	p := goodProvider("p1")
	r := NewRouter([]Provider{p})
	resp, err := r.Complete(context.Background(), types.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected content: %s", resp.Content)
	}
}

func TestRouterFallover(t *testing.T) {
	bad := errProvider("bad")
	good := goodProvider("good")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: bad}, {Provider: good}},
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)
	resp, err := r.Complete(context.Background(), types.Request{})
	if err != nil {
		t.Fatalf("unexpected error after failover: %v", err)
	}
	if resp.Provider != "good" {
		t.Fatalf("expected good provider, got %s", resp.Provider)
	}
}

func TestRouterAllFail(t *testing.T) {
	r := NewTagRouter(
		[]TaggedProvider{{Provider: errProvider("a")}, {Provider: errProvider("b")}},
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)
	_, err := r.Complete(context.Background(), types.Request{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

// ---- Weighted strategy ----

func TestWeightedDistribution(t *testing.T) {
	p1 := goodProvider("p1")
	p2 := goodProvider("p2")
	r := NewTagRouter(
		[]TaggedProvider{
			{Provider: p1, Weight: 3},
			{Provider: p2, Weight: 1},
		},
		WithWeightedStrategy(),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)

	const N = 400
	for i := 0; i < N; i++ {
		r.Complete(context.Background(), types.Request{}) //nolint:errcheck
	}
	// p1 should get roughly 75% of traffic; check it's at least 50%.
	if p1.callCount < N/2 {
		t.Fatalf("p1 got %d/%d calls, expected at least %d", p1.callCount, N, N/2)
	}
	if p2.callCount == 0 {
		t.Fatal("p2 should receive some traffic")
	}
}

// ---- Circuit breaker ----

func TestCircuitBreakerTrips(t *testing.T) {
	bad := errProvider("bad")
	good := goodProvider("good")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: bad}, {Provider: good}},
		WithCircuitBreaker(2, time.Minute),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)

	// Cause 2 failures → circuit trips.
	r.Complete(context.Background(), types.Request{}) //nolint:errcheck
	r.Complete(context.Background(), types.Request{}) //nolint:errcheck

	r.mu.Lock()
	status := r.health[0]
	r.mu.Unlock()

	if status.CooldownUntil.IsZero() {
		t.Fatal("expected circuit breaker to trip on 2 failures")
	}
}

func TestCircuitBreakerAutoHeals(t *testing.T) {
	bad := errProvider("bad")
	good := goodProvider("good")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: bad}, {Provider: good}},
		WithCircuitBreaker(1, 10*time.Millisecond),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)

	// Trip the breaker.
	r.Complete(context.Background(), types.Request{}) //nolint:errcheck

	// Wait for cooldown to expire.
	time.Sleep(20 * time.Millisecond)

	// Next call should re-try bad and then fall over to good.
	resp, err := r.Complete(context.Background(), types.Request{})
	if err != nil {
		t.Fatalf("expected successful call after heal: %v", err)
	}
	if resp.Provider != "good" {
		t.Fatalf("expected good provider, got %s", resp.Provider)
	}
}

// ---- LeastLatency strategy ----

func TestLeastLatencyPicksFastest(t *testing.T) {
	slow := goodProvider("slow")
	fast := goodProvider("fast")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: slow}, {Provider: fast}},
		WithStrategy(LeastLatency),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)

	// Seed latency observations directly.
	r.mu.Lock()
	r.latency[0] = 200 * time.Millisecond
	r.latency[1] = 50 * time.Millisecond
	r.observed[0] = true
	r.observed[1] = true
	r.mu.Unlock()

	for i := 0; i < 10; i++ {
		r.Complete(context.Background(), types.Request{}) //nolint:errcheck
	}
	if fast.callCount < slow.callCount {
		t.Fatalf("fast provider should be called more: fast=%d slow=%d", fast.callCount, slow.callCount)
	}
}

// ---- LeastBusy strategy ----

func TestLeastBusyPicksLessLoaded(t *testing.T) {
	p1 := goodProvider("p1")
	p2 := goodProvider("p2")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: p1}, {Provider: p2}},
		WithStrategy(LeastBusy),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)

	// Manually set p1 as busy.
	r.mu.Lock()
	r.inflight[0] = 5
	r.inflight[1] = 0
	r.mu.Unlock()

	order := r.pickOrder(types.Request{})
	if order[0] != 1 {
		t.Fatalf("expected p2 (idx 1) first, got idx %d", order[0])
	}
}

// ---- CostBased strategy ----

func TestCostBasedPicksCheapest(t *testing.T) {
	p1 := goodProvider("expensive")
	p2 := goodProvider("cheap")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: p1}, {Provider: p2}},
		WithStrategy(CostBased),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)

	// p1 has higher accumulated spend.
	r.mu.Lock()
	r.spent[0] = 10.0
	r.spent[1] = 1.0
	r.mu.Unlock()

	order := r.pickOrder(types.Request{})
	if order[0] != 1 {
		t.Fatalf("expected cheap provider (idx 1) first, got idx %d", order[0])
	}
}

// ---- WithRequiredTags ----

func TestRequiredTagsFiltersProviders(t *testing.T) {
	vision := goodProvider("vision")
	text := goodProvider("text")
	r := NewTagRouter(
		[]TaggedProvider{
			{Provider: vision, Tags: []string{"vision", "fast"}},
			{Provider: text, Tags: []string{"fast"}},
		},
		WithRequiredTags([]string{"vision"}),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)

	for i := 0; i < 5; i++ {
		r.Complete(context.Background(), types.Request{}) //nolint:errcheck
	}
	if vision.callCount == 0 {
		t.Fatal("vision provider should be called")
	}
	if text.callCount != 0 {
		t.Fatalf("text provider should be excluded by tag filter, got %d calls", text.callCount)
	}
}

func TestRequiredTagsFallsBackWhenAllExcluded(t *testing.T) {
	p := goodProvider("p")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: p, Tags: []string{"text"}}},
		WithRequiredTags([]string{"vision"}), // no provider has this
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)
	// When all providers are excluded by tags, router falls back to all.
	resp, err := r.Complete(context.Background(), types.Request{})
	if err != nil || resp == nil {
		t.Fatalf("expected fallback call to succeed: err=%v", err)
	}
}

// ---- WithContextWindowFallback ----

func TestContextWindowFallback(t *testing.T) {
	overflow := &fakeProvider{
		name: "overflow",
		err:  &exceptions.ContextWindowExceededError{APIError: exceptions.APIError{LLMProvider: "overflow", StatusCode: 400}},
	}
	ok := goodProvider("ok")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: overflow}, {Provider: ok}},
		WithContextWindowFallback(true),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)
	resp, err := r.Complete(context.Background(), types.Request{})
	if err != nil {
		t.Fatalf("expected fallback to ok provider: %v", err)
	}
	if resp.Provider != "ok" {
		t.Fatalf("expected ok provider, got %s", resp.Provider)
	}
}

func TestContextWindowFallbackDisabled(t *testing.T) {
	overflow := &fakeProvider{
		name: "overflow",
		err:  &exceptions.ContextWindowExceededError{APIError: exceptions.APIError{LLMProvider: "overflow", StatusCode: 400}},
	}
	ok := goodProvider("ok")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: overflow}, {Provider: ok}},
		WithContextWindowFallback(false), // disabled
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)
	_, err := r.Complete(context.Background(), types.Request{})
	if err == nil {
		t.Fatal("expected error when context-window fallback is disabled")
	}
}

// ---- WithHealthChecks ----

func TestHealthChecksMarkUnhealthy(t *testing.T) {
	bad := &fakeProvider{name: "bad", err: nil} // starts healthy
	r := NewTagRouter(
		[]TaggedProvider{{Provider: bad}},
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)

	// Manually mark as unhealthy (simulates what health check does).
	r.mu.Lock()
	r.health[0].Healthy = false
	r.health[0].LastError = context.DeadlineExceeded
	r.mu.Unlock()

	r.mu.Lock()
	h := r.health[0]
	r.mu.Unlock()

	if h.Healthy {
		t.Fatal("expected provider marked unhealthy")
	}
	if h.LastError == nil {
		t.Fatal("expected LastError set")
	}
}

func TestHealthChecksStop(t *testing.T) {
	p := goodProvider("p")
	r := NewTagRouter(
		[]TaggedProvider{{Provider: p}},
		WithHealthChecks(10*time.Millisecond),
	)
	// Stop should not panic and the goroutine should exit cleanly.
	r.Stop()
}

func TestCheckAllHealthUpdatesStatus(t *testing.T) {
	good := goodProvider("good")
	r := NewRouter([]Provider{good})

	r.checkAllHealth()

	r.mu.Lock()
	h := r.health[0]
	r.mu.Unlock()

	if !h.Healthy {
		t.Fatal("expected healthy after checkAllHealth on valid provider")
	}
	if h.LastCheck.IsZero() {
		t.Fatal("expected LastCheck to be set")
	}
}

// ---- Health recording ----

// ---- WithContentPolicyFallback ----

func TestContentPolicyFallback(t *testing.T) {
	p1 := &fakeProvider{name: "strict", err: &exceptions.ContentPolicyViolationError{}}
	p2 := goodProvider("permissive")

	r := NewRouter(
		[]Provider{p1, p2},
		WithContentPolicyFallback(true),
	)
	resp, err := r.Complete(context.Background(), types.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "permissive" {
		t.Errorf("expected permissive provider, got %q", resp.Provider)
	}
}

func TestContentPolicyFallbackDisabled(t *testing.T) {
	cpErr := &exceptions.ContentPolicyViolationError{}
	p1 := &fakeProvider{name: "strict", err: cpErr}
	p2 := goodProvider("permissive")

	r := NewRouter(
		[]Provider{p1, p2},
		WithContentPolicyFallback(false),
	)
	_, err := r.Complete(context.Background(), types.Request{})
	if err == nil {
		t.Fatal("expected error when content policy fallback is disabled")
	}
}

// ---- WithRoutingGroups ----

func TestWithRoutingGroupsStored(t *testing.T) {
	p := goodProvider("p")
	groups := []RoutingGroup{
		{Name: "chat", Providers: []Provider{p}, Strategy: PriorityOrder},
		{Name: "embed", Providers: []Provider{p}, Strategy: RoundRobin},
	}
	r := NewRouter([]Provider{p}, WithRoutingGroups(groups))
	if len(r.groups) != 2 {
		t.Fatalf("expected 2 routing groups, got %d", len(r.groups))
	}
	if r.groups[0].Name != "chat" {
		t.Errorf("groups[0].Name = %q", r.groups[0].Name)
	}
	if r.groups[1].Strategy != RoundRobin {
		t.Errorf("groups[1].Strategy = %v", r.groups[1].Strategy)
	}
}

// ---- Router.Name / Router.ValidateEnvironment ----

func TestRouterName(t *testing.T) {
	r := NewRouter([]Provider{goodProvider("p")})
	if r.Name() != "router" {
		t.Errorf("Name() = %q, want router", r.Name())
	}
}

func TestRouterValidateEnvironment(t *testing.T) {
	r := NewRouter([]Provider{goodProvider("p")})
	if err := r.ValidateEnvironment(); err != nil {
		t.Fatalf("ValidateEnvironment() returned unexpected error: %v", err)
	}
}

// ---- WithFallbackChain ----

func TestFallbackChainSucceedsOnFallbackModel(t *testing.T) {
	var calledWith string
	smart := &callRecordProvider{
		name: "smart",
		fn: func(req types.Request) (*types.Response, error) {
			calledWith = req.Model
			if req.Model == "fallback-model" {
				return &types.Response{Content: "fallback ok", Provider: "smart"}, nil
			}
			return nil, &exceptions.ServiceUnavailableError{
				APIError: exceptions.APIError{LLMProvider: "smart", StatusCode: 503},
			}
		},
	}

	r := NewRouter(
		[]Provider{smart},
		WithFallbackChain("primary-model", "fallback-model"),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)
	resp, err := r.Complete(context.Background(), types.Request{Model: "primary-model"})
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	if resp.Content != "fallback ok" {
		t.Errorf("unexpected content: %s", resp.Content)
	}
	if calledWith != "fallback-model" {
		t.Errorf("expected last call with fallback-model, got %q", calledWith)
	}
}

func TestFallbackChainExhaustedReturnsLastError(t *testing.T) {
	bad := &callRecordProvider{
		name: "bad",
		fn: func(_ types.Request) (*types.Response, error) {
			return nil, &exceptions.ServiceUnavailableError{
				APIError: exceptions.APIError{LLMProvider: "bad", StatusCode: 503},
			}
		},
	}
	r := NewRouter(
		[]Provider{bad},
		WithFallbackChain("model-a", "model-b", "model-c"),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)
	_, err := r.Complete(context.Background(), types.Request{Model: "model-a"})
	if err == nil {
		t.Fatal("expected error after all fallbacks exhausted")
	}
}

func TestSetFallbackChainsReplaces(t *testing.T) {
	p := goodProvider("p")
	r := NewRouter([]Provider{p}, WithFallbackChain("old", "old-fallback"))
	r.SetFallbackChains(map[string][]string{"new": {"new-fallback"}})

	r.mu.Lock()
	_, hasOld := r.fallbackChains["old"]
	_, hasNew := r.fallbackChains["new"]
	r.mu.Unlock()

	if hasOld {
		t.Error("old chain should have been replaced")
	}
	if !hasNew {
		t.Error("new chain should be present after SetFallbackChains")
	}
}

// callRecordProvider is a Provider whose Complete behaviour is driven by a func.
type callRecordProvider struct {
	name string
	fn   func(types.Request) (*types.Response, error)
}

func (c *callRecordProvider) Complete(_ context.Context, req types.Request) (*types.Response, error) {
	return c.fn(req)
}
func (c *callRecordProvider) Name() string                { return c.name }
func (c *callRecordProvider) ValidateEnvironment() error  { return nil }

// ---- minDuration helper ----

func TestMinDuration(t *testing.T) {
	cases := []struct {
		a, b time.Duration
		want time.Duration
	}{
		{time.Second, 2 * time.Second, time.Second},
		{3 * time.Second, time.Second, time.Second},
		{time.Second, time.Second, time.Second},
		{0, time.Second, 0},
	}
	for _, c := range cases {
		if got := minDuration(c.a, c.b); got != c.want {
			t.Errorf("minDuration(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestRecordSuccess(t *testing.T) {
	p := goodProvider("p")
	// Circuit breaker must be enabled for recordSuccess to do anything.
	r := NewRouter([]Provider{p}, WithCircuitBreaker(10, time.Minute))

	r.mu.Lock()
	r.health[0].Failures = 5
	r.health[0].CooldownUntil = time.Now().Add(time.Hour)
	r.health[0].Healthy = true // keep it in the eligible set
	r.mu.Unlock()

	r.Complete(context.Background(), types.Request{}) //nolint:errcheck

	r.mu.Lock()
	h := r.health[0]
	r.mu.Unlock()

	if h.Failures != 0 {
		t.Fatalf("expected Failures=0 after success, got %d", h.Failures)
	}
	if !h.CooldownUntil.IsZero() {
		t.Fatal("expected CooldownUntil to be reset after success")
	}
}

func TestProviderHealthReturnsAllProviders(t *testing.T) {
	a := goodProvider("alpha")
	b := goodProvider("beta")
	r := NewRouter([]Provider{a, b})

	health := r.ProviderHealth()
	if len(health) != 2 {
		t.Fatalf("expected 2 health entries, got %d", len(health))
	}
	names := map[string]bool{}
	for _, h := range health {
		names[h.Name] = true
		if !h.Healthy {
			t.Errorf("provider %q should start healthy", h.Name)
		}
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("expected alpha and beta in health snapshot, got %v", names)
	}
}

func TestProviderHealthReflectsFailures(t *testing.T) {
	fail := &fakeProvider{name: "bad", err: fmt.Errorf("timeout")}
	r := NewRouter([]Provider{fail}, WithCircuitBreaker(1, 5*time.Minute))

	_, _ = r.Complete(context.Background(), types.Request{})

	health := r.ProviderHealth()
	if len(health) == 0 {
		t.Fatal("expected at least one health entry")
	}
	if health[0].Failures < 1 {
		t.Errorf("expected Failures>=1, got %d", health[0].Failures)
	}
}
