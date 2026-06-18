// Package persistence provides a SQLite-backed store for proxy state.
// It manages API keys, organisations, teams, and usage records so that state
// survives restarts.
package persistence

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// Open opens (or creates) a SQLite database at path and runs all migrations.
// Use ":memory:" for an in-process ephemeral database (useful in tests).
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("persistence: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer; one connection avoids SQLITE_BUSY
	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate runs idempotent DDL statements to bring the schema up to date.
func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS api_keys (
			key           TEXT PRIMARY KEY,
			name          TEXT    NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL,
			last_used     INTEGER NOT NULL,
			expires_at    INTEGER NOT NULL,
			scopes        TEXT    NOT NULL DEFAULT '[]',
			spend_limit   REAL    NOT NULL DEFAULT 0,
			current_spend REAL    NOT NULL DEFAULT 0,
			allowed_cidrs TEXT    NOT NULL DEFAULT '[]',
			org_id        TEXT    NOT NULL DEFAULT '',
			team_id       TEXT    NOT NULL DEFAULT ''
		)`,
		// Migration for existing databases that predate the name column.
		`ALTER TABLE api_keys ADD COLUMN name TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS orgs (
			id            TEXT PRIMARY KEY,
			name          TEXT    NOT NULL,
			created_at    INTEGER NOT NULL,
			budget        REAL    NOT NULL DEFAULT 0,
			current_spend REAL    NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS teams (
			id            TEXT PRIMARY KEY,
			org_id        TEXT    NOT NULL,
			name          TEXT    NOT NULL,
			created_at    INTEGER NOT NULL,
			budget        REAL    NOT NULL DEFAULT 0,
			current_spend REAL    NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS usage_records (
			id                TEXT    PRIMARY KEY,
			key               TEXT    NOT NULL DEFAULT '',
			org_id            TEXT    NOT NULL DEFAULT '',
			team_id           TEXT    NOT NULL DEFAULT '',
			model             TEXT    NOT NULL DEFAULT '',
			provider          TEXT    NOT NULL DEFAULT '',
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			cost_usd          REAL    NOT NULL DEFAULT 0,
			ts                INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS usage_records_key_ts ON usage_records (key, ts)`,
		`CREATE INDEX IF NOT EXISTS usage_records_org_ts  ON usage_records (org_id, ts)`,
		`CREATE INDEX IF NOT EXISTS usage_records_team_ts ON usage_records (team_id, ts)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			// ALTER TABLE ADD COLUMN fails with "duplicate column name" on new
			// databases that already have the column — that is safe to ignore.
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return fmt.Errorf("persistence: migrate: %w", err)
		}
	}
	return nil
}

// UsageRecord is a single completed-request entry written to usage_records.
type UsageRecord struct {
	ID               string
	Key              string
	OrgID            string
	TeamID           string
	Model            string
	Provider         string
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	Timestamp        time.Time
}

// RecordUsage inserts a UsageRecord into the usage_records table.
// Errors are non-fatal — callers should log and continue.
func RecordUsage(db *sql.DB, rec UsageRecord) error {
	_, err := db.Exec(
		`INSERT INTO usage_records
			(id, key, org_id, team_id, model, provider, prompt_tokens, completion_tokens, cost_usd, ts)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Key, rec.OrgID, rec.TeamID, rec.Model, rec.Provider,
		rec.PromptTokens, rec.CompletionTokens, rec.CostUSD, rec.Timestamp.Unix(),
	)
	if err != nil {
		return fmt.Errorf("persistence: record usage: %w", err)
	}
	return nil
}

// UsageFilter controls which usage_records are included in a QueryUsage call.
// Zero values mean "no filter for this field".
type UsageFilter struct {
	Key    string
	OrgID  string
	TeamID string
	From   int64 // unix timestamp; 0 = no lower bound
	To     int64 // unix timestamp; 0 = no upper bound
}

// ModelUsage holds aggregated usage for a single model.
type ModelUsage struct {
	Requests         int     `json:"requests"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

// UsageSummary is the aggregate result returned by QueryUsage.
type UsageSummary struct {
	TotalRequests         int                    `json:"total_requests"`
	TotalPromptTokens     int                    `json:"total_prompt_tokens"`
	TotalCompletionTokens int                    `json:"total_completion_tokens"`
	TotalCostUSD          float64                `json:"total_cost_usd"`
	ByModel               map[string]*ModelUsage `json:"by_model"`
}

// QueryUsage returns aggregated usage statistics matching the given filter.
func QueryUsage(db *sql.DB, f UsageFilter) (*UsageSummary, error) {
	var conds []string
	var args []interface{}

	if f.Key != "" {
		conds = append(conds, "key = ?")
		args = append(args, f.Key)
	}
	if f.OrgID != "" {
		conds = append(conds, "org_id = ?")
		args = append(args, f.OrgID)
	}
	if f.TeamID != "" {
		conds = append(conds, "team_id = ?")
		args = append(args, f.TeamID)
	}
	if f.From > 0 {
		conds = append(conds, "ts >= ?")
		args = append(args, f.From)
	}
	if f.To > 0 {
		conds = append(conds, "ts <= ?")
		args = append(args, f.To)
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	rows, err := db.Query(
		`SELECT model, COUNT(*) AS requests,
		        SUM(prompt_tokens), SUM(completion_tokens), SUM(cost_usd)
		 FROM usage_records`+where+`
		 GROUP BY model`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("persistence: query usage: %w", err)
	}
	defer rows.Close()

	summary := &UsageSummary{ByModel: make(map[string]*ModelUsage)}
	for rows.Next() {
		var model string
		var mu ModelUsage
		if err := rows.Scan(&model, &mu.Requests, &mu.PromptTokens, &mu.CompletionTokens, &mu.CostUSD); err != nil {
			return nil, fmt.Errorf("persistence: query usage scan: %w", err)
		}
		summary.ByModel[model] = &mu
		summary.TotalRequests += mu.Requests
		summary.TotalPromptTokens += mu.PromptTokens
		summary.TotalCompletionTokens += mu.CompletionTokens
		summary.TotalCostUSD += mu.CostUSD
	}
	return summary, rows.Err()
}
