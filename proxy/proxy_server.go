// Package proxy implements an OpenAI-compatible HTTP proxy server that
// dispatches requests to any llmbridge Provider backend.
//
// Endpoints:
//
//	GET  /health                   — liveness check
//	GET  /v1/models                — list registered models
//	GET  /v1/models/{model}        — get single model
//	POST /v1/chat/completions      — chat completion (streaming supported)
//	POST /v1/responses             — Responses API (stateless)
//	POST /v1/embeddings            — embedding generation
//	POST /v1/audio/speech          — text-to-speech
//	POST /admin/key/generate       — create API key     (admin scope)
//	DELETE /admin/key/delete       — delete API key     (admin scope)
//	GET  /admin/keys               — list API keys      (admin scope)
//	GET  /admin/models             — list models        (admin scope)
//	POST /admin/models             — register a model   (admin scope)
//	POST /admin/router             — deploy router cfg  (admin scope)
//	GET  /admin/router             — list router cfgs   (admin scope)
//	POST /admin/orgs               — create org         (admin scope)
//	GET  /admin/orgs               — list orgs          (admin scope)
//	POST /admin/teams              — create team        (admin scope)
//	GET  /admin/teams              — list teams         (admin scope)
package proxy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	llmbridge "github.com/Vedanshu7/llmbridge"
	"github.com/Vedanshu7/llmbridge/caching"
	"github.com/Vedanshu7/llmbridge/guardrails"
	"github.com/Vedanshu7/llmbridge/llms/base"
	"github.com/Vedanshu7/llmbridge/proxy/auth"
	"github.com/Vedanshu7/llmbridge/proxy/config"
	"github.com/Vedanshu7/llmbridge/proxy/management"
	"github.com/Vedanshu7/llmbridge/proxy/metrics"
	"github.com/Vedanshu7/llmbridge/proxy/middleware"
	"github.com/Vedanshu7/llmbridge/types"
)

// Server is the OpenAI-compatible proxy HTTP server.
type Server struct {
	provider    base.LLM
	keyStore    *auth.APIKeyStore
	orgStore    *auth.OrgStore
	rateLimiter *auth.RateLimiter
	collector   *metrics.Collector
	modelReg    *management.ModelRegistry
	routerCfg   *management.RouterConfig
	jwtSecret   []byte
	aliases     map[string]string  // short name → canonical model name
	logFile     string             // path for access log (empty = disabled)
	cache       caching.Cache      // nil = caching disabled
	cacheTTL    time.Duration      // default TTL for cache entries
	guardrails  *guardrails.Engine // nil = guardrails disabled
	mux         *http.ServeMux
}

// NewServer constructs a Server backed by the given LLM provider.
// Pass a *llmbridge.Router as the provider to get multi-backend routing.
func NewServer(provider base.LLM) *Server {
	s := &Server{
		provider:    provider,
		keyStore:    auth.NewAPIKeyStore(),
		orgStore:    auth.NewOrgStore(),
		rateLimiter: auth.NewRateLimiter(),
		collector:   metrics.NewCollector(),
		modelReg:    management.NewModelRegistry(),
		routerCfg:   management.NewRouterConfig(),
	}
	s.mux = s.buildMux()
	return s
}

// RateLimiter returns the server's rate limiter, allowing callers to set
// per-key limits before the server starts.
func (s *Server) RateLimiter() *auth.RateLimiter { return s.rateLimiter }

// Metrics returns the server's metrics collector.
func (s *Server) Metrics() *metrics.Collector { return s.collector }

// KeyStore returns the server's API key store, allowing callers to
// pre-populate keys before the server starts.
func (s *Server) KeyStore() *auth.APIKeyStore { return s.keyStore }

// SetCache attaches a cache to the server. When set, chat completion responses
// are stored and served from cache keyed by the request content.
// Pass a nil TTL to use the default 5-minute TTL.
func (s *Server) SetCache(c caching.Cache, ttl time.Duration) {
	s.cache = c
	if ttl > 0 {
		s.cacheTTL = ttl
	} else {
		s.cacheTTL = 5 * time.Minute
	}
}

// SetGuardrails attaches a guardrails engine that runs on every chat completion
// request and response.
func (s *Server) SetGuardrails(e *guardrails.Engine) {
	s.guardrails = e
}

// SetJWTSecret configures the HS256 secret used to accept JWT bearer tokens.
func (s *Server) SetJWTSecret(secret []byte) {
	s.jwtSecret = secret
	s.mux = s.buildMux() // rebuild mux so auth middleware picks up the new secret
}

