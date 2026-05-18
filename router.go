package llmbridge

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

// Strategy controls how the Router picks a provider for each request.
type Strategy int

const (
	// PriorityOrder tries providers in declaration order, failing over to the
	// next provider on a retryable error (rate-limit, timeout, 5xx).
	PriorityOrder Strategy = iota

	// RoundRobin distributes requests evenly across all providers.
	RoundRobin

	// LeastLatency routes each request to the provider with the lowest
	// exponential moving-average latency observed so far. Falls back to
	// PriorityOrder until each provider has served at least one request.
	LeastLatency
)

// RetryPolicy controls per-provider retry behavior inside the Router.
// On a retryable error the Router waits InitialDelay before the first
// retry, then multiplies by Multiplier up to MaxDelay, then fails over
// to the next provider.
type RetryPolicy struct {
	// MaxAttempts is the number of tries per provider. 1 = no retry.
	MaxAttempts int

	// InitialDelay before the first retry.
	InitialDelay time.Duration

	// Multiplier applied to the delay on each subsequent retry.
	// 2.0 = classic exponential backoff.
	Multiplier float64

	// MaxDelay caps the backoff growth.
	MaxDelay time.Duration
}

// DefaultRetryPolicy is a sensible starting point: two attempts per provider
// with 1-second initial backoff doubling to at most 8 seconds.
var DefaultRetryPolicy = RetryPolicy{
	MaxAttempts:  2,
	InitialDelay: time.Second,
	Multiplier:   2.0,
	MaxDelay:     8 * time.Second,
}

// Router dispatches requests across multiple Provider instances with automatic
// failover and load balancing. It implements Provider itself, so it can be
// used everywhere a single provider is expected.
type Router struct {
	providers []Provider
	strategy  Strategy
	policy    RetryPolicy

	mu       sync.Mutex
	robin    int             // round-robin cursor
	latency  []time.Duration // per-provider EMA latency (LeastLatency)
	observed []bool          // whether each provider has been measured
}

// NewRouter returns a Router that dispatches across the given providers.
// Use With* option functions to customise strategy and retry policy.
//
//	r := llmbridge.NewRouter(
//	    openai.New("gpt-4o", key),
//	    anthropic.New("claude-sonnet-4-6", key),
//	    llmbridge.WithStrategy(llmbridge.PriorityOrder),
//	    llmbridge.WithRetryPolicy(llmbridge.DefaultRetryPolicy),
//	)
func NewRouter(providers []Provider, opts ...RouterOption) *Router {
	r := &Router{
		providers: providers,
		strategy:  PriorityOrder,
		policy:    DefaultRetryPolicy,
		latency:   make([]time.Duration, len(providers)),
		observed:  make([]bool, len(providers)),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RouterOption configures a Router.
type RouterOption func(*Router)

// WithStrategy sets the selection strategy.
func WithStrategy(s Strategy) RouterOption {
	return func(r *Router) { r.strategy = s }
}

// WithRetryPolicy sets the per-provider retry policy.
func WithRetryPolicy(p RetryPolicy) RouterOption {
	return func(r *Router) { r.policy = p }
}

// Name implements Provider. Returns "router".
func (r *Router) Name() string { return "router" }

// Complete implements Provider. Picks a starting provider according to the
// configured strategy, retries within that provider per RetryPolicy, then
// fails over to subsequent providers on retryable errors.
func (r *Router) Complete(ctx context.Context, req Request) (*Response, error) {
	order := r.pickOrder()
	var lastErr error
	for _, idx := range order {
		p := r.providers[idx]
		resp, err := r.tryWithPolicy(ctx, p, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
		// Retryable: log and try next provider.
	}
	return nil, lastErr
}

// tryWithPolicy executes one provider with the retry policy applied.
func (r *Router) tryWithPolicy(ctx context.Context, p Provider, req Request) (*Response, error) {
	policy := r.policy
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 1
	}
	delay := policy.InitialDelay
	var lastErr error

	for attempt := range policy.MaxAttempts {
		start := time.Now()
		resp, err := p.Complete(ctx, req)
		elapsed := time.Since(start)

		if err == nil {
			r.recordLatency(p, elapsed)
			return resp, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
		if attempt < policy.MaxAttempts-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			delay = min(time.Duration(float64(delay)*policy.Multiplier), policy.MaxDelay)
		}
	}
	return nil, lastErr
}

// pickOrder returns provider indices in the order they should be tried.
func (r *Router) pickOrder() []int {
	n := len(r.providers)
	if n == 1 {
		return []int{0}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	switch r.strategy {
	case RoundRobin:
		start := r.robin % n
		r.robin++
		order := make([]int, n)
		for i := range n {
			order[i] = (start + i) % n
		}
		return order

	case LeastLatency:
		// Fall back to PriorityOrder until all providers have been observed.
		for i, seen := range r.observed {
			if !seen {
				_ = i // not all observed yet
				return seqOrder(n)
			}
		}
		// Return indices sorted by ascending latency, with a small random tiebreak.
		return sortedByLatency(r.latency)

	default: // PriorityOrder
		return seqOrder(n)
	}
}

// recordLatency updates the EMA latency for the given provider.
func (r *Router) recordLatency(p Provider, elapsed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, pr := range r.providers {
		if pr.Name() == p.Name() {
			if !r.observed[i] {
				r.latency[i] = elapsed
				r.observed[i] = true
			} else {
				// EMA with alpha = 0.3.
				r.latency[i] = time.Duration(0.7*float64(r.latency[i]) + 0.3*float64(elapsed))
			}
			return
		}
	}
}

func seqOrder(n int) []int {
	out := make([]int, n)
	for i := range n {
		out[i] = i
	}
	return out
}

func sortedByLatency(lat []time.Duration) []int {
	n := len(lat)
	order := seqOrder(n)
	// Simple insertion sort -- small n (typically 2-5 providers).
	for i := 1; i < n; i++ {
		for j := i; j > 0 && lat[order[j]] < lat[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	// Add a small random perturbation to break ties and avoid thundering herd.
	if n > 1 && lat[order[0]] == lat[order[1]] {
		if rand.IntN(2) == 1 {
			order[0], order[1] = order[1], order[0]
		}
	}
	return order
}

func isRetryable(err error) bool {
	var rl *ErrRateLimit
	var to *ErrTimeout
	return errors.As(err, &rl) || errors.As(err, &to)
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
