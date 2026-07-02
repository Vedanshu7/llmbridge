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
//	POST /v1/images/generations    — image generation
//	POST /v1/audio/transcriptions  — audio transcription
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
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	llmbridge "github.com/Vedanshu7/llmbridge"
	"github.com/Vedanshu7/llmbridge/caching"
	"github.com/Vedanshu7/llmbridge/callbacks"
	"github.com/Vedanshu7/llmbridge/guardrails"
	"github.com/Vedanshu7/llmbridge/llms/base"
	"github.com/Vedanshu7/llmbridge/proxy/audit"
	"github.com/Vedanshu7/llmbridge/proxy/auth"
	"github.com/Vedanshu7/llmbridge/proxy/config"
	"github.com/Vedanshu7/llmbridge/proxy/management"
	"github.com/Vedanshu7/llmbridge/proxy/metrics"
	"github.com/Vedanshu7/llmbridge/proxy/middleware"
	"github.com/Vedanshu7/llmbridge/proxy/persistence"
	"github.com/Vedanshu7/llmbridge/proxy/prompts"
	"github.com/Vedanshu7/llmbridge/proxy/secrets"
	"github.com/Vedanshu7/llmbridge/proxy/ui"
	"github.com/Vedanshu7/llmbridge/proxy/webhooks"
	"github.com/Vedanshu7/llmbridge/types"
)

// batchRecord tracks an in-process batch job for non-native-batch providers.
type batchRecord struct {
	status  string
	results []llmbridge.BatchResult
	total   int
}

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

	batchMu      sync.RWMutex
	batchRecords map[string]*batchRecord // batchID → record (in-process fallback)

	promptStore  *prompts.Store
	webhookStore *webhooks.Store
	auditLog     *audit.Log

	// OIDC/SSO
	oidcProviders map[string]*auth.OIDCProvider // name → provider
	oidcStates    *auth.OIDCStateStore

	usageDB *sql.DB // non-nil when backed by SQLite; used for usage_records writes

	verboseLogger callbacks.Handler // nil = disabled; writes full req/resp JSON to file

	otelMgr *callbacks.Manager // nil = disabled; emits OTLP spans to a collector
}

// NewServer constructs a Server backed by the given LLM provider.
// Pass a *llmbridge.Router as the provider to get multi-backend routing.
func NewServer(provider base.LLM) *Server {
	s := &Server{
		provider:      provider,
		keyStore:      auth.NewAPIKeyStore(),
		orgStore:      auth.NewOrgStore(),
		rateLimiter:   auth.NewRateLimiter(),
		collector:     metrics.NewCollector(),
		modelReg:      management.NewModelRegistry(),
		routerCfg:     management.NewRouterConfig(),
		batchRecords:  make(map[string]*batchRecord),
		promptStore:   prompts.NewStore(),
		webhookStore:  webhooks.NewStore(),
		auditLog:      audit.New(1000),
		oidcProviders: make(map[string]*auth.OIDCProvider),
		oidcStates:    auth.NewOIDCStateStore(),
	}
	s.mux = s.buildMux()
	return s
}

