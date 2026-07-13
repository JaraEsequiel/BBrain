package mcp

// Acceptance suite for BBRAIN-2 (porter stemming), AC-5 half: proves the
// staleness "loud signal" actually reaches the mem_search MCP response body —
// the surface the calling agent sees — not just an internal Index/App field
// or a log line. Named TestAcceptance_AC5_TC5_n_... so it never collides with
// the plan's own per-task test, TestMemSearchOmitsStaleKeyWhenIndexCurrent
// (internal/mcp/tools_test.go).
//
// The index-level half of AC-5 (Open() detecting the mismatch) lives in
// internal/index/acceptance_test.go.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/JaraEsequiel/BBrain/internal/app"
	"github.com/JaraEsequiel/BBrain/internal/store"
)

// forceStaleOnDiskIndex rewrites a's on-disk index.db back to the pre-porter
// schema (no tokenize clause, user_version left at 0) with real content —
// simulating an index from before this change that has not been reindexed.
func forceStaleOnDiskIndex(t *testing.T, a *app.App) {
	t.Helper()
	db, err := sql.Open("sqlite", a.Brain.IndexPath())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`DROP TABLE facts_fts`,
		`CREATE VIRTUAL TABLE facts_fts USING fts5(fact_id UNINDEXED, path UNINDEXED, title, body, tags, topic_key, type UNINDEXED, scope UNINDEXED, project UNINDEXED, updated_at UNINDEXED, created_at UNINDEXED)`,
		`PRAGMA user_version = 0`,
		`INSERT INTO facts_fts(fact_id, path, title, body, tags, topic_key, type, scope, project, updated_at, created_at) VALUES ('f1', '/x/f1.md', 'Archive old sessions', 'cleanup', '', '', 'note', 'project', 'bbrain', '', '')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

// TC-5.1 (positive): a mem_search call against a pre-porter, un-reindexed
// on-disk index must surface the staleness notice in the response the agent
// actually receives.
func TestAcceptance_AC5_TC5_1_MemSearchSurfacesStaleNoticeToCaller(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{
		Type: "note", Title: "Archive old sessions", Body: "cleanup",
		Project: "bbrain", Scope: "project",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	forceStaleOnDiskIndex(t, a)

	raw, _ := json.Marshal(map[string]any{"query": "archive", "limit": 10})
	out, err := handleMemSearch(context.Background(), a, raw)
	if err != nil {
		t.Fatalf("handleMemSearch: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("handleMemSearch returned %T, want map[string]any", out)
	}
	if stale, _ := m["stale"].(bool); !stale {
		t.Fatalf("mem_search response = %+v, want \"stale\": true — the loud signal must reach the agent-visible response, not stay internal to Open()/*Index (AC-5 TC-5.1)", m)
	}
	if _, present := m["notice"]; !present {
		t.Fatalf("mem_search response = %+v, want a human-readable \"notice\" key alongside stale (AC-5 TC-5.1)", m)
	}
}

// TC-5.2 (negative): a normal, current-schema index must never false-positive
// the staleness signal, re-confirmed at the outermost (MCP) black-box surface.
func TestAcceptance_AC5_TC5_2_MemSearchOmitsStaleNoticeOnFreshIndex(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{
		Type: "note", Title: "Fresh fact", Body: "current schema",
		Project: "p", Scope: "project",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, _ := json.Marshal(map[string]any{"query": "fresh", "limit": 10})
	out, err := handleMemSearch(context.Background(), a, raw)
	if err != nil {
		t.Fatalf("handleMemSearch: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("handleMemSearch returned %T, want map[string]any", out)
	}
	if _, present := m["stale"]; present {
		t.Fatalf("mem_search response = %+v, want no \"stale\" key against a current index (AC-5 TC-5.2)", m)
	}
}
