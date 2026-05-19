package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
