# BBrain — Plan 4 Design: `bbrain mcp` (MCP stdio server)

**Status:** Approved design (autonomous, per delegated decisions) — ready for implementation planning.
**Date:** 2026-06-23
**Predecessors:** Plans 1–3b (core engine, wikilink graph, wiki build, wiki link/lint).
**Roadmap slot:** Plan 4 — "MCP server + tools: `bbrain mcp` (stdio) exposing mem_* and wiki_* tools."

---

## 1. Goal

Add `bbrain mcp` — a Model Context Protocol server over **stdio** that exposes BBrain's
existing, tested operations as MCP tools so any MCP client (Claude Code, etc.) can save,
search, link, and distil memories. Hand-rolled with the **standard library only** (MCP
over stdio is newline-delimited JSON-RPC 2.0), preserving BBrain's single-static-binary,
zero-runtime-dependency property. No new dependencies.

## 2. Global Constraints (inherited + new)

- **Module:** `bbrain`; **Go:** 1.25; **root:** `/home/vex/Projects/BBrain/`.
- **No new dependencies.** The MCP server is implemented with `encoding/json`, `bufio`,
  `io`, `context`, stdlib only.
- **`.md` is source of truth.** Tools call the existing `internal/app` façade; the MCP
  layer never touches disk directly. Strict layering: `internal/mcp` imports `app` (+ its
  result types) and stdlib; `cmd/bbrain` wires `bbrain mcp` to it.
- **Transport:** stdio, **newline-delimited JSON** — one JSON-RPC message per line on
  stdin/stdout. Logs/diagnostics go to **stderr only** (stdout is the protocol channel and
  must carry nothing but JSON-RPC).
- **Protocol:** JSON-RPC 2.0. Lifecycle: `initialize` → server returns
  `{protocolVersion, capabilities:{tools:{}}, serverInfo}` → client sends
  `notifications/initialized` → then `tools/list` and `tools/call`. Advertise
  `protocolVersion` "2025-06-18".
- **Determinism/tests:** the server reads from an `io.Reader` and writes to an `io.Writer`
  (injectable), so the whole protocol is testable in-process without real pipes. Tool
  handlers run against a real `*app.App` over a `t.TempDir()` brain.

## 3. Architecture

New package `internal/mcp`:

