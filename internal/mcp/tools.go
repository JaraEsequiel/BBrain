package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"bbrain/internal/app"
	"bbrain/internal/fact"
	"bbrain/internal/store"
)

// DefaultTools is BBrain's MCP tool catalog.
func DefaultTools() []Tool {
	return []Tool{
		{Name: "mem_save", Description: "Save a memory (fact) as Markdown and index it.", InputSchema: schemaMemSave, Handler: handleMemSave},
		{Name: "mem_search", Description: "Full-text search memories.", InputSchema: schemaMemSearch, Handler: handleMemSearch},
		{Name: "mem_get", Description: "Fetch one memory by id.", InputSchema: schemaID, Handler: handleMemGet},
		{Name: "mem_delete", Description: "Delete a memory by id.", InputSchema: schemaID, Handler: handleMemDelete},
		{Name: "mem_link", Description: "Add a reasoned typed link between two memories.", InputSchema: schemaMemLink, Handler: handleMemLink},
		{Name: "mem_why", Description: "Explain how two memories are directly related.", InputSchema: schemaMemWhy, Handler: handleMemWhy},
		{Name: "mem_related", Description: "List memories linked to/from a memory.", InputSchema: schemaID, Handler: handleMemRelated},
		{Name: "mem_candidates", Description: "Suggest memories lexically similar but not yet linked.", InputSchema: schemaMemCandidates, Handler: handleMemCandidates},
		{Name: "mem_current_project", Description: "Best-effort current project (env BBRAIN_PROJECT or cwd basename).", InputSchema: schemaEmpty, Handler: handleCurrentProject},
		{Name: "wiki_build", Description: "Distil facts into wiki pages via the configured LLM.", InputSchema: schemaWikiBuild, Handler: handleWikiBuild},
		{Name: "wiki_link", Description: "Grow the fact graph via the configured LLM.", InputSchema: schemaWikiLink, Handler: handleWikiLink},
		{Name: "wiki_lint", Description: "Check (and optionally --fix) wiki/fact consistency.", InputSchema: schemaWikiLint, Handler: handleWikiLint},
	}
}

// ---- schemas (hand-authored JSON Schema literals) ----

var (
	schemaEmpty   = json.RawMessage(`{"type":"object"}`)
	schemaID      = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
	schemaMemSave = json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"},"project":{"type":"string"},"scope":{"type":"string"},"topic_key":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}}},"required":["type","title","body"]}`)
	schemaMemSearch = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`)
	schemaMemLink = json.RawMessage(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"},"relation":{"type":"string"},"why":{"type":"string"}},"required":["from","to","relation","why"]}`)
	schemaMemWhy  = json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"]}`)
	schemaMemCandidates = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"limit":{"type":"integer"}},"required":["id"]}`)
	schemaWikiBuild = json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"},"scope":{"type":"string"},"categories":{"type":"array","items":{"type":"string"}},"dry_run":{"type":"boolean"}}}`)
	schemaWikiLink  = json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"},"scope":{"type":"string"},"limit":{"type":"integer"},"dry_run":{"type":"boolean"}}}`)
	schemaWikiLint  = json.RawMessage(`{"type":"object","properties":{"categories":{"type":"array","items":{"type":"string"}},"fix":{"type":"boolean"}}}`)
)

// ---- views ----

func factView(f fact.Fact) map[string]any {
	return map[string]any{
		"id": f.ID, "type": f.Type, "scope": f.Scope, "project": f.Project,
		"title": f.Title, "body": f.Body, "tags": f.Tags,
		"created_at": f.CreatedAt, "updated_at": f.UpdatedAt,
		"revision_count": f.RevisionCount, "links": f.Links,
	}
}

// ---- handlers ----

type memSaveArgs struct {
	Type, Title, Body, Project, Scope, TopicKey string
	Tags                                        []string
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
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Type, m.Title, m.Body = raw.Type, raw.Title, raw.Body
	m.Project, m.Scope, m.TopicKey, m.Tags = raw.Project, raw.Scope, raw.TopicKey, raw.Tags
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
	})
	if err != nil {
		return nil, err
	}
	return factView(f), nil
}

func handleMemSearch(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 {
		in.Limit = 10
	}
	res, err := a.Search(in.Query, in.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"results": res}, nil
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
	if !ok {
		return map[string]any{"found": false}, nil
	}
	return factView(f), nil
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
