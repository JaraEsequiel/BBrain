package index

// Acceptance suite for BBRAIN-2 (porter stemming). One test per TC-n.m from
// the accepted plan (.dev-tools/plans/BBRAIN-1/BBRAIN-2-porter-stemming.md),
// named TestAcceptance_AC<N>_TC<n.m>_... so these never collide with the
// plan's own fine-grained per-task TDD tests in index_test.go (e.g.
// TestSearchMatchesStemmedTerm, TestOpenDetectsStaleIndex,
// TestSearchDoesNotOverstemDistinctWords, TestPorterRegressionSet).
//
// This file exercises only the public Index surface (Open, Search, SearchAny,
// Reset, Stale) — black-box, not a duplicate of the plan's unit-level tests.

import (
	"database/sql"
	"strings"
	"testing"
)

// ---- AC-1: FTS5 schema uses tokenize = 'porter unicode61' ----

// TC-1.1 (positive): after Reset(), sqlite_master.sql for facts_fts contains
// "porter unicode61".
func TestAcceptance_AC1_TC1_1_SchemaUsesPorterTokenizerAfterReset(t *testing.T) {
	ix := openMem(t)
	must(t, ix.Reset())

	var ddl string
	if err := ix.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'facts_fts'`).Scan(&ddl); err != nil {
		t.Fatalf("read facts_fts DDL: %v", err)
	}
	if !strings.Contains(ddl, "porter unicode61") {
		t.Fatalf("facts_fts DDL = %q, want it to contain \"porter unicode61\" (AC-1 TC-1.1)", ddl)
	}
}

// TC-1.2 (negative): Open() against a brand-new/nonexistent index.db creates
// facts_fts with the porter schema directly — no silent fallback to the old
// implicit unicode61-only table.
func TestAcceptance_AC1_TC1_2_OpenAgainstFreshDBCreatesPorterSchemaDirectly(t *testing.T) {
	ix, err := Open(t.TempDir() + "/index.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	var ddl string
	if err := ix.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'facts_fts'`).Scan(&ddl); err != nil {
		t.Fatalf("read facts_fts DDL: %v", err)
	}
	if !strings.Contains(ddl, "porter unicode61") {
		t.Fatalf("facts_fts DDL = %q, want porter unicode61 on first Open() — no silent fallback to plain unicode61 (AC-1 TC-1.2)", ddl)
	}
}

// ---- shared regression-set fixture (AC-2, AC-3) ----

type acceptanceSeed struct{ id, title, body string }

