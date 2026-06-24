# BBrain — Plan 7: `bbrain install` wizard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A step-by-step interactive wizard `bbrain install` that creates the memory vault (`L/memory` + a degraded-mode `L/CLAUDE.md`) and installs the Claude Code integration (MCP, integration CLAUDE.md block, SessionStart context hook, `/bbrain-recall` + `/bbrain-remember` skills, LLM adapter) at User-global or Project scope, plus `bbrain context` (hook output) and `bbrain uninstall` (clean reversal). Stdlib only.

**Architecture:** New pure builders in `internal/setup` (degraded CLAUDE.md, hook entry + settings merge/remove, skill bodies, mcp/block removers). New `internal/install` package: the stdin `Wizard`, the scope-aware `PlanInstall`/`PlanUninstall` (pure: compute `[]Action`), and `Apply`. `internal/app` gains `Context`. `cmd/bbrain` adds `install`/`uninstall`/`context` and removes `setup`.

**Tech Stack:** Go 1.25, stdlib (`bufio`, `encoding/json`, `io`, `os`, `os/exec`, `path/filepath`, `sort`, `strings`, `flag`). No new dependencies.

## Global Constraints

- **Module:** `bbrain`. **Go:** `go 1.25`. **No new dependencies** (the "wizard" is an stdin prompt flow, never a TUI library).
- **`BBRAIN_HOME = L/memory`** — the brain lives under `memory/`, separate from `L/CLAUDE.md`.
- **Non-destructive + reversible:** shared-file edits are managed merges — a `<!-- BBRAIN:BEGIN -->…END` block (Markdown) or a JSON merge keyed by `bbrain` / a hook entry tagged by `command=bbrain args⊇context`. `uninstall` removes exactly those. Skills live in `bbrain-*` dirs.
- **Idempotent:** re-running `install` replaces only BBrain's managed regions/keys; no duplicates.
- **`--dry-run`** prints actions + content without writing; **non-interactive flags** (`--vault/--scope/--agent/--non-interactive`) drive the same engine (used by tests).
- **Scope → targets:** MCP user→`claude mcp add -s user` / project→`./.mcp.json`; CLAUDE.md user→`~/.claude/CLAUDE.md` / project→`./CLAUDE.md`; hook user→`~/.claude/settings.json` / project→`./.claude/settings.json`; skills user→`~/.claude/skills/<n>/SKILL.md` / project→`./.claude/skills/<n>/SKILL.md`.
- **Layering:** `internal/setup` pure (stdlib only); `internal/install` imports `setup`+`brain`+stdlib (NOT `app`); `app` imports `install` only if needed (it does not here — `cmd` calls `install` directly); `cmd` wires the commands. No import cycles.
- `go test ./...` green + `go vet` clean before each commit.

Design spec: `docs/superpowers/specs/2026-06-23-bbrain-install-wizard-design.md`.

## Verified existing interfaces (do not re-implement)

- `internal/setup`: `const BlockBegin/BlockEnd`; `AdapterScript(model) string`; `MCPEntry(brainHome) map[string]any`; `MergeMCPConfig(existing []byte, brainHome) ([]byte, error)`; `UpsertManagedBlock(doc, block) string`; `ClaudeMDBlock(brainHome, adapterPath) string`; `EnvExportLine(adapterPath) string`. Imports `encoding/json`, `regexp`, `strings`.
- `internal/brain`: `New(root) Brain`; `(Brain).Init() error`; `(Brain).WikiDir() string`, `Root`.
- `internal/app`: `New(root) *App`; `(*App).Init()`; `(*App).Search(q,limit) ([]index.Result, error)`; `(*App).Store.ListFacts() ([]fact.Fact, error)`; `a.Brain.WikiDir()`. `fact.Fact{ID,Type,Project,Title,UpdatedAt,...}`.
- `cmd/bbrain/main.go`: `brainRoot() string`; `runWithIn(args, stdin io.Reader, stdout, stderr io.Writer) int` (switch with `case "setup"` at ~82 + `cmdSetup` at ~371 — REMOVED in Task 4); usage line at ~44. `app.SetupClaudeCode` stays (Plan 6 `VaultMove` uses it).

## Execution note (parallelism)

Task 1 (`internal/setup` extensions) and Task 3 (`app.Context` + `cmd context`) touch disjoint files and may run **in parallel**. Task 2 (`internal/install`) depends on Task 1. Task 4 (cmd install/uninstall) depends on Tasks 1–3.

---

## Task 1: `internal/setup` — new pure builders

**Files:** Modify `internal/setup/setup.go`, `internal/setup/setup_test.go`.

**Interfaces — Produces:** `DegradedClaudeMD(memoryDir string) string`; `SessionStartHookEntry(memoryDir string) map[string]any`; `MergeSettingsHook(existing []byte, memoryDir string) ([]byte, error)`; `RemoveSettingsHook(existing []byte) ([]byte, error)`; `RemoveMCPServer(existing []byte) ([]byte, error)`; `RecallSkill() string`; `RememberSkill() string`; `RemoveManagedBlock(doc string) string`.

- [ ] **Step 1: Write the failing tests** — append to `internal/setup/setup_test.go`:

