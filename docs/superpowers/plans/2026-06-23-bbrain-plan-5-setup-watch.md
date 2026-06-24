# BBrain — Plan 5: `bbrain setup claude-code` + `bbrain watch` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make BBrain installable into Claude Code (`bbrain setup claude-code`: write the LLM agent adapter, register the MCP server via `.mcp.json`, install a managed CLAUDE.md block, write a sourceable env.sh) and keep its index fresh (`bbrain watch`: stdlib polling reindex). Stdlib only.

**Architecture:** New pure package `internal/setup` (artifact builders: adapter script, `.mcp.json` merge, managed-block upsert, CLAUDE.md block, env line). New pure package `internal/watch` (`FactsFingerprint`). `internal/app` gains `SetupClaudeCode` (the only disk-writing piece). `cmd/bbrain` adds `setup` + `watch` commands. No graphical TUI, no file-watch library — flag/dry-run-driven, polling-based.

**Tech Stack:** Go 1.25, stdlib (`encoding/json`, `crypto/sha256`, `io/fs`, `os`, `path/filepath`, `sort`, `strings`, `flag`, `time`). No new dependencies.

## Global Constraints

- **Module:** `bbrain`. **Go:** `go 1.25`. **Root:** `BBrain/`. **No new dependencies.**
- **Idempotent + non-destructive:** Markdown edits use a managed block `<!-- BBRAIN:BEGIN -->`/`<!-- BBRAIN:END -->`; `.mcp.json` is a JSON merge preserving other servers/keys. Re-running replaces only BBrain's region.
- **`--dry-run`** returns/prints actions and exact content without writing.
- **Pure builders** (`internal/setup`, `internal/watch`) take inputs and return strings/bytes; the only disk I/O is `app.SetupClaudeCode` and the `watch` loop.
- **Authoritative formats (verified against Claude Code):** MCP entry `{"type":"stdio","command":"bbrain","args":["mcp"],"env":{"BBRAIN_HOME":"<brain>"}}`; `.mcp.json` = `{"mcpServers":{"<name>":<entry>}}`.
- **Agent adapter** is the runtime-validated Finding #1 script (`docs/runtime-validation-claude-code.md`): `claude -p --output-format text --model <m> --append-system-prompt <role>` piped through a `python3` JSON extractor. Adapter documents the `python3` prerequisite.
- `go test ./...` green + `go vet` clean before each commit.

Design spec: `docs/superpowers/specs/2026-06-23-bbrain-setup-integration-design.md`.

## Execution note (parallelism)

Task 1 (`internal/setup`) and Task 2 (`internal/watch`) are independent new packages (disjoint files) and may be built **in parallel**. Task 3 (`app.SetupClaudeCode`) depends on Task 1. Task 4 (cmd) depends on Tasks 1–3.

---

## File Structure (Plan 5)

- `internal/setup/setup.go` + `internal/setup/setup_test.go` — **create:** pure builders.
- `internal/watch/watch.go` + `internal/watch/watch_test.go` — **create:** `FactsFingerprint`.
- `internal/app/app.go` + `internal/app/app_test.go` — **modify:** `SetupOptions`, `SetupAction`, `SetupClaudeCode`.
- `cmd/bbrain/main.go` + `cmd/bbrain/main_test.go` — **modify:** `setup`/`watch` commands + usage + e2e.

---

## Task 1: `internal/setup` — artifact builders (pure)

**Files:** Create `internal/setup/setup.go`, `internal/setup/setup_test.go`.

**Interfaces:**
- Consumes: stdlib `encoding/json`, `strings`.
- Produces: `const BlockBegin, BlockEnd`; `func AdapterScript(model string) string`; `func MCPEntry(brainHome string) map[string]any`; `func MergeMCPConfig(existing []byte, brainHome string) ([]byte, error)`; `func UpsertManagedBlock(doc, block string) string`; `func ClaudeMDBlock(brainHome, adapterPath string) string`; `func EnvExportLine(adapterPath string) string`.

- [ ] **Step 1: Write the failing tests**

