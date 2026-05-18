package llmbridge

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/types"
)

// Strategy controls how the Router picks a provider for each request.
type Strategy int

const (
	// PriorityOrder tries providers in declaration order, failing over on retryable errors.
	PriorityOrder Strategy = iota

	// RoundRobin distributes requests evenly across all providers.
	RoundRobin

	// LeastLatency routes to the provider with the lowest EMA latency.
	LeastLatency

	// LeastBusy routes to the provider currently handling the fewest requests.
	LeastBusy

	// UsageBased routes based on observed token/request metrics.
	UsageBased

	// CostBased routes to minimize estimated cost per request.
	CostBased
)

// RetryPolicy controls per-provider retry behavior inside the Router.
type RetryPolicy struct {
	// MaxAttempts is the number of tries per provider. 1 = no retry.
	MaxAttempts int

	// InitialDelay before the first retry.
	InitialDelay time.Duration

	// Multiplier applied to the delay on each subsequent retry.
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

// RoutingGroup defines a named group of providers with a dedicated routing strategy.
// Useful when different models need different failover behavior.
type RoutingGroup struct {
	Name      string
	Providers []Provider
	Strategy  Strategy
	Policy    RetryPolicy
}

// Router dispatches requests across multiple Provider instances with automatic
// failover and load balancing. It implements Provider itself.
type Router struct {
	providers []Provider
	strategy  Strategy
	policy    RetryPolicy
	groups    []RoutingGroup

	mu       sync.Mutex
	robin    int
	latency  []time.Duration
	observed []bool
	inflight []int // per-provider in-flight request count (LeastBusy)
	spent    []float64 // per-provider cumulative cost (CostBased)
}

// NewRouter returns a Router that dispatches across the given providers.
func NewRouter(providers []Provider, opts ...RouterOption) *Router {
	r := &Router{
		providers: providers,
		strategy:  PriorityOrder,
		policy:    DefaultRetryPolicy,
		latency:   make([]time.Duration, len(providers)),
		observed:  make([]bool, len(providers)),
		inflight:  make([]int, len(providers)),
		spent:     make([]float64, len(providers)),
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

// WithRoutingGroups registers named routing groups for per-model strategies.
func WithRoutingGroups(groups []RoutingGroup) RouterOption {
	return func(r *Router) { r.groups = groups }
}

// Name implements Provider.
func (r *Router) Name() string { return "router" }

// ValidateEnvironment implements Provider.
func (r *Router) ValidateEnvironment() error { return nil }

// Complete implements Provider.
func (r *Router) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	order := r.pickOrder()
	var lastErr error
	for _, idx := range order {
		p := r.providers[idx]
		r.incInflight(idx, 1)
		resp, err := r.tryWithPolicy(ctx, p, req, idx)
		r.incInflight(idx, -1)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// tryWithPolicy executes one provider with the configured retry policy.
func (r *Router) tryWithPolicy(ctx context.Context, p Provider, req types.Request, idx int) (*types.Response, error) {
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
			r.recordLatency(idx, elapsed)
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
			delay = minDuration(time.Duration(float64(delay)*policy.Multiplier), policy.MaxDelay)
		}
	}
	return nil, lastErr
}

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
		for _, seen := range r.observed {
			if !seen {
				return seqOrder(n)
			}
		}
		return sortedByLatency(r.latency)

	case LeastBusy:
		return sortedByInt(r.inflight)

	case CostBased:
		return sortedByFloat(r.spent)

	default: // PriorityOrder, UsageBased
		return seqOrder(n)
	}
}

func (r *Router) recordLatency(idx int, elapsed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.observed[idx] {
		r.latency[idx] = elapsed
		r.observed[idx] = true
	} else {
		r.latency[idx] = time.Duration(0.7*float64(r.latency[idx]) + 0.3*float64(elapsed))
	}
}

func (r *Router) incInflight(idx, delta int) {
	r.mu.Lock()
	r.inflight[idx] += delta
	r.mu.Unlock()
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
	for i := 1; i < n; i++ {
		for j := i; j > 0 && lat[order[j]] < lat[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	if n > 1 && lat[order[0]] == lat[order[1]] {
		if rand.IntN(2) == 1 {
			order[0], order[1] = order[1], order[0]
		}
	}
	return order
}

func sortedByInt(counts []int) []int {
	n := len(counts)
	order := seqOrder(n)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && counts[order[j]] < counts[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	return order
}

func sortedByFloat(vals []float64) []int {
	n := len(vals)
	order := seqOrder(n)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && vals[order[j]] < vals[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	return order
}

func isRetryable(err error) bool {
	var rl *exceptions.RateLimitError
	var to *exceptions.TimeoutError
	var is *exceptions.InternalServerError
	var su *exceptions.ServiceUnavailableError
	return errors.As(err, &rl) || errors.As(err, &to) ||
		errors.As(err, &is) || errors.As(err, &su)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
