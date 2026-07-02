package wiki

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/fact"
)

// Issue is one consistency problem found by Lint.
type Issue struct {
	Kind     string // dangling-link | dangling-ref | missing-source | invalid-category | orphan-page | stale-page | bad-page | archived-link
	Location string
	Message  string
	Fixable  bool
	Info     bool   // informative only: reported but never counted as a failure nor fixed
	Src, Dst string // populated for dangling-link/archived-link so --fix can act without re-parsing
}

// LintResult reports the issues found and (after --fix) the ones repaired.
type LintResult struct {
	Issues []Issue // remaining (unfixed) issues
	Fixed  []Issue
}

var targetRE = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

// scanTargets returns the bare fact ids referenced as [[id]] in s.
// Each id is returned at most once (order of first occurrence preserved).
func scanTargets(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range targetRE.FindAllString(s, -1) {
		if id := fact.LinkTargetID(m); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Lint runs the deterministic consistency checks over all active facts and
// every wiki page under wikiDir, judging existence against the union of the
// active and archived tiers. Archived facts resolve references (so archiving
// never manufactures dangling/missing issues) but are not themselves linted:
// they are frozen. It never mutates anything. validCategories is the active
// vocabulary.
func Lint(wikiDir string, facts, archived []fact.Fact, validCategories map[string]bool) ([]Issue, error) {
	byID := make(map[string]fact.Fact, len(facts)+len(archived))
	for _, f := range facts {
		byID[f.ID] = f
	}
	archivedID := make(map[string]bool, len(archived))
	for _, f := range archived {
		byID[f.ID] = f
		archivedID[f.ID] = true
	}
	var issues []Issue

	// Fact-side checks (active tier only: archived facts are frozen).
	for _, f := range facts {
		for _, l := range f.Links {
			dst := fact.LinkTargetID(l.Target)
			if dst == "" {
				continue
			}
			if archivedID[dst] {
				// A live edge into the archive tier is legitimate graph signal,
				// not damage: informative, and never fixable so --fix can't cut it.
				issues = append(issues, Issue{
					Kind: "archived-link", Location: "fact " + f.ID,
					Message: fmt.Sprintf("link %s -[%s]-> %s targets an archived fact", f.ID, l.Relation, dst),
					Info:    true, Src: f.ID, Dst: dst,
				})
				continue
			}
			if _, ok := byID[dst]; !ok {
				issues = append(issues, Issue{
					Kind: "dangling-link", Location: "fact " + f.ID,
					Message: fmt.Sprintf("link %s -[%s]-> %s targets a missing fact", f.ID, l.Relation, dst),
					Fixable: true, Src: f.ID, Dst: dst,
				})
			}
		}
		for _, dst := range scanTargets(f.Body) {
			if _, ok := byID[dst]; !ok {
				issues = append(issues, Issue{
					Kind: "dangling-ref", Location: "fact " + f.ID,
					Message: fmt.Sprintf("body of %s references missing fact [[%s]]", f.ID, dst),
				})
			}
		}
	}

	// Page-side checks.
	pages, err := readPages(wikiDir)
	if err != nil {
		return nil, err
	}
	for _, pg := range pages {
		meta, err := ParsePageMeta(pg.Content)
		if err != nil {
			issues = append(issues, Issue{Kind: "bad-page", Location: pg.RelPath, Message: err.Error()})
			continue
		}
		if !validCategories[meta.Category] {
			issues = append(issues, Issue{Kind: "invalid-category", Location: pg.RelPath,
				Message: fmt.Sprintf("page category %q is not in the active vocabulary", meta.Category)})
		}
		missing := 0
		for _, src := range meta.Sources {
			if _, ok := byID[src]; !ok {
				missing++
				issues = append(issues, Issue{Kind: "missing-source", Location: pg.RelPath,
					Message: fmt.Sprintf("page source %q does not exist", src)})
			}
		}
		if len(meta.Sources) > 0 && missing == len(meta.Sources) {
			issues = append(issues, Issue{Kind: "orphan-page", Location: pg.RelPath,
				Message: "every source fact for this page is missing"})
		}
		if gen, perr := time.Parse(time.RFC3339, meta.GeneratedAt); perr == nil {
			for _, src := range meta.Sources {
				sf, ok := byID[src]
				if !ok {
					continue
				}
				if upd, uerr := time.Parse(time.RFC3339, sf.UpdatedAt); uerr == nil && upd.After(gen) {
					issues = append(issues, Issue{Kind: "stale-page", Location: pg.RelPath,
						Message: fmt.Sprintf("source %s was updated after the page's generated_at", src)})
				}
			}
		}
		pageBody := pg.Content
		if i := strings.Index(pageBody, "\n---\n"); i >= 0 {
			pageBody = pageBody[i+len("\n---\n"):]
		}
		for _, dst := range scanTargets(pageBody) {
			if _, ok := byID[dst]; !ok {
				issues = append(issues, Issue{Kind: "dangling-ref", Location: pg.RelPath,
					Message: fmt.Sprintf("page %s references missing fact [[%s]]", pg.RelPath, dst)})
			}
		}
	}
	return issues, nil
}