```go
func TestDegradedClaudeMD(t *testing.T) {
	s := DegradedClaudeMD("/vault/memory")
	for _, want := range []string{"memory/raws/facts", "frontmatter", "[[fact-id]]", "/vault/memory", "wiki/index.md"} {
		if !strings.Contains(s, want) {
			t.Fatalf("degraded doc missing %q:\n%s", want, s)
		}
	}
}

func TestSessionStartHookAndMerge(t *testing.T) {
	// merge into a settings.json that already has an unrelated hook + key
	existing := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]},"env":{"X":"1"}}`)
	out, err := MergeSettingsHook(existing, "/v/memory")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	hooks := root["hooks"].(map[string]any)
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Fatal("merge dropped the unrelated PreToolUse hook")
	}
	if root["env"] == nil {
		t.Fatal("merge dropped top-level env")
	}
	ss := hooks["SessionStart"].([]any)
	if len(ss) != 1 {
		t.Fatalf("want 1 SessionStart entry, got %d", len(ss))
	}
	// the hook command is bbrain context --home /v/memory
	js, _ := json.Marshal(ss)
	for _, want := range []string{`"bbrain"`, `"context"`, `"--home"`, `/v/memory`} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("hook missing %q:\n%s", want, js)
		}
	}
	// idempotent: merging again yields exactly one SessionStart entry
	out2, _ := MergeSettingsHook(out, "/v/memory")
	json.Unmarshal(out2, &root)
	if n := len(root["hooks"].(map[string]any)["SessionStart"].([]any)); n != 1 {
		t.Fatalf("merge not idempotent: %d SessionStart entries", n)
	}
	// removal strips ours, keeps the unrelated one
	rem, err := RemoveSettingsHook(out2)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(rem, &root)
	h := root["hooks"].(map[string]any)
	if _, ok := h["SessionStart"]; ok {
		t.Fatalf("RemoveSettingsHook left SessionStart:\n%s", rem)
	}
	if _, ok := h["PreToolUse"]; !ok {
		t.Fatal("RemoveSettingsHook dropped the unrelated hook")
	}
}

func TestRemoveMCPServer(t *testing.T) {
	existing := []byte(`{"mcpServers":{"bbrain":{"type":"stdio"},"other":{"type":"stdio"}}}`)
	out, err := RemoveMCPServer(existing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "bbrain") {
		t.Fatalf("bbrain not removed:\n%s", out)
	}
	if !strings.Contains(string(out), "other") {
		t.Fatalf("removal dropped the other server:\n%s", out)
	}
}