Create `internal/setup/setup_test.go`:

```go
package setup

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdapterScript(t *testing.T) {
	s := AdapterScript("claude-sonnet-4-6")
	for _, want := range []string{"#!/bin/sh", "claude -p", "--append-system-prompt", "claude-sonnet-4-6", "BBRAIN_CLAUDE_MODEL", "python3", `re.search`} {
		if !strings.Contains(s, want) {
			t.Fatalf("adapter missing %q:\n%s", want, s)
		}
	}
}

func TestMergeMCPConfigInsertsAndPreserves(t *testing.T) {
	existing := []byte(`{"mcpServers":{"other":{"type":"stdio","command":"x"}},"someKey":1}`)
	out, err := MergeMCPConfig(existing, "/home/u/.bbrain/default")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatal("merge dropped the pre-existing 'other' server")
	}
	if root["someKey"] == nil {
		t.Fatal("merge dropped top-level someKey")
	}
	bb := servers["bbrain"].(map[string]any)
	if bb["command"] != "bbrain" || bb["type"] != "stdio" {
		t.Fatalf("bbrain entry wrong: %v", bb)
	}
	env := bb["env"].(map[string]any)
	if env["BBRAIN_HOME"] != "/home/u/.bbrain/default" {
		t.Fatalf("BBRAIN_HOME wrong: %v", env)
	}
}

func TestMergeMCPConfigEmptyInput(t *testing.T) {
	out, err := MergeMCPConfig(nil, "/b")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["mcpServers"].(map[string]any)["bbrain"]; !ok {
		t.Fatalf("bbrain not added to empty config: %s", out)
	}
}

func TestUpsertManagedBlockInsertThenReplaceIdempotent(t *testing.T) {
	block := ClaudeMDBlock("/b", "/b/.bbrain/agents/claude-code.sh")
	// insert into a doc with existing content
	got := UpsertManagedBlock("# Project\n\nHello.\n", block)
	if !strings.Contains(got, "# Project") || strings.Count(got, BlockBegin) != 1 {
		t.Fatalf("insert wrong:\n%s", got)
	}
	// replacing yields exactly one block, and is idempotent
	again := UpsertManagedBlock(got, block)
	if again != got {
		t.Fatalf("upsert not idempotent:\n--first--\n%s\n--second--\n%s", got, again)
	}
	if strings.Count(again, BlockBegin) != 1 || strings.Count(again, BlockEnd) != 1 {
		t.Fatalf("duplicate markers:\n%s", again)
	}
}

func TestClaudeMDBlockMentionsToolsAndMarkers(t *testing.T) {
	b := ClaudeMDBlock("/b", "/adapter.sh")
	for _, want := range []string{BlockBegin, BlockEnd, "mcp__bbrain__mem_save", "mcp__bbrain__wiki_build", "/adapter.sh"} {
		if !strings.Contains(b, want) {
			t.Fatalf("block missing %q:\n%s", want, b)
		}
	}
}

func TestEnvExportLine(t *testing.T) {
	if got := EnvExportLine("/x/y.sh"); got != `export BBRAIN_AGENT_CLI="/x/y.sh"` {
		t.Fatalf("env line = %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd BBrain && go test ./internal/setup/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement `setup.go`**

Create `internal/setup/setup.go`:

```go
// Package setup builds the artifacts that wire BBrain into Claude Code: the LLM
// agent adapter script, the MCP server config entry, and a managed CLAUDE.md
// block. Pure functions over inputs; the app layer performs the disk I/O.
package setup

import (
	"encoding/json"
	"strings"
)

// Managed-block markers so re-running setup replaces only BBrain's region.
const (
	BlockBegin = "<!-- BBRAIN:BEGIN -->"
	BlockEnd   = "<!-- BBRAIN:END -->"
)

