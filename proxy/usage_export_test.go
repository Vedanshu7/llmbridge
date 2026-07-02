package proxy

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/proxy/persistence"
	"github.com/Vedanshu7/llmbridge/types"
)

func newDBTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, err := NewServerWithDB(p, dbPath)
	if err != nil {
		t.Fatalf("NewServerWithDB: %v", err)
	}
	key, err := srv.keyStore.GenerateAPIKey([]string{"admin"})
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	return srv, key
}

func TestAdminUsageExportReturnsCSV(t *testing.T) {
	srv, key := newDBTestServer(t)

	now := time.Now()
	records := []persistence.UsageRecord{
		{ID: "r1", Key: "k1", OrgID: "org1", Model: "gpt-4o", Provider: "openai", PromptTokens: 100, CompletionTokens: 50, CostUSD: 0.01, Timestamp: now},
		{ID: "r2", Key: "k1", OrgID: "org1", Model: "gpt-4o", Provider: "openai", PromptTokens: 200, CompletionTokens: 80, CostUSD: 0.02, Timestamp: now.Add(time.Second)},
	}
	for _, rec := range records {
		if err := persistence.RecordUsage(srv.usageDB, rec); err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/usage/export", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("export: got %d, want 200: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd == "" {
		t.Error("expected Content-Disposition header to be set")
	}

	rows, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(rows) != 3 { // header + 2 records
		t.Fatalf("expected 3 CSV rows (header + 2 records), got %d: %+v", len(rows), rows)
	}
	if rows[0][0] != "id" {
		t.Errorf("unexpected header row: %+v", rows[0])
	}
	if rows[1][0] != "r1" || rows[2][0] != "r2" {
		t.Errorf("unexpected record order: %+v, %+v", rows[1], rows[2])
	}
}

func TestAdminUsageExportNoDBReturns503(t *testing.T) {
	p := &stubProvider{resp: &types.Response{}}
	srv := NewServer(p) // no persistent DB, so srv.usageDB is nil
	adminKey, err := srv.keyStore.GenerateAPIKey([]string{"admin"})
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/usage/export", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503 when no persistent database configured, got %d", w.Code)
	}
}

func TestAdminUsageExportRequiresAdminScope(t *testing.T) {
	p := &stubProvider{resp: &types.Response{}}
	srv, key := newTestServer(p) // "completion" scope only, no "admin"

	req := httptest.NewRequest(http.MethodGet, "/admin/usage/export", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403 for missing admin scope, got %d", w.Code)
	}
}
