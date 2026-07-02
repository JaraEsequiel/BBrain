package app

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/fact"
	"github.com/JaraEsequiel/BBrain/internal/index"
	"github.com/JaraEsequiel/BBrain/internal/store"
	"github.com/JaraEsequiel/BBrain/internal/wiki"
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

func TestSearchFallsBackToOrWhenAndFindsNothing(t *testing.T) {
	a := New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{
		Type: "note", Title: "Juan Jara", Body: "works on fuel-cx",
		Project: "fuel-cx", Scope: "project",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No fact contains all of these terms, so strict AND yields nothing. The OR
	// fallback must still surface the partially-overlapping "Juan Jara" fact —
	// this is the broad-query miss that returned {"results": null} before.
	res, err := a.Search("Juan Jara role company team preferences", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Title != "Juan Jara" {
		t.Fatalf("OR fallback Search = %+v; want the Juan Jara fact", res)
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

func TestWikiBuildPassesArchivedAsCitationUniverse(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	// Archived fact in a DIFFERENT project: the citation universe is the whole
	// archive tier, unfiltered — only distillation honors the project filter.
	arch, err := a.Save(store.SaveInput{Type: "decision", Title: "Old ledger format", Body: "ledger",
		Project: "other", Scope: "project"})
	must(t, err)
	_, err = a.Archive(arch.ID)
	must(t, err)
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Use JWT", Body: "jwt",
		Project: "shopapp", Scope: "project"}); err != nil {
		t.Fatal(err)
	}

	fr := &appFakeRunner{out: `{"pages":[{"slug":"ledger","category":"decisions","title":"Ledger","sources":["` + arch.ID + `"],"body":"# Ledger","change_reason":"created"}]}`}
	a.Runner = fr
	res, err := a.WikiBuild(context.Background(), WikiBuildOptions{Project: "shopapp"})
	must(t, err)
	if len(res.InvalidPages) != 0 {
		t.Fatalf("page citing archived fact rejected: %+v", res.InvalidPages)
	}
	// DeriveBucket must resolve the bucket from the ARCHIVED fact's project.
	if len(res.Written) != 1 || res.Written[0] != "projects/other/decisions/ledger.md" {
		t.Fatalf("written = %+v", res.Written)
	}
	// Archived facts never enter the distillation prompt.
	if strings.Contains(fr.gotPrompt, "Old ledger format") {
		t.Fatalf("archived fact leaked into prompt:\n%s", fr.gotPrompt)
	}
	if !strings.Contains(fr.gotPrompt, "title: Use JWT") {
		t.Fatalf("active fact missing from prompt:\n%s", fr.gotPrompt)
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

func TestWikiLintFixKeepsArchivedLinks(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	x, err := a.Save(store.SaveInput{Type: "decision", Title: "Alpha", Body: "a", Project: "p", Scope: "project"})
	must(t, err)
	y, err := a.Save(store.SaveInput{Type: "decision", Title: "Beta", Body: "b", Project: "p", Scope: "project"})
	must(t, err)
	if _, err := a.Link(x.ID, y.ID, "relates", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Archive(y.ID); err != nil {
		t.Fatal(err)
	}

	res, err := a.WikiLint(WikiLintOptions{Fix: true})
	must(t, err)
	if len(res.Fixed) != 0 {
		t.Fatalf("--fix removed an active->archived link: %+v", res.Fixed)
	}
	found := false
	for _, is := range res.Issues {
		if is.Kind == "archived-link" && is.Info && !is.Fixable && is.Src == x.ID && is.Dst == y.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("archived-link not reported:\n%+v", res.Issues)
	}
	got, _, _ := a.Store.Get(x.ID)
	if len(got.Links) != 1 {
		t.Fatalf("link to archived fact was dropped: %+v", got.Links)
	}
}

func TestWikiLintSameNonInfoIssuesAfterArchive(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f, err := a.Save(store.SaveInput{Type: "decision", Title: "Alpha", Body: "a", Project: "p", Scope: "project"})
	must(t, err)
	// A page citing f, plus a deliberate archive-independent non-info issue
	// (invalid category). generated_at is in the future so the page is never stale.
	gen := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	page := "---\ntitle: T\ncategory: nope\nsources:\n  - " + f.ID + "\ngenerated_at: " + gen + "\n---\n\n# T\n\nsee [[" + f.ID + "]]\n"
	dir := filepath.Join(a.Brain.WikiDir(), "global", "nope")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "pg.md"), []byte(page), 0o644))

	nonInfo := func() []wiki.Issue {
		res, err := a.WikiLint(WikiLintOptions{})
		must(t, err)
		var out []wiki.Issue
		for _, is := range res.Issues {
			if !is.Info {
				out = append(out, is)
			}
		}
		return out
	}
	before := nonInfo()
	if _, err := a.Archive(f.ID); err != nil {
		t.Fatal(err)
	}
	after := nonInfo()
	if len(before) != len(after) {
		t.Fatalf("non-info issues changed after archive:\nbefore=%+v\nafter=%+v", before, after)
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

func TestContextPinnedGlobalShownUnderAnyProject(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "about-me", Title: "About you",
		Body: "User is Vex. Prefers terse answers.", Scope: "global",
		TopicKey: "profile/about-me", Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Proj note",
		Body: "x", Project: "shopapp", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("shopapp", 10)
	must(t, err)
	if !strings.Contains(out, "## About you & pinned context") {
		t.Fatalf("missing pinned heading: %s", out)
	}
	if !strings.Contains(out, "Prefers terse answers") {
		t.Fatalf("pinned full body missing: %s", out)
	}
	if !strings.Contains(out, "background to") {
		t.Fatalf("preamble missing: %s", out)
	}
}

func TestContextPinnedNotDuplicatedInRecent(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "about-me", Title: "About you",
		Body: "body", Scope: "global", TopicKey: "profile/about-me",
		Pinned: true}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("", 10)
	must(t, err)
	if strings.Contains(out, "[about-me] About you") {
		t.Fatalf("pinned fact leaked into Recent bullets: %s", out)
	}
}

func TestContextProjectScopedPinnedHiddenElsewhere(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "note", Title: "Shop pin",
		Body: "only shop", Project: "shopapp", Scope: "project",
		TopicKey: "shop/pin", Pinned: true}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("datacli", 10)
	must(t, err)
	if strings.Contains(out, "only shop") || strings.Contains(out, "## About you & pinned context") {
		t.Fatalf("project-scoped pin leaked to other project: %s", out)
	}
}

func TestContextGlobalNonPinnedVisibleUnderProject(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Global rule",
		Body: "g", Scope: "global"}); err != nil { // Project == ""
		t.Fatal(err)
	}
	out, err := a.Context("shopapp", 10)
	must(t, err)
	if !strings.Contains(out, "Global rule") {
		t.Fatalf("global non-pinned fact dropped under project filter: %s", out)
	}
}

