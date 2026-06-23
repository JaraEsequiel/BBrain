// Package app wires the brain, store, and index together and exposes the
// operations the CLI (and later the MCP server) drive.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bbrain/internal/brain"
	"bbrain/internal/fact"
	"bbrain/internal/index"
	"bbrain/internal/llm"
	"bbrain/internal/store"
	"bbrain/internal/wiki"
)

// App is the high-level façade over one brain.
type App struct {
	Store  *store.Store
	Brain  brain.Brain
	Runner llm.Runner
}

// New builds an App rooted at a brain directory.
func New(root string) *App {
	b := brain.New(root)
	return &App{Store: store.New(b), Brain: b, Runner: llm.NewCLIRunner()}
}

// ensureIndexDir creates the directory that holds the derived index, so the
// index can be opened for writing even on a freshly cleaned brain.
func (a *App) ensureIndexDir() error {
	return os.MkdirAll(filepath.Dir(a.Brain.IndexPath()), 0755)
}

// Init creates the brain structure and builds an initial (empty) index.
func (a *App) Init() error {
	if err := a.Brain.Init(); err != nil {
		return err
	}
	_, err := a.Reindex()
	return err
}

// Reindex rebuilds the FTS index from the .md files on disk and returns how many
// facts were indexed. The index is fully derived: it is cleared first.
func (a *App) Reindex() (int, error) {
	facts, err := a.Store.ListFacts()
	if err != nil {
		return 0, err
	}
	if err := a.ensureIndexDir(); err != nil {
		return 0, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return 0, err
	}
	defer ix.Close()
	if err := ix.Clear(); err != nil {
		return 0, err
	}
	for _, f := range facts {
		if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
			return 0, err
		}
		if err := ix.IndexLinks(f); err != nil {
			return 0, err
		}
	}
	return len(facts), nil
}

