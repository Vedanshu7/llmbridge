package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/proxy/persistence"
)

// ---- APIKeyStore ----

func TestGenerateAndValidate(t *testing.T) {
	s := NewAPIKeyStore()
	key, err := s.GenerateAPIKey([]string{"completion"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(key, "llmb-") {
		t.Fatalf("unexpected key prefix: %s", key)
	}
	info, ok := s.ValidateAPIKey(key)
	if !ok || info == nil {
		t.Fatal("expected valid key")
	}
}

func TestValidateMissing(t *testing.T) {
	s := NewAPIKeyStore()
	_, ok := s.ValidateAPIKey("no-such-key")
	if ok {
		t.Fatal("expected invalid for unknown key")
	}
}

func TestDeleteKey(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.DeleteKey(key)
	_, ok := s.ValidateAPIKey(key)
	if ok {
		t.Fatal("expected invalid after delete")
	}
}

func TestKeyExpiry(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.SetExpiry(key, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	_, ok := s.ValidateAPIKey(key)
	if ok {
		t.Fatal("expected key to be expired")
	}
}

func TestKeyNotExpired(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.SetExpiry(key, time.Minute)
	_, ok := s.ValidateAPIKey(key)
	if !ok {
		t.Fatal("expected key to be valid before expiry")
	}
}

func TestHasScope(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey([]string{"completion"})
	if !s.HasScope(key, "completion") {
		t.Fatal("expected completion scope")
	}
	if s.HasScope(key, "admin") {
		t.Fatal("expected no admin scope")
	}
}

func TestAdminGrantsAll(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey([]string{"admin"})
	if !s.HasScope(key, "completion") {
		t.Fatal("admin should grant completion")
	}
	if !s.HasScope(key, "anything") {
		t.Fatal("admin should grant arbitrary scope")
	}
}

func TestSpendLimit(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	s.SetSpendLimit(key, 1.00)
	if err := s.RecordSpend(key, 0.50); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.RecordSpend(key, 0.60); err == nil {
		t.Fatal("expected spend limit error")
	}
}

// ---- RequireAuth middleware ----

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func TestRequireAuthMissingHeader(t *testing.T) {
	s := NewAPIKeyStore()
	h := RequireAuth(s)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuthInvalidKey(t *testing.T) {
	s := NewAPIKeyStore()
	h := RequireAuth(s)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bad-key")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuthValidKey(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey(nil)
	h := RequireAuth(s)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---- RequireRateLimit middleware ----

func TestRateLimitAllows(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{RequestsPerMin: 10})
	s := NewAPIKeyStore()
	s.ImportKey("k", nil)

	h := RequireRateLimit(rl)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-RateLimit-Limit-Requests") == "" {
		t.Fatal("expected X-RateLimit-Limit-Requests header")
	}
}

func TestRateLimitBlocks(t *testing.T) {
	rl := NewRateLimiter()
	rl.SetLimit("k", RateLimit{RequestsPerMin: 1})

	// Drain.
	rl.Allow("k") //nolint:errcheck

	h := RequireRateLimit(rl)(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
}

// ---- OrgStore ----

func TestCreateAndGetOrg(t *testing.T) {
	s := NewOrgStore()
	org, err := s.CreateOrg("acme", 100.0)
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if org.Name != "acme" || org.Budget != 100.0 {
		t.Fatalf("unexpected org: %+v", org)
	}
	got, ok := s.GetOrg(org.ID)
	if !ok || got.ID != org.ID {
		t.Fatal("GetOrg: expected to find created org")
	}
}

func TestListOrgs(t *testing.T) {
	s := NewOrgStore()
	s.CreateOrg("a", 0) //nolint:errcheck
	s.CreateOrg("b", 0) //nolint:errcheck
	if len(s.ListOrgs()) != 2 {
		t.Fatalf("expected 2 orgs, got %d", len(s.ListOrgs()))
	}
}

func TestCreateTeamUnknownOrg(t *testing.T) {
	s := NewOrgStore()
	_, err := s.CreateTeam("org_doesnotexist", "team1", 0)
	if err == nil {
		t.Fatal("expected error for unknown org")
	}
}

func TestCreateAndGetTeam(t *testing.T) {
	s := NewOrgStore()
	org, _ := s.CreateOrg("testorg", 0)
	team, err := s.CreateTeam(org.ID, "eng", 50.0)
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.OrgID != org.ID || team.Name != "eng" {
		t.Fatalf("unexpected team: %+v", team)
	}
	got, ok := s.GetTeam(team.ID)
	if !ok || got.ID != team.ID {
		t.Fatal("GetTeam: expected to find created team")
	}
}

func TestListTeamsFilterByOrg(t *testing.T) {
	s := NewOrgStore()
	org1, _ := s.CreateOrg("o1", 0)
	org2, _ := s.CreateOrg("o2", 0)
	s.CreateTeam(org1.ID, "t1", 0) //nolint:errcheck
	s.CreateTeam(org1.ID, "t2", 0) //nolint:errcheck
	s.CreateTeam(org2.ID, "t3", 0) //nolint:errcheck

	if len(s.ListTeams(org1.ID)) != 2 {
		t.Fatalf("expected 2 teams for org1, got %d", len(s.ListTeams(org1.ID)))
	}
	if len(s.ListTeams("")) != 3 {
		t.Fatalf("expected 3 teams total, got %d", len(s.ListTeams("")))
	}
}

func TestRecordTeamSpendPropagates(t *testing.T) {
	s := NewOrgStore()
	org, _ := s.CreateOrg("org", 0)
	team, _ := s.CreateTeam(org.ID, "team", 0)

	if err := s.RecordTeamSpend(team.ID, 5.0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotTeam, _ := s.GetTeam(team.ID)
	gotOrg, _ := s.GetOrg(org.ID)
	if gotTeam.CurrentSpend != 5.0 {
		t.Fatalf("team spend: expected 5.0, got %f", gotTeam.CurrentSpend)
	}
	if gotOrg.CurrentSpend != 5.0 {
		t.Fatalf("org spend (propagated): expected 5.0, got %f", gotOrg.CurrentSpend)
	}
}

func TestRecordTeamSpendExceedsTeamBudget(t *testing.T) {
	s := NewOrgStore()
	org, _ := s.CreateOrg("org", 1000.0)
	team, _ := s.CreateTeam(org.ID, "team", 10.0)

	if err := s.RecordTeamSpend(team.ID, 11.0); err == nil {
		t.Fatal("expected team budget exceeded error")
	}
}

func TestRecordTeamSpendExceedsOrgBudget(t *testing.T) {
	s := NewOrgStore()
	org, _ := s.CreateOrg("org", 5.0)
	team, _ := s.CreateTeam(org.ID, "team", 100.0)

	if err := s.RecordTeamSpend(team.ID, 6.0); err == nil {
		t.Fatal("expected org budget exceeded error")
	}
}

func TestRecordOrgSpend(t *testing.T) {
	s := NewOrgStore()
	org, _ := s.CreateOrg("org", 10.0)

	if err := s.RecordOrgSpend(org.ID, 9.0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.RecordOrgSpend(org.ID, 2.0); err == nil {
		t.Fatal("expected org budget exceeded error")
	}
}

func TestKeyOrgTeamAssociation(t *testing.T) {
	ks := NewAPIKeyStore()
	key, _ := ks.GenerateAPIKey([]string{"completion"})
	ks.SetKeyOrg(key, "org_123")
	ks.SetKeyTeam(key, "team_456")
	info, ok := ks.ValidateAPIKey(key)
	if !ok {
		t.Fatal("expected valid key")
	}
	if info.OrgID != "org_123" || info.TeamID != "team_456" {
		t.Fatalf("unexpected org/team: %s / %s", info.OrgID, info.TeamID)
	}
}

// ---- APIKeyFromContext ----

func TestAPIKeyFromContextPresent(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyAPIKey{}, "my-key")
	got := APIKeyFromContext(ctx)
	if got != "my-key" {
		t.Errorf("APIKeyFromContext = %q, want my-key", got)
	}
}

func TestAPIKeyFromContextMissing(t *testing.T) {
	got := APIKeyFromContext(context.Background())
	if got != "" {
		t.Errorf("APIKeyFromContext = %q, want empty", got)
	}
}

// ---- RequireScope ----

func TestRequireScopeAllowed(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey([]string{"admin"})

	h := RequireScope(s, "admin")(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRequireScopeForbidden(t *testing.T) {
	s := NewAPIKeyStore()
	key, _ := s.GenerateAPIKey([]string{"completion"})

	h := RequireScope(s, "admin")(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestRequireScopeMissingHeader(t *testing.T) {
	s := NewAPIKeyStore()
	h := RequireScope(s, "admin")(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// ---- DB-backed store ----

func TestAPIKeyStoreWithDBRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("persistence.Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s, err := NewAPIKeyStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewAPIKeyStoreWithDB: %v", err)
	}

	key, err := s.GenerateAPIKey([]string{"completion"})
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	// Create a new store from the same DB — the key must survive.
	s2, err := NewAPIKeyStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewAPIKeyStoreWithDB (reload): %v", err)
	}
	info, ok := s2.ValidateAPIKey(key)
	if !ok || info == nil {
		t.Fatal("expected key to persist across store reload")
	}
	if len(info.Scopes) != 1 || info.Scopes[0] != "completion" {
		t.Errorf("unexpected scopes: %v", info.Scopes)
	}
}

func TestOrgStoreWithDBRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("persistence.Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s, err := NewOrgStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewOrgStoreWithDB: %v", err)
	}

	org, err := s.CreateOrg("acme", 500.0)
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}

	// Reload from same DB.
	s2, err := NewOrgStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewOrgStoreWithDB (reload): %v", err)
	}
	got, ok := s2.GetOrg(org.ID)
	if !ok {
		t.Fatal("expected org to persist across store reload")
	}
	if got.Name != "acme" || got.Budget != 500.0 {
		t.Errorf("unexpected org: %+v", got)
	}
}

// ---- RoutePolicy ----

func TestRequiredScopeExactMatch(t *testing.T) {
	if s := RequiredScope("/v1/chat/completions"); s != "" {
		t.Errorf("got %q, want empty", s)
	}
}

func TestRequiredScopePrefixMatch(t *testing.T) {
	if s := RequiredScope("/admin/keys"); s != "admin" {
		t.Errorf("got %q, want admin", s)
	}
}

func TestRequiredScopeUnknown(t *testing.T) {
	if s := RequiredScope("/v1/unknown"); s != "" {
		t.Errorf("got %q, want empty", s)
	}
}

func TestCheckRouteAuthValid(t *testing.T) {
	store := NewAPIKeyStore()
	key, _ := store.GenerateAPIKey([]string{"completion"})
	if !CheckRouteAuth(store, key, "/v1/chat/completions") {
		t.Error("expected true for valid key on open route")
	}
}

func TestCheckRouteAuthAdminDenied(t *testing.T) {
	store := NewAPIKeyStore()
	key, _ := store.GenerateAPIKey([]string{"completion"}) // no admin scope
	if CheckRouteAuth(store, key, "/admin/keys") {
		t.Error("expected false: completion key must not access admin route")
	}
}

func TestCheckRouteAuthAdminAllowed(t *testing.T) {
	store := NewAPIKeyStore()
	key, _ := store.GenerateAPIKey([]string{"admin", "completion"})
	if !CheckRouteAuth(store, key, "/admin/keys") {
		t.Error("expected true for admin key on admin route")
	}
}

func TestCheckRouteAuthInvalidKey(t *testing.T) {
	store := NewAPIKeyStore()
	if CheckRouteAuth(store, "bad-key", "/v1/chat/completions") {
		t.Error("expected false for invalid key")
	}
}

// ---- ResolveModel / per-key model alias overrides ----

func TestResolveModelPerKeyWinsOverGlobal(t *testing.T) {
	key := &KeyInfo{
		ModelAliases: map[string]string{"gpt-4": "my-fine-tune"},
	}
	global := map[string]string{"gpt-4": "gpt-4-turbo"}

	got := key.ResolveModel("gpt-4", global)
	if got != "my-fine-tune" {
		t.Errorf("expected my-fine-tune, got %q", got)
	}
}

func TestResolveModelGlobalFallback(t *testing.T) {
	key := &KeyInfo{ModelAliases: map[string]string{}}
	global := map[string]string{"fast": "gpt-4o-mini"}

	got := key.ResolveModel("fast", global)
	if got != "gpt-4o-mini" {
		t.Errorf("expected gpt-4o-mini, got %q", got)
	}
}

func TestResolveModelIdentityWhenNoAlias(t *testing.T) {
	key := &KeyInfo{ModelAliases: map[string]string{}}
	global := map[string]string{}

	got := key.ResolveModel("claude-3", global)
	if got != "claude-3" {
		t.Errorf("expected claude-3 (identity), got %q", got)
	}
}

func TestResolveModelNilKey(t *testing.T) {
	var key *KeyInfo
	global := map[string]string{"fast": "gpt-4o-mini"}

	got := key.ResolveModel("fast", global)
	if got != "gpt-4o-mini" {
		t.Errorf("nil key should fall through to global, got %q", got)
	}
}

func TestSetModelAliasesPersistInMemory(t *testing.T) {
	store := NewAPIKeyStore()
	k, _ := store.GenerateAPIKey([]string{"completion"})

	store.SetModelAliases(k, map[string]string{"gpt-4": "my-model"})

	info, ok := store.ValidateAPIKey(k)
	if !ok {
		t.Fatal("key not found")
	}
	if info.ModelAliases["gpt-4"] != "my-model" {
		t.Errorf("expected my-model, got %q", info.ModelAliases["gpt-4"])
	}
}

// ---- IsPeriodElapsed ----

func TestIsPeriodElapsedDaily(t *testing.T) {
	cases := []struct {
		name      string
		lastReset time.Time
		want      bool
	}{
		{"just reset", time.Now().Add(-1 * time.Hour), false},
		{"24h ago", time.Now().Add(-25 * time.Hour), true},
		{"zero time", time.Time{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsPeriodElapsed("daily", c.lastReset)
			if got != c.want {
				t.Errorf("IsPeriodElapsed(daily, ...) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsPeriodElapsedWeekly(t *testing.T) {
	cases := []struct {
		name      string
		lastReset time.Time
		want      bool
	}{
		{"5 days ago", time.Now().Add(-5 * 24 * time.Hour), false},
		{"8 days ago", time.Now().Add(-8 * 24 * time.Hour), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsPeriodElapsed("weekly", c.lastReset)
			if got != c.want {
				t.Errorf("IsPeriodElapsed(weekly, ...) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsPeriodElapsedMonthly(t *testing.T) {
	now := time.Now()
	sameMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	prevMonth := sameMonth.AddDate(0, -1, 0)

	if IsPeriodElapsed("monthly", sameMonth) {
		t.Error("same month should not be elapsed")
	}
	if !IsPeriodElapsed("monthly", prevMonth) {
		t.Error("previous month should be elapsed")
	}
}

func TestIsPeriodElapsedEmptyPeriod(t *testing.T) {
	if IsPeriodElapsed("", time.Now().Add(-365*24*time.Hour)) {
		t.Error("empty period should never be elapsed")
	}
}

func TestZeroKeySpend(t *testing.T) {
	store := NewAPIKeyStore()
	k, _ := store.GenerateAPIKey([]string{"completion"})
	info, _ := store.ValidateAPIKey(k)
	info.CurrentSpend = 5.0

	store.ZeroKeySpend(k)

	updated, _ := store.ValidateAPIKey(k)
	if updated.CurrentSpend != 0 {
		t.Errorf("expected CurrentSpend=0 after reset, got %f", updated.CurrentSpend)
	}
	if updated.LastReset.IsZero() {
		t.Error("expected LastReset to be set after reset")
	}
}
