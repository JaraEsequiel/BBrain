# BBrain — Plan 7 Design: `bbrain install` (interactive Claude Code integration wizard)

**Status:** Approved design (interactive brainstorm with the user) — ready for implementation planning.
**Date:** 2026-06-23
**Supersedes:** Plan 5's `bbrain setup claude-code` (flag-only). Its pure builders are reused; the `setup` command is removed in favor of `install`.

---

## 1. Goal

A **step-by-step interactive wizard**, `bbrain install`, that sets up the memory vault and wires BBrain into a code agent (Claude Code today), choosing **where** the vault lives and **at what scope** (User/global vs Project) the integration is installed. Fully **reversible** via `bbrain uninstall`. Stdlib only.

The wizard's four steps (matching the user's requirement):
1. **Choose the vault location** `L`. Create `L/memory/` (the BBrain brain) and `L/CLAUDE.md` (a *degraded-mode* reader doc).
2. **Choose the agent** (Claude Code only for now; the model is agent-pluggable).
3. **Choose the scope** — `user` (global) or `project` (cwd).
4. **Install** the integration artifacts (MCP, CLAUDE.md block, SessionStart hook, skills, LLM adapter) into that scope's Claude Code locations.

## 2. The two CLAUDE.md files (the key distinction)

- **`L/CLAUDE.md` (degraded mode):** for sessions/cowork opened *directly in `L`* that have **no** BBrain MCP/CLI access. It documents how to read & write the memory **by hand with plain Read/Write**: the on-disk layout under `L/memory/` (`raws/facts/<id>.md` frontmatter + body, `wiki/`), how to find facts, and how to append a new fact file. It is NOT about tools.
- **Integration CLAUDE.md block (scope-located):** for sessions that **do** have the tools. A managed block in `~/.claude/CLAUDE.md` (user) or `./CLAUDE.md` (project) telling the agent to use the `mcp__bbrain__*` tools and the build→link→lint workflow.

## 3. Global Constraints

- **Module:** `bbrain`; **Go:** 1.25. **No new dependencies** (the "wizard" is an stdin prompt flow with `bufio`, never a TUI library).
- **`BBRAIN_HOME = L/memory`** — the brain lives under `memory/`, separate from `L/CLAUDE.md`.
- **Non-destructive + reversible:** every edit to a shared file (CLAUDE.md, `settings.json`, `.mcp.json`, `~/.claude.json`) is a **managed merge** — a `<!-- BBRAIN:BEGIN -->…<!-- BBRAIN:END -->` block (Markdown) or a JSON merge keyed by `bbrain` / a tagged hook entry — preserving all other content. `uninstall` removes exactly those. Skills are whole files under `bbrain-*` dirs.
- **Idempotent:** re-running `install` at the same scope replaces only BBrain's managed regions / keys; no duplicates.
- **`--dry-run`** prints every action + exact content without writing. **Non-interactive flags** (`--vault`, `--scope`, `--agent`, `--yes`) drive the same engine for automation and tests.
- **Layering:** `internal/setup` (pure artifact builders) and `internal/install` (prompt flow + scope-aware plan/apply/reverse) import only stdlib + `setup`; `internal/app` orchestrates; `cmd/bbrain` wires the commands. The brain itself is created via the existing `brain.Init`/`app`.
- **Authoritative Claude Code locations** (researched, v2.1.x):
  - CLAUDE.md: user `~/.claude/CLAUDE.md`; project `./CLAUDE.md`.
  - MCP: project `./.mcp.json` (`{"mcpServers":{"bbrain":<stdio-entry>}}`); user → `claude mcp add -s user …` (writes `~/.claude.json`). The stdio entry shape is `{"type":"stdio","command":"bbrain","args":["mcp"],"env":{"BBRAIN_HOME":"<L/memory>"}}`.
  - Hooks: user `~/.claude/settings.json`; project `./.claude/settings.json` — under `hooks.SessionStart[].hooks[]` with `{"type":"command","command":"bbrain","args":["context","--home","<L/memory>"], "timeout":…}`. The `--home` flag makes the hook self-contained (no env plumbing) and fully testable.
  - Skills: user `~/.claude/skills/<name>/SKILL.md`; project `./.claude/skills/<name>/SKILL.md`, with YAML frontmatter (`description`, `disable-model-invocation`).

## 4. New CLI surface

- **`bbrain install [--vault L] [--agent claude-code] [--scope user|project] [--model M] [--dry-run] [--yes] [--non-interactive]`** — the wizard. Interactive by default (prompts with defaults); `--non-interactive`/`--yes` runs from flags only. Prints a summary; on `--dry-run` shows full content.
- **`bbrain context [--home L/memory] [--project P] [--limit N]`** — emits memory text to stdout for the SessionStart hook: the `wiki/index.md` digest (if present) + the `N` most-recent facts (optionally filtered to the project), as compact Markdown. `--home` overrides the brain root (defaults to `brainRoot()`); bounded (default `N=10`). Reuses `ListFacts`/`Search`.
- **`bbrain uninstall [--scope user|project] [--agent claude-code] [--purge] [--dry-run]`** — removes every artifact `install` wrote at that scope (managed CLAUDE.md block, the `bbrain` MCP entry, the BBrain SessionStart hook entry, the `bbrain-*` skill dirs, the integration env). With `--purge` it also deletes the vault (`L`); without it, the vault and its data are left intact.

`bbrain setup` (Plan 5) is removed; its `internal/setup` builders are extended and reused.

## 5. Scope → file targets

