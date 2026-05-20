# llmbridge

[![Go Reference](https://pkg.go.dev/badge/github.com/Vedanshu7/llmbridge.svg)](https://pkg.go.dev/github.com/Vedanshu7/llmbridge)
[![CI](https://github.com/Vedanshu7/llmbridge/actions/workflows/ci.yml/badge.svg)](https://github.com/Vedanshu7/llmbridge/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/Vedanshu7/llmbridge)](https://goreportcard.com/report/github.com/Vedanshu7/llmbridge)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A unified Go interface to multiple LLM providers — zero external dependencies.

Switch between OpenAI, Anthropic, Ollama, Groq, Together AI, or any OpenAI-compatible endpoint by changing one line. Your application code never changes.

## Architecture

```
llmbridge/
├── llmbridge.go          # Unified interface + top-level helpers
├── router.go             # Multi-provider routing & failover
├── middleware.go         # Request/response middleware chain
├── cost_calculator.go    # Per-provider cost estimation
├── session.go            # Conversation persistence
├── constants.go          # Model registry & pricing tables
│
├── types/                # All shared types (Request, Response, Message…)
├── exceptions/           # Typed error hierarchy (AuthError, RateLimitError…)
│
├── llms/                 # Provider implementations
│   ├── base/             # LLM, Streamer, EmbedProvider interfaces
│   ├── openai/           # OpenAI + any OpenAI-compatible endpoint
│   │   └── chat/         # handler.go (HTTP) + transformation.go (wire format)
│   ├── anthropic/        # Anthropic Claude
│   │   └── chat/
│   └── compatible/       # Ollama, LM Studio, Groq, Together AI
│
├── caching/              # In-memory request/response cache
└── proxy/                # OpenAI-compatible HTTP proxy server
    ├── auth/             # API key store + middleware
    └── management/       # Key, model, and router management endpoints
```

## Installation

```bash
go get github.com/Vedanshu7/llmbridge@latest
```

Requires Go 1.22+. No external dependencies — only the Go standard library.

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

| Provider | Package | Constructor | Notes |
|---|---|---|---|
| OpenAI | `llms/openai` | `openai.New(model, key)` | GPT-4o, GPT-4o-mini, o1, o3… |
| Anthropic | `llms/anthropic` | `anthropic.New(model, key)` | Claude Opus / Sonnet / Haiku |
| Ollama | `llms/compatible` | `compatible.NewOllama(model)` | Local, requires `ollama` running |
| LM Studio | `llms/compatible` | `compatible.NewLMStudio(model)` | Local, requires LM Studio server |
| Groq | `llms/compatible` | `compatible.NewGroq(model, key)` | Fast inference API |
| Together AI | `llms/compatible` | `compatible.NewTogetherAI(model, key)` | Hosted open-source models |
| Any OpenAI-compat | `llms/compatible` | `compatible.NewCompatible(name, url, key, model)` | Generic adapter |

## Usage

### Streaming

```go
import "github.com/Vedanshu7/llmbridge/llms/anthropic"

p := anthropic.New("claude-sonnet-4-6", os.Getenv("ANTHROPIC_API_KEY"))

ch, err := p.Stream(ctx, llmbridge.Request{
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

resp, err := p.Complete(ctx, llmbridge.Request{
    Messages: []llmbridge.Message{{Role: "user", Content: "Weather in Paris?"}},
    Tools:    tools,
})

if len(resp.ToolCalls) > 0 {
    tc := resp.ToolCalls[0]
    fmt.Println(tc.Name, tc.Arguments)
}
```

### Multi-Provider Router with Failover

```go
import (
    "github.com/Vedanshu7/llmbridge"
    "github.com/Vedanshu7/llmbridge/llms/openai"
    "github.com/Vedanshu7/llmbridge/llms/anthropic"
)

router := llmbridge.NewRouter(
    []llmbridge.Provider{
        openai.New("gpt-4o", os.Getenv("OPENAI_API_KEY")),
        anthropic.New("claude-sonnet-4-6", os.Getenv("ANTHROPIC_API_KEY")),
    },
    llmbridge.WithStrategy(llmbridge.PriorityOrder),
    llmbridge.WithRetryPolicy(llmbridge.DefaultRetryPolicy),
)

// Automatically fails over to Anthropic if OpenAI is rate-limited or down.
resp, err := router.Complete(ctx, req)
```

**Routing strategies:** `PriorityOrder` · `RoundRobin` · `LeastLatency` · `LeastBusy` · `CostBased`

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

cache := caching.NewInMemoryCache()

key := caching.GenerateCacheKey(req)
if resp, ok := cache.Get(key); ok {
    return resp, nil
}
resp, err := provider.Complete(ctx, req)
if err == nil {
    cache.Set(key, resp, 5*time.Minute)
}
```

### Cost Estimation

```go
resp, _ := provider.Complete(ctx, req)
cost, err := llmbridge.CompletionCost(resp)
fmt.Printf("cost: $%.6f\n", cost)
```

### Session Persistence

```go
session := llmbridge.NewSession("openai", "gpt-4o")
session.Add(llmbridge.Message{Role: "user", Content: "Hello!"})
session.Add(llmbridge.Message{Role: "assistant", Content: resp.Content})
_ = session.Save()

// Later, in another process:
session, _ = llmbridge.LoadLatestSession()
req.Messages = session.Messages
```

### Async Completion

```go
ch := llmbridge.AComplete(ctx, provider, req)
result := <-ch
if result.Err != nil { /* handle */ }
fmt.Println(result.Response.Content)
```

### OpenAI-Compatible Proxy Server

Run llmbridge as a drop-in proxy that any OpenAI SDK client can talk to:

```go
import (
    "github.com/Vedanshu7/llmbridge/proxy"
    "github.com/Vedanshu7/llmbridge/llms/anthropic"
)

backend := anthropic.New("claude-sonnet-4-6", os.Getenv("ANTHROPIC_API_KEY"))
srv := proxy.NewServer(backend)

// Pre-generate an API key for clients.
key, _ := srv.KeyStore().GenerateAPIKey([]string{"completion"})
fmt.Println("API key:", key)

// Start on :8080 — any OpenAI SDK can point at http://localhost:8080
srv.Start(ctx, ":8080")
```

**With SQLite persistence** — keys, orgs, and spend survive restarts:

```go
srv, err := proxy.NewServerWithDB(backend, "/data/llmbridge.db")
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
| `POST` | `/v1/chat/completions` | key | Chat completion (streaming supported) |
| `POST` | `/v1/responses` | key | OpenAI Responses API |
| `POST` | `/v1/embeddings` | key | Vector embeddings |
| `POST` | `/v1/audio/speech` | key | Text-to-speech |
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

**From a JSON config file:**

```json
{
  "listen_addr": ":8080",
  "jwt_secret": "change-me",
  "admin_keys": ["llmb-your-admin-key"],
  "log_file": "/var/log/llmbridge.log",
  "cache_ttl_seconds": 300,
  "models": [
    {"name": "gpt-4o", "provider": "openai", "model": "gpt-4o"},
    {"name": "sonnet", "provider": "anthropic", "model": "claude-sonnet-4-6"}
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

**Semantic caching** — cache hits on semantically similar queries:

```go
import (
    "github.com/Vedanshu7/llmbridge/caching"
    "github.com/Vedanshu7/llmbridge/llms/openai"
)

embedder := openai.New("text-embedding-3-small", key)
sc := caching.NewSemanticCache(caching.NewInMemoryCache(), embedder, 0.95)
srv.SetCache(sc, 5*time.Minute)
```

**Typed failover** — route around context-window or content-policy errors:

```go
router := llmbridge.NewRouter(providers,
    llmbridge.WithContextWindowFallback(true),
    llmbridge.WithContentPolicyFallback(true),
)
```

**Observability:**

```go
import (
    "github.com/Vedanshu7/llmbridge/callbacks"
    "github.com/Vedanshu7/llmbridge/proxy/metrics"
)

// Langfuse tracing
mgr := callbacks.NewManager()
mgr.Register(callbacks.LangfuseHandler(publicKey, secretKey, "https://cloud.langfuse.com", nil))

// Prometheus — also exposes /metrics endpoint automatically
collector := metrics.NewCollector()
mgr.Register(collector.CallbackHandler())
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
    var rlErr  *exceptions.RateLimitError
    switch {
    case errors.As(err, &authErr):
        log.Fatal("bad API key:", authErr.LLMProvider)
    case errors.As(err, &rlErr):
        time.Sleep(5 * time.Second)
    }
}
```

**Error types:** `AuthenticationError` · `RateLimitError` · `TimeoutError` · `ContextWindowExceededError` · `ContentPolicyViolationError` · `BudgetExceededError` · `InternalServerError` · and more.

## Adding a New Provider

1. Create `llms/yourprovider/yourprovider.go` — implement `base.LLM`:
    ```go
    type Provider struct { ... }
    func (p *Provider) Name() string { return "yourprovider" }
    func (p *Provider) ValidateEnvironment() error { ... }
    func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) { ... }
    ```
2. Optionally implement `base.Streamer` for SSE streaming.
3. Add `llms/yourprovider/chat/transformation.go` for request/response mapping.
4. Add `llms/yourprovider/cost_calculation.go` with a `CostForResponse` function.
5. Wire it into `cost_calculator.go`'s switch statement.
6. Open a PR — see [CONTRIBUTING.md](CONTRIBUTING.md).

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

## License

[MIT](LICENSE) — © 2025 Vedanshu Joshi