// NewServerWithDB constructs a Server backed by provider and a SQLite database at dbPath.
// State (API keys, orgs, teams, spend) is persisted across restarts.
func NewServerWithDB(provider base.LLM, dbPath string) (*Server, error) {
	db, err := persistence.Open(dbPath)
	if err != nil {
		return nil, err
	}
	keyStore, err := auth.NewAPIKeyStoreWithDB(db)
	if err != nil {
		return nil, err
	}
	orgStore, err := auth.NewOrgStoreWithDB(db)
	if err != nil {
		return nil, err
	}
	ps := prompts.NewStore()
	if err := ps.AttachDB(db); err != nil {
		return nil, err
	}
	s := &Server{
		provider:      provider,
		keyStore:      keyStore,
		orgStore:      orgStore,
		rateLimiter:   auth.NewRateLimiter(),
		collector:     metrics.NewCollector(),
		modelReg:      management.NewModelRegistry(),
		routerCfg:     management.NewRouterConfig(),
		batchRecords:  make(map[string]*batchRecord),
		promptStore:   ps,
		webhookStore:  webhooks.NewStore(),
		auditLog:      audit.New(1000),
		oidcProviders: make(map[string]*auth.OIDCProvider),
		oidcStates:    auth.NewOIDCStateStore(),
		usageDB:       db,
	}
	s.mux = s.buildMux()
	return s, nil
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
// It resolves any configured secrets before applying other settings.
func FromConfig(cfg *config.Config, provider base.LLM) *Server {
	s, _ := fromConfig(cfg, provider, "")
	return s
}

// FromConfigWithDB is like FromConfig but opens a SQLite database at dbPath
// so that API keys, orgs, and spend are persisted across restarts.
func FromConfigWithDB(cfg *config.Config, provider base.LLM, dbPath string) (*Server, error) {
	return fromConfig(cfg, provider, dbPath)
}

func fromConfig(cfg *config.Config, provider base.LLM, dbPath string) (*Server, error) {
	// Resolve external secrets before anything else, so that env vars are set
	// when provider constructors or config values are evaluated.
	if sc := cfg.Secrets; sc != nil && len(sc.Mappings) > 0 {
		loader, err := secrets.NewLoader(sc.Backend, sc.Options)
		if err == nil {
			for envVar, secretPath := range sc.Mappings {
				if val, err := loader.Load(context.Background(), secretPath); err == nil {
					_ = os.Setenv(envVar, val)
				}
			}
		}
	}

	var (
		s   *Server
		err error
	)
	if dbPath != "" {
		s, err = NewServerWithDB(provider, dbPath)
		if err != nil {
			return nil, fmt.Errorf("proxy: open db %s: %w", dbPath, err)
		}
	} else {
		s = NewServer(provider)
	}
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

	// Wire verbose request/response logger from config.
	if cfg.VerboseLogging && cfg.VerboseLogFile != "" {
		if vf, err := os.OpenFile(cfg.VerboseLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			s.verboseLogger = callbacks.VerboseLogHandler(vf)
		}
	}

	// Wire OTEL span exporter from config.
	if cfg.OTELEndpoint != "" {
		svcName := cfg.OTELServiceName
		if svcName == "" {
			svcName = "llmbridge"
		}
		s.otelMgr = callbacks.NewManager()
		s.otelMgr.Register(callbacks.OTELHandler(cfg.OTELEndpoint, svcName, nil))
	}

	// Wire OIDC/SSO from config.
	if cfg.OIDC != nil {
		switch cfg.OIDC.Provider {
		case "google":
			s.oidcProviders["google"] = auth.NewGoogleProvider(cfg.OIDC.ClientID, cfg.OIDC.ClientSecret, cfg.OIDC.RedirectURL)
		case "github":
			s.oidcProviders["github"] = auth.NewGitHubProvider(cfg.OIDC.ClientID, cfg.OIDC.ClientSecret, cfg.OIDC.RedirectURL)
		case "microsoft":
			s.oidcProviders["microsoft"] = auth.NewMicrosoftProvider(cfg.OIDC.ClientID, cfg.OIDC.ClientSecret, cfg.OIDC.RedirectURL, cfg.OIDC.TenantID)
		}
	}

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

	// Wire per-model fallback chains onto the router when the provider is one.
	if cfg.Router != nil && len(cfg.Router.FallbackModels) > 0 {
		if r, ok := provider.(*llmbridge.Router); ok {
			r.SetFallbackChains(cfg.Router.FallbackModels)
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

	return s, nil
}

// Start listens on addr (e.g. ":8080") and serves until the context is cancelled.
func (s *Server) Start(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy: listen %s: %w", addr, err)
	}

	// Wrap mux: metrics first (outermost), then optional access log.
	handler := s.collector.Middleware(s.mux)
	if s.logFile != "" {
		f, err := os.OpenFile(s.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("proxy: open log file %s: %w", s.logFile, err)
		}
		handler = middleware.RequestLogger(f)(handler)
	}

	// Start background budget-reset ticker when the server has a database.
	if s.usageDB != nil {
		go s.runBudgetResets(ctx)
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

	// OIDC/SSO endpoints (unauthenticated — they perform their own auth flow).
	mux.HandleFunc("GET /auth/login", s.handleAuthLogin)
	mux.HandleFunc("GET /auth/callback", s.handleAuthCallback)
	mux.HandleFunc("GET /auth/logout", s.handleAuthLogout)

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
	mux.Handle("POST /v1/images/generations", authedLimited(http.HandlerFunc(s.handleImageGenerate)))
	mux.Handle("POST /v1/audio/transcriptions", authedLimited(http.HandlerFunc(s.handleTranscription)))
	mux.Handle("POST /v1/moderations", authedLimited(http.HandlerFunc(s.handleModerations)))
	mux.Handle("POST /v1/batches", authedLimited(http.HandlerFunc(s.handleBatchCreate)))
	mux.Handle("GET /v1/batches/{batch_id}", authed(http.HandlerFunc(s.handleBatchStatus)))
	mux.Handle("POST /v1/batches/{batch_id}/cancel", authed(http.HandlerFunc(s.handleBatchCancel)))

	// Admin UI — served from embedded static files.
	uiFS := http.FileServer(http.FS(ui.Static))
	mux.Handle("GET /admin/ui", http.StripPrefix("/admin", uiFS))
	mux.Handle("GET /admin/ui/", http.StripPrefix("/admin", uiFS))

	// Admin endpoints (require "admin" scope).
	admin := auth.RequireScope(s.keyStore, "admin")
	km := management.NewKeyManagementWithRateLimiter(s.keyStore, s.rateLimiter)
	mux.Handle("POST /admin/key/generate", admin(http.HandlerFunc(km.HandleGenerate)))
	mux.Handle("DELETE /admin/key/delete", admin(http.HandlerFunc(km.HandleDelete)))
	mux.Handle("GET /admin/keys", admin(http.HandlerFunc(km.HandleList)))
	mux.Handle("PUT /admin/key/rate-limit", admin(http.HandlerFunc(km.HandleSetRateLimit)))
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
	mux.Handle("GET /admin/stats", admin(http.HandlerFunc(s.handleAdminStats)))
	mux.Handle("GET /admin/usage", admin(http.HandlerFunc(s.handleAdminUsage)))
	mux.Handle("GET /admin/usage/export", admin(http.HandlerFunc(s.handleAdminUsageExport)))
	mux.Handle("GET /admin/audit-log", admin(http.HandlerFunc(s.auditLog.HandleList)))

	// Prompt management endpoints.
	mux.Handle("POST /admin/prompts", admin(http.HandlerFunc(s.promptStore.HandleCreate)))
	mux.Handle("GET /admin/prompts", admin(http.HandlerFunc(s.promptStore.HandleList)))
	mux.Handle("GET /admin/prompts/{id}", admin(http.HandlerFunc(s.promptStore.HandleGet)))
	mux.Handle("PUT /admin/prompts/{id}", admin(http.HandlerFunc(s.promptStore.HandleUpdate)))
	mux.Handle("DELETE /admin/prompts/{id}", admin(http.HandlerFunc(s.promptStore.HandleDelete)))
	mux.Handle("POST /admin/prompts/{id}/render", admin(http.HandlerFunc(s.promptStore.HandleRender)))

	// Webhook management endpoints.
	mux.Handle("POST /admin/webhooks", admin(http.HandlerFunc(s.webhookStore.HandleRegister)))
	mux.Handle("GET /admin/webhooks", admin(http.HandlerFunc(s.webhookStore.HandleList)))
	mux.Handle("GET /admin/webhooks/{id}", admin(http.HandlerFunc(s.webhookStore.HandleGet)))
	mux.Handle("DELETE /admin/webhooks/{id}", admin(http.HandlerFunc(s.webhookStore.HandleDelete)))

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
		Alias string `json:"alias"`
		Model string `json:"model"`
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

func (s *Server) handleImageGenerate(w http.ResponseWriter, r *http.Request) {
	gen, ok := s.provider.(base.ImageGenerator)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"provider does not support image generation")
		return
	}

	var body struct {
		Prompt  string `json:"prompt"`
		Model   string `json:"model"`
		N       int    `json:"n"`
		Size    string `json:"size"`
		Quality string `json:"quality"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "prompt field required")
		return
	}

	resp, err := gen.ImageGenerate(r.Context(), types.ImageRequest{
		Prompt:  body.Prompt,
		Model:   body.Model,
		N:       body.N,
		Size:    body.Size,
		Quality: body.Quality,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	type wireImage struct {
		URL           string `json:"url,omitempty"`
		B64JSON       string `json:"b64_json,omitempty"`
		RevisedPrompt string `json:"revised_prompt,omitempty"`
	}
	data := make([]wireImage, len(resp.Images))
	for i, img := range resp.Images {
		data[i] = wireImage{URL: img.URL, B64JSON: img.B64JSON, RevisedPrompt: img.RevisedPrompt}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"created": time.Now().Unix(),
		"data":    data,
	})
}

func (s *Server) handleTranscription(w http.ResponseWriter, r *http.Request) {
	transcriber, ok := s.provider.(base.Transcriber)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"provider does not support audio transcription")
		return
	}

	if err := r.ParseMultipartForm(25 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not parse multipart form: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "file field required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not read file: "+err.Error())
		return
	}

	format := r.FormValue("response_format")
	resp, err := transcriber.Transcribe(r.Context(), types.TranscriptionRequest{
		AudioData: data,
		Model:     r.FormValue("model"),
		Language:  r.FormValue("language"),
		Format:    format,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	if format == "text" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(resp.Text))
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"text": resp.Text})
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
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
	Stream      bool            `json:"stream"`
	Tools       []openAIToolDef `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"` // string or []contentPart
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

// contentPart is one element of a multi-modal message content array.
type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

// textContent extracts the plain text from an openAIMessage's content field,
// which may be a JSON string or a JSON array of content parts.
func (m *openAIMessage) textContent() string {
	if len(m.Content) == 0 {
		return ""
	}
	// Try plain string first.
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	// Array of content parts — concatenate text parts.
	var parts []contentPart
	if json.Unmarshal(m.Content, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// contentParts parses the content field into a slice of ContentPart.
// Returns nil if content is a plain string (non-multimodal).
func (m *openAIMessage) contentParts() []types.ContentPart {
	if len(m.Content) == 0 {
		return nil
	}
	var parts []contentPart
	if json.Unmarshal(m.Content, &parts) != nil {
		return nil
	}
	out := make([]types.ContentPart, 0, len(parts))
	for _, p := range parts {
		cp := types.ContentPart{Type: p.Type, Text: p.Text}
		if p.ImageURL != nil {
			cp.ImageURL = p.ImageURL.URL
		}
		out = append(out, cp)
	}
	return out
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
	handlerStart := time.Now()
	var oaiReq openAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&oaiReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not parse request body")
		return
	}

	// Translate to types.Request.
	req := types.Request{
		Model:       oaiReq.Model,
		Temperature: oaiReq.Temperature,
		MaxTokens:   oaiReq.MaxTokens,
		Stream:      oaiReq.Stream,
	}
	for _, m := range oaiReq.Messages {
		text := m.textContent()
		if m.Role == "system" {
			req.System = text
			continue
		}
		msg := types.Message{
			Role:       m.Role,
			Content:    text,
			Parts:      m.contentParts(),
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, types.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		req.Messages = append(req.Messages, msg)
	}
	// Translate tool definitions.
	for _, td := range oaiReq.Tools {
		t := types.Tool{
			Name:        td.Function.Name,
			Description: td.Function.Description,
		}
		if p, ok := td.Function.Parameters["type"].(string); ok {
			t.Parameters.Type = p
		} else {
			t.Parameters.Type = "object"
		}
		if props, ok := td.Function.Parameters["properties"].(map[string]interface{}); ok {
			t.Parameters.Properties = make(map[string]types.Property, len(props))
			for k, v := range props {
				if pm, ok := v.(map[string]interface{}); ok {
					prop := types.Property{}
					if typ, ok := pm["type"].(string); ok {
						prop.Type = typ
					}
					if desc, ok := pm["description"].(string); ok {
						prop.Description = desc
					}
					t.Parameters.Properties[k] = prop
				}
			}
		}
		if req2, ok := td.Function.Parameters["required"].([]interface{}); ok {
			for _, r := range req2 {
				if s, ok := r.(string); ok {
					t.Parameters.Required = append(t.Parameters.Required, s)
				}
			}
		}
		req.Tools = append(req.Tools, t)
	}

	ctx := r.Context()

	// Resolve model alias: per-key aliases take precedence over global aliases.
	apiKeyForAlias := auth.APIKeyFromContext(ctx)
	if keyInfo, ok := s.keyStore.ValidateAPIKey(apiKeyForAlias); ok {
		req.Model = keyInfo.ResolveModel(req.Model, s.aliases)
	} else if s.aliases != nil {
		if canonical, ok := s.aliases[req.Model]; ok {
			req.Model = canonical
		}
	}

	// Pre-request budget enforcement.
	apiKey := auth.APIKeyFromContext(ctx)
	if apiKey != "" {
		if keyInfo, ok := s.keyStore.ValidateAPIKey(apiKey); ok {
			if keyInfo.SpendLimit > 0 && keyInfo.CurrentSpend >= keyInfo.SpendLimit {
				writeError(w, http.StatusPaymentRequired, "budget_exceeded",
					fmt.Sprintf("key spend limit of $%.4f reached", keyInfo.SpendLimit))
				return
			}
			if keyInfo.OrgID != "" {
				if org, ok := s.orgStore.GetOrg(keyInfo.OrgID); ok && org.Budget > 0 && org.CurrentSpend >= org.Budget {
					writeError(w, http.StatusPaymentRequired, "budget_exceeded",
						fmt.Sprintf("org budget of $%.4f reached", org.Budget))
					return
				}
			}
			if keyInfo.TeamID != "" {
				if teams := s.orgStore.ListTeams(keyInfo.OrgID); len(teams) > 0 {
					for _, t := range teams {
						if t.ID == keyInfo.TeamID && t.Budget > 0 && t.CurrentSpend >= t.Budget {
							writeError(w, http.StatusPaymentRequired, "budget_exceeded",
								fmt.Sprintf("team budget of $%.4f reached", t.Budget))
							return
						}
					}
				}
			}
		}
	}

	// Guardrails — check request before sending to provider.
	if s.guardrails != nil {
		if err := s.guardrails.CheckRequest(&req); err != nil {
			writeError(w, http.StatusBadRequest, "guardrail_violation", err.Error())
			return
		}
	}

	// Fire verbose and OTEL request events before dispatching.
	if s.verboseLogger != nil {
		s.verboseLogger(ctx, callbacks.Event{
			Type:     callbacks.EventRequest,
			Provider: "",
			Model:    req.Model,
			Request:  &req,
			Metadata: map[string]string{"key": apiKey},
		})
	}
	if s.otelMgr != nil {
		s.otelMgr.Fire(ctx, callbacks.Event{
			Type:    callbacks.EventRequest,
			Model:   req.Model,
			Request: &req,
		})
	}

	if oaiReq.Stream {
		s.streamChatCompletion(w, r, ctx, req, handlerStart)
		return
	}

	// Cache lookup (non-streaming only).
	var cacheKey string
	if s.cache != nil {
		if _, ok := s.cache.(*caching.SemanticCache); ok {
			cacheKey = caching.QueryText(req)
		} else {
			cacheKey = caching.GenerateCacheKey(req)
		}
		if cached, ok := s.cache.Get(cacheKey); ok {
			writeJSON(w, http.StatusOK, buildOAIResponse(cached))
			return
		}
	}

	resp, err := s.provider.Complete(ctx, req)
	if err != nil {
		if s.verboseLogger != nil {
			s.verboseLogger(ctx, callbacks.Event{
				Type:     callbacks.EventError,
				Model:    req.Model,
				Request:  &req,
				Error:    err,
				Metadata: map[string]string{"key": apiKey},
			})
		}
		if s.otelMgr != nil {
			s.otelMgr.Fire(ctx, callbacks.Event{
				Type:    callbacks.EventError,
				Model:   req.Model,
				Request: &req,
				Error:   err,
			})
		}
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if s.verboseLogger != nil {
		s.verboseLogger(ctx, callbacks.Event{
			Type:     callbacks.EventResponse,
			Provider: resp.Provider,
			Model:    resp.Model,
			Request:  &req,
			Response: resp,
			Metadata: map[string]string{"key": apiKey},
		})
	}
	if s.otelMgr != nil {
		s.otelMgr.Fire(ctx, callbacks.Event{
			Type:     callbacks.EventResponse,
			Provider: resp.Provider,
			Model:    resp.Model,
			Request:  &req,
			Response: resp,
		})
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
	// Record granular usage for analytics.
	if s.usageDB != nil {
		var orgID, teamID string
		if keyInfo, ok := s.keyStore.ValidateAPIKey(apiKey); ok {
			orgID = keyInfo.OrgID
			teamID = keyInfo.TeamID
		}
		rec := persistence.UsageRecord{
			ID:        newBatchID(),
			Key:       apiKey,
			OrgID:     orgID,
			TeamID:    teamID,
			Model:     resp.Model,
			Provider:  resp.Provider,
			Timestamp: time.Now(),
		}
		if resp.Usage != nil {
			rec.PromptTokens = resp.Usage.PromptTokens
			rec.CompletionTokens = resp.Usage.CompletionTokens
		}
		if cost, cerr := llmbridge.CompletionCost(resp); cerr == nil {
			rec.CostUSD = cost
		}
		_ = persistence.RecordUsage(s.usageDB, rec)
	}

	// Track token usage for rate limiting and Prometheus metrics.
	if resp.Usage != nil {
		if apiKey != "" {
			s.rateLimiter.RecordTokens(apiKey, resp.Usage.TotalTokens)
		}
		s.collector.RecordTokens(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}

	// Fire webhook callbacks for this completion.
	s.fireCompletionWebhooks(ctx, resp, apiKey, "")

	// Write audit entry.
	if s.auditLog != nil {
		entry := audit.Entry{
			Timestamp: time.Now(),
			APIKey:    audit.MaskKey(apiKey),
			Model:     resp.Model,
			Provider:  resp.Provider,
			Status:    http.StatusOK,
			UserIP:    r.RemoteAddr,
			LatencyMS: time.Since(handlerStart).Milliseconds(),
		}
		if resp.Usage != nil {
			entry.PromptTokens = resp.Usage.PromptTokens
			entry.CompletionTokens = resp.Usage.CompletionTokens
		}
		if keyInfo, ok := s.keyStore.ValidateAPIKey(apiKey); ok {
			entry.OrgID = keyInfo.OrgID
			entry.TeamID = keyInfo.TeamID
		}
		if cost, cerr := llmbridge.CompletionCost(resp); cerr == nil {
			entry.Cost = cost
		}
		s.auditLog.Record(entry)
	}

	// Translate to OpenAI response format.
	writeJSON(w, http.StatusOK, buildOAIResponse(resp))
}

func (s *Server) streamChatCompletion(w http.ResponseWriter, r *http.Request, ctx context.Context, req types.Request, handlerStart time.Time) {
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

	var promptTokens, completionTokens int
	var totalContent strings.Builder

	for delta := range ch {
		if delta.Err != nil {
			_, _ = io.WriteString(w, "data: {\"error\":\""+delta.Err.Error()+"\"}\n\n")
			flusher.Flush()
			return
		}
		if delta.Done {
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			flusher.Flush()
			break
		}
		totalContent.WriteString(delta.Content)
		chunk := buildOAIStreamChunk(delta)
		b, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	// Estimate completion tokens from streamed content (providers rarely send usage in SSE).
	completionTokens = len(strings.Fields(totalContent.String()))
	streamAPIKey := auth.APIKeyFromContext(ctx)

	syntheticResp := &types.Response{
		Provider: s.provider.Name(),
		Model:    req.Model,
		Usage:    &types.UsageData{PromptTokens: promptTokens, CompletionTokens: completionTokens},
	}

	// Post-stream budget tracking (best-effort; cost estimate only).
	if streamAPIKey != "" && promptTokens+completionTokens > 0 {
		if cost, costErr := llmbridge.CompletionCost(syntheticResp); costErr == nil && cost > 0 {
			_ = s.keyStore.RecordSpend(streamAPIKey, cost)
			if keyInfo, ok := s.keyStore.ValidateAPIKey(streamAPIKey); ok {
				if keyInfo.TeamID != "" {
					_ = s.orgStore.RecordTeamSpend(keyInfo.TeamID, cost)
				} else if keyInfo.OrgID != "" {
					_ = s.orgStore.RecordOrgSpend(keyInfo.OrgID, cost)
				}
			}
		}
	}
	s.collector.RecordTokens(promptTokens, completionTokens)
	s.fireCompletionWebhooks(ctx, syntheticResp, streamAPIKey, "")

	if s.auditLog != nil {
		entry := audit.Entry{
			Timestamp:        time.Now(),
			APIKey:           audit.MaskKey(streamAPIKey),
			Model:            req.Model,
			Provider:         s.provider.Name(),
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			Status:           http.StatusOK,
			UserIP:           r.RemoteAddr,
			LatencyMS:        time.Since(handlerStart).Milliseconds(),
		}
		if keyInfo, ok := s.keyStore.ValidateAPIKey(streamAPIKey); ok {
			entry.OrgID = keyInfo.OrgID
			entry.TeamID = keyInfo.TeamID
		}
		s.auditLog.Record(entry)
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

// ---- Batch API handlers ----

func (s *Server) handleBatchCreate(w http.ResponseWriter, r *http.Request) {
	// If the underlying provider supports native batching, delegate to it.
	if bp, ok := s.provider.(base.BatchProvider); ok {
		var body struct {
			Requests []types.Request `json:"requests"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid body: "+err.Error())
			return
		}
		batchID, err := bp.BatchCreate(r.Context(), body.Requests)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "provider_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id": batchID, "object": "batch", "status": "queued",
		})
		return
	}

	// Fallback: run concurrently in-process.
	var body struct {
		Requests []types.Request `json:"requests"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid body: "+err.Error())
		return
	}
	bID := "llmb_batch_" + newBatchID()
	rec := &batchRecord{status: "in_progress", total: len(body.Requests)}
	s.batchMu.Lock()
	s.batchRecords[bID] = rec
	s.batchMu.Unlock()

	go func() {
		results := llmbridge.BatchComplete(context.Background(), s.provider, body.Requests)
		s.batchMu.Lock()
		rec.results = results
		rec.status = "completed"
		s.batchMu.Unlock()
	}()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":     bID,
		"object": "batch",
		"status": "in_progress",
		"request_counts": map[string]int{
			"total": len(body.Requests), "completed": 0, "failed": 0,
		},
	})
}

func (s *Server) handleBatchStatus(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("batch_id")

	// Check in-process store first.
	s.batchMu.RLock()
	rec, ok := s.batchRecords[batchID]
	if ok {
		completed, failed := 0, 0
		for _, res := range rec.results {
			if res.Err != nil {
				failed++
			} else {
				completed++
			}
		}
		status, total := rec.status, rec.total
		s.batchMu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":     batchID,
			"object": "batch",
			"status": status,
			"request_counts": map[string]int{
				"total": total, "completed": completed, "failed": failed,
			},
		})
		return
	}
	s.batchMu.RUnlock()

	// Delegate to native provider.
	bp, ok := s.provider.(base.BatchProvider)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "batch not found")
		return
	}
	status, counts, err := bp.BatchStatus(r.Context(), batchID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "provider_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id": batchID, "object": "batch", "status": status, "request_counts": counts,
	})
}

func (s *Server) handleBatchCancel(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("batch_id")
	s.batchMu.Lock()
	if rec, ok := s.batchRecords[batchID]; ok {
		rec.status = "cancelled"
	}
	s.batchMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": batchID, "object": "batch", "status": "cancelled"})
}

func newBatchID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- Admin stats handler ----

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	c := s.collector
	totalRequests := c.Requests2xx.Load() + c.Requests4xx.Load() + c.Requests5xx.Load()
	totalTokens := c.PromptTokens.Load() + c.CompletionTokens.Load()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_requests": totalRequests,
		"total_tokens":   totalTokens,
		"active_keys":    len(s.keyStore.ListKeys()),
		"orgs_count":     len(s.orgStore.ListOrgs()),
	})
}

// parseUsageFilter extracts the common from/to/key/org_id/team_id query
// params shared by the usage and usage-export admin endpoints.
func parseUsageFilter(r *http.Request) persistence.UsageFilter {
	q := r.URL.Query()
	var from, to int64
	if v := q.Get("from"); v != "" {
		fmt.Sscanf(v, "%d", &from) //nolint:errcheck
	}
	if v := q.Get("to"); v != "" {
		fmt.Sscanf(v, "%d", &to) //nolint:errcheck
	}
	return persistence.UsageFilter{
		Key:    q.Get("key"),
		OrgID:  q.Get("org_id"),
		TeamID: q.Get("team_id"),
		From:   from,
		To:     to,
	}
}

func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	if s.usageDB == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"note":    "usage analytics requires a persistent database (start with -db flag)",
			"summary": persistence.UsageSummary{ByModel: map[string]*persistence.ModelUsage{}},
		})
		return
	}
	summary, err := persistence.QueryUsage(s.usageDB, parseUsageFilter(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"summary": summary})
}

