package app

import (
	"context"
	"strings"
	"testing"

	"bbrain/internal/index"
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func containsID(rs []index.Result, id string) bool {
	for _, r := range rs {
		if r.FactID == id {
			return true
		}
	}
	return false
}

func TestLinkThenWhyAndRelated(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f1, err := a.Save(store.SaveInput{Type: "architecture", Title: "Auth model", Body: "jwt",
		Project: "bbrain", Scope: "project"})
	must(t, err)
	f2, err := a.Save(store.SaveInput{Type: "decision", Title: "Session storage", Body: "redis",
		Project: "bbrain", Scope: "project"})
	must(t, err)

	if _, err := a.Link(f1.ID, f2.ID, "depends-on", "auth model assumes the session storage"); err != nil {
		t.Fatalf("Link: %v", err)
	}

	edges, err := a.Why(f1.ID, f2.ID)
	must(t, err)
	if len(edges) != 1 || edges[0].Relation != "depends-on" || edges[0].Why == "" {
		t.Fatalf("Why = %+v", edges)
	}
	// Symmetric for querying.
	rev, err := a.Why(f2.ID, f1.ID)
	must(t, err)
	if len(rev) != 1 {
		t.Fatalf("Why is not symmetric: %+v", rev)
	}

	out, err := a.Related(f1.ID)
	must(t, err)
	if len(out) != 1 || out[0].FactID != f2.ID || out[0].Direction != "out" {
		t.Fatalf("Related(f1) = %+v", out)
	}
	in, err := a.Related(f2.ID)
	must(t, err)
	if len(in) != 1 || in[0].FactID != f1.ID || in[0].Direction != "in" {
		t.Fatalf("Related(f2) = %+v", in)
	}
}

func TestReindexRebuildsEdges(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f1, err := a.Save(store.SaveInput{Type: "architecture", Title: "A", Body: "x", Project: "p", Scope: "project"})
	must(t, err)
	f2, err := a.Save(store.SaveInput{Type: "architecture", Title: "B", Body: "y", Project: "p", Scope: "project"})
	must(t, err)
	if _, err := a.Link(f1.ID, f2.ID, "relates", "linked"); err != nil {
		t.Fatal(err)
	}

	// A fresh App over the same root rebuilds the edge table from the .md alone.
	a2 := New(a.Brain.Root)
	if _, err := a2.Reindex(); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	edges, err := a2.Why(f1.ID, f2.ID)
	must(t, err)
	if len(edges) != 1 {
		t.Fatalf("edges after reindex = %+v, want 1 (links must rebuild from .md)", edges)
	}
}

func TestCandidatesExcludesSelfAndLinked(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f1, err := a.Save(store.SaveInput{Type: "decision", Title: "Use JWT for auth", Body: "stateless tokens",
		Project: "bbrain", Scope: "project"})
	must(t, err)
	f2, err := a.Save(store.SaveInput{Type: "decision", Title: "Auth token rotation", Body: "rotate jwt tokens",
		Project: "bbrain", Scope: "project"})
	must(t, err)

	cands, err := a.Candidates(f1.ID, 10)
	must(t, err)
	if !containsID(cands, f2.ID) {
		t.Fatalf("candidates should include the similar f2: %+v", cands)
	}
	if containsID(cands, f1.ID) {
		t.Fatalf("candidates must exclude the fact itself: %+v", cands)
	}

	if _, err := a.Link(f1.ID, f2.ID, "relates", "both about auth"); err != nil {
		t.Fatal(err)
	}
	cands2, err := a.Candidates(f1.ID, 10)
	must(t, err)
	if containsID(cands2, f2.ID) {
		t.Fatalf("candidates must exclude an already-linked fact: %+v", cands2)
	}
}

type appFakeRunner struct {
	out       string
	gotPrompt string
}

func (f *appFakeRunner) Run(ctx context.Context, prompt string) (string, error) {
	f.gotPrompt = prompt
	return f.out, nil
}

func TestWikiBuildWiringWithFakeRunner(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f1, err := a.Save(store.SaveInput{Type: "decision", Title: "Use JWT", Body: "jwt",
		Project: "shopapp", Scope: "project"})
	must(t, err)
	a.Runner = &appFakeRunner{out: `{"pages":[{"slug":"auth-model","category":"decisions","title":"Auth model","sources":["` + f1.ID + `"],"body":"# Auth model","change_reason":"created"}]}`}

	res, err := a.WikiBuild(context.Background(), WikiBuildOptions{})
	must(t, err)
	if len(res.Written) != 1 || res.Written[0] != "projects/shopapp/decisions/auth-model.md" {
		t.Fatalf("written = %+v", res.Written)
	}
}

func TestWikiBuildFiltersByProject(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Alpha", Body: "a", Project: "shopapp", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Beta", Body: "b", Project: "datacli", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	fr := &appFakeRunner{out: `{"pages":[]}`}
	a.Runner = fr
	if _, err := a.WikiBuild(context.Background(), WikiBuildOptions{Project: "datacli"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fr.gotPrompt, "title: Alpha") {
		t.Fatalf("project filter leaked shopapp fact into prompt:\n%s", fr.gotPrompt)
	}
	if !strings.Contains(fr.gotPrompt, "title: Beta") {
		t.Fatalf("project filter dropped the datacli fact:\n%s", fr.gotPrompt)
	}
}