// FromConfig constructs a Server pre-configured from a config.Config.
func FromConfig(cfg *config.Config, provider base.LLM) *Server {
	s := NewServer(provider)
	if cfg.JWTSecret != "" {
		s.jwtSecret = []byte(cfg.JWTSecret)
		s.mux = s.buildMux()
	}
	for _, key := range cfg.AdminKeys {
		s.keyStore.ImportKey(key, []string{"admin", "completion"})
	}
	for _, m := range cfg.Models {
		s.modelReg.RegisterModel(m.Name, management.ModelInfo{Provider: m.Provider, Model: m.Model})
	}
	if len(cfg.Aliases) > 0 {
		s.aliases = cfg.Aliases
	}
	s.logFile = cfg.LogFile

	// Seed orgs and teams from config.
	for _, oe := range cfg.Orgs {
		org, err := s.orgStore.CreateOrg(oe.Name, oe.Budget)
		if err != nil {
			continue
		}
		for _, te := range oe.Teams {
			_, _ = s.orgStore.CreateTeam(org.ID, te.Name, te.Budget)
		}
	}

	// Wire caching from config.
	if cfg.CacheTTLSeconds != -1 {
		ttl := 5 * time.Minute
		if cfg.CacheTTLSeconds > 0 {
			ttl = time.Duration(cfg.CacheTTLSeconds) * time.Second
		}
		s.SetCache(caching.NewInMemoryCache(), ttl)
	}

	// Wire guardrails from config.
	if g := cfg.Guardrails; g != nil {
		var rules []guardrails.Rule
		if g.MaxInputLength > 0 {
			rules = append(rules, guardrails.MaxInputLength(g.MaxInputLength))
		}
		if g.MaxOutputLength > 0 {
			rules = append(rules, guardrails.MaxOutputLength(g.MaxOutputLength))
		}
		if g.MaxOutputTokens > 0 {
			rules = append(rules, guardrails.MaxOutputTokens(g.MaxOutputTokens))
		}
		if len(g.BlockKeywords) > 0 {
			rules = append(rules, guardrails.BlockKeywords(g.BlockKeywords))
		}
		if g.BlockPII {
			rules = append(rules, guardrails.BlockPIIPatterns())
		}
		if g.BlockPromptInjection {
			rules = append(rules, guardrails.BlockPromptInjection())
		}
		if len(rules) > 0 {
			s.guardrails = guardrails.New(rules...)
		}
	}

	return s
}

// Start listens on addr (e.g. ":8080") and serves until the context is cancelled.
func (s *Server) Start(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy: listen %s: %w", addr, err)
	}

	// Wrap mux: metrics first (outermost), then optional access log.
	var handler http.Handler = s.collector.Middleware(s.mux)
	if s.logFile != "" {
		f, err := os.OpenFile(s.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("proxy: open log file %s: %w", s.logFile, err)
		}
		handler = middleware.RequestLogger(f)(handler)
	}

	srv := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("proxy: serve: %w", err)
	}
	return nil
}

// ServeHTTP implements http.Handler, allowing the Server to be embedded in
// tests or other frameworks.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Public endpoints.
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.collector.Handler())

	// Authenticated LLM endpoints (require valid API key or JWT + rate limit).
	authed := auth.RequireAuth(s.keyStore, s.jwtSecret)
	limited := auth.RequireRateLimit(s.rateLimiter)
	authedLimited := func(h http.Handler) http.Handler { return authed(limited(h)) }
	mux.Handle("GET /v1/models", authed(http.HandlerFunc(s.handleListModels)))
	mux.Handle("GET /v1/models/{model}", authed(http.HandlerFunc(s.handleGetModel)))
	mux.Handle("POST /v1/chat/completions", authedLimited(http.HandlerFunc(s.handleChatCompletion)))
	mux.Handle("POST /v1/responses", authedLimited(http.HandlerFunc(s.handleResponses)))
	mux.Handle("POST /v1/embeddings", authedLimited(http.HandlerFunc(s.handleEmbeddings)))
	mux.Handle("POST /v1/audio/speech", authedLimited(http.HandlerFunc(s.handleSpeech)))
	mux.Handle("POST /v1/moderations", authedLimited(http.HandlerFunc(s.handleModerations)))

	// Admin endpoints (require "admin" scope).
	admin := auth.RequireScope(s.keyStore, "admin")
	km := management.NewKeyManagement(s.keyStore)
	mux.Handle("POST /admin/key/generate", admin(http.HandlerFunc(km.HandleGenerate)))
	mux.Handle("DELETE /admin/key/delete", admin(http.HandlerFunc(km.HandleDelete)))
	mux.Handle("GET /admin/keys", admin(http.HandlerFunc(km.HandleList)))
	mux.Handle("GET /admin/models", admin(http.HandlerFunc(s.modelReg.HandleList)))
	mux.Handle("POST /admin/models", admin(http.HandlerFunc(s.modelReg.HandleRegister)))
	mux.Handle("POST /admin/router", admin(http.HandlerFunc(s.routerCfg.HandleDeploy)))
	mux.Handle("GET /admin/router", admin(http.HandlerFunc(s.routerCfg.HandleList)))
	mux.Handle("GET /admin/aliases", admin(http.HandlerFunc(s.handleListAliases)))
	mux.Handle("POST /admin/aliases", admin(http.HandlerFunc(s.handleSetAlias)))
	mux.Handle("POST /admin/orgs", admin(http.HandlerFunc(s.handleCreateOrg)))
	mux.Handle("GET /admin/orgs", admin(http.HandlerFunc(s.handleListOrgs)))
	mux.Handle("POST /admin/teams", admin(http.HandlerFunc(s.handleCreateTeam)))
	mux.Handle("GET /admin/teams", admin(http.HandlerFunc(s.handleListTeams)))

	return mux
}