// Save persists a fact and incrementally indexes it.
func (a *App) Save(in store.SaveInput) (fact.Fact, error) {
	f, err := a.Store.Save(in)
	if err != nil {
		return fact.Fact{}, err
	}
	if err := a.ensureIndexDir(); err != nil {
		return fact.Fact{}, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return fact.Fact{}, err
	}
	defer ix.Close()
	if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

// Search runs a lexical search over the index.
func (a *App) Search(query string, limit int) ([]index.Result, error) {
	if err := a.ensureIndexDir(); err != nil {
		return nil, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	return ix.Search(query, limit)
}

// Link adds (or updates) a reasoned wikilink from srcID to dstID on the source
// fact's .md, then incrementally re-indexes that fact's edges.
func (a *App) Link(srcID, dstID, relation, why string) (fact.Fact, error) {
	f, err := a.Store.AddLink(srcID, dstID, relation, why)
	if err != nil {
		return fact.Fact{}, err
	}
	if err := a.ensureIndexDir(); err != nil {
		return fact.Fact{}, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return fact.Fact{}, err
	}
	defer ix.Close()
	if err := ix.IndexLinks(f); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

// Why returns the reasoned edges directly connecting two facts (either direction).
func (a *App) Why(aID, bID string) ([]index.Edge, error) {
	if err := a.ensureIndexDir(); err != nil {
		return nil, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	return ix.Why(aID, bID)
}

// Related returns every fact linked to or from id, with direction.
func (a *App) Related(id string) ([]index.Neighbor, error) {
	if err := a.ensureIndexDir(); err != nil {
		return nil, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	return ix.Neighbors(id)
}

// Candidates surfaces facts lexically similar to the given fact but not yet linked
// to it — the raw material for spotting correlations and conflicts. It OR-matches
// the fact's title and tags against the FTS index, then drops the fact itself and
// anything it already links to. Returns at most limit results.
func (a *App) Candidates(id string, limit int) ([]index.Result, error) {
	f, ok, err := a.Store.Get(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("candidates: fact %q not found", id)
	}
	linked := map[string]bool{id: true}
	for _, l := range f.Links {
		linked[fact.LinkTargetID(l.Target)] = true
	}
	terms := f.Title
	if len(f.Tags) > 0 {
		terms += " " + strings.Join(f.Tags, " ")
	}

	if err := a.ensureIndexDir(); err != nil {
		return nil, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	// Over-fetch so that, after dropping self + already-linked, we can still return
	// up to limit results.
	res, err := ix.SearchAny(terms, limit+len(linked))
	if err != nil {
		return nil, err
	}
	out := make([]index.Result, 0, limit)
	for _, r := range res {
		if linked[r.FactID] {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// WikiBuildOptions configures App.WikiBuild.
type WikiBuildOptions struct {
	Project    string
	Scope      string
	Categories []string // extra categories added to the default vocabulary
	DryRun     bool
}

// WikiBuild runs the LLM-driven wiki build over the brain's facts (optionally
// filtered by project/scope) and writes the distilled pages, index, and log.
func (a *App) WikiBuild(ctx context.Context, opts WikiBuildOptions) (wiki.BuildResult, error) {
	facts, err := a.Store.ListFacts()
	if err != nil {
		return wiki.BuildResult{}, err
	}
	var filtered []fact.Fact
	for _, f := range facts {
		if opts.Project != "" && f.Project != opts.Project {
			continue
		}
		if opts.Scope != "" && f.Scope != opts.Scope {
			continue
		}
		filtered = append(filtered, f)
	}
	if err := os.MkdirAll(a.Brain.WikiDir(), 0o755); err != nil {
		return wiki.BuildResult{}, err
	}

	cats := append([]string{}, wiki.DefaultCategories...)
	seen := map[string]bool{}
	for _, c := range cats {
		seen[c] = true
	}
	for _, c := range opts.Categories {
		if c = strings.TrimSpace(c); c != "" && !seen[c] {
			cats = append(cats, c)
			seen[c] = true
		}
	}

	return wiki.Build(ctx, wiki.BuildOptions{
		WikiDir:    a.Brain.WikiDir(),
		Facts:      filtered,
		Categories: cats,
		Runner:     a.Runner,
		Now:        a.Store.Now,
		DryRun:     opts.DryRun,
	})
}

// WikiLinkOptions configures App.WikiLink.
type WikiLinkOptions struct {
	Project string
	Scope   string
	Limit   int // max FTS candidates per fact; <=0 means 8
	DryRun  bool
}

// snippet collapses whitespace in body and returns at most max runes — enough
// context for the LLM to judge relatedness without sending the whole body.
func snippet(body string, max int) string {
	s := strings.Join(strings.Fields(body), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}

// WikiLink grows the reasoned fact graph: for each fact (optionally filtered by
// project/scope) it gathers FTS candidates, asks the LLM which are related and
// how, validates, and writes the new links via a.Link. Re-runs are idempotent
// (Candidates already excludes already-linked facts). On --dry-run nothing is
// written.
func (a *App) WikiLink(ctx context.Context, opts WikiLinkOptions) (wiki.LinkResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 8
	}
	facts, err := a.Store.ListFacts()
	if err != nil {
		return wiki.LinkResult{}, err
	}
	var filtered []fact.Fact
	for _, f := range facts {
		if opts.Project != "" && f.Project != opts.Project {
			continue
		}
		if opts.Scope != "" && f.Scope != opts.Scope {
			continue
		}
		filtered = append(filtered, f)
	}

	candMap := map[string][]wiki.Candidate{}
	for _, f := range filtered {
		res, err := a.Candidates(f.ID, limit)
		if err != nil {
			return wiki.LinkResult{}, err
		}
		var cs []wiki.Candidate
		for _, r := range res {
			snip := ""
			if cf, ok, err := a.Store.Get(r.FactID); err != nil {
				return wiki.LinkResult{}, err
			} else if ok {
				snip = snippet(cf.Body, 240)
			}
			cs = append(cs, wiki.Candidate{ID: r.FactID, Title: r.Title, Type: r.Type, Project: r.Project, Snippet: snip})
		}
		candMap[f.ID] = cs
	}

	proposals, err := wiki.Link(ctx, wiki.LinkOptions{Facts: filtered, Candidates: candMap, Runner: a.Runner})
	if err != nil {
		return wiki.LinkResult{}, err
	}

	var written []wiki.Edge
	skipped := 0
	for _, fp := range proposals {
		src, ok, err := a.Store.Get(fp.Src)
		if err != nil {
			return wiki.LinkResult{}, err
		}
		linked := map[string]bool{}
		if ok {
			for _, l := range src.Links {
				linked[fact.LinkTargetID(l.Target)] = true
			}
		}
		for _, p := range fp.Links {
			if linked[p.Dst] {
				skipped++
				continue
			}
			if !opts.DryRun {
				if _, err := a.Link(fp.Src, p.Dst, p.Relation, p.Why); err != nil {
					return wiki.LinkResult{}, err
				}
			}
			written = append(written, wiki.Edge{Src: fp.Src, Dst: p.Dst, Relation: p.Relation, Why: p.Why})
		}
	}

	if !opts.DryRun && len(written) > 0 {
		now := a.Store.Now().UTC().Format(time.RFC3339)
		var sb strings.Builder
		sb.WriteString("\n## " + now + " — wiki link\n")
		for _, e := range written {
			sb.WriteString(fmt.Sprintf("- %s -[%s]-> %s: %s\n", e.Src, e.Relation, e.Dst, e.Why))
		}
		if skipped > 0 {
			sb.WriteString(fmt.Sprintf("- (skipped %d already-linked)\n", skipped))
		}
		if err := os.MkdirAll(a.Brain.WikiDir(), 0o755); err != nil {
			return wiki.LinkResult{}, err
		}
		if err := wiki.AppendLog(a.Brain.WikiDir(), sb.String()); err != nil {
			return wiki.LinkResult{}, err
		}
	}

	return wiki.LinkResult{Written: written, Skipped: skipped, DryRun: opts.DryRun}, nil
}

// RemoveLink drops the reasoned wikilink from srcID to dstID on the source
// fact's .md, then re-indexes that fact's edges.
func (a *App) RemoveLink(srcID, dstID string) (fact.Fact, error) {
	f, removed, err := a.Store.RemoveLink(srcID, dstID)
	if err != nil {
		return fact.Fact{}, err
	}
	if !removed {
		return f, nil
	}
	if err := a.ensureIndexDir(); err != nil {
		return fact.Fact{}, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return fact.Fact{}, err
	}
	defer ix.Close()
	if err := ix.IndexLinks(f); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

// WikiLintOptions configures App.WikiLint.
type WikiLintOptions struct {
	Categories []string // extra categories merged into the default vocabulary
	Fix        bool
}

// WikiLint runs the deterministic consistency checks over the whole brain. With
// Fix, it drops dangling fact links (via RemoveLink) and always regenerates the
// derived wiki/index.md; everything else is reported for the human to resolve.
func (a *App) WikiLint(opts WikiLintOptions) (wiki.LintResult, error) {
	facts, err := a.Store.ListFacts()
	if err != nil {
		return wiki.LintResult{}, err
	}
	valid := map[string]bool{}
	for _, c := range wiki.DefaultCategories {
		valid[c] = true
	}
	for _, c := range opts.Categories {
		if c = strings.TrimSpace(c); c != "" {
			valid[c] = true
		}
	}
	issues, err := wiki.Lint(a.Brain.WikiDir(), facts, valid)
	if err != nil {
		return wiki.LintResult{}, err
	}
	if !opts.Fix {
		return wiki.LintResult{Issues: issues}, nil
	}

	var remaining, fixed []wiki.Issue
	for i := 0; i < len(issues); i++ {
		is := issues[i]
		if is.Kind == "dangling-link" && is.Fixable {
			if _, err := a.RemoveLink(is.Src, is.Dst); err != nil {
				remaining = append(remaining, issues[i:]...)
				return wiki.LintResult{Issues: remaining, Fixed: fixed}, err
			}
			fixed = append(fixed, is)
			continue
		}
		remaining = append(remaining, is)
	}
	// The index is derived: always regenerate it on --fix.
	if err := os.MkdirAll(a.Brain.WikiDir(), 0o755); err != nil {
		return wiki.LintResult{}, err
	}
	if err := wiki.RegenerateIndex(a.Brain.WikiDir()); err != nil {
		return wiki.LintResult{}, err
	}
	return wiki.LintResult{Issues: remaining, Fixed: fixed}, nil
}