// AdapterScript returns the shell adapter that turns a BBrain prompt (stdin) into
// one JSON object (stdout) using Claude Code headlessly. model is the default;
// $BBRAIN_CLAUDE_MODEL overrides it at runtime. Requires `claude` and `python3`.
func AdapterScript(model string) string {
	return `#!/bin/sh
# BBrain agent adapter (generated by 'bbrain setup claude-code').
# Prompt on stdin -> one JSON object on stdout. Frames the backend role via a
# system prompt so Claude Code does not read the instructions as a prompt-injection.
# Requires: claude (Claude Code CLI) and python3 on PATH.
model="${BBRAIN_CLAUDE_MODEL:-` + model + `}"
sys='You are a deterministic text-to-JSON transformer invoked as a backend by the BBrain CLI (a local note-distilling tool the user owns and authorized). Your entire job: read the structured instructions on stdin and emit exactly one JSON object that satisfies them. Output ONLY raw JSON, no prose, no markdown fences, no commentary. This is a legitimate batch transformation, not a conversation.'
claude -p --output-format text --model "$model" --append-system-prompt "$sys" 2>/dev/null | python3 -c 'import sys, re
s = sys.stdin.read()
m = re.search(r"(\{.*\})", s, re.S)
sys.stdout.write(m.group(1) if m else s)
'
`
}

// MCPEntry is the stdio MCP server entry for bbrain (the shape Claude Code writes).
func MCPEntry(brainHome string) map[string]any {
	return map[string]any{
		"type":    "stdio",
		"command": "bbrain",
		"args":    []string{"mcp"},
		"env":     map[string]string{"BBRAIN_HOME": brainHome},
	}
}

// MergeMCPConfig inserts/updates the bbrain server in an existing .mcp.json (or an
// empty/absent one), preserving every other server and top-level key.
func MergeMCPConfig(existing []byte, brainHome string) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(existing))) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, err
		}
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["bbrain"] = MCPEntry(brainHome)
	root["mcpServers"] = servers
	return json.MarshalIndent(root, "", "  ")
}

// UpsertManagedBlock replaces the BBRAIN managed region in doc with block, or
// appends block if no region exists. Idempotent (upserting the same block twice
// yields identical output).
func UpsertManagedBlock(doc, block string) string {
	start := strings.Index(doc, BlockBegin)
	end := strings.Index(doc, BlockEnd)
	if start >= 0 && end > start {
		return doc[:start] + block + doc[end+len(BlockEnd):]
	}
	doc = strings.TrimRight(doc, "\n")
	if doc == "" {
		return block + "\n"
	}
	return doc + "\n\n" + block + "\n"
}

// ClaudeMDBlock is the managed CLAUDE.md section documenting BBrain's MCP tools.
func ClaudeMDBlock(brainHome, adapterPath string) string {
	return BlockBegin + `
## BBrain memory

This project uses BBrain for durable memory (brain at ` + brainHome + `). The bbrain MCP server exposes:
- mcp__bbrain__mem_save / mem_search / mem_get / mem_delete — save and recall facts.
- mcp__bbrain__mem_link / mem_why / mem_related / mem_candidates — the reasoned graph.
- mcp__bbrain__wiki_build / wiki_link / wiki_lint — distil and maintain the wiki.

Save durable decisions and learnings via mem_save; search with mem_search before answering.
The wiki LLM backend is $BBRAIN_AGENT_CLI -> ` + adapterPath + `. Workflow: build -> link -> rebuild; wiki_lint --fix for consistency.
` + BlockEnd
}

