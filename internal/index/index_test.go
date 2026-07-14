package index

import (
	"database/sql"
	"testing"

	"github.com/JaraEsequiel/BBrain/internal/fact"
)

func openMem(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

func sampleFact(id, title, body, typ, project string) fact.Fact {
	return fact.Fact{ID: id, Title: title, Body: body, Type: typ,
		Scope: "project", Project: project}
}

func TestSearchFindsByTitleAndBody(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for auth", "stateless tokens", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Postgres choice", "relational database", "decision", "bbrain"), "/x/f2.md"))

	res, err := ix.Search("jwt", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].FactID != "f1" {
		t.Fatalf("Search(jwt) = %+v, want only f1", res)
	}
	if res[0].Path != "/x/f1.md" || res[0].Title != "Use JWT for auth" {
		t.Fatalf("result fields wrong: %+v", res[0])
	}
}

func TestIndexFactIsUpsert(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Old title", "old body", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f1", "New title carrot", "new body", "decision", "bbrain"), "/x/f1.md"))

	if res, _ := ix.Search("carrot", 10, "", ""); len(res) != 1 {
		t.Fatalf("Search(carrot) = %+v, want 1 (new content)", res)
	}
	if res, _ := ix.Search("old", 10, "", ""); len(res) != 0 {
		t.Fatalf("Search(old) = %+v, want 0 (old content gone)", res)
	}
}

func TestSearchQueryWithSpecialCharsDoesNotError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Auth (v2) AND tokens", "body", "decision", "bbrain"), "/x/f1.md"))
	if _, err := ix.Search(`auth (v2) AND "tokens`, 10, "", ""); err != nil {
		t.Fatalf("Search with FTS5 special chars should not error: %v", err)
	}
}

func TestSearchNoMatchReturnsNonNilSlice(t *testing.T) {
	ix := openMem(t)
	// A zero-match search (and a blank query) must yield a non-nil empty slice so
	// the MCP layer serializes "results": [] rather than null — null reads as error.
	for _, q := range []string{"nothingmatchesthis", ""} {
		got, err := ix.Search(q, 10, "", "")
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if got == nil {
			t.Fatalf("Search(%q) returned nil slice; want non-nil empty", q)
		}
		if len(got) != 0 {
			t.Fatalf("Search(%q) = %v; want empty", q, got)
		}
	}
}

func TestResetEmptiesIndex(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT", "body", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.Reset())
	if res, _ := ix.Search("jwt", 10, "", ""); len(res) != 0 {
		t.Fatalf("after Reset, Search = %+v, want empty", res)
	}
}

func TestIndexLinksAndWhy(t *testing.T) {
	ix := openMem(t)
	f := sampleFact("a", "Auth model", "body", "architecture", "p")
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(f))

	edges, err := ix.Why("a", "b")
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	if len(edges) != 1 || edges[0].SrcID != "a" || edges[0].DstID != "b" ||
		edges[0].Relation != "depends-on" || edges[0].Why != "needs b" {
		t.Fatalf("Why(a,b) = %+v", edges)
	}
	// The reverse query returns the same edge (relation is symmetric for querying).
	rev, err := ix.Why("b", "a")
	if err != nil {
		t.Fatalf("Why reverse: %v", err)
	}
	if len(rev) != 1 {
		t.Fatalf("Why(b,a) = %+v, want 1", rev)
	}
}

func TestNeighborsReturnsInAndOutEdges(t *testing.T) {
	ix := openMem(t)
	fa := sampleFact("a", "A", "x", "decision", "p")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "relates", Why: "r"}}
	must(t, ix.IndexLinks(fa))
	fc := sampleFact("c", "C", "z", "decision", "p")
	fc.Links = []fact.Link{{Target: "[[a]]", Relation: "supersedes", Why: "s"}}
	must(t, ix.IndexLinks(fc))

	ns, err := ix.Neighbors("a")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 2 {
		t.Fatalf("Neighbors(a) = %+v, want 2 (out to b, in from c)", ns)
	}
	var dirs = map[string]string{}
	for _, n := range ns {
		dirs[n.FactID] = n.Direction
	}
	if dirs["b"] != "out" || dirs["c"] != "in" {
		t.Fatalf("directions wrong: %+v", dirs)
	}
}

