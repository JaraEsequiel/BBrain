package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JaraEsequiel/BBrain/internal/app"
	"github.com/JaraEsequiel/BBrain/internal/fact"
	"github.com/JaraEsequiel/BBrain/internal/store"
)

// DefaultTools is BBrain's MCP tool catalog.
func DefaultTools() []Tool {
	return []Tool{
		{Name: "mem_save", Description: "Save a memory (fact) as Markdown and index it.", InputSchema: schemaMemSave, Handler: handleMemSave},
		{Name: "mem_search", Description: "Full-text search memories.", InputSchema: schemaMemSearch, Handler: handleMemSearch},
		{Name: "mem_get", Description: "Fetch one memory by id.", InputSchema: schemaID, Handler: handleMemGet},
		{Name: "mem_delete", Description: "Delete a memory by id.", InputSchema: schemaID, Handler: handleMemDelete},
		{Name: "mem_archive", Description: "Archive facts by explicit id (batch). Only in response to explicit user intent (\"archive that\", \"remove that from search\") — never autonomous housekeeping, never bulk-by-filter.", InputSchema: schemaIDs, Handler: handleMemArchive},
		{Name: "mem_unarchive", Description: "Unarchive facts by explicit id (batch). Only in response to explicit user intent — never autonomous housekeeping, never bulk-by-filter.", InputSchema: schemaIDs, Handler: handleMemUnarchive},
		{Name: "mem_link", Description: "Add a reasoned typed link between two memories.", InputSchema: schemaMemLink, Handler: handleMemLink},
		{Name: "mem_why", Description: "Explain how two memories are directly related.", InputSchema: schemaMemWhy, Handler: handleMemWhy},
		{Name: "mem_related", Description: "List memories linked to/from a memory.", InputSchema: schemaID, Handler: handleMemRelated},
		{Name: "mem_candidates", Description: "Suggest memories lexically similar but not yet linked.", InputSchema: schemaMemCandidates, Handler: handleMemCandidates},
		{Name: "mem_browse", Description: "List facts filtered by project/type, without a search query. Returns title+id+type only — use mem_get for the full body.", InputSchema: schemaMemBrowse, Handler: handleMemBrowse},
		{Name: "mem_current_project", Description: "Best-effort current project (env BBRAIN_PROJECT or cwd basename).", InputSchema: schemaEmpty, Handler: handleCurrentProject},
		{Name: "wiki_build", Description: "Distil facts into wiki pages via the configured LLM.", InputSchema: schemaWikiBuild, Handler: handleWikiBuild},
		{Name: "wiki_link", Description: "Grow the fact graph via the configured LLM.", InputSchema: schemaWikiLink, Handler: handleWikiLink},
		{Name: "wiki_lint", Description: "Check (and optionally --fix) wiki/fact consistency.", InputSchema: schemaWikiLint, Handler: handleWikiLint},
	}
}

// ---- schemas (hand-authored JSON Schema literals) ----

var (
	schemaEmpty         = json.RawMessage(`{"type":"object"}`)
	schemaID            = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
	schemaIDs           = json.RawMessage(`{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string"}}},"required":["ids"]}`)
	schemaMemSave       = json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"},"project":{"type":"string"},"scope":{"type":"string"},"topic_key":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}},"pinned":{"type":"boolean"}},"required":["type","title","body"]}`)
	schemaMemSearch     = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"},"project":{"type":"string"},"type":{"type":"string"}},"required":["query"]}`)
	schemaMemLink       = json.RawMessage(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"},"relation":{"type":"string"},"why":{"type":"string"}},"required":["from","to","relation","why"]}`)
	schemaMemWhy        = json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"]}`)
	schemaMemCandidates = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"limit":{"type":"integer"}},"required":["id"]}`)
	schemaMemBrowse     = json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"},"type":{"type":"string"}}}`)
	schemaWikiBuild     = json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"},"scope":{"type":"string"},"categories":{"type":"array","items":{"type":"string"}},"dry_run":{"type":"boolean"}}}`)
	schemaWikiLink      = json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"},"scope":{"type":"string"},"limit":{"type":"integer"},"dry_run":{"type":"boolean"}}}`)
	schemaWikiLint      = json.RawMessage(`{"type":"object","properties":{"categories":{"type":"array","items":{"type":"string"}},"fix":{"type":"boolean"}}}`)
)

// ---- views ----

func factView(f fact.Fact) map[string]any {
	return map[string]any{
		"id": f.ID, "type": f.Type, "scope": f.Scope, "project": f.Project,
		"title": f.Title, "body": f.Body, "tags": f.Tags,
		"created_at": f.CreatedAt, "updated_at": f.UpdatedAt,
		"revision_count": f.RevisionCount, "links": f.Links,
		"pinned": f.Pinned,
	}
}

// ---- handlers ----

type memSaveArgs struct {
	Type, Title, Body, Project, Scope, TopicKey string
	Tags                                        []string
	Pinned                                      bool
}

