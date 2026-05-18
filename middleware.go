package llmbridge

import "context"

// Handler is the inner function type used within a middleware chain.
// It matches the signature of Provider.Complete.
type Handler func(ctx context.Context, req Request) (*Response, error)

// Middleware wraps a Handler to add cross-cutting behavior such as logging,
// metrics, caching, request transformation, or response post-processing.
//
// A middleware receives the request and a next function representing the rest
// of the chain. Call next to forward the request inward; manipulate req or
// the returned Response before or after calling next.
//
// Example -- request logger:
//
//	func Logger(log *slog.Logger) llmbridge.Middleware {
//	    return func(ctx context.Context, req llmbridge.Request, next llmbridge.Handler) (*llmbridge.Response, error) {
//	        log.Info("llm request", "provider", ctx.Value("provider"))
//	        resp, err := next(ctx, req)
//	        log.Info("llm response", "tokens", len(resp.Content), "err", err)
//	        return resp, err
//	    }
//	}
type Middleware func(ctx context.Context, req Request, next Handler) (*Response, error)

// Chain wraps provider with the given middleware in order: the first middleware
// in the slice is the outermost (first to run on a request, last on a response).
// The returned Provider satisfies the Provider interface; it is NOT a Streamer
// even when the inner provider implements streaming.
//
//	p := llmbridge.Chain(
//	    openai.New("gpt-4o", key),
//	    RateLimit(10),
//	    Logger(slog.Default()),
//	)
func Chain(provider Provider, mw ...Middleware) Provider {
	return &chainedProvider{inner: provider, chain: mw}
}

// chainedProvider wraps a Provider with a middleware slice.
type chainedProvider struct {
	inner Provider
	chain []Middleware
}

func (c *chainedProvider) Name() string { return c.inner.Name() }

func (c *chainedProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	return buildChain(c.inner, c.chain)(ctx, req)
}

// buildChain assembles middleware into a single Handler, innermost first.
func buildChain(p Provider, mw []Middleware) Handler {
	// Start with the provider itself as the innermost handler.
	var h Handler = p.Complete

	// Wrap from right to left so that mw[0] is outermost.
	for i := len(mw) - 1; i >= 0; i-- {
		m := mw[i]
		inner := h
		h = func(ctx context.Context, req Request) (*Response, error) {
			return m(ctx, req, inner)
		}
	}
	return h
}
