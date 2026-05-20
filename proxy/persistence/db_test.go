package persistence

import (
	"testing"
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
