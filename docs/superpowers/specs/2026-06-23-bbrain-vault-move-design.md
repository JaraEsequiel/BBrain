# BBrain — Plan 6 Design: `bbrain vault move` (relocatable memory vault)

**Status:** Approved design (autonomous, per delegated decisions) — ready for implementation planning.
**Date:** 2026-06-23
**Predecessors:** Plans 1–5 (engine, graph, wiki, MCP server, setup/install).
**Roadmap slot:** Plan 6 — "Relocatable memory vault: `bbrain vault move <dest>` moves the brain, reindexes (FTS `path` holds absolute paths), refreshes agent-integration blocks that embed the old path."

---

## 1. Goal

`bbrain vault move <dest>` relocates the entire brain directory to a user-chosen location, **rebuilds the derived index** at the new path (the FTS `path` column stores absolute paths, so a move requires a rebuild — clean because the index is disposable), and **refreshes the path-bearing integration artifacts** (the brain's `env.sh`, and optionally a project's `.mcp.json`/CLAUDE.md). Stdlib only.

## 2. Global Constraints

- **Module:** `bbrain`; **Go:** 1.25; **root:** `BBrain/`. **No new dependencies.**
- **`.md` is source of truth; the index is disposable** — after a move it is rebuilt from disk at the new root, never moved-and-trusted (its `path` column would be stale).
- **Non-destructive / safe:** refuse when `dest == src`, or when `dest` already exists and is non-empty (never clobber). Prefer `os.Rename` (atomic within a filesystem); on a cross-device error (`EXDEV`), fall back to copy-tree-then-remove-src, verifying the copy before deleting the source.
- **Path resolution:** the brain root is `brainRoot()` = `$BBRAIN_HOME` or `~/.bbrain/default`. `vault move` operates on that resolved root. After the move the user must point `$BBRAIN_HOME` at `<dest>` (the command prints this); we never edit the user's shell rc.
- **Layering:** `internal/vault` imports only stdlib; `internal/app` orchestrates (resolve root → `vault.Move` → reindex at dest → regen artifacts), reusing `internal/setup.EnvExportLine` and `App.SetupClaudeCode`. `cmd/bbrain` wires the command.
- `go test ./...` green + `go vet` clean before each commit.

## 3. Architecture

New package `internal/vault`:
- `func Move(src, dest string) error` — relocate the tree at `src` to `dest`. Validates: `src` exists and is a dir; `dest != src`; `dest` does not already exist as a non-empty dir (an empty/absent `dest` is fine). Tries `os.Rename(src, dest)`; on failure that looks cross-device, `copyTree(src, dest)` then `os.RemoveAll(src)` (only after a successful copy). Creates `dest`'s parent as needed.
- `func copyTree(src, dest string) error` (unexported) — `filepath.WalkDir` over `src`, recreating dirs (preserving mode) and copying files (`io.Copy`, preserving mode) under `dest`.

`internal/app`:
- `type VaultMoveOptions struct { ProjectDir string }` (optional: refresh this project's integration at the new home).
- `func (a *App) VaultMove(dest string, opts VaultMoveOptions) (newRoot string, err error)`:
  1. `src := a.Brain.Root`; call `vault.Move(src, dest)`.
  2. Build `nb := app.New(dest)`; `nb.Reindex()` (rebuild the index with correct absolute paths under `dest`).
  3. Regenerate `<dest>/.bbrain/env.sh` = `setup.EnvExportLine(<dest>/.bbrain/agents/claude-code.sh)` **iff** that adapter exists (i.e. setup was run before the move) — so the env file points at the moved adapter, not the old path.
  4. If `opts.ProjectDir != ""`, call `nb.SetupClaudeCode(SetupOptions{ProjectDir: opts.ProjectDir, BrainHome: dest})` to rewrite that project's `.mcp.json` + CLAUDE.md + env.sh at the new home.
  5. Return `dest`.

`cmd/bbrain`:
- `bbrain vault move <dest> [--project DIR]` — moves the resolved brain to `<dest>`; prints the relocation, the reindex count, and the reminder to `export BBRAIN_HOME=<dest>` (and that `--project` refreshed that project's integration, if given). `vault` with no/other subcommand → usage (exit 2).

## 4. Out of scope (deferred)

- Auto-discovering and refreshing **all** projects that ran `setup` (we don't track them) — `vault move` refreshes the brain-internal `env.sh` always and one `--project` on request; the printed reminder covers the rest (re-run `bbrain setup claude-code` per project, or update `$BBRAIN_HOME`).
- Editing the user's shell rc or the global Claude Code config — out of scope (we only print guidance).
- A `vault` subcommand family beyond `move` (e.g. `vault info`) — only `move` for Plan 6.

## 5. Testing strategy

- **`internal/vault`:** `Move` relocates a populated tree (files + nested dirs + modes preserved), leaves `src` gone, `dest` complete; refuses `dest == src` and a non-empty existing `dest`; `copyTree` is exercised directly (same-filesystem, so simulate the fallback by calling `copyTree` + asserting a faithful copy). Missing `src` errors.
- **`internal/app`:** `VaultMove` moves a brain (saved facts survive at `dest`, `src` gone), the index is rebuilt (`Search` at `dest` returns the moved facts with new paths), `env.sh` is regenerated to the new adapter path when an adapter exists (and skipped when it doesn't), and `--project` refresh rewrites `.mcp.json` with the new `BBRAIN_HOME`.
- **CLI e2e:** `bbrain vault move <dest>` moves a brain created at `$BBRAIN_HOME`, prints the new root + reindex count; `bbrain search` against the moved brain (`BBRAIN_HOME=<dest>`) finds a fact.
- **Runtime (post-merge):** create a brain, `setup`, `vault move` it, then confirm `bbrain wiki build` (with `BBRAIN_HOME=<dest>`, sourcing the regenerated `env.sh`) still drives Claude Code. Record in `docs/runtime-validation-claude-code.md`.
- `go test ./...` green + `go vet` clean before each commit.