func TestIndexLinksIsUpsert(t *testing.T) {
	ix := openMem(t)
	f := sampleFact("a", "A", "x", "decision", "p")
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "relates", Why: "first"}}
	must(t, ix.IndexLinks(f))
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "conflicts-with", Why: "second"}}
	must(t, ix.IndexLinks(f))

	edges, _ := ix.Why("a", "b")
	if len(edges) != 1 || edges[0].Relation != "conflicts-with" || edges[0].Why != "second" {
		t.Fatalf("re-indexing must replace edges: %+v", edges)
	}
}

func TestResetAlsoEmptiesLinks(t *testing.T) {
	ix := openMem(t)
	f := sampleFact("a", "A", "x", "decision", "p")
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "relates", Why: "r"}}
	must(t, ix.IndexLinks(f))
	must(t, ix.Reset())
	if edges, _ := ix.Why("a", "b"); len(edges) != 0 {
		t.Fatalf("after Reset, Why = %+v, want empty", edges)
	}
}

func TestSearchAnyMatchesAnyTerm(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for auth", "stateless tokens", "decision", "p"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Postgres choice", "relational database", "decision", "p"), "/x/f2.md"))

	// AND search (Search) for two terms in different facts matches nothing.
	if res, _ := ix.Search("jwt database", 10, "", ""); len(res) != 0 {
		t.Fatalf("Search(AND) = %+v, want 0", res)
	}
	// OR search (SearchAny) matches both.
	res, err := ix.SearchAny("jwt database", 10, "", "")
	if err != nil {
		t.Fatalf("SearchAny: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("SearchAny = %+v, want 2", res)
	}
}

func TestBuildMatchHelpers(t *testing.T) {
	cases := []struct {
		name, in, and, or string
	}{
		{"two terms", "jwt database", `"jwt" "database"`, `"jwt" OR "database"`},
		{"single term", "postgres", `"postgres"`, `"postgres"`},
		{"embedded quote", `a"b`, `"a""b"`, `"a""b"`},
		{"collapses whitespace", "  jwt   auth  ", `"jwt" "auth"`, `"jwt" OR "auth"`},
		{"blank query", "   ", "", ""},
	}
	for _, c := range cases {
		if got := buildMatch(c.in); got != c.and {
			t.Errorf("%s: buildMatch(%q) = %q, want %q", c.name, c.in, got, c.and)
		}
		if got := buildMatchAny(c.in); got != c.or {
			t.Errorf("%s: buildMatchAny(%q) = %q, want %q", c.name, c.in, got, c.or)
		}
	}
}

func TestDeleteFactRemovesFromSearchAndLinks(t *testing.T) {
	ix, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	f := fact.Fact{ID: "f1", Title: "JWT auth", Body: "tokens", Links: []fact.Link{{Target: "[[f2]]", Relation: "relates", Why: "x"}}}
	if err := ix.IndexFact(f, "p"); err != nil {
		t.Fatal(err)
	}
	if err := ix.IndexLinks(f); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteFact("f1"); err != nil {
		t.Fatal(err)
	}
	res, _ := ix.Search("jwt", 10, "", "")
	if len(res) != 0 {
		t.Fatalf("search still returns deleted fact: %v", res)
	}
	if n, _ := ix.Neighbors("f2"); len(n) != 0 {
		t.Fatalf("links survived delete: %v", n)
	}
}

