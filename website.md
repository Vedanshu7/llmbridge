---
title: llmbridge
type: Developer Tool
projectURL: llmbridge
descriptionShort: A unified Go SDK and proxy for OpenAI, Anthropic, Gemini, Bedrock, Cohere, and Ollama — swap providers without changing your code.
descriptionLong: llmbridge is a unified Go interface to every major LLM provider. Switch between OpenAI, Anthropic, Gemini, Bedrock, Azure, Cohere, Ollama, Groq, or any OpenAI-compatible endpoint by changing one line — application code never changes. One API covers chat, streaming, tool use, embeddings, TTS, and image generation. A router adds multi-provider failover with weighted routing, circuit breaking, and typed fallback for context-window and content-policy errors. Ships with an OpenAI-compatible HTTP proxy, four cache backends (in-memory, disk, Redis, and semantic cosine-similarity), per-key budget and spend tracking, guardrails for PII and prompt injection, and observability via Langfuse tracing and Prometheus metrics. Multi-tenant auth with SSO/OIDC, deployed via Docker, docker-compose, or a Helm chart.
viewCodeUrl: https://github.com/Vedanshu7/llmbridge
viewProjectUrl: https://pkg.go.dev/github.com/Vedanshu7/llmbridge
projectImg: /project-image/llmbridge.svg
technologies:
  - Go
  - Redis
  - Prometheus
  - SQLite
  - Docker
  - Kubernetes
  - GitHub Actions
---