// buildOldSchemaIndex creates an Index whose facts_fts was created under the
// pre-porter schema (no tokenize clause at all — FTS5's implicit unicode61
// default) — the "old tokenizer" baseline every AC-2/AC-3 case compares
// against, mirroring a real on-disk index from before this change.
func buildOldSchemaIndex(t *testing.T, corpus []acceptanceSeed) *Index {
	t.Helper()
	db, err := sql.Open("sqlite", t.TempDir()+"/old.db")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE facts_fts USING fts5(fact_id UNINDEXED, path UNINDEXED, title, body, tags, topic_key, type UNINDEXED, scope UNINDEXED, project UNINDEXED, updated_at UNINDEXED, created_at UNINDEXED)`); err != nil {
		t.Fatalf("create pre-porter schema: %v", err)
	}
	old := &Index{db: db}
	for _, s := range corpus {
		must(t, old.IndexFact(sampleFact(s.id, s.title, s.body, "note", "p"), "/x/"+s.id+".md"))
	}
	return old
}

// ---- AC-2: regression set recall >= old tokenizer on every query ----

// TC-2.1 (positive) + TC-2.2 (negative, folded in): for every term, porter
// recall must never be lower than the old tokenizer's — a non-zero old result
// must never drop, let alone drop to zero.
func TestAcceptance_AC2_TC2_1_RegressionRecallNeverBelowOldTokenizer(t *testing.T) {
	corpus := []acceptanceSeed{
		{"g1", "Reindexing the vault after a schema change", "bbrain reindex drops and rebuilds facts_fts"},
		{"g2", "Archiving distilled session summaries", "cleanup task, not a deletion"},
		{"g3", "Migrating the FTS5 schema safely", "no ALTER ADD COLUMN, drop and rebuild instead"},
		{"g4", "Testing the porter tokenizer change", "acceptance suite for BBRAIN-2"},
		{"g5", "Use JWT for stateless authentication", "tokens instead of server-side sessions"},
		{"g6", "Postgres as the relational database choice", "chosen over SQLite for the main app"},
	}
	old := buildOldSchemaIndex(t, corpus)
	defer old.Close()
	newIx := openMem(t)
	for _, s := range corpus {
		must(t, newIx.IndexFact(sampleFact(s.id, s.title, s.body, "note", "p"), "/x/"+s.id+".md"))
	}

	terms := []string{"reindex", "archiving", "migrate", "testing", "authentication", "database"}
	for _, term := range terms {
		oldRes, err := old.Search(term, 10, "", "")
		if err != nil {
			t.Fatalf("old.Search(%q): %v", term, err)
		}
		newRes, err := newIx.Search(term, 10, "", "")
		if err != nil {
			t.Fatalf("newIx.Search(%q): %v", term, err)
		}
		if len(newRes) < len(oldRes) {
			t.Errorf("%q: porter recall %d < old tokenizer recall %d — regression (AC-2 TC-2.1/TC-2.2)", term, len(newRes), len(oldRes))
		}
	}
}

// ---- AC-3: recall strictly > 0 on previously-zero inflected queries (English) ----

// TC-3.1 (positive): English inflected terms that return 0 under the old
// tokenizer must return >=1 once porter stems them to their shared root.
func TestAcceptance_AC3_TC3_1_EnglishInflectedRecallBecomesNonZero(t *testing.T) {
	corpus := []acceptanceSeed{
		{"h1", "Archive old sessions", "cleanup task for the vault"},
		{"h2", "Migrating a schema change on the FTS5 index", "drop and rebuild instead of ALTER"},
	}
	old := buildOldSchemaIndex(t, corpus)
	defer old.Close()
	newIx := openMem(t)
	for _, s := range corpus {
		must(t, newIx.IndexFact(sampleFact(s.id, s.title, s.body, "note", "p"), "/x/"+s.id+".md"))
	}

	// "archiving" only appears as "archive" in the corpus (gerund inflection);
	// "migrate" only appears as "migrating" (verb inflection) — both are 0
	// under the old unicode61-only tokenizer and must become >=1 once porter
	// stems both forms to the same root.
	cases := []string{"archiving", "migrate"}
	for _, term := range cases {
		oldRes, err := old.Search(term, 10, "", "")
		if err != nil {
			t.Fatalf("old.Search(%q): %v", term, err)
		}
		if len(oldRes) != 0 {
			t.Fatalf("test setup invalid: old.Search(%q) = %d, want 0 (need a genuinely zero baseline for AC-3)", term, len(oldRes))
		}
		newRes, err := newIx.Search(term, 10, "", "")
		if err != nil {
			t.Fatalf("newIx.Search(%q): %v", term, err)
		}
		if len(newRes) == 0 {
			t.Errorf("%q: porter recall = 0, want >=1 — inflected English term should stem-match (AC-3 TC-3.1)", term)
		}
	}
}

// TC-3.2 (negative): a Spanish inflected term ("decisiones" vs "decisión")
// that stays at 0 under both tokenizers must NOT be miscounted as a
// regression or a passing case — porter is English-only (Snowball), so this
// is an explicit, documented exclusion (D3), not a silent gap. The assertion
// is deliberately the weak AC-2 bar (>= old), never AC-3's strict->0 bar.
func TestAcceptance_AC3_TC3_2_SpanishInflectedNotMiscountedAsRegressionOrPass(t *testing.T) {
	corpus := []acceptanceSeed{
		{"s1", "Decisión de arquitectura: usar SQLite embebido", "decisiones tomadas durante el diseño del proyecto"},
	}
	old := buildOldSchemaIndex(t, corpus)
	defer old.Close()
	newIx := openMem(t)
	for _, s := range corpus {
		must(t, newIx.IndexFact(sampleFact(s.id, s.title, s.body, "note", "p"), "/x/"+s.id+".md"))
	}

	oldRes, err := old.Search("decisiones", 10, "", "")
	if err != nil {
		t.Fatalf("old.Search: %v", err)
	}
	newRes, err := newIx.Search("decisiones", 10, "", "")
	if err != nil {
		t.Fatalf("newIx.Search: %v", err)
	}
	if len(newRes) < len(oldRes) {
		t.Errorf("decisiones: porter recall %d < old %d — Spanish must never regress even though it is documented as unimproved (AC-2/D3, AC-3 TC-3.2)", len(newRes), len(oldRes))
	}
}

// ---- AC-4: SearchAny OR-fallback still works ----

// TC-4.1 (positive): the AND-then-OR fallback pattern (mirrors App.Search)
// still surfaces a partial match once the index is porter-tokenized.
func TestAcceptance_AC4_TC4_1_SearchAnyFallbackStillWorksUnderPorterIndex(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for auth", "stateless tokens", "decision", "p"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Postgres choice", "relational database", "decision", "p"), "/x/f2.md"))

	if res, _ := ix.Search("jwt database", 10, "", ""); len(res) != 0 {
		t.Fatalf("Search(AND) = %+v, want 0 (no single fact has both terms)", res)
	}
	res, err := ix.SearchAny("jwt database", 10, "", "")
	if err != nil {
		t.Fatalf("SearchAny: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("SearchAny(OR) = %+v, want 2 — AND-then-OR fallback (AC-4 TC-4.1) must still work once the index is porter-tokenized", res)
	}
}

// TC-4.2 (negative): terms that match nothing under either tokenizer must
// still return empty — stemming must not introduce false positives in the
// OR-fallback path.
func TestAcceptance_AC4_TC4_2_SearchAnyNoFalsePositivesOnNoMatch(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for auth", "stateless tokens", "decision", "p"), "/x/f1.md"))

	res, err := ix.SearchAny("kubernetes wombat", 10, "", "")
	if err != nil {
		t.Fatalf("SearchAny: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("SearchAny(no-match terms) = %+v, want 0 — porter stemming must not conjure a false positive (AC-4 TC-4.2)", res)
	}
}

// ---- AC-5: Open() detects a schema-version mismatch, surfaces a loud signal ----
//
// The index-level half of AC-5 — Open() must be able to tell the caller a
// pre-porter, un-reindexed index.db is stale. See internal/mcp/acceptance_test.go
// for the outer-surface half (the signal actually reaching the mem_search
// MCP response, not staying internal to *Index).

// TC-5.1 (positive): Open() against an on-disk index.db whose facts_fts was
// created under the old (no explicit tokenize) schema, with real content,
// must be detectable as stale by the caller.
func TestAcceptance_AC5_TC5_1_OpenDetectsStaleIndexAgainstOldSchema(t *testing.T) {
	path := t.TempDir() + "/index.db"

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	for _, stmt := range []string{
		`CREATE VIRTUAL TABLE facts_fts USING fts5(fact_id UNINDEXED, path UNINDEXED, title, body, tags, topic_key, type UNINDEXED, scope UNINDEXED, project UNINDEXED, updated_at UNINDEXED, created_at UNINDEXED)`,
		`INSERT INTO facts_fts(fact_id, path, title, body, tags, topic_key, type, scope, project, updated_at, created_at) VALUES ('f1', '/x/f1.md', 'Archive old sessions', 'cleanup', '', '', 'task', 'project', 'p', '', '')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	db.Close()

	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	if !ix.Stale() {
		t.Fatalf("Stale() = false, want true against a pre-porter on-disk index with real content (AC-5 TC-5.1)")
	}
}

