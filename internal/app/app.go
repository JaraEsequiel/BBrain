// Package app wires the brain, store, and index together and exposes the
// operations the CLI (and later the MCP server) drive.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/brain"
	"github.com/JaraEsequiel/BBrain/internal/fact"
	"github.com/JaraEsequiel/BBrain/internal/index"
	"github.com/JaraEsequiel/BBrain/internal/llm"
	"github.com/JaraEsequiel/BBrain/internal/setup"
	"github.com/JaraEsequiel/BBrain/internal/store"
	"github.com/JaraEsequiel/BBrain/internal/vault"
	"github.com/JaraEsequiel/BBrain/internal/wiki"
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
	// wiki (build+link) is the App's only LLM consumer, and distillation is a
	// long non-interactive op, so the runner gets the larger DistillTimeout.
	return &App{Store: store.New(b), Brain: b, Runner: llm.NewCLIRunnerForTimeout(b.Root, llm.DistillTimeout)}
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
	if err := ix.Reset(); err != nil {
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

// Search runs a lexical search over the index. It first tries strict AND (every
// term must co-occur) for precision, then falls back to OR (any term) when AND
// finds nothing — so a broad multi-keyword query like "Juan Jara role company"
// still surfaces partially-overlapping facts instead of returning empty. BM25
// keeps the best-covering facts on top. Single-term queries are unaffected (AND
// and OR are identical there). stale is true when the on-disk index predates
// this ticket's tokenizer/schema change and hasn't been reindexed yet.
func (a *App) Search(query string, limit int, project, typ string) (results []index.Result, stale bool, err error) {
	if err := a.ensureIndexDir(); err != nil {
		return nil, false, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, false, err
	}
	defer ix.Close()
	res, err := ix.Search(query, limit, project, typ)
	if err != nil {
		return nil, ix.Stale(), err
	}
	if len(res) == 0 {
		res, err = ix.SearchAny(query, limit, project, typ)
		return res, ix.Stale(), err
	}
	return res, ix.Stale(), nil
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
	res, err := ix.SearchAny(terms, limit+len(linked), "", "") // ponytail: Candidates unscoped (D4) — no public filter param, no AC/caller needs it
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

// BrowseResult is the minimal projection mem_browse/bbrain list return — title, id,
// and type only, never a fact's full body (avoids flooding an agent's context on
// what's meant to be a lightweight discovery step; full detail is a mem_get away).
type BrowseResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// Browse lists facts filtered by project/type, strict exact-match, empty means
// unfiltered. Deliberately NOT Context()'s leak-through semantics — a project-less
// fact must never appear under a non-empty project filter here.
func (a *App) Browse(project, typ string) ([]BrowseResult, error) {
	facts, err := a.Store.ListFacts()
	if err != nil {
		return nil, err
	}
	out := make([]BrowseResult, 0, len(facts))
	for _, f := range facts {
		if project != "" && f.Project != project {
			continue
		}
		if typ != "" && f.Type != typ {
			continue
		}
		out = append(out, BrowseResult{ID: f.ID, Title: f.Title, Type: f.Type})
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
	// Citation universe: the whole archive tier, unfiltered — the project/scope
	// filter only decides what gets distilled, not what pages may cite.
	archived, err := a.Store.ListArchived()
	if err != nil {
		return wiki.BuildResult{}, err
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
		Archived:   archived,
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

	proposals, failed, err := wiki.Link(ctx, wiki.LinkOptions{Facts: filtered, Candidates: candMap, Runner: a.Runner})
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

	// Log when anything happened worth recording: links written OR facts dropped
	// after exhausting retries (so a run that produced only failures still leaves a
	// trail to re-run against).
	if !opts.DryRun && (len(written) > 0 || len(failed) > 0) {
		now := a.Store.Now().UTC().Format(time.RFC3339)
		var sb strings.Builder
		sb.WriteString("\n## " + now + " — wiki link\n")
		for _, e := range written {
			sb.WriteString(fmt.Sprintf("- %s -[%s]-> %s: %s\n", e.Src, e.Relation, e.Dst, e.Why))
		}
		if skipped > 0 {
			sb.WriteString(fmt.Sprintf("- (skipped %d already-linked)\n", skipped))
		}
		for _, fl := range failed {
			sb.WriteString(fmt.Sprintf("- FAILED %s: %s\n", fl.FactID, fl.Err))
		}
		if err := os.MkdirAll(a.Brain.WikiDir(), 0o755); err != nil {
			return wiki.LinkResult{}, err
		}
		if err := wiki.AppendLog(a.Brain.WikiDir(), sb.String()); err != nil {
			return wiki.LinkResult{}, err
		}
	}

	return wiki.LinkResult{Written: written, Skipped: skipped, Failed: failed, DryRun: opts.DryRun}, nil
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

// WikiLint runs the deterministic consistency checks over the whole brain,
// resolving references against both the active and archived tiers. With Fix, it
// drops dangling fact links (via RemoveLink) and always regenerates the derived
// wiki/index.md; everything else — including informative archived-link issues,
// which are never fixable — is reported for the human to resolve.
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
	archived, err := a.Store.ListArchived()
	if err != nil {
		return wiki.LintResult{}, err
	}
	issues, err := wiki.Lint(a.Brain.WikiDir(), facts, archived, valid)
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

// Get returns a fact by id (ok=false if absent).
func (a *App) Get(id string) (fact.Fact, bool, error) {
	return a.Store.Get(id)
}

// GetArchived returns a fact from the archive tier by id (ok=false if absent).
func (a *App) GetArchived(id string) (fact.Fact, bool, error) {
	return a.Store.GetArchived(id)
}

// Archive moves a fact to the archive tier and drops it from the derived index
// (FTS row + outgoing edges), so archived facts never surface in Search.
// Incoming edges stay in the links table as dangling by design (mem_related
// tolerates them).
func (a *App) Archive(id string) (fact.Fact, error) {
	f, err := a.Store.Archive(id)
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
	if err := ix.DeleteFact(id); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

// Unarchive moves a fact back to the active tier and re-indexes it (FTS row +
// outgoing edges), so it surfaces in Search again.
func (a *App) Unarchive(id string) (fact.Fact, error) {
	f, err := a.Store.Unarchive(id)
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
	// PathFor points at the active tier — where the fact just returned to.
	if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
		return fact.Fact{}, err
	}
	if err := ix.IndexLinks(f); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

// ArchiveFilter selects which active facts qualify for archiving. Given filters
// AND-compose; Types is an internal OR; IDs are explicit picks unioned into the
// result. A fully empty filter is an error: archiving demands explicit selection.
type ArchiveFilter struct {
	Types     []string
	OlderThan time.Duration // qualify when now - updated_at > OlderThan
	Distilled bool          // qualify only when cited as a source by ≥1 wiki page
	Project   string
	IDs       []string
}

// ArchiveCandidate is one fact the plan selected, with why — or why not.
type ArchiveCandidate struct {
	Fact    fact.Fact
	Reasons []string // e.g. ["type=session-summary", "older-than", "distilled"] or ["explicit"]
	Skipped string   // non-empty => not archivable: "pinned" | "not found" | "already archived"
}

// matchesArchiveFilter reports whether f qualifies under every given filter,
// returning the qualification reasons when it does.
func matchesArchiveFilter(f fact.Fact, fl ArchiveFilter, distilled map[string]bool, now time.Time) ([]string, bool) {
	var reasons []string
	if len(fl.Types) > 0 {
		matched := ""
		for _, t := range fl.Types {
			if f.Type == t {
				matched = t
				break
			}
		}
		if matched == "" {
			return nil, false
		}
		reasons = append(reasons, "type="+matched)
	}
	if fl.OlderThan > 0 {
		ts, err := time.Parse(time.RFC3339, f.UpdatedAt)
		// Unparseable updated_at never qualifies (fail-safe).
		if err != nil || now.Sub(ts) <= fl.OlderThan {
			return nil, false
		}
		reasons = append(reasons, "older-than")
	}
	if fl.Distilled {
		if !distilled[f.ID] {
			return nil, false
		}
		reasons = append(reasons, "distilled")
	}
	if fl.Project != "" {
		if f.Project != fl.Project {
			return nil, false
		}
		reasons = append(reasons, "project="+fl.Project)
	}
	return reasons, true
}

// PlanArchive computes the archive candidates for a filter without touching
// disk or index — it is the dry-run. Result = filter matches ∪ explicit IDs
// (deduped). Pinned facts never qualify via filters; an explicit pinned ID is
// returned marked Skipped so the caller can report it.
func (a *App) PlanArchive(fl ArchiveFilter) ([]ArchiveCandidate, error) {
	hasFilter := len(fl.Types) > 0 || fl.OlderThan > 0 || fl.Distilled || fl.Project != ""
	if !hasFilter && len(fl.IDs) == 0 {
		return nil, fmt.Errorf("plan-archive: empty selection — pass at least one filter or explicit id")
	}

	var distilled map[string]bool
	if fl.Distilled {
		var err error
		if distilled, err = wiki.SourceIDs(a.Brain.WikiDir()); err != nil {
			return nil, err
		}
	}

	now := a.Store.Now()
	var out []ArchiveCandidate
	planned := map[string]bool{}
	if hasFilter {
		facts, err := a.Store.ListFacts()
		if err != nil {
			return nil, err
		}
		for _, f := range facts {
			if f.Pinned {
				continue // pinned is never a candidate via filters
			}
			reasons, ok := matchesArchiveFilter(f, fl, distilled, now)
			if !ok {
				continue
			}
			out = append(out, ArchiveCandidate{Fact: f, Reasons: reasons})
			planned[f.ID] = true
		}
	}

	for _, id := range fl.IDs {
		if planned[id] {
			continue // union: already selected by filters
		}
		planned[id] = true
		if !fact.ValidID(id) {
			out = append(out, ArchiveCandidate{Fact: fact.Fact{ID: id}, Skipped: "not found"})
			continue
		}
		f, ok, err := a.Store.Get(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			skip := "not found"
			if _, archived, aerr := a.Store.GetArchived(id); aerr != nil {
				return nil, aerr
			} else if archived {
				skip = "already archived"
			}
			out = append(out, ArchiveCandidate{Fact: fact.Fact{ID: id}, Skipped: skip})
			continue
		}
		if f.Pinned {
			out = append(out, ArchiveCandidate{Fact: f, Skipped: "pinned"})
			continue
		}
		out = append(out, ArchiveCandidate{Fact: f, Reasons: []string{"explicit"}})
	}
	return out, nil
}

// Delete removes a fact's .md and drops it from the derived index. Returns
// (false, nil) if the fact did not exist.
func (a *App) Delete(id string) (bool, error) {
	deleted, err := a.Store.Delete(id)
	if err != nil {
		return false, err
	}
	if !deleted {
		return false, nil
	}
	if err := a.ensureIndexDir(); err != nil {
		return false, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return false, err
	}
	defer ix.Close()
	if err := ix.DeleteFact(id); err != nil {
		return false, err
	}
	return true, nil
}

// SetupOptions configures App.SetupClaudeCode.
type SetupOptions struct {
	ProjectDir string // where .mcp.json + CLAUDE.md go (default: cwd, set by caller)
	BrainHome  string // brain root for the adapter/env (default: a.Brain.Root)
	Model      string // claude model for the adapter (default: claude-sonnet-4-6)
	DryRun     bool
}

// SetupAction is one file the setup writes (or would write, on dry-run).
type SetupAction struct {
	Path    string
	Summary string
	Content string
	Mode    os.FileMode
}

// SetupClaudeCode computes (and unless DryRun, writes) the four integration
// artifacts: the agent adapter, a merged .mcp.json, a managed CLAUDE.md block, and
// a sourceable env.sh. It is idempotent.
func (a *App) SetupClaudeCode(opts SetupOptions) ([]SetupAction, error) {
	if opts.Model == "" {
		opts.Model = "claude-sonnet-4-6"
	}
	if opts.BrainHome == "" {
		opts.BrainHome = a.Brain.Root
	}
	adapterPath := filepath.Join(opts.BrainHome, ".bbrain", "agents", "claude-code.sh")

	actions := []SetupAction{
		{Path: adapterPath, Summary: "agent adapter (point BBRAIN_AGENT_CLI here)", Content: setup.AdapterScript(opts.Model), Mode: 0o755},
	}

	mcpPath := filepath.Join(opts.ProjectDir, ".mcp.json")
	existing, _ := os.ReadFile(mcpPath) // absent -> nil, merged into a fresh config
	merged, err := setup.MergeMCPConfig(existing, opts.BrainHome)
	if err != nil {
		return nil, fmt.Errorf("setup: .mcp.json: %w", err)
	}
	actions = append(actions, SetupAction{Path: mcpPath, Summary: "register bbrain MCP server", Content: string(merged) + "\n", Mode: 0o644})

	cmPath := filepath.Join(opts.ProjectDir, "CLAUDE.md")
	doc, _ := os.ReadFile(cmPath)
	updated := setup.UpsertManagedBlock(string(doc), setup.ClaudeMDBlock(opts.BrainHome, adapterPath))
	actions = append(actions, SetupAction{Path: cmPath, Summary: "managed CLAUDE.md block", Content: updated, Mode: 0o644})

	envPath := filepath.Join(opts.BrainHome, ".bbrain", "env.sh")
	actions = append(actions, SetupAction{Path: envPath, Summary: "BBRAIN_AGENT_CLI + BBRAIN_HOME export (source this)", Content: setup.EnvExportLine(adapterPath, opts.BrainHome) + "\n", Mode: 0o644})

	if opts.DryRun {
		return actions, nil
	}
	for _, act := range actions {
		if err := os.MkdirAll(filepath.Dir(act.Path), 0o755); err != nil {
			return nil, fmt.Errorf("setup: mkdir %s: %w", filepath.Dir(act.Path), err)
		}
		if err := os.WriteFile(act.Path, []byte(act.Content), act.Mode); err != nil {
			return nil, fmt.Errorf("setup: write %s: %w", act.Path, err)
		}
	}
	return actions, nil
}

// VaultMoveOptions configures App.VaultMove.
type VaultMoveOptions struct {
	ProjectDir string // optional: refresh this project's integration at the new home
}

// VaultMove relocates the brain to dest, rebuilds the index there, regenerates the
// brain's env.sh to point at the moved adapter (when setup was run), and optionally
// refreshes a project's integration. Returns the new root and the reindexed count.
func (a *App) VaultMove(dest string, opts VaultMoveOptions) (string, int, error) {
	if err := vault.Move(a.Brain.Root, dest); err != nil {
		return "", 0, err
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		absDest = dest
	}
	nb := New(absDest)
	indexed, err := nb.Reindex()
	if err != nil {
		return "", 0, fmt.Errorf("brain moved to %s but reindex failed: %w", absDest, err)
	}
	adapter := filepath.Join(absDest, ".bbrain", "agents", "claude-code.sh")
	if _, statErr := os.Stat(adapter); statErr == nil {
		envPath := filepath.Join(absDest, ".bbrain", "env.sh")
		if err := os.WriteFile(envPath, []byte(setup.EnvExportLine(adapter, absDest)+"\n"), 0o644); err != nil {
			return "", 0, fmt.Errorf("brain moved to %s but env.sh refresh failed: %w", absDest, err)
		}
	}
	if opts.ProjectDir != "" {
		if _, err := nb.SetupClaudeCode(SetupOptions{ProjectDir: opts.ProjectDir, BrainHome: absDest}); err != nil {
			return "", 0, fmt.Errorf("brain moved to %s but project refresh failed: %w", absDest, err)
		}
	}
	return absDest, indexed, nil
}

// Context emits a compact Markdown digest of the brain's memory for a session-start
// hook: the wiki index (if present) plus the most-recent facts, optionally filtered
// by project. limit<=0 means 10.
func (a *App) Context(project string, limit int) (string, error) {
	if limit <= 0 {
		limit = 10
	}
	facts, err := a.Store.ListFacts()
	if err != nil {
		return "", err
	}

	// visible: a fact passes the project filter when it is global (no project)
	// or its project matches the requested one. Empty `project` shows everything.
	visible := func(f fact.Fact) bool {
		return project == "" || f.Project == "" || f.Project == project
	}

	// Pinned: full body, always-on, global-visible. Not bounded by `limit`.
	var pinned []fact.Fact
	pinnedID := map[string]bool{}
	for _, f := range facts {
		if f.Pinned && visible(f) {
			pinned = append(pinned, f)
			pinnedID[f.ID] = true
		}
	}
	sort.Slice(pinned, func(i, j int) bool { return pinned[i].UpdatedAt > pinned[j].UpdatedAt })

	// Recent: project-filtered bullets, excluding anything already pinned.
	var recent []fact.Fact
	for _, f := range facts {
		if pinnedID[f.ID] || !visible(f) {
			continue
		}
		recent = append(recent, f)
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].UpdatedAt > recent[j].UpdatedAt })
	if len(recent) > limit {
		recent = recent[:limit]
	}

	var sb strings.Builder
	sb.WriteString("# BBrain memory context\n")

	if len(pinned) > 0 {
		sb.WriteString("\n## About you & pinned context\n\n")
		sb.WriteString("This is always-on context: who the user is, their preferences, and how to\n")
		sb.WriteString("work with them. Keep it in mind throughout the session — it's background to\n")
		sb.WriteString("factor into your work, not a task to act on.\n")
		for _, f := range pinned {
			sb.WriteString(fmt.Sprintf("\n### %s\n\n", f.Title))
			sb.WriteString(strings.TrimRight(f.Body, "\n"))
			sb.WriteString("\n")
		}
	}

	if b, err := os.ReadFile(filepath.Join(a.Brain.WikiDir(), "index.md")); err == nil {
		sb.WriteString("\n## Wiki index\n")
		sb.Write(b)
		sb.WriteString("\n")
	}

	sb.WriteString("\n## Recent facts\n")
	if len(recent) == 0 {
		sb.WriteString("(none yet)\n")
	}
	for _, f := range recent {
		sb.WriteString(fmt.Sprintf("- [%s] %s (%s) — id %s\n", f.Type, f.Title, f.Project, f.ID))
	}
	return sb.String(), nil
}
