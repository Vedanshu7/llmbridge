package llmbridge

import (
	"context"
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

// ---- Health recording ----

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