// ---- Handler implementations ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	models := s.modelReg.ListModels()
	var data []modelObj
	for name := range models {
		data = append(data, modelObj{ID: name, Object: "model", OwnedBy: "llmbridge"})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

func (s *Server) handleGetModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("model")
	if _, ok := s.modelReg.GetModel(name); !ok {
		writeError(w, http.StatusNotFound, "not_found_error", "model not found: "+name)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       name,
		"object":   "model",
		"owned_by": "llmbridge",
	})
}

func (s *Server) handleListAliases(w http.ResponseWriter, r *http.Request) {
	if s.aliases == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"aliases": map[string]string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"aliases": s.aliases})
}

func (s *Server) handleSetAlias(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alias    string `json:"alias"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Alias == "" || body.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "alias and model fields required")
		return
	}
	if s.aliases == nil {
		s.aliases = make(map[string]string)
	}
	s.aliases[body.Alias] = body.Model
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "alias": body.Alias, "model": body.Model})
}

func (s *Server) handleSpeech(w http.ResponseWriter, r *http.Request) {
	speaker, ok := s.provider.(base.SpeechProvider)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"provider does not support text-to-speech")
		return
	}

	var body struct {
		Input          string  `json:"input"`
		Model          string  `json:"model"`
		Voice          string  `json:"voice"`
		ResponseFormat string  `json:"response_format"`
		Speed          float64 `json:"speed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Input == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "input field required")
		return
	}

	req := types.SpeechRequest{
		Input:          body.Input,
		Model:          body.Model,
		Voice:          body.Voice,
		ResponseFormat: body.ResponseFormat,
		Speed:          body.Speed,
	}
	resp, err := speaker.Speech(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	ct := "audio/mpeg"
	switch resp.Format {
	case "opus":
		ct = "audio/ogg"
	case "aac":
		ct = "audio/aac"
	case "flac":
		ct = "audio/flac"
	case "wav":
		ct = "audio/wav"
	case "pcm":
		ct = "audio/pcm"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp.Audio)
}

func (s *Server) handleModerations(w http.ResponseWriter, r *http.Request) {
	mod, ok := s.provider.(base.Moderator)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"provider does not support content moderation")
		return
	}

	var body struct {
		Input string `json:"input"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Input == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "input field required")
		return
	}

	resp, err := mod.Moderate(r.Context(), types.ModerationRequest{Input: body.Input, Model: body.Model})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	type wireResult struct {
		Flagged        bool               `json:"flagged"`
		Categories     map[string]bool    `json:"categories"`
		CategoryScores map[string]float64 `json:"category_scores"`
	}
	results := make([]wireResult, len(resp.Results))
	for i, res := range resp.Results {
		results[i] = wireResult{
			Flagged:        res.Flagged,
			Categories:     res.Categories,
			CategoryScores: res.CategoryScores,
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      resp.ID,
		"model":   resp.Model,
		"results": results,
	})
}

// openAIChatRequest is the subset of the OpenAI chat completions request we parse.
type openAIChatRequest struct {
	Model       string             `json:"model"`
	Messages    []openAIMessage    `json:"messages"`
	Temperature float64            `json:"temperature"`
	MaxTokens   int                `json:"max_tokens"`
	Stream      bool               `json:"stream"`
	Tools       []openAIToolDef    `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Parameters  map[string]interface{} `json:"parameters"`
	} `json:"function"`
}

