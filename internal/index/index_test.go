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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
