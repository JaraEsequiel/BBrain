# BBrain — Plan 5 Design: `bbrain setup` (Claude Code integration) + `bbrain watch`

**Status:** Approved design (autonomous, per delegated decisions) — ready for implementation planning.
**Date:** 2026-06-23
**Predecessors:** Plans 1–4 (engine, graph, wiki build/link/lint, MCP server).
**Roadmap slot:** Plan 5 — "TUI install + agent integration: `bbrain init`/`bbrain setup <agent>`, Claude Code hooks plugin, managed BEGIN/END block, watcher-driven auto-reindex."

---

## 1. Goal

Make BBrain trivially installable into Claude Code and keep its index fresh:
- **`bbrain setup claude-code`** — productionize the runtime-validated integration: write the LLM **agent adapter** (so `wiki build`/`link` can call Claude headlessly), register the **MCP server** via a project `.mcp.json`, and install a **managed CLAUDE.md block** documenting the tools + memory conventions. Idempotent; `--dry-run` previews.
- **`bbrain watch`** — a stdlib polling watcher that re-indexes when `raws/facts` changes.

Stdlib only — **no TUI/watcher dependencies**. The "TUI" is a clear, flag-driven, dry-run-previewable setup flow (a heavyweight terminal-UI library is explicitly rejected as it would break the single-static-binary, zero-dep property every prior plan upholds).

## 2. Global Constraints

- **Module:** `bbrain`; **Go:** 1.25; **root:** `/home/vex/Projects/BBrain/`. **No new dependencies.**
- **Idempotent + non-destructive:** all file edits use a managed block delimited by `<!-- BBRAIN:BEGIN -->` / `<!-- BBRAIN:END -->` (for Markdown) or a JSON merge that **preserves** unrelated content (other MCP servers, the rest of CLAUDE.md). Re-running replaces only the managed region.
- **`--dry-run`** prints every action and the exact content that would be written, touching nothing.
- **Generation is pure + testable:** the artifact builders (`internal/setup`) are pure functions over inputs returning strings/bytes; disk I/O lives in a thin app-layer method. Tests assert content + merge/idempotency without real Claude Code.
- **Authoritative formats** (verified against what Claude Code itself writes):
  - MCP server entry: `{"type":"stdio","command":"bbrain","args":["mcp"],"env":{"BBRAIN_HOME":"<brain>"}}`.
  - `.mcp.json` shape: `{"mcpServers":{"<name>": <entry>}}` (project-scoped, shareable).
- **Agent adapter** is the validated Finding #1 script (`docs/runtime-validation-claude-code.md`): `claude -p --output-format text --model <m> --append-system-prompt <role>` piped through a JSON-extracting `python3` one-liner. (`python3` is the dev platform's interpreter; the adapter documents this prerequisite.)

## 3. Architecture

