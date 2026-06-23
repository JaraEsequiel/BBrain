package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bbrain/internal/fact"
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
	issues, err := Lint(dir, facts, map[string]bool{"decisions": true})
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
	issues, err := Lint(dir, facts, valid)
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
	issues, err := Lint(dir, facts, map[string]bool{"people": true})
	must(t, err)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got:\n%+v", issues)
	}
}
