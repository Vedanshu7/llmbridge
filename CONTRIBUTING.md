# Contributing to llmbridge

Thank you for contributing! Please read this fully before opening a PR.

## Hard Requirements (PRs that don't meet these will be closed)

| Requirement | Detail |
|---|---|
| **Fork the repo** | Do not push branches to `Vedanshu7/llmbridge` directly |
| **No `main` branch PRs** | Your PR branch must be named (e.g. `feat/xyz`, `fix/abc`) |
| **At least 1 test** | Every code change needs a test — no exceptions |
| **Isolated scope** | One PR = one problem. No bundling unrelated changes |
| **All CI must pass** | `build`, `test`, `lint`, `tidy`, `docker` — all green |
| **Sign the CLA** | Check the CLA box in the PR template |

---

## Workflow

### 1. Open an issue first

Discuss the approach before writing code. Good problems attract good solutions. PRs without a linked issue may be closed.

### 2. Fork and clone

```bash
# Fork via GitHub UI, then:
git clone https://github.com/<your-username>/llmbridge.git
cd llmbridge
```

### 3. Create a feature branch — never work on `main`

```bash
git checkout -b feat/gemini-provider    # new feature
git checkout -b fix/anthropic-streaming # bug fix
git checkout -b docs/proxy-quickstart   # documentation
git checkout -b test/router-strategies  # tests
```

**Branch naming:**

| Type | Pattern | Example |
|---|---|---|
| Feature | `feat/<description>` | `feat/cohere-embeddings` |
| Bug fix | `fix/<description>` | `fix/openai-tool-merge` |
| Docs | `docs/<topic>` | `docs/router-guide` |
| Refactor | `refactor/<area>` | `refactor/caching-interface` |
| Test | `test/<area>` | `test/bedrock-transform` |

### 4. Make your changes

Follow the [Code Style](#code-style) section below.

### 5. Run all checks locally

```bash
go build ./...       # must pass
go test ./...        # must pass — add tests for your change
go vet ./...         # must pass
go mod tidy          # go.mod and go.sum must stay clean
```

### 6. Open a pull request

Push to **your fork** and open a PR against `Vedanshu7/llmbridge:main`.

Fill in the [PR template](.github/PULL_REQUEST_TEMPLATE.md) completely, including CI run links.

---

## Testing Requirements

- **Every PR must include at least one test.** This is a hard requirement — not optional.
- Tests live alongside the package they test (e.g. `llms/openai/openai_test.go`).
- Use table-driven tests where possible.
- Use `httptest.NewServer` / mock responses — **do not make real API calls in tests**.
- Run the race detector locally: `go test -race ./...`

---

## Adding a New Provider

New LLM providers are the most common contribution. Follow the pattern in `llms/openai/`.

**File structure:**
```
llms/yourprovider/
├── yourprovider.go        # Provider struct, New(), Complete(), Name()
├── common_utils.go        # HTTP helpers
├── cost_calculation.go    # Pricing table + CostForResponse()
└── chat/
    ├── handler.go         # MakeCall(), MakeStreamCall()
    └── transformation.go  # Wire types, ToRequest(), FromResponse()
```

**Checklist:**
- [ ] `Provider` struct with `New(model, apiKey string) *Provider`
- [ ] `Name() string` — lowercase, stable identifier
- [ ] `Complete(ctx, req) (*types.Response, error)` — sets `resp.Provider` and `resp.Usage`
- [ ] `Stream(ctx, req) (<-chan types.Delta, error)` — if the provider supports SSE
- [ ] `cost_calculation.go` with `CostForResponse(*types.Response) (float64, error)`
- [ ] Wire into the switch in `cost_calculator.go`
- [ ] Model entries added to `ModelInfoDB` in `constants.go`
- [ ] At least one test in `llms/yourprovider/yourprovider_test.go`

---

## Code Style

- Run `gofmt` before committing (most editors do this automatically).
- Follow standard Go idioms — [Effective Go](https://go.dev/doc/effective_go) is the reference.
- **No external dependencies.** llmbridge is stdlib-only (except `modernc.org/sqlite` for the proxy). PRs that add external imports will not be merged.
- **No explanatory comments** — only comments that explain *why* (hidden constraints, workarounds, non-obvious invariants).
- Keep interfaces small and stable. Think carefully before adding methods to `base.LLM`.

---

## Commit Message Format

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>

[optional body]
```

**Types:** `feat` · `fix` · `docs` · `refactor` · `test` · `chore`

```
feat(llms): add Google Gemini provider
fix(anthropic): handle empty tool_use blocks in streaming
docs: add proxy quickstart to README
test(router): add weighted strategy coverage
```

---

## CI Checks

All five checks must be green before a review will be assigned:

| Check | What it runs |
|---|---|
| `build` | `go build ./...` on Go 1.22–1.25 |
| `test` | `go test -race ./...` |
| `lint` | `golangci-lint run` |
| `tidy` | `go mod tidy` — fails if `go.mod`/`go.sum` are not clean |
| `docker` | Multi-arch Docker build |

---

## Review Process

1. CI must be fully green.
2. At least **one maintainer approval** is required.
3. Stale reviews are dismissed when new commits are pushed — re-approval is needed.
4. Conversations must be resolved before merge.

---

Questions? Open a [Discussion](https://github.com/Vedanshu7/llmbridge/discussions) or comment in the issue thread.