func TestContextNoPinnedHeadingWhenNonePinned(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Plain",
		Body: "p", Project: "p", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("", 10)
	must(t, err)
	if strings.Contains(out, "## About you & pinned context") {
		t.Fatalf("pinned heading shown with no pinned facts: %s", out)
	}
}

// ---- story-03: Archive / Unarchive / GetArchived / PlanArchive ----

// snapshotTree hashes every file under root: the "did anything on disk change"
// oracle for PlanArchive's no-mutation guarantee.
func snapshotTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	snap := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		snap[rel] = sha256.Sum256(b)
		return nil
	})
	must(t, err)
	return snap
}

func candidateByID(cs []ArchiveCandidate, id string) (ArchiveCandidate, bool) {
	for _, c := range cs {
		if c.Fact.ID == id {
			return c, true
		}
	}
	return ArchiveCandidate{}, false
}

func TestArchiveRemovesFromSearchAndGetArchivedFinds(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f, err := a.Save(store.SaveInput{Type: "decision", Title: "Zeta quokka tokens", Body: "quokka body",
		Project: "p", Scope: "project"})
	must(t, err)
	res, err := a.Search("quokka", 10)
	must(t, err)
	if len(res) != 1 {
		t.Fatalf("pre-archive search = %+v, want 1 hit", res)
	}

	got, err := a.Archive(f.ID)
	must(t, err)
	if got.ID != f.ID {
		t.Fatalf("Archive returned %+v", got)
	}
	// Q1/criterio 1: exact terms of the fact yield 0 hits post-archive.
	res, err = a.Search("quokka", 10)
	must(t, err)
	if len(res) != 0 {
		t.Fatalf("post-archive search = %+v, want 0 hits", res)
	}
	if _, ok, _ := a.Get(f.ID); ok {
		t.Fatal("Get still finds the fact in the active tier")
	}
	af, ok, err := a.GetArchived(f.ID)
	must(t, err)
	if !ok || af.ID != f.ID || af.Title != f.Title {
		t.Fatalf("GetArchived = %+v ok=%v", af, ok)
	}
}

