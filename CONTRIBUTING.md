# Contributing to llmbridge

Thank you for taking the time to contribute! This document covers everything you need to get started.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [How to Contribute](#how-to-contribute)
- [Development Setup](#development-setup)
- [Project Structure](#project-structure)
- [Adding a New Provider](#adding-a-new-provider)
- [Submitting a Pull Request](#submitting-a-pull-request)
- [Commit Message Format](#commit-message-format)
- [Code Style](#code-style)

---

## Code of Conduct

This project follows our [Code of Conduct](CODE_OF_CONDUCT.md). By participating you agree to abide by it.

---

## How to Contribute

### Report a Bug

Open an issue using the **Bug Report** template. Include:
- Go version (`go version`)
- OS and architecture
- Minimal reproducible example
- Expected vs actual behaviour

### Request a Feature

Open an issue using the **Feature Request** template. Describe the use case before proposing a solution — good problems attract good solutions.

### Add a New Provider

New LLM provider integrations are the most common contribution. See [Adding a New Provider](#adding-a-new-provider) below.

### Fix a Bug or Implement a Feature

1. Check that an issue exists (or create one first) so we can discuss the approach.
2. Fork the repo and create a branch: `git checkout -b feat/my-feature` or `git checkout -b fix/issue-123`.
3. Make your changes following the [Code Style](#code-style) guidelines.
4. Ensure `go build ./...` and `go vet ./...` pass.
5. Open a pull request against `main`.

---

## Development Setup

```bash
# Clone your fork
git clone https://github.com/<your-username>/llmbridge.git
cd llmbridge

# Verify everything builds
go build ./...
go vet ./...

# Run tests (when available)
go test ./...
```

**Requirements:** Go 1.22 or later. No other tools or dependencies needed — the project has zero external dependencies by design.

---

## Project Structure

```
llmbridge/
├── types/            # All shared data types — start here when adding a field to Request/Response
├── exceptions/       # Typed error hierarchy
├── llms/
│   ├── base/         # Provider interfaces — only change with careful consideration
│   ├── openai/       # Reference implementation to follow when adding providers
│   │   └── chat/     # handler.go (HTTP transport) + transformation.go (wire format)
│   ├── anthropic/    # Shows how to handle non-OpenAI wire formats
│   └── compatible/   # Thin wrappers for OpenAI-compatible endpoints
├── caching/          # Cache interface + in-memory implementation
├── proxy/            # HTTP proxy server
│   ├── auth/
│   └── management/
├── router.go         # Multi-provider routing
├── middleware.go     # Middleware chain
├── cost_calculator.go
└── session.go
```

---

## Adding a New Provider

Follow the pattern established by `llms/openai/`. Each provider lives in its own package:

```
llms/yourprovider/
├── yourprovider.go        # Provider struct, New(), Complete(), Name(), ValidateEnvironment()
├── common_utils.go        # HTTP helpers (do, doStream, checkStatus)
├── cost_calculation.go    # Pricing table + CostForResponse()
└── chat/
    ├── handler.go         # MakeCall(), MakeStreamCall(), ReadSSE()
    └── transformation.go  # Wire types + ToRequest(), FromResponse()
```

**Checklist for new providers:**

- [ ] `Provider` struct with `New(model, apiKey string) *Provider`
- [ ] `Name() string` — lowercase, stable identifier
- [ ] `ValidateEnvironment() error` — checks required env vars
- [ ] `Complete(ctx, req) (*types.Response, error)` — sets `resp.Provider` and `resp.Usage`
- [ ] `Stream(ctx, req) (<-chan types.Delta, error)` — if the provider supports SSE
- [ ] `cost_calculation.go` with a `CostForResponse(*types.Response) (float64, error)` function
- [ ] Wire `CostForResponse` into the switch in `cost_calculator.go`
- [ ] Add model entries to `ModelInfoDB` in `constants.go`
- [ ] Add constructor to `llms/compatible/providers.go` if it speaks the OpenAI wire format
- [ ] `go build ./...` and `go vet ./...` pass

---

## Submitting a Pull Request

1. **One PR per concern** — a provider addition should not also refactor the router.
2. **Keep the diff small** — easier to review, faster to merge.
3. **Fill in the PR template** — describe what changed and why.
4. **All CI checks must pass** before a review will be assigned.
5. At least **one approval** from a maintainer is required to merge.

### Branch naming

| Type | Pattern | Example |
|---|---|---|
| Feature | `feat/<short-description>` | `feat/gemini-provider` |
| Bug fix | `fix/<issue-or-description>` | `fix/anthropic-tool-merge` |
| Docs | `docs/<topic>` | `docs/proxy-quickstart` |
| Refactor | `refactor/<area>` | `refactor/router-strategies` |

---

## Commit Message Format

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>

[optional body]
```

**Types:** `feat` · `fix` · `docs` · `refactor` · `test` · `chore`

**Examples:**
```
feat(llms): add Google Gemini provider
fix(anthropic): handle empty tool_use blocks in streaming
docs: add proxy quickstart to README
```

---

## Code Style

- Run `gofmt` before committing (most editors do this automatically).
- Follow standard Go idioms — when in doubt, read [Effective Go](https://go.dev/doc/effective_go).
- **No external dependencies.** llmbridge is stdlib-only by design. PRs that add external imports will not be merged.
- **No comments that explain what the code does** — only comments that explain *why* (non-obvious constraints, workarounds, invariants).
- Keep interfaces small. `base.LLM` has three methods for a reason.

---

Questions? Open a [Discussion](https://github.com/Vedanshu7/llmbridge/discussions) or ping in the issue thread.