func (m *memSaveArgs) UnmarshalJSON(b []byte) error {
	var raw struct {
		Type     string   `json:"type"`
		Title    string   `json:"title"`
		Body     string   `json:"body"`
		Project  string   `json:"project"`
		Scope    string   `json:"scope"`
		TopicKey string   `json:"topic_key"`
		Tags     []string `json:"tags"`
		Pinned   bool     `json:"pinned"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Type, m.Title, m.Body = raw.Type, raw.Title, raw.Body
	m.Project, m.Scope, m.TopicKey, m.Tags = raw.Project, raw.Scope, raw.TopicKey, raw.Tags
	m.Pinned = raw.Pinned
	return nil
}

func handleMemSave(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in memSaveArgs
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	f, err := a.Save(store.SaveInput{
		Type: in.Type, Title: in.Title, Body: in.Body,
		Project: in.Project, Scope: in.Scope, TopicKey: in.TopicKey, Tags: in.Tags,
		Pinned: in.Pinned,
	})
	if err != nil {
		return nil, err
	}
	view := factView(f)
	// Lexical hint: surface existing similar, not-yet-linked facts so the agent can
	// link/reconcile (mem_link conflicts-with/supersedes, or re-save with the same
	// topic_key) instead of silently duplicating or contradicting. A hint failure
	// must never fail the save, so the error is intentionally ignored.
	if cands, cerr := a.Candidates(f.ID, 5); cerr == nil && len(cands) > 0 {
		view["related"] = cands
	}
	return view, nil
}

func handleMemSearch(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Query   string `json:"query"`
		Limit   int    `json:"limit"`
		Project string `json:"project"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 {
		in.Limit = 10
	}
	res, stale, err := a.Search(in.Query, in.Limit, in.Project, in.Type)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"results": res}
	if stale {
		out["stale"] = true
		out["notice"] = "search index predates a schema change and hasn't been reindexed (run `bbrain reindex`) — results may be incomplete"
	}
	return out, nil
}

func handleMemGet(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	f, ok, err := a.Get(in.ID)
	if err != nil {
		return nil, err
	}
	if ok {
		return factView(f), nil
	}
	// Not active — fall back to the archive tier (Q2): archived is "out of the
	// way", not unreachable. Active responses stay byte-identical to today.
	f, ok, err = a.GetArchived(in.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{"found": false}, nil
	}
	view := factView(f)
	view["archived"] = true
	return view, nil
}

func handleMemDelete(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	deleted, err := a.Delete(in.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": deleted}, nil
}

// maxBatchIDs bounds mem_archive/mem_unarchive's ids array: these tools are
// for explicit, small, user-driven batches ("archive that"), not bulk ops —
// a huge list is already outside the guardrail's intent, so reject it before
// looping rather than doing unbounded filesystem work.
const maxBatchIDs = 1000

func handleMemArchive(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if len(in.IDs) > maxBatchIDs {
		return nil, fmt.Errorf("mem_archive: %d ids exceeds the %d-id batch limit", len(in.IDs), maxBatchIDs)
	}
	archived := make([]string, 0, len(in.IDs))
	for _, id := range in.IDs {
		if _, err := a.Archive(id); err != nil {
			continue
		}
		archived = append(archived, id)
	}
	return map[string]any{"ids": archived, "count": len(archived)}, nil
}

func handleMemUnarchive(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if len(in.IDs) > maxBatchIDs {
		return nil, fmt.Errorf("mem_unarchive: %d ids exceeds the %d-id batch limit", len(in.IDs), maxBatchIDs)
	}
	unarchived := make([]string, 0, len(in.IDs))
	for _, id := range in.IDs {
		if _, err := a.Unarchive(id); err != nil {
			continue
		}
		unarchived = append(unarchived, id)
	}
	return map[string]any{"ids": unarchived, "count": len(unarchived)}, nil
}

func handleMemLink(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var rawArgs struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Relation string `json:"relation"`
		Why      string `json:"why"`
	}
	if err := json.Unmarshal(raw, &rawArgs); err != nil {
		return nil, err
	}
	f, err := a.Link(rawArgs.From, rawArgs.To, rawArgs.Relation, rawArgs.Why)
	if err != nil {
		return nil, err
	}
	return factView(f), nil
}

func handleMemWhy(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	edges, err := a.Why(in.A, in.B)
	if err != nil {
		return nil, err
	}
	return map[string]any{"edges": edges}, nil
}

func handleMemRelated(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	n, err := a.Related(in.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"neighbors": n}, nil
}

func handleMemCandidates(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		ID    string `json:"id"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 {
		in.Limit = 8
	}
	res, err := a.Candidates(in.ID, in.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"candidates": res}, nil
}

func handleMemBrowse(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Project string `json:"project"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	res, err := a.Browse(in.Project, in.Type)
	if err != nil {
		return nil, err
	}
	return map[string]any{"results": res}, nil
}

func handleCurrentProject(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	return map[string]any{"project": currentProject()}, nil
}

// currentProject is a best-effort guess: $BBRAIN_PROJECT, else the cwd basename.
func currentProject() string {
	if p := strings.TrimSpace(os.Getenv("BBRAIN_PROJECT")); p != "" {
		return p
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Base(wd)
	}
	return ""
}

func handleWikiBuild(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Project    string   `json:"project"`
		Scope      string   `json:"scope"`
		Categories []string `json:"categories"`
		DryRun     bool     `json:"dry_run"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return a.WikiBuild(ctx, app.WikiBuildOptions{Project: in.Project, Scope: in.Scope, Categories: in.Categories, DryRun: in.DryRun})
}

func handleWikiLink(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Project string `json:"project"`
		Scope   string `json:"scope"`
		Limit   int    `json:"limit"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return a.WikiLink(ctx, app.WikiLinkOptions{Project: in.Project, Scope: in.Scope, Limit: in.Limit, DryRun: in.DryRun})
}

func handleWikiLint(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Categories []string `json:"categories"`
		Fix        bool     `json:"fix"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return a.WikiLint(app.WikiLintOptions{Categories: in.Categories, Fix: in.Fix})
}
