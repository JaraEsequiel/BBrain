# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

BBrain is a single-binary Go memory system for AI agents (in the spirit of [engram](https://github.com/Gentleman-Programming/engram), studied as prior art but not a dependency). Two defining choices:

- **`.md` files are the source of truth.** The SQLite/FTS5 index under `.bbrain/` is **derived and disposable** — deletable without data loss, rebuilt from the `.md` files via `bbrain reindex`.
- **LLM-wiki pattern.** A raw layer (`raws/`) that BBrain and the user write, and a distilled layer (`wiki/`) maintained by a pluggable LLM, connected by reasoned, typed wikilinks.

One brain serves all projects. Separation between projects lives in fact frontmatter (`project` / `scope`), not in the directory tree. Agents read/write the brain from any working directory via MCP tools — the brain's own `CLAUDE.md` is only for opening a session *inside* the brain folder.

The full design spec is `docs/superpowers/specs/2026-06-22-bbrain-design.md` (Spanish); per-feature specs and implementation plans live alongside it under `docs/superpowers/`.

## Commands

Standard Go module (`go 1.25`, module `bbrain`). No Makefile.

- Build: `go build ./...` (binary: `go build -o bbrain ./cmd/bbrain`)
- All tests: `go test ./...`
- One package: `go test ./internal/wiki`
- One test: `go test ./internal/wiki -run TestParseResponse`
- Vet: `go vet ./...`

## Architecture

The brain on disk (root = `$BBRAIN_HOME`, default `~/.bbrain/default`):
```
raws/facts/      atomic memories as .md (flat; frontmatter holds type/scope/project/topic_key/tags/links)
raws/user-raws/  user's raw notes
wiki/            distilled pages + index.md (catalog) + log.md (append-only ingest/lint log)
.bbrain/         derived FTS index (index.db), agent adapter, env.sh
```

Package layering (`internal/`), inner to outer:

- **`fact`** — the only package that knows the on-disk format: YAML frontmatter + `# Title` + body. `Marshal`/`Parse` round-trip. A `Link` is `{target, relation, why}`.
- **`brain`** — locates/initializes a brain root; `Init` is idempotent (never clobbers user edits).
- **`store`** — reads/writes facts as `.md`. `topic_key` makes a save an **upsert** (rewrites the same file). Atomic writes via `natefinch/atomic`.
- **`index`** — derived FTS5 search (embedded `modernc.org/sqlite`, no cgo). Mirrors facts + their link edges; fully rebuildable. Never a source of truth.
- **`app`** — the **façade** (`app.App`) that wires store + index + llm and exposes every operation the CLI and MCP server drive (Save, Search, Link, Why, Related, Candidates, Wiki*, VaultMove, Context, SetupClaudeCode). Most index-mutating ops re-open the index, mutate incrementally, and `defer Close()`. Start here to trace any feature.
- **`llm`** — boundary to a pluggable external LLM CLI (`$BBRAIN_AGENT_CLI`, prompt on stdin → response on stdout). Used only by the wiki layer.
- **`wiki`** — LLM-driven distillation (`Build`), graph growth (`Link` — proposes reasoned edges from FTS candidates), and deterministic `Lint`. `wiki_lint --fix` drops dangling links and regenerates the derived `index.md`.
- **`mcp`** — minimal MCP server over stdio (`bbrain mcp`). `DefaultTools()` is the catalog: `mem_save/search/get/delete/link/why/related/candidates/current_project`, `wiki_build/link/lint`. This is the primary agent interface.
- **`install` / `setup`** — wire BBrain into Claude Code: the agent adapter script, a merged `.mcp.json`, a managed `CLAUDE.md` block, and a sourceable `env.sh`. Idempotent; `install` has an interactive wizard (`bbrain install`) plus `--non-interactive`/`--dry-run`.
- **`vault`** — relocate a brain (`bbrain vault move`); caller rebuilds the index and refreshes integration at the new home.
- **`watch`** — fingerprints `raws/facts/` so `bbrain watch` reindexes on change.

The CLI (`cmd/bbrain/main.go`) is a thin dispatcher: each subcommand parses flags and calls one `app.App` method.

## Conventions

- **The `.md` is authoritative; the index is cache.** Any change to a fact must go through `store`/`app` so the index stays in sync — never write `index.db` as if it were truth.
- **Idempotency is a hard requirement** for `Init`, `install`/`setup`, and the wiki ops (re-running must not duplicate). `Candidates` already excludes already-linked facts so `wiki_link` re-runs cleanly.
- **Stdlib-first, near-zero runtime deps** (only sqlite, yaml, atomic). Keep it that way.
- Link relations are a fixed set: `relates | depends-on | conflicts-with | supersedes | scoped | compatible`. Every link needs a non-empty `why`.

<!-- context-index:start -->
## Project Context Index

This index maps each facet of this repo to a context file under `.claude/context/`. Each file is
the authoritative map of its facet — read it before working on that facet instead of re-deriving
what it documents; if your assumptions conflict with a file, the file wins. One exception: these
files are regenerated snapshots (`/vex:init-repo`), so for pinned versions and exact command
strings the repo's own canonical sources (version-pin files and manifests) win on any conflict.

- [Project Coding Stack](.claude/context/stack.md) — runtime, package manager, workspaces,
  framework & test runner per workspace, canonical commands. **Read always**, before any
  build/test/deps command.
- [Architecture](.claude/context/architecture.md) — map of the repo's workspaces: what each
  one does, dependency edges. **Read when** planning/implementing across workspaces or
  deciding where code goes.
<!-- context-index:end -->


