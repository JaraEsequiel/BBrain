package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bbrain/internal/fact"
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

// linkRunner emits a link proposal only for the source fact's prompt.
type linkRunner struct{ srcID, dstID string }

func (r *linkRunner) Run(ctx context.Context, prompt string) (string, error) {
	if strings.Contains(prompt, "## Source fact\n### "+r.srcID+"\n") {
		return `{"links":[{"dst":"` + r.dstID + `","relation":"relates","why":"both about jwt"}]}`, nil
	}
	return `{"links":[]}`, nil
}

func TestWikiLinkWritesEdge(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	src, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT access tokens", Body: "access", Project: "shopapp", Scope: "project"})
	must(t, err)
	dst, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT refresh tokens", Body: "refresh", Project: "shopapp", Scope: "project"})
	must(t, err)
	a.Runner = &linkRunner{srcID: src.ID, dstID: dst.ID}

	res, err := a.WikiLink(context.Background(), WikiLinkOptions{})
	must(t, err)
	if len(res.Written) != 1 || res.Written[0].Src != src.ID || res.Written[0].Dst != dst.ID || res.Written[0].Relation != "relates" {
		t.Fatalf("written = %+v", res.Written)
	}
	// The link must be on the source fact's .md.
	got, ok, err := a.Store.Get(src.ID)
	must(t, err)
	if !ok || len(got.Links) != 1 || fact.LinkTargetID(got.Links[0].Target) != dst.ID {
		t.Fatalf("source links = %+v", got.Links)
	}
}

func TestWikiLinkDryRunWritesNothing(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	src, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT access tokens", Body: "access", Project: "shopapp", Scope: "project"})
	must(t, err)
	dst, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT refresh tokens", Body: "refresh", Project: "shopapp", Scope: "project"})
	must(t, err)
	a.Runner = &linkRunner{srcID: src.ID, dstID: dst.ID}

	res, err := a.WikiLink(context.Background(), WikiLinkOptions{DryRun: true})
	must(t, err)
	if !res.DryRun || len(res.Written) != 1 {
		t.Fatalf("dry-run result = %+v", res)
	}
	got, _, _ := a.Store.Get(src.ID)
	if len(got.Links) != 0 {
		t.Fatalf("dry-run wrote a link: %+v", got.Links)
	}
}

func TestWikiLinkIsIdempotent(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	src, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT access tokens", Body: "access", Project: "shopapp", Scope: "project"})
	must(t, err)
	dst, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT refresh tokens", Body: "refresh", Project: "shopapp", Scope: "project"})
	must(t, err)
	a.Runner = &linkRunner{srcID: src.ID, dstID: dst.ID}

	if _, err := a.WikiLink(context.Background(), WikiLinkOptions{}); err != nil {
		t.Fatal(err)
	}
	// Second run: dst is already linked, so a.Candidates drops it -> no proposal,
	// nothing written, and (if it were re-proposed) it would be skipped.
	res, err := a.WikiLink(context.Background(), WikiLinkOptions{})
	must(t, err)
	if len(res.Written) != 0 {
		t.Fatalf("second run wrote %+v, want nothing", res.Written)
	}
	got, _, _ := a.Store.Get(src.ID)
	if len(got.Links) != 1 {
		t.Fatalf("idempotency broken: source links = %+v", got.Links)
	}
}

func TestWikiLintReportsAndFixes(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	x, err := a.Save(store.SaveInput{Type: "decision", Title: "Alpha", Body: "a", Project: "p", Scope: "project"})
	must(t, err)
	y, err := a.Save(store.SaveInput{Type: "decision", Title: "Beta", Body: "b", Project: "p", Scope: "project"})
	must(t, err)
	// A real link, then delete the target fact's .md so the link dangles.
	if _, err := a.Link(x.ID, y.ID, "relates", "x"); err != nil {
		t.Fatal(err)
	}
	must(t, os.Remove(filepath.Join(a.Brain.FactsDir(), y.ID+".md")))

	// Report-only: the dangling link is reported and remains.
	res, err := a.WikiLint(WikiLintOptions{})
	must(t, err)
	found := false
	for _, is := range res.Issues {
		if is.Kind == "dangling-link" && is.Src == x.ID && is.Dst == y.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("dangling-link not reported:\n%+v", res.Issues)
	}

	// --fix: the dangling link is dropped from the source fact.
	res, err = a.WikiLint(WikiLintOptions{Fix: true})
	must(t, err)
	if len(res.Fixed) != 1 || res.Fixed[0].Kind != "dangling-link" {
		t.Fatalf("fixed = %+v", res.Fixed)
	}
	got, _, _ := a.Store.Get(x.ID)
	if len(got.Links) != 0 {
		t.Fatalf("link not dropped: %+v", got.Links)
	}
}

func TestAppGetAndDelete(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT tokens", Body: "stateless", Project: "p", Scope: "project"})
	must(t, err)
	got, ok, err := a.Get(f.ID)
	must(t, err)
	if !ok || got.ID != f.ID {
		t.Fatalf("Get = %+v, ok=%v", got, ok)
	}
	deleted, err := a.Delete(f.ID)
	must(t, err)
	if !deleted {
		t.Fatal("Delete returned false")
	}
	if _, ok, _ := a.Get(f.ID); ok {
		t.Fatal("Get still finds the fact after Delete")
	}
	// Index reflects the delete too.
	res, err := a.Search("jwt", 10)
	must(t, err)
	if len(res) != 0 {
		t.Fatalf("search returns deleted fact: %v", res)
	}
}

func TestSetupClaudeCodeDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	a := New(t.TempDir())
	must(t, a.Init())
	actions, err := a.SetupClaudeCode(SetupOptions{ProjectDir: dir, BrainHome: a.Brain.Root, DryRun: true})
	must(t, err)
	if len(actions) != 4 {
		t.Fatalf("want 4 actions, got %d", len(actions))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("dry-run wrote files: %v", entries)
	}
}

func TestSetupClaudeCodeWritesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	a := New(home)
	must(t, a.Init())
	_, err := a.SetupClaudeCode(SetupOptions{ProjectDir: dir, BrainHome: home})
	must(t, err)

	// adapter executable
	adapter := filepath.Join(home, ".bbrain", "agents", "claude-code.sh")
	info, err := os.Stat(adapter)
	must(t, err)
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("adapter not executable: %v", info.Mode())
	}
	// .mcp.json valid + has bbrain
	mcp, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	must(t, err)
	if !strings.Contains(string(mcp), `"bbrain"`) || !strings.Contains(string(mcp), `"BBRAIN_HOME"`) {
		t.Fatalf(".mcp.json = %s", mcp)
	}
	// CLAUDE.md block
	cm, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	must(t, err)
	if !strings.Contains(string(cm), "BBRAIN:BEGIN") || !strings.Contains(string(cm), "mcp__bbrain__mem_save") {
		t.Fatalf("CLAUDE.md = %s", cm)
	}
	// env.sh
	if _, err := os.Stat(filepath.Join(home, ".bbrain", "env.sh")); err != nil {
		t.Fatalf("env.sh missing: %v", err)
	}

	// idempotent: second run leaves exactly one managed block
	_, err = a.SetupClaudeCode(SetupOptions{ProjectDir: dir, BrainHome: home})
	must(t, err)
	cm2, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if strings.Count(string(cm2), "BBRAIN:BEGIN") != 1 {
		t.Fatalf("duplicate managed block after re-run:\n%s", cm2)
	}
}

func TestVaultMoveRelocatesAndReindexes(t *testing.T) {
	src := t.TempDir()
	a := New(src)
	must(t, a.Init())
	f, err := a.Save(store.SaveInput{Type: "decision", Title: "Movable JWT", Body: "tokens", Project: "p", Scope: "project"})
	must(t, err)
	dest := filepath.Join(t.TempDir(), "moved")

	newRoot, n, err := a.VaultMove(dest, VaultMoveOptions{})
	must(t, err)
	if newRoot != dest || n < 1 {
		t.Fatalf("VaultMove = %q, %d", newRoot, n)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source brain still present after move")
	}
	// Facts survive at dest and the index was rebuilt there.
	nb := New(dest)
	got, ok, err := nb.Get(f.ID)
	must(t, err)
	if !ok || got.Title != "Movable JWT" {
		t.Fatalf("fact missing at dest: %+v ok=%v", got, ok)
	}
	res, err := nb.Search("jwt", 10)
	must(t, err)
	if len(res) == 0 {
		t.Fatal("search returns nothing after move (index not rebuilt)")
	}
}

func TestVaultMoveRefreshesProject(t *testing.T) {
	src := t.TempDir()
	a := New(src)
	must(t, a.Init())
	proj := t.TempDir()
	dest := filepath.Join(t.TempDir(), "moved")

	_, _, err := a.VaultMove(dest, VaultMoveOptions{ProjectDir: proj})
	must(t, err)
	mcp, err := os.ReadFile(filepath.Join(proj, ".mcp.json"))
	must(t, err)
	if !strings.Contains(string(mcp), dest) {
		t.Fatalf(".mcp.json not pointed at new home %q:\n%s", dest, mcp)
	}
}

func TestVaultMoveRegeneratesEnvSh(t *testing.T) {
	src := t.TempDir()
	a := New(src)
	must(t, a.Init())
	// simulate a prior `setup` by placing an adapter under the brain.
	adapterDir := filepath.Join(src, ".bbrain", "agents")
	must(t, os.MkdirAll(adapterDir, 0o755))
	must(t, os.WriteFile(filepath.Join(adapterDir, "claude-code.sh"), []byte("#!/bin/sh\n"), 0o755))
	dest := filepath.Join(t.TempDir(), "moved")

	newRoot, _, err := a.VaultMove(dest, VaultMoveOptions{})
	must(t, err)
	env, err := os.ReadFile(filepath.Join(newRoot, ".bbrain", "env.sh"))
	must(t, err)
	if !strings.Contains(string(env), newRoot) {
		t.Fatalf("env.sh not regenerated to the new home %q:\n%s", newRoot, env)
	}
}

func TestContextRecentFactsAndFilter(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Alpha JWT", Body: "a", Project: "shopapp", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Beta Redis", Body: "b", Project: "datacli", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("", 10)
	must(t, err)
	if !strings.Contains(out, "Alpha JWT") || !strings.Contains(out, "Beta Redis") {
		t.Fatalf("context = %s", out)
	}
	// project filter
	filtered, err := a.Context("shopapp", 10)
	must(t, err)
	if !strings.Contains(filtered, "Alpha JWT") || strings.Contains(filtered, "Beta Redis") {
		t.Fatalf("filtered context leaked: %s", filtered)
	}
}

func TestContextEmptyBrain(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	out, err := a.Context("", 10)
	must(t, err)
	if !strings.Contains(out, "BBrain memory context") {
		t.Fatalf("empty context = %s", out)
	}
	if !strings.Contains(out, "(none yet)") {
		t.Fatalf("empty context should say (none yet): %s", out)
	}
}
