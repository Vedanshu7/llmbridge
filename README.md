# llmbridge

[![Go Reference](https://pkg.go.dev/badge/github.com/Vedanshu7/llmbridge.svg)](https://pkg.go.dev/github.com/Vedanshu7/llmbridge)
[![CI](https://github.com/Vedanshu7/llmbridge/actions/workflows/ci.yml/badge.svg)](https://github.com/Vedanshu7/llmbridge/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/Vedanshu7/llmbridge)](https://goreportcard.com/report/github.com/Vedanshu7/llmbridge)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/Vedanshu7/llmbridge)](https://github.com/Vedanshu7/llmbridge/releases)
[![codecov](https://codecov.io/gh/Vedanshu7/llmbridge/branch/main/graph/badge.svg)](https://codecov.io/gh/Vedanshu7/llmbridge)
[![CodeQL](https://github.com/Vedanshu7/llmbridge/actions/workflows/codeql.yml/badge.svg)](https://github.com/Vedanshu7/llmbridge/actions/workflows/codeql.yml)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/placeholder/badge)](https://www.bestpractices.dev)

A unified Go interface to multiple LLM providers.

Switch between OpenAI, Anthropic, Gemini, Bedrock, Azure, Cohere, Ollama, Groq, or any OpenAI-compatible endpoint by changing one line. Your application code never changes.

## Features

- **Unified interface** — one API across all providers: chat, streaming, tool use, embeddings, TTS, image generation
- **Router** — multi-provider failover with five strategies, weighted routing, circuit breaker, typed fallback for context-window and content-policy errors
- **Proxy server** — OpenAI-compatible HTTP proxy; drop in front of any backend
- **Caching** — in-memory, disk, Redis, and semantic (cosine-similarity) caches
- **Budget & spend tracking** — per-key/org/team limits with threshold alerts
- **Guardrails** — input/output length limits, PII detection, keyword blocking, prompt injection detection
- **Observability** — Langfuse tracing, Prometheus metrics, JSON access logs, webhooks
- **Auth & multi-tenancy** — API key management, SSO/OIDC (Google, GitHub, Microsoft), orgs and teams, SQLite persistence
- **Secret management** — AWS Secrets Manager, GCP Secret Manager, HashiCorp Vault
- **Deployment** — Docker (multi-arch), docker-compose, Helm chart, GitHub Actions

## Architecture

```
llmbridge/
├── llmbridge.go          # Unified interface + top-level helpers
├── router.go             # Multi-provider routing, failover, circuit breaker
├── middleware.go         # Request/response middleware chain
├── cost_calculator.go    # Per-provider cost estimation
├── session.go            # Conversation persistence
├── constants.go          # Model registry & pricing tables
│
├── types/                # Shared types (Request, Response, Message…)
├── exceptions/           # Typed error hierarchy
├── budget/               # Per-key/org spend tracking and alerts
├── caching/              # In-memory, disk, Redis, semantic caches
├── callbacks/            # Langfuse, Prometheus, JSON log, webhook handlers
├── guardrails/           # Input/output safety rules engine
├── tokencount/           # Token counting utilities
├── toolbuilder/          # Fluent builder for tool/function definitions
├── prompttpl/            # Prompt template helpers
│
├── llms/                 # Provider implementations
│   ├── base/             # LLM, Streamer, EmbedProvider, ImageGenerator interfaces
│   ├── openai/           # OpenAI (chat, embeddings, TTS, image gen, batch, files)
│   ├── anthropic/        # Anthropic Claude
│   ├── azure/            # Azure OpenAI Service
│   ├── bedrock/          # AWS Bedrock (Titan, Claude, Llama…)
│   ├── cohere/           # Cohere Command
│   ├── gemini/           # Google Gemini
│   └── compatible/       # Ollama, LM Studio, Groq, Together AI, xAI, any OpenAI-compat
│
└── proxy/                # OpenAI-compatible HTTP proxy server
    ├── auth/             # API key store, rate limiting, SSO/OIDC
    ├── audit/            # Audit logging
    ├── config/           # JSON config loader
    ├── management/       # Key, model, router, alias management endpoints
    ├── metrics/          # Prometheus collector + /metrics handler
    ├── middleware/       # HTTP access log middleware
    ├── persistence/      # SQLite-backed key/org/team store
    ├── prompts/          # Stored prompt management
    ├── secrets/          # AWS / GCP / Vault secret backends
    ├── ui/               # Embedded admin SPA
    └── webhooks/         # Outbound webhook delivery
```

## Installation

```bash
go get github.com/Vedanshu7/llmbridge@latest
```

Requires Go 1.25+. The only external dependency is `modernc.org/sqlite` (pure-Go, no CGo), used by the proxy server for persistence.

## Quick Start

```go
import (
    "github.com/Vedanshu7/llmbridge"
    "github.com/Vedanshu7/llmbridge/llms/openai"
)

p := openai.New("gpt-4o-mini", os.Getenv("OPENAI_API_KEY"))

resp, err := p.Complete(ctx, llmbridge.Request{
    System:   "You are a helpful assistant.",
    Messages: []llmbridge.Message{
        {Role: "user", Content: "What is the capital of France?"},
    },
})
fmt.Println(resp.Content) // Paris
```

## Supported Providers

| Provider | Package | Constructor |
|---|---|---|
| OpenAI | `llms/openai` | `openai.New(model, key)` |
| Anthropic | `llms/anthropic` | `anthropic.New(model, key)` |
| Azure OpenAI | `llms/azure` | `azure.New(model, endpoint, key)` |
| AWS Bedrock | `llms/bedrock` | `bedrock.New(model, region)` |
| Cohere | `llms/cohere` | `cohere.New(model, key)` |
| Google Gemini | `llms/gemini` | `gemini.New(model, key)` |
| Ollama | `llms/compatible` | `compatible.NewOllama(model)` |
| LM Studio | `llms/compatible` | `compatible.NewLMStudio(model)` |
| Groq | `llms/compatible` | `compatible.NewGroq(model, key)` |
| Together AI | `llms/compatible` | `compatible.NewTogetherAI(model, key)` |
| xAI / Grok | `llms/compatible` | `compatible.NewXAI(model, key)` |
| Any OpenAI-compat | `llms/compatible` | `compatible.NewCompatible(name, url, key, model)` |

## Usage

### Streaming

```go
ch, err := provider.Stream(ctx, llmbridge.Request{
    Messages: []llmbridge.Message{{Role: "user", Content: "Tell me a story."}},
})
for delta := range ch {
    if delta.Err != nil { /* handle */ }
    if delta.Done { break }
    fmt.Print(delta.Content)
}
```

### Tool Use (Function Calling)

```go
tools := []llmbridge.Tool{{
    Name:        "get_weather",
    Description: "Get current weather for a city.",
    Parameters: llmbridge.Schema{
        Type: "object",
        Properties: map[string]llmbridge.Property{
            "city": {Type: "string", Description: "City name"},
        },
        Required: []string{"city"},
    },
}}

resp, err := provider.Complete(ctx, llmbridge.Request{
    Messages: []llmbridge.Message{{Role: "user", Content: "Weather in Paris?"}},
    Tools:    tools,
})
```

### Embeddings

```go
import "github.com/Vedanshu7/llmbridge/llms/openai"

p := openai.New("text-embedding-3-small", key)
vecs, err := p.Embed(ctx, []string{"hello world", "foo bar"})
```

### Multi-Provider Router

```go
router := llmbridge.NewRouter(
    []llmbridge.Provider{
        openai.New("gpt-4o", os.Getenv("OPENAI_API_KEY")),
        anthropic.New("claude-sonnet-4-6", os.Getenv("ANTHROPIC_API_KEY")),
    },
    llmbridge.WithStrategy(llmbridge.RoundRobin),
    llmbridge.WithRetryPolicy(llmbridge.DefaultRetryPolicy),
    llmbridge.WithCircuitBreaker(5, 30*time.Second),
    llmbridge.WithContextWindowFallback(true),
)

resp, err := router.Complete(ctx, req)
```

**Routing strategies:** `PriorityOrder` · `RoundRobin` · `LeastLatency` · `LeastBusy` · `CostBased` · `Weighted`

### Middleware

```go
func Logger(log *slog.Logger) llmbridge.Middleware {
    return func(ctx context.Context, req llmbridge.Request, next llmbridge.Handler) (*llmbridge.Response, error) {
        start := time.Now()
        resp, err := next(ctx, req)
        log.Info("llm call", "latency", time.Since(start), "err", err)
        return resp, err
    }
}

p := llmbridge.Chain(openai.New("gpt-4o", key), Logger(slog.Default()))
```

### Caching

```go
import "github.com/Vedanshu7/llmbridge/caching"

// Exact-match cache
cache := caching.NewInMemoryCache()

// Semantic cache — hits on meaning, not exact text
embedder := openai.New("text-embedding-3-small", key)
sc := caching.NewSemanticCache(cache, embedder, 0.95)
```

### Budget & Spend Tracking

```go
import "github.com/Vedanshu7/llmbridge/budget"

tracker := budget.NewTracker()
tracker.SetLimit("my-key", 10.00)               // $10 limit
tracker.OnAlert(func(key string, spend float64) {
    log.Printf("key %s at $%.2f", key, spend)
})

cost, _ := llmbridge.CompletionCost(resp)
if err := tracker.Record("my-key", cost); err != nil {
    // budget.ErrBudgetExceeded
}
```

### Guardrails

```go
import "github.com/Vedanshu7/llmbridge/guardrails"

engine, _ := guardrails.NewEngine(
    guardrails.MaxInputLength(50000),
    guardrails.BlockPIIPatterns(),
    guardrails.BlockPromptInjection(),
)

if err := engine.Check(req); err != nil {
    // handle violation
}
```

### Cost Estimation

```go
resp, _ := provider.Complete(ctx, req)
cost, err := llmbridge.CompletionCost(resp)
fmt.Printf("cost: $%.6f\n", cost)
```

### OpenAI-Compatible Proxy Server

Run llmbridge as a drop-in proxy that any OpenAI SDK client can talk to:

```go
import (
    "github.com/Vedanshu7/llmbridge/proxy"
    "github.com/Vedanshu7/llmbridge/llms/anthropic"
)

backend := anthropic.New("claude-sonnet-4-6", os.Getenv("ANTHROPIC_API_KEY"))
srv, err := proxy.NewServerWithDB(backend, "/data/llmbridge.db")

key, _ := srv.KeyStore().GenerateAPIKey([]string{"completion"})
fmt.Println("API key:", key)

srv.Start(ctx, ":8080")
```

Or via the CLI:

```bash
llmbridge server -config config.json -db /data/llmbridge.db
```

**Proxy endpoints:**

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/health` | public | Liveness check |
| `GET` | `/metrics` | public | Prometheus text metrics |
| `GET` | `/v1/models` | key | List registered models |
| `GET` | `/v1/models/{model}` | key | Get single model |
| `POST` | `/v1/chat/completions` | key | Chat completion (streaming supported) |
| `POST` | `/v1/embeddings` | key | Vector embeddings |
| `POST` | `/v1/audio/speech` | key | Text-to-speech |
| `POST` | `/v1/moderations` | key | Content moderation |
| `POST` | `/v1/batches` | key | Create batch job |
| `GET` | `/v1/batches/{id}` | key | Batch status |
| `POST` | `/v1/batches/{id}/cancel` | key | Cancel batch |
| `GET` | `/auth/login?provider=google\|github\|microsoft` | public | Start SSO flow |
| `GET` | `/auth/callback` | public | SSO callback |
| `GET` | `/admin/ui` | public | Web admin interface |
| `GET` | `/admin/stats` | admin | Aggregated metrics |
| `POST` | `/admin/key/generate` | admin | Create API key |
| `DELETE` | `/admin/key/delete` | admin | Delete API key |
| `GET` | `/admin/keys` | admin | List API keys |
| `GET/POST` | `/admin/models` | admin | List / register models |
| `GET/POST` | `/admin/router` | admin | List / deploy router configs |
| `GET/POST` | `/admin/aliases` | admin | Model name aliases |
| `GET/POST` | `/admin/orgs` | admin | Organizations |
| `GET/POST` | `/admin/teams` | admin | Teams |

**Config file:**

```json
{
  "listen_addr": ":8080",
  "jwt_secret": "change-me",
  "admin_keys": ["llmb-your-admin-key"],
  "log_file": "/var/log/llmbridge.log",
  "cache_ttl_seconds": 300,
  "models": [
    {"name": "gpt-4o",  "provider": "openai",    "model": "gpt-4o"},
    {"name": "sonnet",  "provider": "anthropic",  "model": "claude-sonnet-4-6"}
  ],
  "aliases": {"fast": "gpt-4o"},
  "router": {"strategy": "round_robin", "retries": 2},
  "guardrails": {
    "max_input_length": 100000,
    "block_pii": true,
    "block_prompt_injection": true
  },
  "oidc": {
    "provider": "google",
    "client_id": "...",
    "client_secret": "...",
    "redirect_url": "http://localhost:8080/auth/callback"
  },
  "secrets": {
    "backend": "vault",
    "options": {"vault_addr": "http://vault:8200"},
    "mappings": {"OPENAI_API_KEY": "prod/openai-key"}
  },
  "orgs": [
    {"name": "Acme", "budget": 500, "teams": [{"name": "Engineering", "budget": 200}]}
  ]
}
```

**Docker:**

```bash
docker compose up
# or
docker run -p 8080:8080 \
  -e OPENAI_API_KEY \
  -v ./config.json:/config.json:ro \
  ghcr.io/vedanshu7/llmbridge server -config /config.json
```

## Error Handling

All provider errors are typed and unwrappable:

```go
import "github.com/Vedanshu7/llmbridge/exceptions"

resp, err := provider.Complete(ctx, req)
if err != nil {
    var authErr *exceptions.AuthenticationError
    var rlErr   *exceptions.RateLimitError
    var cwErr   *exceptions.ContextWindowExceededError
    switch {
    case errors.As(err, &authErr):
        log.Fatal("bad API key:", authErr.LLMProvider)
    case errors.As(err, &rlErr):
        time.Sleep(5 * time.Second)
    case errors.As(err, &cwErr):
        // switch to a model with a larger context window
    }
}
```

**Error types:** `AuthenticationError` · `RateLimitError` · `TimeoutError` · `ContextWindowExceededError` · `ContentPolicyViolationError` · `BudgetExceededError` · `InternalServerError` · and more.

## Adding a New Provider

1. Create `llms/yourprovider/yourprovider.go` — implement `base.LLM`:
    ```go
    type Provider struct { ... }
    func (p *Provider) Name() string { return "yourprovider" }
    func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) { ... }
    ```
2. Optionally implement `base.Streamer` (SSE), `base.EmbedProvider` (embeddings), or `base.ImageGenerator`.
3. Add `llms/yourprovider/chat/transformation.go` for request/response wire-format mapping.
4. Add `llms/yourprovider/cost_calculation.go` and wire it into `cost_calculator.go`.
5. Open a PR — see [CONTRIBUTING.md](CONTRIBUTING.md).

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

## Acknowledgements

Inspired by [LiteLLM](https://github.com/BerriAI/litellm) — a Go-native reimplementation of its core concepts (unified provider interface, proxy server, routing, caching, spend tracking, and observability). All code is written from scratch in Go.

## License

[MIT](LICENSE) — © 2025 Vedanshu Joshi
