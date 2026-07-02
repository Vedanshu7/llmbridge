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
		// Migration for per-key model aliases.
		`ALTER TABLE api_keys ADD COLUMN model_aliases TEXT NOT NULL DEFAULT '{}'`,
		// Migration for budget reset periods.
		`ALTER TABLE api_keys ADD COLUMN reset_period TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE api_keys ADD COLUMN last_reset INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orgs ADD COLUMN reset_period TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orgs ADD COLUMN last_reset INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE teams ADD COLUMN reset_period TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE teams ADD COLUMN last_reset INTEGER NOT NULL DEFAULT 0`,
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

// ---- Budget reset helpers ----

// ZeroSpend resets current_spend to 0 and updates last_reset to now for the
// given row in table (one of "api_keys", "orgs", "teams") identified by id.
func ZeroSpend(db *sql.DB, table, id string) error {
	now := time.Now().Unix()
	var query string
	switch table {
	case "api_keys":
		query = `UPDATE api_keys SET current_spend = 0, last_reset = ? WHERE key = ?`
	case "orgs":
		query = `UPDATE orgs SET current_spend = 0, last_reset = ? WHERE id = ?`
	case "teams":
		query = `UPDATE teams SET current_spend = 0, last_reset = ? WHERE id = ?`
	default:
		return fmt.Errorf("persistence: ZeroSpend: unknown table %q", table)
	}
	if _, err := db.Exec(query, now, id); err != nil {
		return fmt.Errorf("persistence: ZeroSpend: %w", err)
	}
	return nil
}

// ResetCandidate is a row that may need its spend budget reset.
type ResetCandidate struct {
	ID          string
	ResetPeriod string
	LastReset   time.Time
}

// QueryResetCandidates returns all rows from table that have a non-empty reset_period.
func QueryResetCandidates(db *sql.DB, table string) ([]ResetCandidate, error) {
	var idCol, query string
	switch table {
	case "api_keys":
		idCol = "key"
		query = `SELECT key, reset_period, last_reset FROM api_keys WHERE reset_period != ''`
	case "orgs":
		idCol = "id"
		query = `SELECT id, reset_period, last_reset FROM orgs WHERE reset_period != ''`
	case "teams":
		idCol = "id"
		query = `SELECT id, reset_period, last_reset FROM teams WHERE reset_period != ''`
	default:
		return nil, fmt.Errorf("persistence: QueryResetCandidates: unknown table %q", table)
	}
	_ = idCol
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("persistence: QueryResetCandidates: %w", err)
	}
	defer rows.Close()
	var out []ResetCandidate
	for rows.Next() {
		var c ResetCandidate
		var lastReset int64
		if err := rows.Scan(&c.ID, &c.ResetPeriod, &lastReset); err != nil {
			return nil, fmt.Errorf("persistence: QueryResetCandidates scan: %w", err)
		}
		if lastReset > 0 {
			c.LastReset = time.Unix(lastReset, 0)
		}
		out = append(out, c)
	}
	return out, rows.Err()
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

// QueryUsageRecords returns raw usage_records rows matching the filter,
// ordered by timestamp ascending. Unlike QueryUsage, which returns per-model
// aggregates, this is used for CSV/audit export of individual requests.
func QueryUsageRecords(db *sql.DB, f UsageFilter) ([]UsageRecord, error) {
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
		`SELECT id, key, org_id, team_id, model, provider, prompt_tokens, completion_tokens, cost_usd, ts
		 FROM usage_records`+where+`
		 ORDER BY ts ASC`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("persistence: query usage records: %w", err)
	}
	defer rows.Close()

	var out []UsageRecord
	for rows.Next() {
		var rec UsageRecord
		var ts int64
		if err := rows.Scan(&rec.ID, &rec.Key, &rec.OrgID, &rec.TeamID, &rec.Model, &rec.Provider,
			&rec.PromptTokens, &rec.CompletionTokens, &rec.CostUSD, &ts); err != nil {
			return nil, fmt.Errorf("persistence: query usage records scan: %w", err)
		}
		rec.Timestamp = time.Unix(ts, 0)
		out = append(out, rec)
	}
	return out, rows.Err()
}
