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

	"bbrain/internal/brain"
	"bbrain/internal/fact"
	"bbrain/internal/index"
	"bbrain/internal/llm"
	"bbrain/internal/setup"
	"bbrain/internal/store"
	"bbrain/internal/vault"
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

// Get returns a fact by id (ok=false if absent).
func (a *App) Get(id string) (fact.Fact, bool, error) {
	return a.Store.Get(id)
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
	actions = append(actions, SetupAction{Path: envPath, Summary: "BBRAIN_AGENT_CLI export (source this)", Content: setup.EnvExportLine(adapterPath) + "\n", Mode: 0o644})

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
		if err := os.WriteFile(envPath, []byte(setup.EnvExportLine(adapter)+"\n"), 0o644); err != nil {
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