func TestUnarchiveRestoresSearchAndEdges(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f1, err := a.Save(store.SaveInput{Type: "decision", Title: "Wombat cache", Body: "wombat",
		Project: "p", Scope: "project"})
	must(t, err)
	f2, err := a.Save(store.SaveInput{Type: "decision", Title: "Other", Body: "o",
		Project: "p", Scope: "project"})
	must(t, err)
	if _, err := a.Link(f1.ID, f2.ID, "relates", "test edge"); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Archive(f1.ID); err != nil {
		t.Fatal(err)
	}
	if res, _ := a.Search("wombat", 10); len(res) != 0 {
		t.Fatalf("archived fact still in search: %+v", res)
	}

	got, err := a.Unarchive(f1.ID)
	must(t, err)
	if got.ID != f1.ID {
		t.Fatalf("Unarchive returned %+v", got)
	}
	// Criterio: back in search AND edges re-indexed (IndexFact + IndexLinks).
	res, err := a.Search("wombat", 10)
	must(t, err)
	if len(res) != 1 || res[0].FactID != f1.ID {
		t.Fatalf("post-unarchive search = %+v", res)
	}
	edges, err := a.Why(f1.ID, f2.ID)
	must(t, err)
	if len(edges) != 1 {
		t.Fatalf("edges not re-indexed after unarchive: %+v", edges)
	}
}

func TestPlanArchiveEmptyFilterErrors(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.PlanArchive(ArchiveFilter{}); err == nil {
		t.Fatal("PlanArchive with a fully empty filter must error (explicit selection required)")
	}
}

func TestPlanArchiveDoesNotMutate(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f, err := a.Save(store.SaveInput{Type: "session-summary", Title: "Old session", Body: "s",
		Project: "p", Scope: "project"})
	must(t, err)

	before := snapshotTree(t, a.Brain.Root)
	cands, err := a.PlanArchive(ArchiveFilter{Types: []string{"session-summary"}})
	must(t, err)
	if _, ok := candidateByID(cands, f.ID); !ok {
		t.Fatalf("plan missed the matching fact: %+v", cands)
	}
	after := snapshotTree(t, a.Brain.Root)
	if len(before) != len(after) {
		t.Fatalf("plan changed the file set: %d -> %d files", len(before), len(after))
	}
	for rel, h := range before {
		if after[rel] != h {
			t.Fatalf("plan mutated %s", rel)
		}
	}
}

func TestPlanArchiveDistilledRequiresCitation(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	cited, err := a.Save(store.SaveInput{Type: "decision", Title: "Cited fact", Body: "c",
		Project: "p", Scope: "project"})
	must(t, err)
	uncited, err := a.Save(store.SaveInput{Type: "decision", Title: "Uncited fact", Body: "u",
		Project: "p", Scope: "project"})
	must(t, err)
	// Fixture wiki page citing only `cited` (criterio 6 del PRD).
	page := "---\ntitle: T\ncategory: decisions\nsources:\n  - " + cited.ID +
		"\ngenerated_at: 2026-01-01T00:00:00Z\n---\n\n# T\n\nbody\n"
	dir := filepath.Join(a.Brain.WikiDir(), "projects", "p", "decisions")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "t.md"), []byte(page), 0o644))

	cands, err := a.PlanArchive(ArchiveFilter{Distilled: true})
	must(t, err)
	c, ok := candidateByID(cands, cited.ID)
	if !ok || c.Skipped != "" {
		t.Fatalf("cited fact must qualify under --distilled: %+v", cands)
	}
	if !strings.Contains(strings.Join(c.Reasons, ","), "distilled") {
		t.Fatalf("reasons should name distilled: %+v", c.Reasons)
	}
	if _, ok := candidateByID(cands, uncited.ID); ok {
		t.Fatalf("a fact cited by no page must NEVER qualify under --distilled: %+v", cands)
	}
}

func TestPlanArchivePinnedNeverCandidate(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	pinned, err := a.Save(store.SaveInput{Type: "about-me", Title: "Pinned profile", Body: "p",
		Scope: "global", TopicKey: "profile/about", Pinned: true})
	must(t, err)

	// Never a candidate via filters.
	cands, err := a.PlanArchive(ArchiveFilter{Types: []string{"about-me"}})
	must(t, err)
	if _, ok := candidateByID(cands, pinned.ID); ok {
		t.Fatalf("pinned fact matched by filters: %+v", cands)
	}
	// Explicit ID: returned but skipped, not archivable.
	cands, err = a.PlanArchive(ArchiveFilter{IDs: []string{pinned.ID}})
	must(t, err)
	c, ok := candidateByID(cands, pinned.ID)
	if !ok || c.Skipped != "pinned" {
		t.Fatalf("explicit pinned id must come back skipped=pinned: %+v", cands)
	}
}

