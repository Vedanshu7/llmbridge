package llmbridge

import (
	"context"

	"github.com/Vedanshu7/llmbridge/types"
)

// Handler is the inner function type used within a middleware chain.
type Handler func(ctx context.Context, req types.Request) (*types.Response, error)

// Middleware wraps a Handler to add cross-cutting behavior such as logging,
// metrics, caching, request transformation, or response post-processing.
//
//	func Logger(log *slog.Logger) llmbridge.Middleware {
//	    return func(ctx context.Context, req llmbridge.Request, next llmbridge.Handler) (*llmbridge.Response, error) {
//	        log.Info("llm request", "provider", ctx.Value("provider"))
//	        resp, err := next(ctx, req)
//	        log.Info("llm response", "tokens", len(resp.Content), "err", err)
//	        return resp, err
//	    }
//	}
type Middleware func(ctx context.Context, req types.Request, next Handler) (*types.Response, error)

// Chain wraps provider with the given middleware in order: the first middleware
// in the slice is the outermost (first to run on a request, last on a response).
// The returned Provider satisfies the Provider interface; it is NOT a Streamer
// even when the inner provider implements streaming.
func Chain(provider Provider, mw ...Middleware) Provider {
	return &chainedProvider{inner: provider, chain: mw}
}

type chainedProvider struct {
	inner Provider
	chain []Middleware
}

func (c *chainedProvider) Name() string { return c.inner.Name() }

func (c *chainedProvider) ValidateEnvironment() error { return c.inner.ValidateEnvironment() }

func (c *chainedProvider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	return buildChain(c.inner, c.chain)(ctx, req)
}

func buildChain(p Provider, mw []Middleware) Handler {
	var h Handler = p.Complete
	for i := len(mw) - 1; i >= 0; i-- {
		m := mw[i]
		inner := h
		h = func(ctx context.Context, req types.Request) (*types.Response, error) {
			return m(ctx, req, inner)
		}
	}
	return h
}