// handleAdminUsageExport streams raw usage_records rows matching the filter
// as a CSV attachment, for spend-log auditing/export.
func (s *Server) handleAdminUsageExport(w http.ResponseWriter, r *http.Request) {
	if s.usageDB == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable",
			"usage export requires a persistent database (start with -db flag)")
		return
	}
	records, err := persistence.QueryUsageRecords(s.usageDB, parseUsageFilter(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="usage_export.csv"`)
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"id", "key", "org_id", "team_id", "model", "provider",
		"prompt_tokens", "completion_tokens", "cost_usd", "timestamp",
	})
	for _, rec := range records {
		_ = cw.Write([]string{
			rec.ID, rec.Key, rec.OrgID, rec.TeamID, rec.Model, rec.Provider,
			strconv.Itoa(rec.PromptTokens), strconv.Itoa(rec.CompletionTokens),
			strconv.FormatFloat(rec.CostUSD, 'f', 6, 64), rec.Timestamp.UTC().Format(time.RFC3339),
		})
	}
	cw.Flush()
}

// ---- Webhook helpers ----

// fireCompletionWebhooks delivers a completion event to all matching webhooks.
// orgID is derived from the API key's org if not provided directly.
func (s *Server) fireCompletionWebhooks(ctx context.Context, resp *types.Response, apiKey, orgID string) {
	if s.webhookStore == nil {
		return
	}
	meta := map[string]string{}
	if orgID == "" && apiKey != "" {
		if ki, ok := s.keyStore.ValidateAPIKey(apiKey); ok {
			orgID = ki.OrgID
		}
	}
	if orgID != "" {
		meta["org_id"] = orgID
	}
	event := callbacks.Event{
		Type:     callbacks.EventResponse,
		Provider: resp.Provider,
		Model:    resp.Model,
		Response: resp,
		Metadata: meta,
	}
	s.webhookStore.Handler()(ctx, event)
}

// ---- OIDC/SSO handlers ----

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if len(s.oidcProviders) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "OIDC authentication is not configured")
		return
	}
	providerName := r.URL.Query().Get("provider")
	p, ok := s.oidcProviders[providerName]
	if !ok {
		names := make([]string, 0, len(s.oidcProviders))
		for k := range s.oidcProviders {
			names = append(names, k)
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"unknown provider; supported: "+strings.Join(names, ", "))
		return
	}
	state, err := s.oidcStates.Issue(providerName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "could not generate state token")
		return
	}
	http.Redirect(w, r, p.AuthURL(state), http.StatusFound)
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "state and code query parameters are required")
		return
	}
	providerName, ok := s.oidcStates.Consume(state)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid or expired state token")
		return
	}
	p, ok := s.oidcProviders[providerName]
	if !ok {
		writeError(w, http.StatusInternalServerError, "server_error", "provider not found: "+providerName)
		return
	}
	ctx := r.Context()
	tok, err := p.Exchange(ctx, code)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "token exchange failed: "+err.Error())
		return
	}
	user, err := p.UserInfo(ctx, tok)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "userinfo fetch failed: "+err.Error())
		return
	}
	apiKey, err := s.oidcUpsertKey(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "could not issue API key: "+err.Error())
		return
	}
	// Pass the key in the URL fragment so it is never sent to the server in logs.
	http.Redirect(w, r, "/admin/ui#key="+apiKey, http.StatusFound)
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

// oidcUpsertKey finds an existing SSO-issued key for the user or creates one.
func (s *Server) oidcUpsertKey(user *auth.OIDCUser) (string, error) {
	label := "sso:" + user.Email
	for _, info := range s.keyStore.ListKeys() {
		if info.Name == label {
			return info.Key, nil
		}
	}
	key, err := s.keyStore.GenerateAPIKey([]string{"completion"})
	if err != nil {
		return "", err
	}
	s.keyStore.SetKeyName(key, label)
	return key, nil
}

// ---- Helpers ----

// runBudgetResets checks all keys/orgs/teams for elapsed reset periods and
// zeros their current_spend accordingly. It runs once at startup and then
// every hour while the server is live.
func (s *Server) runBudgetResets(ctx context.Context) {
	s.applyBudgetResets()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.applyBudgetResets()
		}
	}
}

func (s *Server) applyBudgetResets() {
	candidates, err := persistence.QueryResetCandidates(s.usageDB, "api_keys")
	if err == nil {
		for _, c := range candidates {
			if auth.IsPeriodElapsed(c.ResetPeriod, c.LastReset) {
				s.keyStore.ZeroKeySpend(c.ID)
				_ = persistence.ZeroSpend(s.usageDB, "api_keys", c.ID)
			}
		}
	}
	for _, table := range []string{"orgs", "teams"} {
		candidates, err := persistence.QueryResetCandidates(s.usageDB, table)
		if err != nil {
			continue
		}
		for _, c := range candidates {
			if auth.IsPeriodElapsed(c.ResetPeriod, c.LastReset) {
				_ = persistence.ZeroSpend(s.usageDB, table, c.ID)
			}
		}
	}
}

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