// EnvExportLine is the shell line that points BBrain at the agent adapter.
func EnvExportLine(adapterPath string) string {
	return `export BBRAIN_AGENT_CLI="` + adapterPath + `"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd BBrain && go test ./internal/setup/` → PASS. Then `go vet ./internal/setup/`.

- [ ] **Step 5: Commit**

```bash
cd BBrain
git add internal/setup/
git commit -m "feat(setup): pure builders — agent adapter, .mcp.json merge, managed CLAUDE.md block"
```

---

## Task 2: `internal/watch` — `FactsFingerprint` (pure)

**Files:** Create `internal/watch/watch.go`, `internal/watch/watch_test.go`.

**Interfaces:**
- Consumes: stdlib `crypto/sha256`, `encoding/hex`, `fmt`, `io/fs`, `os`, `path/filepath`, `sort`, `strings`.
- Produces: `func FactsFingerprint(dir string) (string, error)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/watch/watch_test.go`:

```go
package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFactsFingerprintChangesAndIsStable(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "alpha")
	fp1, err := FactsFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == "" {
		t.Fatal("fingerprint empty for non-empty dir")
	}
	// stable when nothing changes
	fp1b, _ := FactsFingerprint(dir)
	if fp1b != fp1 {
		t.Fatal("fingerprint not stable across calls")
	}
	// changes when a fact is added
	write("b.md", "beta")
	fp2, _ := FactsFingerprint(dir)
	if fp2 == fp1 {
		t.Fatal("fingerprint unchanged after adding a fact")
	}
	// changes when a fact's content changes (size differs)
	time.Sleep(5 * time.Millisecond)
	write("a.md", "alpha-modified-longer")
	fp3, _ := FactsFingerprint(dir)
	if fp3 == fp2 {
		t.Fatal("fingerprint unchanged after modifying a fact")
	}
	// non-.md files are ignored
	write("notes.txt", "ignored")
	fp4, _ := FactsFingerprint(dir)
	if fp4 != fp3 {
		t.Fatal("fingerprint changed for a non-.md file")
	}
}

func TestFactsFingerprintMissingDir(t *testing.T) {
	fp, err := FactsFingerprint(filepath.Join(t.TempDir(), "nope"))
	if err != nil || fp != "" {
		t.Fatalf("missing dir = %q, %v; want \"\", nil", fp, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd BBrain && go test ./internal/watch/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement `watch.go`**

Create `internal/watch/watch.go`:

```go
// Package watch detects changes to a brain's raw facts so the derived index can
// be rebuilt. Stdlib only; no filesystem-notification dependency.
package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FactsFingerprint returns a stable hash of every .md under dir (relpath + size +
// modtime). A missing dir yields ("", nil), so a watch loop treats "no facts yet"
// as a stable state rather than an error.
func FactsFingerprint(dir string) (string, error) {
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		lines = append(lines, fmt.Sprintf("%s\x00%d\x00%d", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd BBrain && go test ./internal/watch/` → PASS. Then `go vet ./internal/watch/`.

- [ ] **Step 5: Commit**

```bash
cd BBrain
git add internal/watch/
git commit -m "feat(watch): FactsFingerprint — stdlib change detection over raws/facts"
```

---

## Task 3: `internal/app` — `SetupClaudeCode`

**Files:** Modify `internal/app/app.go`, `internal/app/app_test.go`.

**Interfaces:**
- Consumes: `bbrain/internal/setup` (Task 1); stdlib `os`, `path/filepath` (already imported).
- Produces: `type SetupOptions struct { ProjectDir, BrainHome, Model string; DryRun bool }`; `type SetupAction struct { Path, Summary, Content string; Mode os.FileMode }`; `func (a *App) SetupClaudeCode(opts SetupOptions) ([]SetupAction, error)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/app_test.go` (add `"os"`, `"path/filepath"`, `"strings"` to its import block if not already present — `os` and `path/filepath` were added in a prior plan; ensure all three are present):

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd BBrain && go test ./internal/app/`
Expected: FAIL (undefined `SetupClaudeCode`, `SetupOptions`).

- [ ] **Step 3: Implement `SetupClaudeCode`**

In `internal/app/app.go`, add `"bbrain/internal/setup"` to the import block (and ensure `os`, `path/filepath` are present — they are). Append:

```go
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
			return nil, err
		}
		if err := os.WriteFile(act.Path, []byte(act.Content), act.Mode); err != nil {
			return nil, err
		}
	}
	return actions, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd BBrain && go test ./internal/app/` → PASS. Then `go vet ./internal/app/`.

- [ ] **Step 5: Commit**

```bash
cd BBrain
git add internal/app/
git commit -m "feat(app): SetupClaudeCode — write adapter, .mcp.json, CLAUDE.md block, env.sh (idempotent)"
```

---

## Task 4: `cmd/bbrain` — `setup` + `watch` commands

**Files:** Modify `cmd/bbrain/main.go`, `cmd/bbrain/main_test.go`.

**Interfaces:**
- Consumes: `app.SetupClaudeCode`/`SetupOptions`, `watch.FactsFingerprint`, `App.Reindex`, `brainRoot`; stdlib `flag`, `fmt`, `time`, `path/filepath`.
- Produces: `bbrain setup claude-code [...]` and `bbrain watch [...]`.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/bbrain/main_test.go`:

```go
func TestEndToEndSetupClaudeCode(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"setup", "claude-code", "--dir", proj, "--home", home}, &out, &errOut); code != 0 {
		t.Fatalf("setup: %s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(proj, ".mcp.json")); err != nil {
		t.Fatalf(".mcp.json not written: %v", err)
	}
	cm, _ := os.ReadFile(filepath.Join(proj, "CLAUDE.md"))
	if !strings.Contains(string(cm), "mcp__bbrain__mem_save") {
		t.Fatalf("CLAUDE.md = %s", cm)
	}
}

func TestSetupDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	run([]string{"init"}, &out, &errOut)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"setup", "claude-code", "--dir", proj, "--home", home, "--dry-run"}, &out, &errOut); code != 0 {
		t.Fatalf("setup --dry-run: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "[dry-run]") {
		t.Fatalf("dry-run banner missing: %s", out.String())
	}
	if entries, _ := os.ReadDir(proj); len(entries) != 0 {
		t.Fatalf("dry-run wrote files: %v", entries)
	}
}

func TestEndToEndWatchOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	run([]string{"init"}, &out, &errOut)
	run([]string{"save", "--title", "Watched fact", "--project", "p", "--type", "decision", "--body", "b"}, &out, &errOut)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"watch", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("watch --once: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "reindexed") {
		t.Fatalf("watch output = %q", out.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd BBrain && go test ./cmd/...`
Expected: FAIL (`setup`/`watch` unknown).

- [ ] **Step 3: Wire the commands**

In `cmd/bbrain/main.go`, add `"time"`, `"path/filepath"`, and `"bbrain/internal/watch"` to the import block. Add two cases to the `runWithIn` switch (before `default`):

```go
	case "setup":
		return cmdSetup(args[1:], stdout, stderr)
	case "watch":
		return cmdWatch(args[1:], stdout, stderr)
```

Update the usage line to include `setup|watch`. Append:

```go
func cmdSetup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "claude-code" {
		fmt.Fprintln(stderr, "setup: usage: bbrain setup claude-code [--dir D] [--home H] [--model M] [--dry-run]")
		return 2
	}
	fs := flag.NewFlagSet("setup claude-code", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "project dir for .mcp.json + CLAUDE.md")
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	model := fs.String("model", "claude-sonnet-4-6", "claude model for the adapter")
	dry := fs.Bool("dry-run", false, "print actions without writing")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	bh := *home
	if bh == "" {
		bh = brainRoot()
	}
	a := app.New(brainRoot())
	actions, err := a.SetupClaudeCode(app.SetupOptions{ProjectDir: *dir, BrainHome: bh, Model: *model, DryRun: *dry})
	if err != nil {
		fmt.Fprintf(stderr, "setup: %v\n", err)
		return 1
	}
	if *dry {
		fmt.Fprintln(stdout, "[dry-run] would write:")
	}
	for _, act := range actions {
		fmt.Fprintf(stdout, "%s — %s\n", act.Path, act.Summary)
		if *dry {
			fmt.Fprintln(stdout, act.Content)
		}
	}
	if !*dry {
		fmt.Fprintf(stdout, "\nDone. In this project Claude Code reads .mcp.json automatically; set the wiki backend with: source %s\n",
			filepath.Join(bh, ".bbrain", "env.sh"))
	}
	return 0
}

func cmdWatch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	interval := fs.Int("interval", 2, "poll interval in seconds")
	once := fs.Bool("once", false, "check once and exit (no loop)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a := app.New(brainRoot())
	factsDir := a.Brain.FactsDir()
	last := ""
	for {
		fp, err := watch.FactsFingerprint(factsDir)
		if err != nil {
			fmt.Fprintf(stderr, "watch: %v\n", err)
			return 1
		}
		if fp != last {
			n, err := a.Reindex()
			if err != nil {
				fmt.Fprintf(stderr, "watch: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "reindexed %d facts\n", n)
			last = fp
		}
		if *once {
			return 0
		}
		time.Sleep(time.Duration(*interval) * time.Second)
	}
}
```

- [ ] **Step 4: Run the full suite**

Run: `cd BBrain && go test ./...` → PASS. Then `go vet ./...`.

- [ ] **Step 5: Manual smoke test**

```bash
cd BBrain
go build ./cmd/bbrain
rm -rf /tmp/bbrain-setup-smoke && mkdir -p /tmp/bbrain-setup-smoke/proj
export BBRAIN_HOME=/tmp/bbrain-setup-smoke/brain
./bbrain init
echo "--- dry-run ---"; ./bbrain setup claude-code --dir /tmp/bbrain-setup-smoke/proj --home "$BBRAIN_HOME" --dry-run | head -20
echo "--- write ---";   ./bbrain setup claude-code --dir /tmp/bbrain-setup-smoke/proj --home "$BBRAIN_HOME"
echo "--- .mcp.json ---"; cat /tmp/bbrain-setup-smoke/proj/.mcp.json
echo "--- CLAUDE.md ---"; cat /tmp/bbrain-setup-smoke/proj/CLAUDE.md
echo "--- adapter executable? ---"; ls -l "$BBRAIN_HOME/.bbrain/agents/claude-code.sh"
echo "--- watch --once ---"; ./bbrain save --title "Smoke fact" --project p --type decision --body b >/dev/null; ./bbrain watch --once
unset BBRAIN_HOME
```
Expected: dry-run prints 4 actions with content and writes nothing; the write run creates a valid `.mcp.json` (with a `bbrain` stdio server), a CLAUDE.md with the managed block + `mcp__bbrain__*` tools, an executable adapter, and `env.sh`; `watch --once` prints `reindexed N facts`.

- [ ] **Step 6: Commit**

```bash
cd BBrain
git add cmd/bbrain/
git commit -m "feat(cli): bbrain setup claude-code + bbrain watch (with e2e)"
```

---

## Task 5 (runtime, not SDD): validate the install against live Claude Code

After merge, in a temp project: `bbrain setup claude-code --dir <proj> --home <brain>`, then from `<proj>` run `claude mcp get bbrain` (read from `.mcp.json`) → expect **Connected**, and confirm the CLAUDE.md managed block renders. Source `env.sh` and run `bbrain wiki build` to confirm the adapter drives Claude. Record in `docs/runtime-validation-claude-code.md`. (Controller-performed, inline.)

---

## Self-Review

**1. Spec coverage:** adapter + `.mcp.json` + managed CLAUDE.md block + env.sh (Task 1 builders, Task 3 writer); `bbrain setup claude-code` with `--dry-run` (Task 4); `bbrain watch` stdlib polling reindex (Task 2 + Task 4); idempotency (UpsertManagedBlock + MergeMCPConfig + the app idempotency test). Deferred TUI/hooks per spec §5. ✓

**2. Placeholder scan:** Every step has complete code + commands + expected output. ✓

**3. Type consistency:** `setup.AdapterScript/MCPEntry/MergeMCPConfig/UpsertManagedBlock/ClaudeMDBlock/EnvExportLine` defined Task 1, consumed Task 3. `watch.FactsFingerprint` defined Task 2, consumed Task 4. `app.SetupOptions/SetupAction/SetupClaudeCode` defined Task 3, consumed Task 4. ✓

**4. Import/dependency sanity:** `internal/setup` and `internal/watch` import only stdlib; `app` imports `setup`; `cmd` imports `app` + `watch`. No cycles, no `go.mod` change. ✓