func TestPlanArchiveOlderThanUsesInjectedNowAndFailsSafe(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a.Store.Now = func() time.Time { return t0 }
	old, err := a.Save(store.SaveInput{Type: "note", Title: "Old note", Body: "o", Project: "p", Scope: "project"})
	must(t, err)
	a.Store.Now = func() time.Time { return t0.Add(40 * 24 * time.Hour) }
	fresh, err := a.Save(store.SaveInput{Type: "note", Title: "Fresh note", Body: "f", Project: "p", Scope: "project"})
	must(t, err)
	// A fact whose updated_at is unparseable must NOT qualify (fail-safe).
	bad := fact.Fact{ID: fact.NewID("2020-01-01", "bad stamp"), Type: "note", Scope: "project",
		Project: "p", CreatedAt: "garbage", UpdatedAt: "garbage", RevisionCount: 1,
		Title: "Bad stamp", Body: "b"}
	must(t, os.WriteFile(filepath.Join(a.Brain.FactsDir(), bad.ID+".md"), []byte(fact.Marshal(bad)), 0o644))

	cands, err := a.PlanArchive(ArchiveFilter{OlderThan: 30 * 24 * time.Hour})
	must(t, err)
	if _, ok := candidateByID(cands, old.ID); !ok {
		t.Fatalf("40d-old fact must qualify under older-than 30d: %+v", cands)
	}
	if _, ok := candidateByID(cands, fresh.ID); ok {
		t.Fatalf("fresh fact must not qualify: %+v", cands)
	}
	if _, ok := candidateByID(cands, bad.ID); ok {
		t.Fatalf("unparseable updated_at must fail safe (not qualify): %+v", cands)
	}
}

func TestPlanArchiveTypesOrProjectAnd(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	s1, err := a.Save(store.SaveInput{Type: "session-summary", Title: "S1", Body: "s", Project: "alpha", Scope: "project"})
	must(t, err)
	n1, err := a.Save(store.SaveInput{Type: "note", Title: "N1", Body: "n", Project: "alpha", Scope: "project"})
	must(t, err)
	s2, err := a.Save(store.SaveInput{Type: "session-summary", Title: "S2", Body: "s", Project: "beta", Scope: "project"})
	must(t, err)
	d1, err := a.Save(store.SaveInput{Type: "decision", Title: "D1", Body: "d", Project: "alpha", Scope: "project"})
	must(t, err)

	// Types is an internal OR...
	cands, err := a.PlanArchive(ArchiveFilter{Types: []string{"session-summary", "note"}})
	must(t, err)
	for _, id := range []string{s1.ID, n1.ID, s2.ID} {
		if _, ok := candidateByID(cands, id); !ok {
			t.Fatalf("types OR missed %s: %+v", id, cands)
		}
	}
	if _, ok := candidateByID(cands, d1.ID); ok {
		t.Fatalf("decision must not match types filter: %+v", cands)
	}
	// ...ANDed with project.
	cands, err = a.PlanArchive(ArchiveFilter{Types: []string{"session-summary"}, Project: "alpha"})
	must(t, err)
	if _, ok := candidateByID(cands, s1.ID); !ok {
		t.Fatalf("AND composition missed s1: %+v", cands)
	}
	if _, ok := candidateByID(cands, s2.ID); ok {
		t.Fatalf("project filter leaked beta fact: %+v", cands)
	}
}

func TestPlanArchiveExplicitIDsUnionAndSkips(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	s1, err := a.Save(store.SaveInput{Type: "session-summary", Title: "S1", Body: "s", Project: "p", Scope: "project"})
	must(t, err)
	d1, err := a.Save(store.SaveInput{Type: "decision", Title: "D1", Body: "d", Project: "p", Scope: "project"})
	must(t, err)
	gone, err := a.Save(store.SaveInput{Type: "note", Title: "Gone", Body: "g", Project: "p", Scope: "project"})
	must(t, err)
	if _, err := a.Archive(gone.ID); err != nil {
		t.Fatal(err)
	}

	cands, err := a.PlanArchive(ArchiveFilter{
		Types: []string{"session-summary"},
		IDs:   []string{d1.ID, s1.ID, gone.ID, "2020-01-01-no-such-fact"},
	})
	must(t, err)
	// matches ∪ explicit IDs, deduped: s1 appears exactly once.
	seen := 0
	for _, c := range cands {
		if c.Fact.ID == s1.ID {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("s1 must appear exactly once in the union, got %d: %+v", seen, cands)
	}
	if c, ok := candidateByID(cands, d1.ID); !ok || c.Skipped != "" {
		t.Fatalf("explicit active id must be archivable: %+v", cands)
	}
	if c, ok := candidateByID(cands, gone.ID); !ok || c.Skipped != "already archived" {
		t.Fatalf("already-archived id must come back skipped: %+v", cands)
	}
	if c, ok := candidateByID(cands, "2020-01-01-no-such-fact"); !ok || c.Skipped != "not found" {
		t.Fatalf("unknown id must come back skipped=not found: %+v", cands)
	}
}
