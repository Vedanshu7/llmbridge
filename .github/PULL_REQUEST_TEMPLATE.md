## Description

<!-- What does this PR do? Why is this change needed? -->

Fixes #<!-- issue number -->

## Type of Change

- [ ] New provider
- [ ] Bug fix
- [ ] New feature
- [ ] Refactor
- [ ] Documentation
- [ ] Test / chore

## Changes

<!-- Bullet list of what changed and why. Be specific. -->

## Testing

**CI runs:**
- Branch creation CI run: <!-- paste link -->
- Last commit CI run: <!-- paste link -->

**Checklist:**
- [ ] Added at least 1 test covering the change (`go test ./...` passes)
- [ ] `go build ./...` passes
- [ ] `go vet ./...` passes
- [ ] `go mod tidy` — no changes to `go.mod` / `go.sum` unless intentional

## Scope

- [ ] This PR addresses **one specific problem** (no unrelated changes bundled in)

## For New Providers

- [ ] Implements `base.LLM` interface (`Name`, `Complete`)
- [ ] `cost_calculation.go` added and wired into `cost_calculator.go`
- [ ] Model entries added to `constants.go`
- [ ] Streaming implemented if provider supports SSE (`base.Streamer`)

## Screenshots / Proof

<!-- For bug fixes: before/after. For new features: example output or test result. Delete if not applicable. -->

## CLA

- [ ] I have read and agree to the [Contributor License Agreement](../CLA.md)