- **Transport / loop** (`server.go`): `Server{ App *app.App; Tools []Tool }`;
  `func (s *Server) Serve(ctx, in io.Reader, out io.Writer) error` reads newline-delimited
  JSON-RPC requests, dispatches by `method`, writes responses. Handles `initialize`,
  `notifications/initialized` (no response — it's a notification), `tools/list`,
  `tools/call`, `ping`. Unknown methods → JSON-RPC error `-32601` (method not found).
  Malformed JSON → `-32700` (parse error). Invalid params → `-32602`.
- **Tool model** (`tool.go`): `Tool{ Name, Description string; InputSchema json.RawMessage;
  Handler func(ctx context.Context, a *app.App, args json.RawMessage) (any, error) }`.
  `tools/list` returns `{tools:[{name,description,inputSchema}]}`. `tools/call` looks up the
  tool by `params.name`, invokes `Handler(ctx, app, params.arguments)`, and wraps the
  returned value as MCP tool content: `{content:[{type:"text", text:<json-encoded result>}],
  isError:false}`. A handler error → `{content:[{type:"text", text:<message>}],
  isError:true}` (tool-level error, NOT a JSON-RPC protocol error — per MCP, tool failures
  are reported in the result with `isError:true`).
- **Tool catalog** (`tools.go`): `func DefaultTools() []Tool` — the registry below. Each
  handler unmarshals its typed args, calls the matching `app` method, returns a plain
  struct that JSON-encodes to a useful payload.

`cmd/bbrain/main.go`: add `case "mcp"` → `cmdMCP` which builds `app.New(brainRoot())`,
constructs `mcp.Server{App: a, Tools: mcp.DefaultTools()}`, and calls
`Serve(ctx, os.Stdin, os.Stdout)`. Diagnostics to stderr.

## 4. Tool catalog

Memory tools (map 1:1 to `app`):

| Tool | Args | Calls | Returns |
|---|---|---|---|
| `mem_save` | `{type, title, body, project?, scope?, topic_key?, tags?[]}` | `App.Save` | the saved fact `{id,type,scope,project,title,...}` |
| `mem_search` | `{query, limit?}` (limit default 10) | `App.Search` | `{results:[{fact_id,title,type,project,path}]}` |
| `mem_get` | `{id}` | `App.Get` (new thin wrapper over `Store.Get`) | the fact, or `{found:false}` |
| `mem_delete` | `{id}` | `App.Delete` (new) | `{deleted:bool}` |
| `mem_link` | `{from, to, relation, why}` | `App.Link` | the updated source fact |
| `mem_why` | `{a, b}` | `App.Why` | `{edges:[{src_id,dst_id,relation,why}]}` |
| `mem_related` | `{id}` | `App.Related` | `{neighbors:[{fact_id,relation,why,direction}]}` |
| `mem_candidates` | `{id, limit?}` | `App.Candidates` | `{candidates:[...Result]}` |
| `mem_current_project` | `{}` | env/cwd (new helper) | `{project}` |

Wiki tools (map to `app`, each takes a context):

| Tool | Args | Calls |
|---|---|---|
| `wiki_build` | `{project?, scope?, categories?[], dry_run?}` | `App.WikiBuild` |
| `wiki_link` | `{project?, scope?, limit?, dry_run?}` | `App.WikiLink` |
| `wiki_lint` | `{categories?[], fix?}` | `App.WikiLint` |

Each tool's `InputSchema` is a JSON Schema object literal (type:object, properties, required)
authored by hand as a `json.RawMessage` constant — small and explicit, no schema-gen dep.

## 5. New capabilities required (small, additive)

- `store.Delete(id string) (bool, error)` — remove `raws/facts/<id>.md` (returns false if
  it didn't exist; no error on absence). Atomic at the filesystem level (single `os.Remove`).
- `app.Delete(id string) (bool, error)` — `store.Delete` then drop the fact from the index
  (`index` needs a `DeleteFact(id)` — a `DELETE FROM facts_fts WHERE fact_id=?` plus
  `DELETE FROM links WHERE src_id=?`). Mirrors how `Save`/`Link` keep the index in sync.
- `app.Get(id string) (fact.Fact, bool, error)` — thin pass-through to `Store.Get`.
- `mem_current_project`: a helper returning `$BBRAIN_PROJECT` if set, else the basename of
  `$PWD` (best-effort; empty string is a valid "unknown").

## 6. Claude Code integration (runtime)

After build, register the server with Claude Code:
`claude mcp add bbrain -- <abs-path>/bbrain mcp` (with `BBRAIN_HOME` in the env if needed).
Runtime acceptance: `claude mcp list` shows `bbrain` connected; a `tools/list` exposes the
catalog; a `mem_save` + `mem_search` round-trip works from Claude Code. This is part of the
Plan-4 runtime-validation step (not a unit test).

## 7. Out of scope (deferred, tracked)

- `sessions` (session start/stop tracking) — needs a session model not yet designed.
- `mem_context` (assembled context bundle for a query/project) — needs a relevance/assembly
  design; `mem_search` + `mem_related` cover the primitive needs for now.
- `mem_update` — `mem_save` with a `topic_key` already upserts in place (Plan 1 semantics);
  a distinct id-addressed update is deferred.
- Resources/prompts MCP capabilities — tools only for Plan 4.
- HTTP/SSE transport — stdio only.

## 8. Testing strategy

- **Protocol (in-process):** drive `Serve` with an `io.Reader` of newline-delimited requests
  and capture the `io.Writer`: assert the `initialize` result (protocolVersion, tools
  capability, serverInfo), that `notifications/initialized` produces no response, `tools/list`
  returns the full catalog, `tools/call` of `mem_save` then `mem_search` round-trips, an
  unknown method yields `-32601`, malformed JSON yields `-32700`, and a tool handler error
  yields a result with `isError:true` (not a protocol error).
- **Tool handlers:** each handler tested directly against a real `*app.App` over a temp brain
  (save→get→delete, link→why, candidates, wiki_build with a fake runner, etc.).
- **New store/app/index methods:** `Delete` removes the file + index rows; `Get` round-trips.
- `go test ./...` green and `go vet` clean before each commit.
