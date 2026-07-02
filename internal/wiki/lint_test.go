package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaraEsequiel/BBrain/internal/fact"
)

func lintWritePage(t *testing.T, dir, rel, cat, gen string, sources []string, body string) {
	t.Helper()
	s := "---\ntitle: T\ncategory: " + cat + "\nsources:\n"
	for _, src := range sources {
		s += "  - " + src + "\n"
	}
	s += "generated_at: " + gen + "\n---\n\n# T\n\n" + body + "\n"
	p := filepath.Join(dir, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(s), 0o644))
}

func hasIssue(issues []Issue, kind, locContains string) bool {
	for _, is := range issues {
		if is.Kind == kind && strings.Contains(is.Location, locContains) {
			return true
		}
	}
	return false
}

func TestLintDetectsDanglingLinkAndRef(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{
		{ID: "f1", Title: "F1", Body: "see [[ghost]] here", Links: []fact.Link{
			{Target: "[[missing]]", Relation: "relates", Why: "x"},
		}},
	}
	issues, err := Lint(dir, facts, nil, map[string]bool{"decisions": true})
	must(t, err)
	if !hasIssue(issues, "dangling-link", "f1") {
		t.Fatalf("missing dangling-link:\n%+v", issues)
	}
	if !hasIssue(issues, "dangling-ref", "f1") {
		t.Fatalf("missing dangling-ref:\n%+v", issues)
	}
	// the dangling-link must be fixable and carry src/dst
	for _, is := range issues {
		if is.Kind == "dangling-link" {
			if !is.Fixable || is.Src != "f1" || is.Dst != "missing" {
				t.Fatalf("dangling-link issue = %+v", is)
			}
		}
		if is.Kind == "dangling-ref" && is.Fixable {
			t.Fatalf("dangling-ref must not be fixable: %+v", is)
		}
	}
}

func TestLintDetectsPageIssues(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "f1", Title: "F1", Body: "b", UpdatedAt: "2026-06-23T18:00:00Z"}}
	// invalid category + a missing source ("gone")
	lintWritePage(t, dir, "global/nope/bad.md", "nope", "2026-06-23T16:00:00Z", []string{"f1", "gone"}, "body")
	// orphan: its only source is missing
	lintWritePage(t, dir, "global/people/orphan.md", "people", "2026-06-23T16:00:00Z", []string{"gone"}, "body")
	// stale: source f1 updated_at (18:00) > generated_at (16:00)
	lintWritePage(t, dir, "global/people/stale.md", "people", "2026-06-23T16:00:00Z", []string{"f1"}, "body")

	valid := map[string]bool{"people": true}
	issues, err := Lint(dir, facts, nil, valid)
	must(t, err)
	if !hasIssue(issues, "invalid-category", "bad.md") {
		t.Fatalf("missing invalid-category:\n%+v", issues)
	}
	if !hasIssue(issues, "missing-source", "bad.md") {
		t.Fatalf("missing missing-source:\n%+v", issues)
	}
	if !hasIssue(issues, "orphan-page", "orphan.md") {
		t.Fatalf("missing orphan-page:\n%+v", issues)
	}
	if !hasIssue(issues, "stale-page", "stale.md") {
		t.Fatalf("missing stale-page:\n%+v", issues)
	}
}

func TestLintClean(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "f1", Title: "F1", Body: "b", UpdatedAt: "2026-06-23T16:00:00Z"}}
	lintWritePage(t, dir, "global/people/ok.md", "people", "2026-06-23T18:00:00Z", []string{"f1"}, "see [[f1]]")
	issues, err := Lint(dir, facts, nil, map[string]bool{"people": true})
	must(t, err)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got:\n%+v", issues)
	}
}

func TestLintResolvesArchivedTier(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{
		{ID: "f1", Title: "F1", Body: "see [[arch1]]", UpdatedAt: "2026-06-23T16:00:00Z", Links: []fact.Link{
			{Target: "[[arch1]]", Relation: "relates", Why: "x"},
		}},
	}
	archived := []fact.Fact{
		// Frozen tier: its own dangling link/ref must NOT be linted.
		{ID: "arch1", Title: "A1", Body: "see [[nowhere]]", UpdatedAt: "2026-06-23T16:00:00Z", Links: []fact.Link{
			{Target: "[[nowhere]]", Relation: "relates", Why: "x"},
		}},
	}
	// Page sourcing only the archived fact, body referencing it.
	lintWritePage(t, dir, "global/people/pg.md", "people", "2026-06-23T18:00:00Z", []string{"arch1"}, "see [[arch1]]")

	issues, err := Lint(dir, facts, archived, map[string]bool{"people": true})
	must(t, err)
	for _, kind := range []string{"dangling-link", "dangling-ref", "missing-source", "orphan-page", "stale-page"} {
		if hasIssue(issues, kind, "") {
			t.Fatalf("unexpected %s:\n%+v", kind, issues)
		}
	}
	var got []Issue
	for _, is := range issues {
		if is.Kind == "archived-link" {
			got = append(got, is)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one archived-link, got:\n%+v", issues)
	}
	if is := got[0]; is.Fixable || !is.Info || is.Src != "f1" || is.Dst != "arch1" {
		t.Fatalf("archived-link = %+v", is)
	}
}

func TestLintStalePageCountsArchivedSources(t *testing.T) {
	dir := t.TempDir()
	// Archived source edited by hand after the page was generated -> stale.
	archived := []fact.Fact{{ID: "arch1", Title: "A1", Body: "b", UpdatedAt: "2026-06-23T20:00:00Z"}}
	lintWritePage(t, dir, "global/people/pg.md", "people", "2026-06-23T18:00:00Z", []string{"arch1"}, "body")
	issues, err := Lint(dir, nil, archived, map[string]bool{"people": true})
	must(t, err)
	if !hasIssue(issues, "stale-page", "pg.md") {
		t.Fatalf("missing stale-page:\n%+v", issues)
	}
}

func TestLintDetectsBadPage(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "f1", Title: "F1", Body: "b"}}
	p := filepath.Join(dir, "global", "people", "broken.md")
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	// opening delimiter but no closing one -> ParsePageMeta fails -> bad-page
	must(t, os.WriteFile(p, []byte("---\ntitle: T\nno closing delimiter\n"), 0o644))
	issues, err := Lint(dir, facts, nil, map[string]bool{"people": true})
	must(t, err)
	if !hasIssue(issues, "bad-page", "broken.md") {
		t.Fatalf("missing bad-page issue:\n%+v", issues)
	}
}