New package `internal/setup` (pure artifact builders):
- `AdapterScript(model string) string` — the claude→JSON adapter shell script (Finding #1), with `$BBRAIN_CLAUDE_MODEL` override and `model` as the default.
- `MCPEntry(brainHome string) map[string]any` — the bbrain stdio server entry.
- `MergeMCPConfig(existing []byte, brainHome string) ([]byte, error)` — parse existing `.mcp.json` (or empty/absent → `{}`), set `mcpServers.bbrain` to `MCPEntry`, preserve all other keys/servers, re-marshal indented. Returns the new bytes.
- `ClaudeMDBlock(brainHome, adapterPath string) string` — the managed Markdown block: a short "BBrain memory" section listing the `mcp__bbrain__*` tools, the `BBRAIN_AGENT_CLI` adapter path, and the build→link→rebuild/lint workflow. Wrapped in the BEGIN/END markers.
- `UpsertManagedBlock(doc, block string) string` — if `doc` contains a BEGIN…END region, replace it with `block`; else append `block` (with a leading blank line). Idempotent: upserting the same block twice yields identical output.
- `EnvExportLine(adapterPath string) string` — `export BBRAIN_AGENT_CLI="<adapterPath>"`.

`internal/app` (thin orchestration, does the I/O):
- `type SetupOptions struct { ProjectDir, BrainHome, Model string; DryRun bool }`
- `type SetupAction struct { Path, Summary string; Content string }` (Content for dry-run display)
- `func (a *App) SetupClaudeCode(opts SetupOptions) ([]SetupAction, error)` — computes the four actions:
  1. write adapter → `<BrainHome>/.bbrain/agents/claude-code.sh` (mode 0755),
  2. merge `<ProjectDir>/.mcp.json`,
  3. upsert managed block in `<ProjectDir>/CLAUDE.md`,
  4. write `<BrainHome>/.bbrain/env.sh` containing the `EnvExportLine` (user `source`s it; we never edit shell rc files).
  On `DryRun`, return the actions **without** writing. Otherwise write each (creating dirs) and return what was done.

`internal/watch` (or app): `func FactsFingerprint(dir string) (string, error)` — a stable hash of every `*.md`'s relpath + size + modtime under `dir`; missing dir → empty fingerprint. `bbrain watch` polls it.

`cmd/bbrain`:
- `bbrain setup claude-code [--dir D] [--home H] [--model M] [--dry-run]` — `--dir` defaults to cwd; `--home` defaults to `brainRoot()`; `--model` defaults to `claude-sonnet-4-6`. Prints a human summary (and full content on `--dry-run`).
- `bbrain watch [--interval S]` (default 2s) — loop: compute `FactsFingerprint(FactsDir)`; on change call `App.Reindex()` and print `reindexed N facts`; sleep S. Runs until interrupted (Ctrl-C / SIGINT). `--once` runs a single check (testable, non-looping).

## 4. Managed CLAUDE.md block (content sketch)

```
<!-- BBRAIN:BEGIN -->
## BBrain memory

This project uses BBrain for durable memory. The `bbrain` MCP server exposes:
- `mcp__bbrain__mem_save` / `mem_search` / `mem_get` / `mem_delete` — save & recall facts.
- `mcp__bbrain__mem_link` / `mem_why` / `mem_related` / `mem_candidates` — the reasoned graph.
- `mcp__bbrain__wiki_build` / `wiki_link` / `wiki_lint` — distil & maintain the wiki.

Save durable decisions/learnings via `mem_save`; recall with `mem_search` before answering.
The wiki LLM backend is `$BBRAIN_AGENT_CLI` → `<adapterPath>`. Workflow: build → link → rebuild; `wiki_lint --fix` for consistency.
<!-- BBRAIN:END -->
```

## 5. Out of scope (deferred, tracked)

- A graphical/interactive TUI (bubbletea etc.) — rejected (dependency + the flag/dry-run flow covers install). `bbrain init` stays as-is.
- Claude Code **hooks plugin** (auto-save on session events) — deferred; the MCP tools + CLAUDE.md guidance already let the agent save/recall. A hooks generator can be a Plan 5b.
- Editing the user's shell rc — we only write a sourceable `env.sh` and print the line.
- Other agents (`setup <agent>` for non-Claude) — Plan 5 ships `claude-code`; the `internal/setup` builders are agent-agnostic enough to extend later.

## 6. Testing strategy

- **Pure builders (`internal/setup`):** adapter script contains `claude -p`, `--append-system-prompt`, the model, and the python JSON-extractor; `MergeMCPConfig` adds `mcpServers.bbrain` while preserving a pre-existing `other` server and is idempotent; `UpsertManagedBlock` inserts when absent, replaces when present, and is idempotent (twice == once); `ClaudeMDBlock` is wrapped in the BEGIN/END markers and names the `mcp__bbrain__` tools.
- **App:** `SetupClaudeCode` with `DryRun:true` writes nothing but returns 4 actions; with `DryRun:false` writes the adapter (0755), a valid `.mcp.json` (re-parseable, contains bbrain), the CLAUDE.md block, and `env.sh`; re-running is idempotent (no duplicate blocks/servers).
- **Watch:** `FactsFingerprint` changes when a fact is added/modified and is stable otherwise; `bbrain watch --once` reindexes when the fingerprint differs from a stored marker.
- **CLI e2e:** `bbrain setup claude-code --dry-run` prints the plan; `bbrain setup claude-code --dir <tmp> --home <tmp>` creates the files and the `.mcp.json` parses with a `bbrain` server.
- **Runtime (post-merge):** in a temp project, run `bbrain setup claude-code`, then `claude mcp get bbrain` (from `.mcp.json`) → Connected, and confirm the CLAUDE.md block renders. Record in `docs/runtime-validation-claude-code.md`.
- `go test ./...` green + `go vet` clean before each commit.
