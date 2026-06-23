package app

import (
	"testing"

	"bbrain/internal/store"
)

func TestSaveThenSearch(t *testing.T) {
	a := New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{
		Type: "decision", Title: "Use JWT for auth", Body: "stateless tokens",
		Project: "bbrain", Scope: "project",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := a.Search("jwt", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Title != "Use JWT for auth" {
		t.Fatalf("Search = %+v", res)
	}
}

func TestReindexRebuildsFromDisk(t *testing.T) {
	a := New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save(store.SaveInput{
		Type: "decision", Title: "Use Postgres", Body: "relational",
		Project: "bbrain", Scope: "project",
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a thrown-away index: a fresh App over the same root reindexes.
	a2 := New(a.Brain.Root)
	n, err := a2.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if n != 1 {
		t.Fatalf("Reindex count = %d, want 1", n)
	}
	res, _ := a2.Search("postgres", 10)
	if len(res) != 1 {
		t.Fatalf("Search after reindex = %+v, want 1", res)
	}
}

func TestSearchOnUninitializedBrainReturnsNoResults(t *testing.T) {
	a := New(t.TempDir()) // note: no Init() — .bbrain/ does not exist
	res, err := a.Search("anything", 10)
	if err != nil {
		t.Fatalf("Search on uninitialized brain should not error, got: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("Search on empty index = %+v, want no results", res)
	}
}
