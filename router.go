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

// HealthStatus records the last known health of a provider.
type HealthStatus struct {
	Healthy       bool
	LastCheck     time.Time
	LastError     error
	Failures      int       // consecutive failure count (reset on success)
	CooldownUntil time.Time // skip provider until this time (circuit breaker)
}

// TaggedProvider pairs a Provider with routing tags and an optional weight.
type TaggedProvider struct {
	Provider Provider
	Tags     []string // e.g. ["fast", "cheap", "vision"]
	Weight   int      // relative traffic weight for Weighted strategy; 0 treated as 1
}

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

	// Weighted distributes traffic proportionally to each provider's Weight field.
	Weighted
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
	tags      [][]string // parallel to providers; nil slice = any tags
	weights   []int      // parallel to providers; used by Weighted strategy
	strategy  Strategy
	policy    RetryPolicy
	groups    []RoutingGroup

	contextWindowFallback bool
	requiredTags          []string

	cbThreshold int           // circuit breaker: failures before cooldown (0 = disabled)
	cbCooldown  time.Duration // circuit breaker: cooldown duration

	mu       sync.Mutex
	robin    int
	latency  []time.Duration
	observed []bool
	inflight []int     // per-provider in-flight request count (LeastBusy)
	spent    []float64 // per-provider cumulative cost (CostBased)

	health     []HealthStatus
	healthStop context.CancelFunc
}