// TC-5.2 (negative): a brand-new/nonexistent index.db (nothing to have gone
// stale yet) must not fire the mismatch signal.
func TestAcceptance_AC5_TC5_2_OpenDoesNotFlagFreshEmptyIndexAsStale(t *testing.T) {
	ix, err := Open(t.TempDir() + "/index.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	if ix.Stale() {
		t.Fatalf("Stale() = true, want false — a brand-new/nonexistent index.db has nothing stale to warn about (AC-5 TC-5.2)")
	}
}

// ---- AC-6: word-boundary assertions hold, no porter over-stemming false positives ----

// TC-6.1 (positive): existing exact-term matches must still hold — stemming
// is additive, not a replacement for exact matching.
func TestAcceptance_AC6_TC6_1_ExactTermMatchesStillHoldUnderPorter(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for auth", "stateless tokens", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Postgres choice", "relational database", "decision", "bbrain"), "/x/f2.md"))

	res, err := ix.Search("jwt", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].FactID != "f1" {
		t.Fatalf("Search(jwt) = %+v, want only f1 — exact matches must still hold under porter (AC-6 TC-6.1)", res)
	}
}

// TC-6.2 (negative): porter must not conflate distinct words that happen to
// share a prefix (stemming false positive).
//
// NOTE: the plan's own Task 5/AC-6 fixture used "university"/"universe" for
// this guard, on the untested assumption that porter wouldn't conflate them.
// Empirically verified against SQLite FTS5's actual porter tokenizer (see
// acceptance-test-author's re-dispatch report): it does conflate them, both
// reducing to stem "univers" — this is a well-documented false positive of
// the classic Porter (1980) algorithm, cited in Porter's own paper. Using
// that pair here would make this test fail once Task 1 lands *doing exactly
// what the plan says*, not because of a bug — an invalid guard. Replaced with
// "authentication"/"author", empirically confirmed non-conflating under the
// same tokenizer (see the re-dispatch report for the verification method).
func TestAcceptance_AC6_TC6_2_PorterDoesNotOverstemDistinctWords(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Authentication roadmap", "JWT and session tokens", "note", "p"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Author guidelines document", "style and formatting notes", "note", "p"), "/x/f2.md"))

	res, err := ix.Search("authentication", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Title != "Authentication roadmap" {
		t.Fatalf("Search(authentication) = %+v, want only the Authentication fact — porter must not conflate authentication/author (AC-6 TC-6.2)", res)
	}
}
