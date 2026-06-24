package persistence

import (
	"testing"
	"time"
)

func TestOpenMemory(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify all three tables exist.
	for _, table := range []string{"api_keys", "orgs", "teams"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Running Migrate a second time must not error (all CREATE IF NOT EXISTS + safe ALTER).
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestAPIKeyRoundTrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(
		`INSERT INTO api_keys (key, name, created_at, last_used, expires_at, scopes, spend_limit, current_spend, allowed_cidrs, org_id, team_id)
		 VALUES ('llmb-test', 'mykey', 1700000000, 0, 0, '["completion"]', 10.0, 2.5, '[]', '', '')`,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var key, name, scopes string
	var spendLimit, currentSpend float64
	err = db.QueryRow(`SELECT key, name, scopes, spend_limit, current_spend FROM api_keys WHERE key='llmb-test'`).
		Scan(&key, &name, &scopes, &spendLimit, &currentSpend)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if key != "llmb-test" {
		t.Errorf("key = %q", key)
	}
	if name != "mykey" {
		t.Errorf("name = %q", name)
	}
	if spendLimit != 10.0 {
		t.Errorf("spend_limit = %f", spendLimit)
	}
	if currentSpend != 2.5 {
		t.Errorf("current_spend = %f", currentSpend)
	}
}

func TestUsageRecordsTableExists(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var name string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='usage_records'`).Scan(&name)
	if err != nil {
		t.Fatalf("usage_records table not found: %v", err)
	}
}

func TestRecordAndQueryUsage(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now()
	records := []UsageRecord{
		{ID: "r1", Key: "k1", OrgID: "org1", TeamID: "t1", Model: "gpt-4o", Provider: "openai", PromptTokens: 100, CompletionTokens: 50, CostUSD: 0.01, Timestamp: now},
		{ID: "r2", Key: "k1", OrgID: "org1", TeamID: "t1", Model: "gpt-4o", Provider: "openai", PromptTokens: 200, CompletionTokens: 80, CostUSD: 0.02, Timestamp: now},
		{ID: "r3", Key: "k2", OrgID: "org2", TeamID: "t2", Model: "claude-sonnet-4-6", Provider: "anthropic", PromptTokens: 150, CompletionTokens: 60, CostUSD: 0.015, Timestamp: now},
	}
	for _, rec := range records {
		if err := RecordUsage(db, rec); err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}
	}

	cases := []struct {
		name             string
		filter           UsageFilter
		wantRequests     int
		wantPromptTokens int
	}{
		{"all records", UsageFilter{}, 3, 450},
		{"filter by key k1", UsageFilter{Key: "k1"}, 2, 300},
		{"filter by key k2", UsageFilter{Key: "k2"}, 1, 150},
		{"filter by org1", UsageFilter{OrgID: "org1"}, 2, 300},
		{"filter by team2", UsageFilter{TeamID: "t2"}, 1, 150},
		{"time range includes all", UsageFilter{From: now.Unix() - 1, To: now.Unix() + 1}, 3, 450},
		{"time range excludes all", UsageFilter{From: now.Unix() + 100}, 0, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			summary, err := QueryUsage(db, c.filter)
			if err != nil {
				t.Fatalf("QueryUsage: %v", err)
			}
			if summary.TotalRequests != c.wantRequests {
				t.Errorf("TotalRequests = %d, want %d", summary.TotalRequests, c.wantRequests)
			}
			if summary.TotalPromptTokens != c.wantPromptTokens {
				t.Errorf("TotalPromptTokens = %d, want %d", summary.TotalPromptTokens, c.wantPromptTokens)
			}
		})
	}
}

func TestQueryUsageByModel(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now()
	recs := []UsageRecord{
		{ID: "a", Model: "gpt-4o", Provider: "openai", PromptTokens: 10, CompletionTokens: 5, CostUSD: 0.001, Timestamp: now},
		{ID: "b", Model: "gpt-4o", Provider: "openai", PromptTokens: 20, CompletionTokens: 10, CostUSD: 0.002, Timestamp: now},
		{ID: "c", Model: "gpt-4o-mini", Provider: "openai", PromptTokens: 5, CompletionTokens: 2, CostUSD: 0.0001, Timestamp: now},
	}
	for _, r := range recs {
		_ = RecordUsage(db, r)
	}

	summary, err := QueryUsage(db, UsageFilter{})
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	if _, ok := summary.ByModel["gpt-4o"]; !ok {
		t.Error("expected gpt-4o in ByModel")
	}
	if summary.ByModel["gpt-4o"].Requests != 2 {
		t.Errorf("gpt-4o requests = %d, want 2", summary.ByModel["gpt-4o"].Requests)
	}
	if _, ok := summary.ByModel["gpt-4o-mini"]; !ok {
		t.Error("expected gpt-4o-mini in ByModel")
	}
}

func TestOrgTeamRoundTrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO orgs (id, name, created_at, budget, current_spend) VALUES ('org1', 'Acme', 1700000000, 500.0, 12.5)`)
	if err != nil {
		t.Fatalf("insert org: %v", err)
	}
	_, err = db.Exec(`INSERT INTO teams (id, org_id, name, created_at, budget, current_spend) VALUES ('team1', 'org1', 'Eng', 1700000000, 100.0, 5.0)`)
	if err != nil {
		t.Fatalf("insert team: %v", err)
	}

	var orgName string
	db.QueryRow(`SELECT name FROM orgs WHERE id='org1'`).Scan(&orgName) //nolint:errcheck
	if orgName != "Acme" {
		t.Errorf("org name = %q", orgName)
	}

	var teamOrgID string
	db.QueryRow(`SELECT org_id FROM teams WHERE id='team1'`).Scan(&teamOrgID) //nolint:errcheck
	if teamOrgID != "org1" {
		t.Errorf("team org_id = %q", teamOrgID)
	}
}

func TestZeroSpend(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO api_keys (key, name, created_at, last_used, expires_at, scopes, spend_limit, current_spend, allowed_cidrs, org_id, team_id, model_aliases, reset_period, last_reset)
		VALUES ('k1', '', 0, 0, 0, '[]', 0, 9.99, '[]', '', '', '{}', 'daily', 0)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := ZeroSpend(db, "api_keys", "k1"); err != nil {
		t.Fatalf("ZeroSpend: %v", err)
	}

	var spend float64
	var lastReset int64
	db.QueryRow(`SELECT current_spend, last_reset FROM api_keys WHERE key='k1'`).Scan(&spend, &lastReset) //nolint:errcheck
	if spend != 0 {
		t.Errorf("expected current_spend=0, got %f", spend)
	}
	if lastReset == 0 {
		t.Error("expected last_reset to be updated")
	}
}

func TestQueryResetCandidates(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Insert keys with and without reset_period.
	stmts := []string{
		`INSERT INTO api_keys (key, name, created_at, last_used, expires_at, scopes, spend_limit, current_spend, allowed_cidrs, org_id, team_id, model_aliases, reset_period, last_reset) VALUES ('k1', '', 0, 0, 0, '[]', 0, 0, '[]', '', '', '{}', 'daily', 0)`,
		`INSERT INTO api_keys (key, name, created_at, last_used, expires_at, scopes, spend_limit, current_spend, allowed_cidrs, org_id, team_id, model_aliases, reset_period, last_reset) VALUES ('k2', '', 0, 0, 0, '[]', 0, 0, '[]', '', '', '{}', '', 0)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	candidates, err := QueryResetCandidates(db, "api_keys")
	if err != nil {
		t.Fatalf("QueryResetCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].ID != "k1" {
		t.Errorf("expected k1, got %q", candidates[0].ID)
	}
	if candidates[0].ResetPeriod != "daily" {
		t.Errorf("expected daily, got %q", candidates[0].ResetPeriod)
	}
}