func (s *Server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	var oaiReq openAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&oaiReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not parse request body")
		return
	}

	// Resolve model alias if configured.
	if s.aliases != nil {
		if canonical, ok := s.aliases[oaiReq.Model]; ok {
			oaiReq.Model = canonical
		}
	}

	// Translate to types.Request.
	req := types.Request{
		Model:       oaiReq.Model,
		Temperature: oaiReq.Temperature,
		MaxTokens:   oaiReq.MaxTokens,
		Stream:      oaiReq.Stream,
	}
	for _, m := range oaiReq.Messages {
		if m.Role == "system" {
			req.System = m.Content
		} else {
			req.Messages = append(req.Messages, types.Message{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}

	ctx := r.Context()

	// Guardrails — check request before sending to provider.
	if s.guardrails != nil {
		if err := s.guardrails.CheckRequest(&req); err != nil {
			writeError(w, http.StatusBadRequest, "guardrail_violation", err.Error())
			return
		}
	}

	if oaiReq.Stream {
		s.streamChatCompletion(w, r, ctx, req)
		return
	}

	// Cache lookup (non-streaming only).
	var cacheKey string
	if s.cache != nil {
		cacheKey = caching.GenerateCacheKey(req)
		if cached, ok := s.cache.Get(cacheKey); ok {
			writeJSON(w, http.StatusOK, buildOAIResponse(cached))
			return
		}
	}

	resp, err := s.provider.Complete(ctx, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	// Guardrails — check response before sending to client.
	if s.guardrails != nil {
		if err := s.guardrails.CheckResponse(resp); err != nil {
			writeError(w, http.StatusBadRequest, "guardrail_violation", err.Error())
			return
		}
	}

	// Store in cache on success.
	if s.cache != nil && cacheKey != "" {
		s.cache.Set(cacheKey, resp, s.cacheTTL)
	}

	apiKey := auth.APIKeyFromContext(ctx)
	// Track per-key spend and propagate to org/team budgets.
	if cost, costErr := llmbridge.CompletionCost(resp); costErr == nil && cost > 0 {
		if apiKey != "" {
			_ = s.keyStore.RecordSpend(apiKey, cost)
			if keyInfo, ok := s.keyStore.ValidateAPIKey(apiKey); ok {
				if keyInfo.TeamID != "" {
					_ = s.orgStore.RecordTeamSpend(keyInfo.TeamID, cost)
				} else if keyInfo.OrgID != "" {
					_ = s.orgStore.RecordOrgSpend(keyInfo.OrgID, cost)
				}
			}
		}
	}
	// Track token usage for rate limiting and Prometheus metrics.
	if resp.Usage != nil {
		if apiKey != "" {
			s.rateLimiter.RecordTokens(apiKey, resp.Usage.TotalTokens)
		}
		s.collector.RecordTokens(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}

	// Translate to OpenAI response format.
	writeJSON(w, http.StatusOK, buildOAIResponse(resp))
}

func (s *Server) streamChatCompletion(w http.ResponseWriter, r *http.Request, ctx context.Context, req types.Request) {
	streamer, ok := s.provider.(base.Streamer)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"provider does not support streaming")
		return
	}

	ch, err := streamer.Stream(ctx, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "server_error", "streaming not supported by transport")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for delta := range ch {
		if delta.Err != nil {
			_, _ = io.WriteString(w, "data: {\"error\":\""+delta.Err.Error()+"\"}\n\n")
			flusher.Flush()
			return
		}
		if delta.Done {
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		chunk := buildOAIStreamChunk(delta)
		b, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
}

func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	embedder, ok := s.provider.(base.EmbedProvider)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"provider does not support embeddings")
		return
	}

	var body struct {
		Input []string `json:"input"`
		Model string   `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not parse request body")
		return
	}

	vecs, err := embedder.Embed(r.Context(), body.Input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	type embeddingObj struct {
		Object    string    `json:"object"`
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	}
	data := make([]embeddingObj, len(vecs))
	for i, v := range vecs {
		data[i] = embeddingObj{Object: "embedding", Embedding: v, Index: i}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   data,
		"model":  body.Model,
	})
}

// ---- Response builders ----

func buildOAIResponse(resp *types.Response) map[string]interface{} {
	msg := map[string]interface{}{
		"role":    "assistant",
		"content": resp.Content,
	}
	if len(resp.ToolCalls) > 0 {
		tcs := make([]map[string]interface{}, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			tcs[i] = map[string]interface{}{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]string{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			}
		}
		msg["tool_calls"] = tcs
	}

	choice := map[string]interface{}{
		"index":         0,
		"message":       msg,
		"finish_reason": "stop",
	}
	out := map[string]interface{}{
		"object":  "chat.completion",
		"model":   resp.Model,
		"choices": []interface{}{choice},
	}
	if resp.Usage != nil {
		out["usage"] = map[string]int{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		}
	}
	return out
}

func buildOAIStreamChunk(d types.Delta) map[string]interface{} {
	delta := map[string]interface{}{"content": d.Content}
	if d.ToolCall != nil {
		delta["tool_calls"] = []map[string]interface{}{
			{
				"id":   d.ToolCall.ID,
				"type": "function",
				"function": map[string]string{
					"name":      d.ToolCall.Name,
					"arguments": d.ToolCall.Arguments,
				},
			},
		}
	}
	return map[string]interface{}{
		"object":  "chat.completion.chunk",
		"choices": []interface{}{map[string]interface{}{"index": 0, "delta": delta}},
	}
}

// ---- Org/team handlers ----

func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string  `json:"name"`
		Budget float64 `json:"budget"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "name field required")
		return
	}
	org, err := s.orgStore.CreateOrg(body.Name, body.Budget)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, org)
}

