// Package persistence provides a SQLite-backed store for proxy state.
// It manages API keys, organisations, and teams so that state survives restarts.
package persistence

import (
	"database/sql"
	"fmt"
	"strings"

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