func TestLastSavedAt(t *testing.T) {
	ix, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	mk := func(id, project, updated string) fact.Fact {
		return fact.Fact{ID: id, Title: "t", Project: project, UpdatedAt: updated, CreatedAt: updated}
	}
	for _, f := range []fact.Fact{
		mk("a", "BBrain", "2026-06-24T10:00:00Z"),
		mk("b", "BBrain", "2026-06-24T12:00:00Z"),
		mk("c", "Other", "2026-06-24T15:00:00Z"),
	} {
		if err := ix.IndexFact(f, f.ID+".md"); err != nil {
			t.Fatal(err)
		}
	}

	ts, ok, err := ix.LastSavedAt("BBrain")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || ts != "2026-06-24T12:00:00Z" {
		t.Fatalf("LastSavedAt(BBrain) = %q,%v; want 2026-06-24T12:00:00Z,true", ts, ok)
	}
	if _, ok, _ := ix.LastSavedAt("Nope"); ok {
		t.Fatal("LastSavedAt(Nope) ok=true; want false")
	}
}

func TestResetRecreatesSchema(t *testing.T) {
	ix, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	if err := ix.IndexFact(fact.Fact{ID: "a", Project: "P", UpdatedAt: "2026-06-24T10:00:00Z"}, "a.md"); err != nil {
		t.Fatal(err)
	}
	if err := ix.Reset(); err != nil {
		t.Fatal(err)
	}
	// Reset clears all rows...
	if _, ok, _ := ix.LastSavedAt("P"); ok {
		t.Fatal("after Reset, expected no rows")
	}
	// ...and leaves a usable, current-schema table behind.
	if err := ix.IndexFact(fact.Fact{ID: "b", Project: "P", UpdatedAt: "2026-06-24T11:00:00Z"}, "b.md"); err != nil {
		t.Fatalf("IndexFact after Reset: %v", err)
	}
}

func TestSearchMatchesStemmedTerm(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Archive old sessions", "cleanup task for the vault", "task", "p"), "/x/f1.md"))

	res, err := ix.Search("archiving", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("Search(archiving) = %+v, want 1 match via porter stemming of \"archive\"", res)
	}
}

func TestResetStampsSchemaVersion(t *testing.T) {
	ix := openMem(t)
	must(t, ix.Reset())

	var version int
	if err := ix.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	if version != indexSchemaVersion {
		t.Fatalf("user_version = %d, want %d", version, indexSchemaVersion)
	}
}

func TestOpenDetectsStaleIndex(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/index.db"

	// Build an index under the pre-porter (old) schema, with real content, and
	// never stamp a schema version — simulates an on-disk index from before
	// this change that hasn't been reindexed.
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
		t.Fatalf("Stale() = false, want true against a pre-porter on-disk index with content")
	}
}

func TestOpenIgnoresFreshEmptyIndex(t *testing.T) {
	ix, err := Open(t.TempDir() + "/index.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	if ix.Stale() {
		t.Fatalf("Stale() = true, want false — a brand-new empty index has nothing stale to warn about")
	}
}

func TestSearchDoesNotOverstemDistinctWords(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for authentication", "stateless tokens", "note", "p"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Author guidelines for the changelog", "writing style notes", "note", "p"), "/x/f2.md"))

	res, err := ix.Search("authentication", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Title != "Use JWT for authentication" {
		t.Fatalf("Search(authentication) = %+v, want only the authentication fact — porter must not conflate authentication/author", res)
	}
}