func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"orgs": s.orgStore.ListOrgs()})
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrgID  string  `json:"org_id"`
		Name   string  `json:"name"`
		Budget float64 `json:"budget"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OrgID == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "org_id and name fields required")
		return
	}
	team, err := s.orgStore.CreateTeam(body.OrgID, body.Name, body.Budget)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	writeJSON(w, http.StatusOK, map[string]interface{}{"teams": s.orgStore.ListTeams(orgID)})
}

// handleResponses implements the OpenAI Responses API (POST /v1/responses).
// The endpoint is stateless: previous_response_id is accepted but ignored
// because llmbridge does not persist server-side conversation state.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	var raw struct {
		Model              string          `json:"model"`
		Input              json.RawMessage `json:"input"` // string | []{"role","content"} items
		MaxOutputTokens    int             `json:"max_output_tokens,omitempty"`
		Temperature        float64         `json:"temperature,omitempty"`
		PreviousResponseID string          `json:"previous_response_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil || len(raw.Input) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not parse request body; input is required")
		return
	}

	req := types.Request{
		Model:       raw.Model,
		MaxTokens:   raw.MaxOutputTokens,
		Temperature: raw.Temperature,
	}

	// Input is either a bare string or an array of {role, content} items.
	var inputStr string
	if err := json.Unmarshal(raw.Input, &inputStr); err == nil {
		req.Messages = []types.Message{{Role: "user", Content: inputStr}}
	} else {
		var items []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw.Input, &items); err != nil || len(items) == 0 {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "input must be a string or non-empty array of message objects")
			return
		}
		for _, item := range items {
			if item.Role == "system" {
				req.System = item.Content
			} else {
				req.Messages = append(req.Messages, types.Message{Role: item.Role, Content: item.Content})
			}
		}
	}

	ctx := r.Context()

	if s.guardrails != nil {
		if err := s.guardrails.CheckRequest(&req); err != nil {
			writeError(w, http.StatusBadRequest, "guardrail_violation", err.Error())
			return
		}
	}

	resp, err := s.provider.Complete(ctx, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	if s.guardrails != nil {
		if err := s.guardrails.CheckResponse(resp); err != nil {
			writeError(w, http.StatusBadRequest, "guardrail_violation", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, buildResponsesAPIResponse(resp))
}

func buildResponsesAPIResponse(resp *types.Response) map[string]interface{} {
	var contentItems []map[string]interface{}
	if resp.Content != "" {
		contentItems = append(contentItems, map[string]interface{}{
			"type": "output_text",
			"text": resp.Content,
		})
	}
	for _, tc := range resp.ToolCalls {
		contentItems = append(contentItems, map[string]interface{}{
			"type":      "tool_call",
			"id":        tc.ID,
			"name":      tc.Name,
			"arguments": tc.Arguments,
		})
	}

	out := map[string]interface{}{
		"id":         "resp_" + newShortID(),
		"object":     "response",
		"created_at": time.Now().Unix(),
		"model":      resp.Model,
		"status":     "completed",
		"output": []interface{}{
			map[string]interface{}{
				"type":    "message",
				"id":      "msg_" + newShortID(),
				"role":    "assistant",
				"content": contentItems,
			},
		},
	}
	if resp.Usage != nil {
		out["usage"] = map[string]int{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		}
	}
	return out
}

func newShortID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// ---- Helpers ----

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, errType, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]string{
			"message": msg,
			"type":    errType,
		},
	})
}