// NewRouter returns a Router that dispatches across the given providers.
func NewRouter(providers []Provider, opts ...RouterOption) *Router {
	n := len(providers)
	ws := make([]int, n)
	for i := range ws {
		ws[i] = 1
	}
	r := &Router{
		providers: providers,
		tags:      make([][]string, n),
		weights:   ws,
		strategy:  PriorityOrder,
		policy:    DefaultRetryPolicy,
		latency:   make([]time.Duration, n),
		observed:  make([]bool, n),
		inflight:  make([]int, n),
		spent:     make([]float64, n),
		health:    makeHealthSlice(n),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// NewTagRouter returns a Router where each provider carries routing tags and optional weights.
// Use WithRequiredTags to filter providers by tag at request time.
// Use WithStrategy(Weighted) to route proportionally by Weight.
func NewTagRouter(providers []TaggedProvider, opts ...RouterOption) *Router {
	n := len(providers)
	ps := make([]Provider, n)
	ts := make([][]string, n)
	ws := make([]int, n)
	for i, tp := range providers {
		ps[i] = tp.Provider
		ts[i] = tp.Tags
		if tp.Weight > 0 {
			ws[i] = tp.Weight
		} else {
			ws[i] = 1
		}
	}
	r := &Router{
		providers: ps,
		tags:      ts,
		weights:   ws,
		strategy:  PriorityOrder,
		policy:    DefaultRetryPolicy,
		latency:   make([]time.Duration, n),
		observed:  make([]bool, n),
		inflight:  make([]int, n),
		spent:     make([]float64, n),
		health:    makeHealthSlice(n),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func makeHealthSlice(n int) []HealthStatus {
	s := make([]HealthStatus, n)
	for i := range s {
		s[i] = HealthStatus{Healthy: true}
	}
	return s
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

// WithContextWindowFallback enables failover when a provider returns
// ContextWindowExceededError, trying the next provider in the order.
func WithContextWindowFallback(enabled bool) RouterOption {
	return func(r *Router) { r.contextWindowFallback = enabled }
}

// WithRequiredTags restricts routing to providers whose tag set is a superset
// of all the given tags. Only meaningful when using NewTagRouter.
func WithRequiredTags(tags []string) RouterOption {
	return func(r *Router) { r.requiredTags = tags }
}

// WithWeightedStrategy is a convenience option that sets the Weighted strategy.
func WithWeightedStrategy() RouterOption {
	return WithStrategy(Weighted)
}

// WithCircuitBreaker enables the circuit breaker. After threshold consecutive
// failures on a provider, it is placed in cooldown for the given duration.
// Set threshold to 0 to disable (default).
func WithCircuitBreaker(threshold int, cooldown time.Duration) RouterOption {
	return func(r *Router) {
		r.cbThreshold = threshold
		r.cbCooldown = cooldown
	}
}

// WithHealthChecks starts a background goroutine that calls ValidateEnvironment()
// on each provider every interval. Providers that error are marked unhealthy and
// skipped in routing until they recover.
func WithHealthChecks(interval time.Duration) RouterOption {
	return func(r *Router) {
		ctx, cancel := context.WithCancel(context.Background())
		r.healthStop = cancel
		go r.runHealthChecks(ctx, interval)
	}
}

// Stop cancels the health check goroutine if one was started.
func (r *Router) Stop() {
	if r.healthStop != nil {
		r.healthStop()
	}
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
			r.recordSuccess(idx)
			return resp, nil
		}
		r.recordFailure(idx)
		lastErr = err
		if !isRetryable(err) && !r.isFallbackable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// recordSuccess resets the circuit breaker failure count for a provider.
func (r *Router) recordSuccess(idx int) {
	if r.cbThreshold <= 0 {
		return
	}
	r.mu.Lock()
	r.health[idx].Failures = 0
	r.health[idx].CooldownUntil = time.Time{}
	r.mu.Unlock()
}

// recordFailure increments the failure count and trips the circuit breaker
// once the threshold is reached.
func (r *Router) recordFailure(idx int) {
	if r.cbThreshold <= 0 {
		return
	}
	r.mu.Lock()
	r.health[idx].Failures++
	if r.health[idx].Failures >= r.cbThreshold {
		r.health[idx].Healthy = false
		r.health[idx].CooldownUntil = time.Now().Add(r.cbCooldown)
	}
	r.mu.Unlock()
}

// isFallbackable returns true if the error should trigger failover to the next provider.
func (r *Router) isFallbackable(err error) bool {
	if !r.contextWindowFallback {
		return false
	}
	var cw *exceptions.ContextWindowExceededError
	return errors.As(err, &cw)
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

	r.mu.Lock()
	defer r.mu.Unlock()

	// Build candidate set: healthy (or cooldown expired), matching required tags.
	now := time.Now()
	eligible := make([]int, 0, n)
	for i := range n {
		// Auto-recover from circuit breaker cooldown.
		if !r.health[i].Healthy && !r.health[i].CooldownUntil.IsZero() && now.After(r.health[i].CooldownUntil) {
			r.health[i].Healthy = true
			r.health[i].Failures = 0
			r.health[i].CooldownUntil = time.Time{}
		}
		if !r.health[i].Healthy {
			continue
		}
		if len(r.requiredTags) > 0 && !hasAllTags(r.tags[i], r.requiredTags) {
			continue
		}
		eligible = append(eligible, i)
	}
	// Fallback: if all providers are unhealthy/in-cooldown, try them all anyway.
	if len(eligible) == 0 {
		eligible = seqOrder(n)
	}

	if len(eligible) == 1 {
		return eligible
	}

	switch r.strategy {
	case RoundRobin:
		k := len(eligible)
		start := r.robin % k
		r.robin++
		order := make([]int, k)
		for i := range k {
			order[i] = eligible[(start+i)%k]
		}
		return order

	case LeastLatency:
		allObserved := true
		for _, idx := range eligible {
			if !r.observed[idx] {
				allObserved = false
				break
			}
		}
		if !allObserved {
			return eligible
		}
		lat := make([]time.Duration, len(eligible))
		for i, idx := range eligible {
			lat[i] = r.latency[idx]
		}
		sorted := sortedByLatency(lat)
		out := make([]int, len(sorted))
		for i, si := range sorted {
			out[i] = eligible[si]
		}
		return out

	case LeastBusy:
		inf := make([]int, len(eligible))
		for i, idx := range eligible {
			inf[i] = r.inflight[idx]
		}
		sorted := sortedByInt(inf)
		out := make([]int, len(sorted))
		for i, si := range sorted {
			out[i] = eligible[si]
		}
		return out

	case CostBased:
		sp := make([]float64, len(eligible))
		for i, idx := range eligible {
			sp[i] = r.spent[idx]
		}
		sorted := sortedByFloat(sp)
		out := make([]int, len(sorted))
		for i, si := range sorted {
			out[i] = eligible[si]
		}
		return out

	case Weighted:
		// Build an expanded list where each provider appears Weight times,
		// then pick one uniformly at random as the first choice.
		var pool []int
		for _, idx := range eligible {
			w := r.weights[idx]
			if w <= 0 {
				w = 1
			}
			for range w {
				pool = append(pool, idx)
			}
		}
		chosen := pool[rand.IntN(len(pool))]
		// Return chosen first, then the rest in priority order for fallback.
		out := []int{chosen}
		for _, idx := range eligible {
			if idx != chosen {
				out = append(out, idx)
			}
		}
		return out

	default: // PriorityOrder, UsageBased
		return eligible
	}
}

// hasAllTags returns true if providerTags contains every tag in required.
func hasAllTags(providerTags, required []string) bool {
	set := make(map[string]struct{}, len(providerTags))
	for _, t := range providerTags {
		set[t] = struct{}{}
	}
	for _, t := range required {
		if _, ok := set[t]; !ok {
			return false
		}
	}
	return true
}

// runHealthChecks pings all providers on interval and updates HealthStatus.
func (r *Router) runHealthChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.checkAllHealth()
		}
	}
}

func (r *Router) checkAllHealth() {
	for i, p := range r.providers {
		err := p.ValidateEnvironment()
		r.mu.Lock()
		r.health[i] = HealthStatus{
			Healthy:   err == nil,
			LastCheck: time.Now(),
			LastError: err,
		}
		r.mu.Unlock()
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