func TestSkillsAndRemoveBlock(t *testing.T) {
	if r := RecallSkill(); !strings.Contains(r, "description:") || !strings.Contains(r, "mcp__bbrain__mem_search") {
		t.Fatalf("recall skill:\n%s", r)
	}
	if r := RememberSkill(); !strings.Contains(r, "mcp__bbrain__mem_save") {
		t.Fatalf("remember skill:\n%s", r)
	}
	doc := "# Top\n\n" + ClaudeMDBlock("/m", "/a.sh") + "\n"
	out := RemoveManagedBlock(doc)
	if strings.Contains(out, BlockBegin) || strings.Contains(out, BlockEnd) {
		t.Fatalf("managed block not removed:\n%s", out)
	}
	if !strings.Contains(out, "# Top") {
		t.Fatalf("RemoveManagedBlock dropped user content:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail** — `cd BBrain && go test ./internal/setup/` → FAIL (undefined identifiers).

- [ ] **Step 3: Implement** — append to `internal/setup/setup.go` (imports `encoding/json`, `strings` already present):

```go
// DegradedClaudeMD is the L/CLAUDE.md placed at the vault root: it teaches a
// session with NO bbrain MCP/CLI how to read and write the memory by hand.
func DegradedClaudeMD(memoryDir string) string {
	return `# BBrain memory (manual access)

This folder is a BBrain memory vault. A session opened here WITHOUT the bbrain MCP
server or CLI must read and write memory by hand with plain file tools. The memory
lives at ` + memoryDir + `.

## Layout
- memory/raws/facts/<id>.md — one fact per file: YAML frontmatter, then "# Title", then a Markdown body.
  Frontmatter: id (<YYYY-MM-DD>-<slug>), type, scope, project, tags, links (target/relation/why),
  created_at, updated_at (RFC3339 UTC), revision_count. The body cites other facts as [[fact-id]].
- memory/wiki/ — distilled pages; wiki/index.md is the catalog, wiki/log.md the history.

## To recall
Read memory/wiki/index.md first, then open the relevant memory/raws/facts/*.md.
List titles with: grep -rh "^# " memory/raws/facts/

## To remember
Create memory/raws/facts/<date>-<kebab-title>.md with the frontmatter above (unique id, RFC3339 UTC
timestamps, revision_count: 1). The derived index rebuilds when the CLI/MCP next runs.
`
}

// SessionStartHookEntry is the Claude Code SessionStart hook that injects memory
// context by running "bbrain context --home <memoryDir>".
func SessionStartHookEntry(memoryDir string) map[string]any {
	return map[string]any{
		"matcher": "startup|resume",
		"hooks": []map[string]any{
			{"type": "command", "command": "bbrain", "args": []string{"context", "--home", memoryDir}, "timeout": 30},
		},
	}
}

// isBBrainHook reports whether a SessionStart entry is BBrain's (command "bbrain"
// with "context" in its args), so merge/remove can target exactly it.
func isBBrainHook(e any) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	hs, ok := m["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hs {
		hm, ok := h.(map[string]any)
		if !ok || hm["command"] != "bbrain" {
			continue
		}
		if args, ok := hm["args"].([]any); ok {
			for _, a := range args {
				if a == "context" {
					return true
				}
			}
		}
	}
	return false
}

// MergeSettingsHook inserts/replaces BBrain's SessionStart hook in a settings.json,
// preserving every other hook and top-level key. Idempotent.
func MergeSettingsHook(existing []byte, memoryDir string) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(existing))) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, err
		}
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	var kept []any
	if arr, ok := hooks["SessionStart"].([]any); ok {
		for _, e := range arr {
			if !isBBrainHook(e) {
				kept = append(kept, e)
			}
		}
	}
	kept = append(kept, SessionStartHookEntry(memoryDir))
	hooks["SessionStart"] = kept
	root["hooks"] = hooks
	return json.MarshalIndent(root, "", "  ")
}

// RemoveSettingsHook removes BBrain's SessionStart hook (and empties SessionStart/hooks
// if nothing else remains), preserving all other content.
func RemoveSettingsHook(existing []byte) ([]byte, error) {
	if len(strings.TrimSpace(string(existing))) == 0 {
		return existing, nil
	}
	root := map[string]any{}
	if err := json.Unmarshal(existing, &root); err != nil {
		return nil, err
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return existing, nil
	}
	if arr, ok := hooks["SessionStart"].([]any); ok {
		var kept []any
		for _, e := range arr {
			if !isBBrainHook(e) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(hooks, "SessionStart")
		} else {
			hooks["SessionStart"] = kept
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	return json.MarshalIndent(root, "", "  ")
}

// RemoveMCPServer drops the bbrain server from a .mcp.json, preserving others.
func RemoveMCPServer(existing []byte) ([]byte, error) {
	if len(strings.TrimSpace(string(existing))) == 0 {
		return existing, nil
	}
	root := map[string]any{}
	if err := json.Unmarshal(existing, &root); err != nil {
		return nil, err
	}
	if servers, ok := root["mcpServers"].(map[string]any); ok {
		delete(servers, "bbrain")
		if len(servers) == 0 {
			delete(root, "mcpServers")
		} else {
			root["mcpServers"] = servers
		}
	}
	return json.MarshalIndent(root, "", "  ")
}

// RecallSkill is the /bbrain-recall SKILL.md body.
func RecallSkill() string {
	return `---
description: Recall relevant memories from BBrain for the current task
disable-model-invocation: false
---

# Recall (BBrain)

Search BBrain memory for context relevant to the request, then summarize.

1. Call mcp__bbrain__mem_search with a query from the user's request (use $ARGUMENTS if provided).
2. For promising hits, call mcp__bbrain__mem_get to read the full fact.
3. Summarize the relevant decisions and learnings, citing them by id.
`
}

// RememberSkill is the /bbrain-remember SKILL.md body.
func RememberSkill() string {
	return `---
description: Save a durable decision or learning to BBrain memory
disable-model-invocation: false
---

# Remember (BBrain)

Persist a durable fact to BBrain memory.

Call mcp__bbrain__mem_save with:
- title: a concise summary ($ARGUMENTS if the user provided the text)
- body: details and rationale (Markdown; cite related facts as [[fact-id]])
- type: one of decision|architecture|bugfix|pattern|config|discovery|learning
- project and scope when known.
`
}

// RemoveManagedBlock strips the BBrain managed block from doc (for uninstall).
func RemoveManagedBlock(doc string) string {
	start := strings.Index(doc, BlockBegin)
	end := strings.Index(doc, BlockEnd)
	if start >= 0 && end > start {
		return strings.TrimLeft(doc[:start]+doc[end+len(BlockEnd):], "\n")
	}
	return doc
}
```

- [ ] **Step 4: Run tests** — `go test ./internal/setup/` → PASS; `go vet ./internal/setup/`.
- [ ] **Step 5: Commit** — `git add internal/setup/ && git commit -m "feat(setup): builders for degraded CLAUDE.md, SessionStart hook, skills, removers"`

---

## Task 2: `internal/install` — wizard + scope-aware plan/apply

**Files:** Create `internal/install/install.go`, `internal/install/install_test.go`.

**Interfaces:**
- Consumes: `bbrain/internal/setup` (Task 1 + existing), `bbrain/internal/brain`; stdlib `bufio`, `fmt`, `io`, `os`, `os/exec`, `path/filepath`, `strings`.
- Produces: `type Options struct {...}`, `type Action struct {...}`, `func PlanInstall(Options) ([]Action, error)`, `func PlanUninstall(Options) ([]Action, error)`, `func Apply([]Action) error`, `func Wizard(in io.Reader, out io.Writer, def Options) (Options, error)`.

- [ ] **Step 1: Write the failing tests** — create `internal/install/install_test.go`:

```go
package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func projOpts(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	return Options{
		Vault:      filepath.Join(root, "vault"),
		Agent:      "claude-code",
		Scope:      "project",
		Model:      "claude-sonnet-4-6",
		HomeDir:    filepath.Join(root, "home"),
		ProjectDir: filepath.Join(root, "proj"),
	}
}

func TestPlanInstallProjectScopeActions(t *testing.T) {
	o := projOpts(t)
	acts, err := PlanInstall(o)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	paths := []string{}
	for _, a := range acts {
		kinds[a.Kind]++
		paths = append(paths, a.Path)
	}
	if kinds["mkbrain"] != 1 || kinds["merge-mcp"] != 1 || kinds["merge-settings"] != 1 {
		t.Fatalf("unexpected action kinds: %v", kinds)
	}
	joined := strings.Join(paths, "\n")
	for _, want := range []string{
		filepath.Join(o.Vault, "CLAUDE.md"),
		filepath.Join(o.Vault, "memory"),
		filepath.Join(o.ProjectDir, ".mcp.json"),
		filepath.Join(o.ProjectDir, "CLAUDE.md"),
		filepath.Join(o.ProjectDir, ".claude", "settings.json"),
		filepath.Join(o.ProjectDir, ".claude", "skills", "bbrain-recall", "SKILL.md"),
		filepath.Join(o.ProjectDir, ".claude", "skills", "bbrain-remember", "SKILL.md"),
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plan missing action for %s:\n%s", want, joined)
		}
	}
}

func TestPlanInstallUserScopeUsesMCPCLI(t *testing.T) {
	o := projOpts(t)
	o.Scope = "user"
	acts, err := PlanInstall(o)
	if err != nil {
		t.Fatal(err)
	}
	var sawCLI bool
	for _, a := range acts {
		if a.Kind == "mcp-cli" {
			sawCLI = true
			if strings.Join(a.Argv, " ") != "claude mcp add -s user bbrain -e BBRAIN_HOME="+filepath.Join(o.Vault, "memory")+" -- bbrain mcp" {
				t.Fatalf("mcp-cli argv = %v", a.Argv)
			}
		}
		// user-scope CLAUDE.md/settings go under HomeDir/.claude
		if a.Kind == "merge-settings" && !strings.Contains(a.Path, filepath.Join(o.HomeDir, ".claude")) {
			t.Fatalf("user-scope settings path = %s", a.Path)
		}
	}
	if !sawCLI {
		t.Fatal("user scope must register MCP via the claude CLI")
	}
}

func TestApplyAndUninstallProjectScope(t *testing.T) {
	o := projOpts(t)
	acts, err := PlanInstall(o)
	must(t, err)
	must(t, Apply(acts))

	// vault brain + degraded CLAUDE.md
	if _, err := os.Stat(filepath.Join(o.Vault, "memory", "raws", "facts")); err != nil {
		t.Fatalf("memory brain not created: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(o.Vault, "CLAUDE.md")); !strings.Contains(string(b), "manual access") {
		t.Fatal("degraded CLAUDE.md missing")
	}
	// project integration
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, ".mcp.json")); !strings.Contains(string(b), "bbrain") {
		t.Fatal(".mcp.json missing bbrain")
	}
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, "CLAUDE.md")); !strings.Contains(string(b), "BBRAIN:BEGIN") {
		t.Fatal("integration CLAUDE.md block missing")
	}
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, ".claude", "settings.json")); !strings.Contains(string(b), "SessionStart") {
		t.Fatal("SessionStart hook missing")
	}
	if _, err := os.Stat(filepath.Join(o.ProjectDir, ".claude", "skills", "bbrain-recall", "SKILL.md")); err != nil {
		t.Fatalf("recall skill missing: %v", err)
	}
	// idempotent re-apply
	acts2, _ := PlanInstall(o)
	must(t, Apply(acts2))
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, "CLAUDE.md")); strings.Count(string(b), "BBRAIN:BEGIN") != 1 {
		t.Fatal("re-apply duplicated the managed block")
	}

	// uninstall reverses (vault kept)
	uacts, err := PlanUninstall(o)
	must(t, err)
	must(t, Apply(uacts))
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, "CLAUDE.md")); strings.Contains(string(b), "BBRAIN:BEGIN") {
		t.Fatal("uninstall left the managed block")
	}
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, ".mcp.json")); strings.Contains(string(b), "bbrain") {
		t.Fatal("uninstall left bbrain in .mcp.json")
	}
	if _, err := os.Stat(filepath.Join(o.ProjectDir, ".claude", "skills", "bbrain-recall")); !os.IsNotExist(err) {
		t.Fatal("uninstall left the recall skill")
	}
	if _, err := os.Stat(filepath.Join(o.Vault, "memory")); err != nil {
		t.Fatal("uninstall without --purge deleted the vault")
	}
}

func TestWizardParsesStdin(t *testing.T) {
	in := strings.NewReader("/my/vault\n\nuser\n") // vault, agent(blank→default), scope
	def := Options{Vault: "/default", Agent: "claude-code", Scope: "project"}
	var out strings.Builder
	got, err := Wizard(in, &out, def)
	must(t, err)
	if got.Vault != "/my/vault" || got.Agent != "claude-code" || got.Scope != "user" {
		t.Fatalf("wizard result = %+v", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail** — `cd BBrain && go test ./internal/install/` → FAIL (package missing).

- [ ] **Step 3: Implement** — create `internal/install/install.go`:

```go
// Package install builds and applies the BBrain → Claude Code integration: a stdin
// wizard, a scope-aware plan of filesystem/CLI actions, and an applier. Reversible.
package install

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"bbrain/internal/brain"
	"bbrain/internal/setup"
)

// Options is a resolved install/uninstall configuration.
type Options struct {
	Vault      string // L (chosen vault location)
	Agent      string // "claude-code"
	Scope      string // "user" | "project"
	Model      string // claude model for the adapter
	HomeDir    string // ~ root (Claude Code user config parent); for tests
	ProjectDir string // cwd for project scope; for tests
	DryRun     bool
	Purge      bool // uninstall: also delete the vault
}

func (o Options) memoryDir() string  { return filepath.Join(o.Vault, "memory") }
func (o Options) adapterPath() string {
	return filepath.Join(o.memoryDir(), ".bbrain", "agents", "claude-code.sh")
}
func (o Options) claudeUserDir() string { return filepath.Join(o.HomeDir, ".claude") }

func (o Options) claudeMDPath() string {
	if o.Scope == "user" {
		return filepath.Join(o.claudeUserDir(), "CLAUDE.md")
	}
	return filepath.Join(o.ProjectDir, "CLAUDE.md")
}
func (o Options) settingsPath() string {
	if o.Scope == "user" {
		return filepath.Join(o.claudeUserDir(), "settings.json")
	}
	return filepath.Join(o.ProjectDir, ".claude", "settings.json")
}
func (o Options) skillsDir() string {
	if o.Scope == "user" {
		return filepath.Join(o.claudeUserDir(), "skills")
	}
	return filepath.Join(o.ProjectDir, ".claude", "skills")
}

// Action is one filesystem/CLI operation in a plan.
type Action struct {
	Kind    string   // mkbrain|write|merge-md|merge-mcp|merge-settings|mcp-cli|remove-md|remove-mcp|remove-settings|rmdir
	Path    string   // target (empty for mcp-cli)
	Summary string
	Content string   // new full content (for write/merge; shown on dry-run)
	Mode    os.FileMode
	Argv    []string // for mcp-cli
}

// PlanInstall computes the ordered actions for opts (pure: reads existing files to
// compute merges but writes nothing).
func PlanInstall(o Options) ([]Action, error) {
	mem := o.memoryDir()
	adapter := o.adapterPath()
	var acts []Action

	// 1. vault: brain + degraded CLAUDE.md + adapter + env.sh
	acts = append(acts,
		Action{Kind: "mkbrain", Path: mem, Summary: "create memory vault (BBRAIN_HOME)"},
		Action{Kind: "write", Path: filepath.Join(o.Vault, "CLAUDE.md"), Summary: "degraded-mode reader doc",
			Content: setup.DegradedClaudeMD(mem), Mode: 0o644},
		Action{Kind: "write", Path: adapter, Summary: "LLM agent adapter",
			Content: setup.AdapterScript(o.Model), Mode: 0o755},
		Action{Kind: "write", Path: filepath.Join(mem, ".bbrain", "env.sh"), Summary: "BBRAIN_AGENT_CLI export",
			Content: setup.EnvExportLine(adapter) + "\n", Mode: 0o644},
	)

	// 2. integration CLAUDE.md (managed block)
	cmPath := o.claudeMDPath()
	doc, _ := os.ReadFile(cmPath)
	acts = append(acts, Action{Kind: "merge-md", Path: cmPath, Summary: "integration CLAUDE.md block",
		Content: setup.UpsertManagedBlock(string(doc), setup.ClaudeMDBlock(mem, adapter)), Mode: 0o644})

	// 3. MCP registration
	if o.Scope == "user" {
		acts = append(acts, Action{Kind: "mcp-cli", Summary: "register bbrain MCP (user scope)",
			Argv: []string{"claude", "mcp", "add", "-s", "user", "bbrain", "-e", "BBRAIN_HOME=" + mem, "--", "bbrain", "mcp"}})
	} else {
		mcpPath := filepath.Join(o.ProjectDir, ".mcp.json")
		existing, _ := os.ReadFile(mcpPath)
		merged, err := setup.MergeMCPConfig(existing, mem)
		if err != nil {
			return nil, err
		}
		acts = append(acts, Action{Kind: "merge-mcp", Path: mcpPath, Summary: "register bbrain MCP (project)",
			Content: string(merged) + "\n", Mode: 0o644})
	}

	// 4. SessionStart hook
	setPath := o.settingsPath()
	setBytes, _ := os.ReadFile(setPath)
	mergedSet, err := setup.MergeSettingsHook(setBytes, mem)
	if err != nil {
		return nil, err
	}
	acts = append(acts, Action{Kind: "merge-settings", Path: setPath, Summary: "SessionStart context hook",
		Content: string(mergedSet) + "\n", Mode: 0o644})

	// 5. skills
	skills := o.skillsDir()
	acts = append(acts,
		Action{Kind: "write", Path: filepath.Join(skills, "bbrain-recall", "SKILL.md"), Summary: "skill /bbrain-recall",
			Content: setup.RecallSkill(), Mode: 0o644},
		Action{Kind: "write", Path: filepath.Join(skills, "bbrain-remember", "SKILL.md"), Summary: "skill /bbrain-remember",
			Content: setup.RememberSkill(), Mode: 0o644},
	)
	return acts, nil
}

// PlanUninstall computes the reversal actions for opts.
func PlanUninstall(o Options) ([]Action, error) {
	var acts []Action
	// integration CLAUDE.md: strip the managed block (if the file exists)
	if doc, err := os.ReadFile(o.claudeMDPath()); err == nil {
		acts = append(acts, Action{Kind: "remove-md", Path: o.claudeMDPath(), Summary: "remove integration CLAUDE.md block",
			Content: setup.RemoveManagedBlock(string(doc))})
	}
	// MCP
	if o.Scope == "user" {
		acts = append(acts, Action{Kind: "mcp-cli", Summary: "unregister bbrain MCP (user)",
			Argv: []string{"claude", "mcp", "remove", "-s", "user", "bbrain"}})
	} else {
		mcpPath := filepath.Join(o.ProjectDir, ".mcp.json")
		if existing, err := os.ReadFile(mcpPath); err == nil {
			cleaned, err := setup.RemoveMCPServer(existing)
			if err != nil {
				return nil, err
			}
			acts = append(acts, Action{Kind: "remove-mcp", Path: mcpPath, Summary: "remove bbrain from .mcp.json",
				Content: string(cleaned) + "\n"})
		}
	}
	// settings hook
	if existing, err := os.ReadFile(o.settingsPath()); err == nil {
		cleaned, err := setup.RemoveSettingsHook(existing)
		if err != nil {
			return nil, err
		}
		acts = append(acts, Action{Kind: "remove-settings", Path: o.settingsPath(), Summary: "remove SessionStart hook",
			Content: string(cleaned) + "\n"})
	}
	// skills
	acts = append(acts,
		Action{Kind: "rmdir", Path: filepath.Join(o.skillsDir(), "bbrain-recall"), Summary: "remove /bbrain-recall skill"},
		Action{Kind: "rmdir", Path: filepath.Join(o.skillsDir(), "bbrain-remember"), Summary: "remove /bbrain-remember skill"},
	)
	if o.Purge {
		acts = append(acts, Action{Kind: "rmdir", Path: o.Vault, Summary: "purge the vault (DELETES memory)"})
	}
	return acts, nil
}

// Apply executes a plan.
func Apply(actions []Action) error {
	for _, a := range actions {
		switch a.Kind {
		case "mkbrain":
			if err := brain.New(a.Path).Init(); err != nil {
				return fmt.Errorf("install: init vault %s: %w", a.Path, err)
			}
		case "write", "merge-md", "merge-mcp", "merge-settings", "remove-md", "remove-mcp", "remove-settings":
			if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
				return err
			}
			mode := a.Mode
			if mode == 0 {
				mode = 0o644
			}
			if err := os.WriteFile(a.Path, []byte(a.Content), mode); err != nil {
				return fmt.Errorf("install: write %s: %w", a.Path, err)
			}
		case "mcp-cli":
			out, err := exec.Command(a.Argv[0], a.Argv[1:]...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("install: %v: %w (%s)", a.Argv, err, strings.TrimSpace(string(out)))
			}
		case "rmdir":
			if err := os.RemoveAll(a.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

// Wizard runs the step-by-step prompts over in/out, returning resolved Options.
// Blank answers keep the shown default.
func Wizard(in io.Reader, out io.Writer, def Options) (Options, error) {
	r := bufio.NewReader(in)
	ask := func(label, dflt string) (string, error) {
		fmt.Fprintf(out, "%s [%s]: ", label, dflt)
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", err
		}
		if line = strings.TrimSpace(line); line == "" {
			return dflt, nil
		}
		return line, nil
	}
	o := def
	var err error
	if o.Vault, err = ask("Vault location", def.Vault); err != nil {
		return o, err
	}
	if o.Agent, err = ask("Agent (claude-code)", def.Agent); err != nil {
		return o, err
	}
	if o.Scope, err = ask("Scope (user|project)", def.Scope); err != nil {
		return o, err
	}
	if o.Agent != "claude-code" {
		return o, fmt.Errorf("install: only 'claude-code' is supported, got %q", o.Agent)
	}
	if o.Scope != "user" && o.Scope != "project" {
		return o, fmt.Errorf("install: scope must be 'user' or 'project', got %q", o.Scope)
	}
	return o, nil
}
```

- [ ] **Step 4: Run tests** — `go test ./internal/install/` → PASS; `go test ./...`; `go vet ./...`.
- [ ] **Step 5: Commit** — `git add internal/install/ && git commit -m "feat(install): wizard + scope-aware install/uninstall plan + apply"`

---

## Task 3: `internal/app` `Context` + `cmd bbrain context`

**Files:** Modify `internal/app/app.go`, `internal/app/app_test.go`, `cmd/bbrain/main.go`, `cmd/bbrain/main_test.go`.

**Interfaces — Produces:** `func (a *App) Context(project string, limit int) (string, error)`; `bbrain context [--home H] [--project P] [--limit N]`.

- [ ] **Step 1: Write the failing tests** — append to `internal/app/app_test.go`:

```go
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
}
```

Append to `cmd/bbrain/main_test.go`:

```go
func TestEndToEndContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	run([]string{"init"}, &out, &errOut)
	run([]string{"save", "--title", "Context fact", "--project", "p", "--type", "decision", "--body", "jwt"}, &out, &errOut)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"context", "--home", home}, &out, &errOut); code != 0 {
		t.Fatalf("context: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "Context fact") {
		t.Fatalf("context output = %q", out.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail** — `go test ./internal/app/ ./cmd/...` → FAIL (undefined `Context`; `context` unknown command).

- [ ] **Step 3: Implement `app.Context`** — in `internal/app/app.go`, add `"sort"` to the import block, then append:

```go
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
	var fs []fact.Fact
	for _, f := range facts {
		if project != "" && f.Project != project {
			continue
		}
		fs = append(fs, f)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].UpdatedAt > fs[j].UpdatedAt })
	if len(fs) > limit {
		fs = fs[:limit]
	}
	var sb strings.Builder
	sb.WriteString("# BBrain memory context\n")
	if b, err := os.ReadFile(filepath.Join(a.Brain.WikiDir(), "index.md")); err == nil {
		sb.WriteString("\n## Wiki index\n")
		sb.Write(b)
		sb.WriteString("\n")
	}
	sb.WriteString("\n## Recent facts\n")
	if len(fs) == 0 {
		sb.WriteString("(none yet)\n")
	}
	for _, f := range fs {
		sb.WriteString(fmt.Sprintf("- [%s] %s (%s) — id %s\n", f.Type, f.Title, f.Project, f.ID))
	}
	return sb.String(), nil
}
```

- [ ] **Step 4: Implement `cmd context`** — in `cmd/bbrain/main.go`, add a case to the `runWithIn` switch (before `default`):

```go
	case "context":
		return cmdContext(args[1:], stdout, stderr)
```

Append:

```go
func cmdContext(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	project := fs.String("project", "", "only include facts in this project")
	limit := fs.Int("limit", 10, "max recent facts")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	a := app.New(root)
	out, err := a.Context(*project, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "context: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, out)
	return 0
}
```

- [ ] **Step 5: Run tests** — `go test ./...` → PASS; `go vet ./...`.
- [ ] **Step 6: Commit** — `git add internal/app/ cmd/bbrain/ && git commit -m "feat(app,cli): bbrain context — memory digest for the SessionStart hook"`

---

## Task 4: `cmd bbrain install` / `uninstall` + remove `setup`

**Files:** Modify `cmd/bbrain/main.go`, `cmd/bbrain/main_test.go`.

**Interfaces:**
- Consumes: `bbrain/internal/install` (Task 2); `os`, `flag`, `fmt`, `io`, `strings` (present).
- Produces: `bbrain install [...]`, `bbrain uninstall [...]`; removes `bbrain setup`.

- [ ] **Step 1: Write the failing e2e tests** — append to `cmd/bbrain/main_test.go`:

```go
func TestEndToEndInstallUninstallProject(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	vault := filepath.Join(t.TempDir(), "vault")
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer

	args := []string{"install", "--non-interactive", "--agent", "claude-code", "--scope", "project",
		"--vault", vault, "--project", proj}
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("install: %s", errOut.String())
	}
	for _, p := range []string{
		filepath.Join(vault, "memory", "raws", "facts"),
		filepath.Join(vault, "CLAUDE.md"),
		filepath.Join(proj, ".mcp.json"),
		filepath.Join(proj, "CLAUDE.md"),
		filepath.Join(proj, ".claude", "settings.json"),
		filepath.Join(proj, ".claude", "skills", "bbrain-recall", "SKILL.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("install did not create %s: %v", p, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(proj, ".mcp.json")); !strings.Contains(string(b), filepath.Join(vault, "memory")) {
		t.Fatalf(".mcp.json BBRAIN_HOME wrong:\n%s", b)
	}

	// uninstall reverses (vault kept)
	out.Reset()
	errOut.Reset()
	uargs := []string{"uninstall", "--scope", "project", "--project", proj, "--vault", vault}
	if code := run(uargs, &out, &errOut); code != 0 {
		t.Fatalf("uninstall: %s", errOut.String())
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "CLAUDE.md")); strings.Contains(string(b), "BBRAIN:BEGIN") {
		t.Fatal("uninstall left the managed block")
	}
	if _, err := os.Stat(filepath.Join(vault, "memory")); err != nil {
		t.Fatal("uninstall without --purge deleted the vault")
	}
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	proj := t.TempDir()
	vault := filepath.Join(t.TempDir(), "vault")
	t.Setenv("HOME", t.TempDir())
	var out, errOut bytes.Buffer
	args := []string{"install", "--non-interactive", "--scope", "project", "--vault", vault, "--project", proj, "--dry-run"}
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("install --dry-run: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "[dry-run]") {
		t.Fatalf("missing dry-run banner: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(proj, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote .mcp.json")
	}
}

func TestSetupCommandRemoved(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"setup", "claude-code"}, &out, &errOut); code == 0 {
		t.Fatal("`setup` should be removed (non-zero exit expected)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail** — `go test ./cmd/...` → FAIL (`install`/`uninstall` unknown; `setup` still works).

- [ ] **Step 3: Wire commands + remove `setup`** — In `cmd/bbrain/main.go`:

(a) add `"bbrain/internal/install"` to the import block.

(b) in `runWithIn`'s switch, **remove** the `case "setup": return cmdSetup(...)` line and add:

```go
	case "install":
		return cmdInstall(args[1:], stdin, stdout, stderr)
	case "uninstall":
		return cmdUninstall(args[1:], stdout, stderr)
```

(c) update the usage line (replace `setup` with `install|uninstall|context`):

```go
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex|link|why|related|candidates|wiki|install|uninstall|context|watch|vault|mcp> [args]")
```

(d) **delete** the entire `cmdSetup` function.

(e) append `cmdInstall` + `cmdUninstall` + a `defaultVault` helper:

```go
func defaultVault() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, "bbrain")
	}
	return "bbrain"
}

func cmdInstall(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vault := fs.String("vault", defaultVault(), "vault location L (memory + degraded CLAUDE.md)")
	agent := fs.String("agent", "claude-code", "code agent to integrate")
	scope := fs.String("scope", "", "install scope: user|project")
	model := fs.String("model", "claude-sonnet-4-6", "claude model for the LLM adapter")
	dry := fs.Bool("dry-run", false, "print actions without writing")
	nonInteractive := fs.Bool("non-interactive", false, "use flags only; no prompts")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	o := install.Options{Vault: *vault, Agent: *agent, Scope: *scope, Model: *model,
		HomeDir: home, ProjectDir: cwd, DryRun: *dry}
	if !*nonInteractive {
		def := o
		if def.Scope == "" {
			def.Scope = "project"
		}
		resolved, err := install.Wizard(stdin, stdout, def)
		if err != nil {
			fmt.Fprintf(stderr, "install: %v\n", err)
			return 1
		}
		o.Vault, o.Agent, o.Scope = resolved.Vault, resolved.Agent, resolved.Scope
	}
	if o.Scope != "user" && o.Scope != "project" {
		fmt.Fprintln(stderr, "install: --scope must be user or project")
		return 2
	}
	actions, err := install.PlanInstall(o)
	if err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}
	if o.DryRun {
		fmt.Fprintln(stdout, "[dry-run] would do:")
		for _, a := range actions {
			fmt.Fprintf(stdout, "- %s — %s\n", a.Path, a.Summary)
		}
		return 0
	}
	if err := install.Apply(actions); err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed BBrain (%s scope). Memory vault: %s\n", o.Scope, filepath.Join(o.Vault, "memory"))
	fmt.Fprintf(stdout, "wiki backend: source %s\n", filepath.Join(o.Vault, "memory", ".bbrain", "env.sh"))
	return 0
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vault := fs.String("vault", defaultVault(), "vault location (for --purge)")
	agent := fs.String("agent", "claude-code", "code agent")
	scope := fs.String("scope", "", "scope: user|project")
	purge := fs.Bool("purge", false, "also delete the vault (DESTROYS memory)")
	dry := fs.Bool("dry-run", false, "print actions without writing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *scope != "user" && *scope != "project" {
		fmt.Fprintln(stderr, "uninstall: --scope must be user or project")
		return 2
	}
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	o := install.Options{Vault: *vault, Agent: *agent, Scope: *scope, HomeDir: home, ProjectDir: cwd, DryRun: *dry, Purge: *purge}
	actions, err := install.PlanUninstall(o)
	if err != nil {
		fmt.Fprintf(stderr, "uninstall: %v\n", err)
		return 1
	}
	if o.DryRun {
		fmt.Fprintln(stdout, "[dry-run] would do:")
		for _, a := range actions {
			fmt.Fprintf(stdout, "- %s — %s\n", a.Path, a.Summary)
		}
		return 0
	}
	if err := install.Apply(actions); err != nil {
		fmt.Fprintf(stderr, "uninstall: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "uninstalled BBrain (%s scope).%s\n", o.Scope,
		map[bool]string{true: " Vault purged.", false: " Vault kept."}[o.Purge])
	return 0
}
```

(f) If removing `cmdSetup` leaves `filepath` unused, it stays used (other commands use it). If any import becomes unused after deleting `cmdSetup`, remove it — but `install` adds `filepath`/`os` usage so they remain used.

- [ ] **Step 4: Run the full suite** — `go test ./...` → PASS; `go vet ./...`.

- [ ] **Step 5: Manual smoke test**

```bash
cd BBrain
go build -o ~/.local/bin/bbrain ./cmd/bbrain
rm -rf /tmp/bb-wiz && mkdir -p /tmp/bb-wiz/proj
# non-interactive project install
bbrain install --non-interactive --scope project --vault /tmp/bb-wiz/vault --project /tmp/bb-wiz/proj
echo "--- .mcp.json ---";  cat /tmp/bb-wiz/proj/.mcp.json
echo "--- settings ---";   cat /tmp/bb-wiz/proj/.claude/settings.json
echo "--- skills ---";     ls /tmp/bb-wiz/proj/.claude/skills
echo "--- vault degraded CLAUDE.md ---"; head -5 /tmp/bb-wiz/vault/CLAUDE.md
echo "--- interactive wizard (acepta defaults con Enter) ---"
printf '/tmp/bb-wiz/vault2\n\nproject\n' | bbrain install --project /tmp/bb-wiz/proj
echo "--- uninstall ---"; bbrain uninstall --scope project --project /tmp/bb-wiz/proj --vault /tmp/bb-wiz/vault
```
Expected: project install writes `.mcp.json` (con `BBRAIN_HOME=/tmp/bb-wiz/vault/memory`), `.claude/settings.json` (SessionStart hook), `.claude/skills/bbrain-{recall,remember}/SKILL.md`, `./CLAUDE.md` block, and the vault (`vault/memory` + degraded `vault/CLAUDE.md`). The interactive run prompts 3 steps. `uninstall` strips the block + hook + skills, keeps the vault.

- [ ] **Step 6: Commit**

```bash
cd BBrain
git add cmd/bbrain/
git commit -m "feat(cli): bbrain install/uninstall wizard; remove setup"
```

---

## Task 5 (runtime, not SDD): validate the wizard against live Claude Code

After merge: `bbrain install --non-interactive --scope project --vault <tmp> --project <tmpproj>`; from `<tmpproj>` run `claude mcp get bbrain` → expect Connected; run the hook command `bbrain context --home <tmp>/memory` (valid Markdown); confirm `.claude/skills/bbrain-*` and the CLAUDE.md block render; then `bbrain uninstall` cleans it. Also test the interactive flow by piping answers. Record in `docs/runtime-validation-claude-code.md`. (Controller-performed, inline.)

---

## Self-Review

**1. Spec coverage:** wizard 4 steps (Task 2 `Wizard` + Task 4 `cmdInstall`); vault `L/memory` + degraded `L/CLAUDE.md` (Task 1 builder + Task 2 plan); scope-aware MCP/CLAUDE.md/hook/skills (Task 2 `PlanInstall`); `bbrain context` hook (Task 3); reversible `uninstall` (Task 2 `PlanUninstall` + Task 4); supersede `setup` (Task 4 removal). ✓

**2. Placeholder scan:** every step has complete code + commands + expected output. ✓

**3. Type consistency:** setup builders (Task 1) consumed by `install` (Task 2) with matching signatures; `install.Options/Action/PlanInstall/PlanUninstall/Apply/Wizard` (Task 2) consumed by `cmd` (Task 4); `app.Context(project,limit)` (Task 3) consumed by `cmd context` (Task 3). `ClaudeMDBlock`/`MergeMCPConfig`/`AdapterScript`/`EnvExportLine` reused from Plan 5 unchanged. ✓

**4. Import/dependency sanity:** `setup` stdlib-only; `install` imports `setup`+`brain`+stdlib (no `app` → no cycle); `app` adds `sort`; `cmd` adds `install`. `app.SetupClaudeCode` kept (VaultMove). No `go.mod` change. ✓