| Artifact | `user` (global) | `project` (cwd) |
|---|---|---|
| MCP `bbrain` (stdio, `BBRAIN_HOME=L/memory`) | `~/.claude.json` (user scope) | `./.mcp.json` |
| Integration CLAUDE.md block | `~/.claude/CLAUDE.md` | `./CLAUDE.md` |
| SessionStart hook (`bbrain context`) | `~/.claude/settings.json` | `./.claude/settings.json` |
| Skills `bbrain-recall`, `bbrain-remember` | `~/.claude/skills/<n>/SKILL.md` | `./.claude/skills/<n>/SKILL.md` |
| LLM adapter + `env.sh` (`BBRAIN_AGENT_CLI`) | `L/memory/.bbrain/agents/claude-code.sh` + `L/memory/.bbrain/env.sh` | same |

The skills `bbrain-recall` (`/bbrain-recall` → "search the bbrain memory via `mem_search` and summarize") and `bbrain-remember` (`/bbrain-remember <text>` → "save a durable fact via `mem_save`") are SKILL.md instructions referencing the MCP tools. The SessionStart hook injects `bbrain context` output at session start.

## 6. Artifact builders (`internal/setup`, extended — all pure)

- `DegradedClaudeMD(memoryDir string) string` — the `L/CLAUDE.md` by-hand reader doc.
- `IntegrationClaudeMDBlock(memoryDir string) string` — the managed integration block (BEGIN/END), names the `mcp__bbrain__*` tools.
- `MCPEntry(memoryDir)`, `MergeMCPConfig(existing, memoryDir)` — reuse from Plan 5 (now keyed on `memory`).
- `SessionStartHookEntry(memoryDir string) map[string]any` — the JSON hook object (`bbrain context --home <memoryDir>`); `MergeSettingsHook(existing []byte, memoryDir string) ([]byte, error)` — insert/replace BBrain's SessionStart hook in a settings.json, preserving other hooks/keys, idempotent; and the inverse `RemoveSettingsHook(existing []byte) ([]byte, error)`. (BBrain's hook entry is identified by its `command`=`bbrain` + `args` containing `context`, so removal is exact.)
- `RecallSkill() string`, `RememberSkill() string` — the two SKILL.md bodies.
- `AdapterScript(model)`, `EnvExportLine(path)`, `UpsertManagedBlock`, `RemoveManagedBlock` — reuse/extend Plan 5 (add the removal inverse for uninstall).

## 7. Install engine (`internal/install`, new)

- `type Options struct { Vault, Agent, Scope, Model string; HomeDir, ProjectDir string; DryRun bool }` (HomeDir/ProjectDir injectable for tests).
- `type Action struct { Path, Summary, Content string; Mode os.FileMode; Kind string }` (Kind: write|merge-json|merge-md|mcp-cli|mkbrain).
- `func PlanInstall(opts Options) ([]Action, error)` — compute the ordered actions for `(vault, scope, agent)` (create `L/memory` brain + `L/CLAUDE.md`; then the scope-located integration artifacts). Pure (no I/O); returns what *would* be done.
- `func Apply(actions []Action) error` — execute (mkdir + write/merge; the `mcp-cli` action shells out to `claude mcp add -s user` for user scope, or is a `.mcp.json` write for project scope; `mkbrain` runs `brain.Init` on `L/memory`).
- `func PlanUninstall(opts Options) ([]Action, error)` / reuse `Apply` with removal-kind actions — reverse the managed regions/keys/skill dirs (+ vault on `--purge`).
- `func Wizard(in io.Reader, out io.Writer, defaults Options) (Options, error)` — the stdin prompt flow (4 steps, defaults shown, validated), returning the resolved Options. Testable with scripted stdin.

`internal/app`: `Install`/`Uninstall`/`Context` thin orchestrators. `cmd/bbrain`: `install`, `uninstall`, `context` (and drop `setup`).

## 8. Out of scope (deferred)

- Agents other than Claude Code (the model is pluggable; only `claude-code` ships).
- A Claude Code **plugin** package (we chose loose files per scope); a plugin packer can be a later plan.
- Editing the user's shell rc (we write a sourceable `env.sh` and print the line, as in Plan 5).
- Auto-discovering every project that ran `install` during a `vault move` (Plan 6 already refreshes the brain env + one `--project`).

## 9. Testing strategy

- **Pure builders (`internal/setup`):** each artifact's content (degraded vs integration CLAUDE.md, hook JSON, SKILL bodies); `MergeSettingsHook` preserves other hooks and is idempotent and reversible; `MergeMCPConfig`/`UpsertManagedBlock` as in Plan 5 + their removal inverses.
- **Install engine (`internal/install`):** `PlanInstall` yields the right action set per scope (user vs project) pointing at injected HomeDir/ProjectDir; `Apply` creates `L/memory` (a valid brain), `L/CLAUDE.md`, and the scope files; **idempotent** re-run; `PlanUninstall`+`Apply` removes exactly what install added and leaves the vault unless `--purge`; `Wizard` parses scripted stdin (location/agent/scope) with defaults.
- **`bbrain context`:** emits the wiki index + recent facts; bounded by `--limit`; empty brain → a minimal, valid output.
- **CLI e2e:** `bbrain install --non-interactive --vault <tmp> --scope project --project <tmp> --yes` writes `.mcp.json` (parses, has `bbrain`), `./CLAUDE.md` block, `.claude/settings.json` hook, `.claude/skills/bbrain-*`; `bbrain uninstall` reverses it; `--dry-run` writes nothing.
- **Runtime (post-merge):** run the wizard at project scope into a temp project, confirm `claude mcp get bbrain` ✔ Connected, the SessionStart hook command (`bbrain context`) emits valid text, and the skills/CLAUDE.md render; then `bbrain uninstall` cleans it. Record in `docs/runtime-validation-claude-code.md`.
- `go test ./...` green + `go vet` clean before each commit.