func TestPorterRegressionSet(t *testing.T) {
	type seedFact struct{ id, title, body string }
	corpus := []seedFact{
		{"f1", "Migrating a schema change on the FTS5 index", "no ALTER ADD COLUMN available, drop and rebuild instead"},
		{"f2", "mem_search fallback to SearchAny when AND returns zero", "the OR-based fallback search path for broad queries"},
		{"f3", "Archiving old sessions from the vault", "cleanup task, not a deletion"},
		{"f4", "Reindexing the vault after a schema change", "bbrain rebuilds facts_fts from disk"},
		{"f5", "Decisión de arquitectura: usar SQLite embebido", "decisiones tomadas durante el diseño del proyecto"},
		{"f6", "Use JWT for authenticating requests", "tokens instead of server-side sessions"},
		{"f7", "Choosing Postgres over other relational databases", "chosen over SQLite for the main app"},
		{"f8", "Testing strategies for the index package", "table-driven tests for buildMatch helpers"},
	}

	buildOld := func(t *testing.T) *Index {
		t.Helper()
		path := t.TempDir() + "/index.db"
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("sql.Open: %v", err)
		}
		if _, err := db.Exec(`CREATE VIRTUAL TABLE facts_fts USING fts5(fact_id UNINDEXED, path UNINDEXED, title, body, tags, topic_key, type UNINDEXED, scope UNINDEXED, project UNINDEXED, updated_at UNINDEXED, created_at UNINDEXED)`); err != nil {
			t.Fatalf("create old schema: %v", err)
		}
		old := &Index{db: db}
		for _, f := range corpus {
			must(t, old.IndexFact(sampleFact(f.id, f.title, f.body, "note", "p"), "/x/"+f.id+".md"))
		}
		return old
	}

	old := buildOld(t)
	defer old.Close()
	newIx := openMem(t)
	for _, f := range corpus {
		must(t, newIx.IndexFact(sampleFact(f.id, f.title, f.body, "note", "p"), "/x/"+f.id+".md"))
	}

	cases := []struct {
		term    string
		english bool // false = Spanish, exempt from the "strictly higher" bar (D3)
	}{
		{"migrate", true},
		{"reindex", true},
		{"archive", true},
		{"authentication", true},
		{"database", true},
		{"test", true},
		{"decisiones", false},
		{"decisión", false},
	}

	for _, c := range cases {
		oldRes, err := old.Search(c.term, 10, "", "")
		if err != nil {
			t.Fatalf("old.Search(%q): %v", c.term, err)
		}
		newRes, err := newIx.Search(c.term, 10, "", "")
		if err != nil {
			t.Fatalf("newIx.Search(%q): %v", c.term, err)
		}

		if len(newRes) < len(oldRes) {
			t.Errorf("%q: porter recall %d < old recall %d — regression (AC-2)", c.term, len(newRes), len(oldRes))
		}
		if c.english && len(oldRes) == 0 && len(newRes) == 0 {
			t.Errorf("%q: expected porter to find a stemmed match for this English inflected term (AC-3), got 0 under both tokenizers", c.term)
		}
	}
}

// BBRAIN-4 (Search scoping): AC-1 project filter, AC-2 type filter, AC-3 no
// filter behaves identically to today, AC-4 zero-match filter returns empty,
// not an error.
func TestSearchFiltersByProjectAndType(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "shared term", "body", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "shared term", "body", "preference", "vexforge"), "/x/f2.md"))

	// AC-1 TC-1.1: project filter excludes other projects
	res, err := ix.Search("shared", 10, "bbrain", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].FactID != "f1" {
		t.Fatalf("AC-1 TC-1.1 project filter: want only f1, got %+v", res)
	}

	// AC-2 TC-2.1: type filter excludes other types
	res, err = ix.Search("shared", 10, "", "preference")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].FactID != "f2" {
		t.Fatalf("AC-2 TC-2.1 type filter: want only f2, got %+v", res)
	}

	// AC-1+AC-2 combined: both filters apply conjunctively (project AND type)
	must(t, ix.IndexFact(sampleFact("f3", "shared term", "body", "decision", "vexforge"), "/x/f3.md"))
	res, err = ix.Search("shared", 10, "vexforge", "preference")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].FactID != "f2" {
		t.Fatalf("combined project+type filter: want only f2 (vexforge+preference), got %+v", res)
	}

	// AC-3 TC-3.1: no filter → all three facts, identical to pre-change behavior
	res, err = ix.Search("shared", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("AC-3 TC-3.1 no filter: want all 3 facts, got %+v", res)
	}

	// AC-4 TC-4.1/TC-4.2: project filter with zero matches → empty, not error
	res, err = ix.Search("shared", 10, "nonexistent", "")
	if err != nil {
		t.Fatalf("AC-4 TC-4.2 Search with nonexistent project: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("AC-4 TC-4.1 zero-match project filter: want empty, got %+v", res)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
