# BBrain

A single-binary memory system for AI agents. One brain, many projects, plain
Markdown on disk.

BBrain gives coding agents (Claude Code and any MCP client) a durable,
inspectable long-term memory: facts are atomic `.md` files you can read, grep,
and edit by hand, connected into a knowledge graph and distilled into an
LLM-maintained wiki.

## Two defining choices

- **`.md` files are the source of truth.** The SQLite/FTS5 index under
  `.bbrain/` is *derived and disposable* — delete it without losing data, rebuild
  it from the Markdown with `bbrain reindex`. Your memory is never trapped in a
  binary database.
- **The LLM-wiki pattern.** A *raw* layer (`raws/`, written by you and the agent)
  and a *distilled* layer (`wiki/`, maintained by a pluggable LLM), connected by
  reasoned, typed wikilinks (`relates`, `depends-on`, `conflicts-with`,
  `supersedes`, `scoped`, `compatible` — each with a non-empty `why`).

One brain serves every project. Separation between projects lives in fact
frontmatter (`project` / `scope`), not in the directory tree.

## Install

### Prebuilt binary (no Go required) — macOS & Linux

```sh
curl -fsSL https://raw.githubusercontent.com/JaraEsequiel/BBrain/master/install.sh | sh
```

Supports `amd64` and `arm64`. Override the target dir with `BBRAIN_BIN_DIR` or
pin a version with `BBRAIN_VERSION=v0.1.0`. The script downloads the right
binary from [GitHub Releases](https://github.com/JaraEsequiel/BBrain/releases),
verifies it runs, and tells you the next step.

### With Go

```sh
go install github.com/JaraEsequiel/BBrain/cmd/bbrain@latest
```

### From source

```sh
git clone https://github.com/JaraEsequiel/BBrain
cd BBrain
go build -o bbrain ./cmd/bbrain
```

### Update an existing install

The installer is idempotent — updating is re-running it. Your brain (the `.md`
files under `$BBRAIN_HOME`) is never touched; only the binary is replaced.

```sh
# 1. Replace the binary with the latest release (overwrites in place)
curl -fsSL https://raw.githubusercontent.com/JaraEsequiel/BBrain/master/install.sh | sh
bbrain version          # confirm the new version

# 2. Refresh the Claude Code integration so the MCP config is regenerated
bbrain install

# 3. Restart Claude Code so it picks up the new binary and config
```

Step 2 matters as much as step 1: it regenerates the MCP entry to pass the brain
home as `bbrain mcp --home <path>` instead of relying solely on the `env` block,
which some Claude Code setups don't propagate to the stdio child. Skipping it can
leave tools silently returning empty even after the binary is updated.

> With Go installs, update with `go install github.com/JaraEsequiel/BBrain/cmd/bbrain@latest`,
> then run steps 2–3. Pin a specific release with `BBRAIN_VERSION=v0.1.2` on the
> `install.sh` one-liner.

## Quickstart

```sh
# Create a brain (defaults to ~/.bbrain/default)
bbrain init

# Save and recall facts
bbrain save --type decision --title "Use JWT for auth" --body "stateless tokens" --project myapp
bbrain search "auth"

# Wire BBrain into Claude Code (MCP server + managed CLAUDE.md block + env)
bbrain install
```

`bbrain install` runs an interactive wizard; add `--non-interactive` or
`--dry-run` for scripted setups. From then on your agent reads and writes the
brain through MCP tools — from any working directory.

## How the agent uses it (MCP)

`bbrain mcp` exposes a minimal MCP server over stdio. The tool catalog:

| Tool | Purpose |
|------|---------|
| `mem_save` / `mem_search` / `mem_get` / `mem_delete` | save, recall, manage facts |
| `mem_link` / `mem_why` / `mem_related` / `mem_candidates` | build & query the knowledge graph |
| `mem_current_project` | resolve the active project context |
| `wiki_build` / `wiki_link` / `wiki_lint` | distil and maintain the wiki |

A save with a `topic_key` is an **upsert** (it rewrites the same `.md` file), so
re-running is safe. `Candidates` excludes already-linked facts, so graph growth
is idempotent.

## The brain on disk

Root = `$BBRAIN_HOME` (default `~/.bbrain/default`):

```
raws/facts/      atomic memories as .md (frontmatter: type/scope/project/topic_key/tags/links)
raws/user-raws/  your own raw notes
wiki/            distilled pages + index.md (catalog) + log.md (ingest/lint log)
.bbrain/         derived FTS index (index.db) — rebuildable, never a source of truth
```

## Architecture

Pure Go (`go 1.25`), stdlib-first, near-zero runtime dependencies (only
`modernc.org/sqlite`, `gopkg.in/yaml.v3`, `natefinch/atomic`). No cgo — which is
why one CI runner cross-compiles every macOS/Linux target.

Package layering (`internal/`, inner → outer):

- **`fact`** — the on-disk format: YAML frontmatter + `# Title` + body.
- **`brain`** — locates / initializes a brain root (idempotent `Init`).
- **`store`** — reads/writes facts as `.md`; `topic_key` makes a save an upsert.
- **`index`** — derived FTS5 search (embedded sqlite, no cgo); fully rebuildable.
- **`app`** — the façade wiring store + index + llm; the entry point for tracing any feature.
- **`llm`** — boundary to a pluggable external LLM CLI (`$BBRAIN_AGENT_CLI`).
- **`wiki`** — LLM-driven distillation (`Build`), graph growth (`Link`), deterministic `Lint`.
- **`mcp`** — the MCP server and tool catalog (the primary agent interface).
- **`install` / `setup`** — wire BBrain into Claude Code, idempotently.
- **`vault`** — relocate a brain (`bbrain vault move`).
- **`watch`** — reindex on change.

The CLI (`cmd/bbrain/main.go`) is a thin dispatcher: each subcommand calls one
`app.App` method.

## Development

```sh
go build ./...        # build everything
go test ./...         # run all tests
go vet ./...          # vet
```

Releases are cut by pushing a `v*` tag; a GitHub Actions workflow cross-compiles
the binaries and publishes them with checksums.

## Acknowledgements

BBrain's proactive-memory protocol was informed by studying
[engram](https://github.com/Gentleman-Programming/engram) by Alan Buscaglia
(MIT licensed). BBrain takes a different path (Markdown as source of truth,
LLM-wiki distillation) but the prior art was a valuable reference.

## License

[MIT](LICENSE) © 2026 Esequiel Jara
