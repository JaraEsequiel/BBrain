package index

import (
	"testing"

	"bbrain/internal/fact"
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

	res, err := ix.Search("jwt", 10)
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

	if res, _ := ix.Search("carrot", 10); len(res) != 1 {
		t.Fatalf("Search(carrot) = %+v, want 1 (new content)", res)
	}
	if res, _ := ix.Search("old", 10); len(res) != 0 {
		t.Fatalf("Search(old) = %+v, want 0 (old content gone)", res)
	}
}

func TestSearchQueryWithSpecialCharsDoesNotError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Auth (v2) AND tokens", "body", "decision", "bbrain"), "/x/f1.md"))
	if _, err := ix.Search(`auth (v2) AND "tokens`, 10); err != nil {
		t.Fatalf("Search with FTS5 special chars should not error: %v", err)
	}
}

func TestClearEmptiesIndex(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT", "body", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.Clear())
	if res, _ := ix.Search("jwt", 10); len(res) != 0 {
		t.Fatalf("after Clear, Search = %+v, want empty", res)
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

func TestClearAlsoEmptiesLinks(t *testing.T) {
	ix := openMem(t)
	f := sampleFact("a", "A", "x", "decision", "p")
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "relates", Why: "r"}}
	must(t, ix.IndexLinks(f))
	must(t, ix.Clear())
	if edges, _ := ix.Why("a", "b"); len(edges) != 0 {
		t.Fatalf("after Clear, Why = %+v, want empty", edges)
	}
}

func TestSearchAnyMatchesAnyTerm(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for auth", "stateless tokens", "decision", "p"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Postgres choice", "relational database", "decision", "p"), "/x/f2.md"))

	// AND search (Search) for two terms in different facts matches nothing.
	if res, _ := ix.Search("jwt database", 10); len(res) != 0 {
		t.Fatalf("Search(AND) = %+v, want 0", res)
	}
	// OR search (SearchAny) matches both.
	res, err := ix.SearchAny("jwt database", 10)
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
