---
key: BBRAIN-5
type: Story
title: "Search - Browse: Add mem_browse MCP tool and bbrain list CLI"
status: in-review
pr: https://github.com/JaraEsequiel/BBrain/pull/32
parent: BBRAIN-3
area: search
labels: [mcp, cli]
points: 5
created: 2026-07-13T23:11:08Z
updated: 2026-07-14T15:46:22Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-13
---

## Context
`store.ListFacts()` (`internal/store`) already lists facts directly from the `.md` source of truth, decoupled from the FTS index — but it is internal-only, called by 6 operations inside `app.go`. There is no CLI/MCP path to browse "what does the brain know about project X" without guessing FTS query terms first. This Story exposes `ListFacts()` as a thin, filterable wrapper: a new `mem_browse` MCP tool and a new `bbrain list` CLI subcommand, both filterable by `project`/`type`, returning a compact shape (title+id+type per fact, not full body) so results don't flood an agent's context — full body remains a `mem_get` follow-up call.

## Acceptance Criteria
AC-1  Given facts exist for project "bbrain" and project "vexforge"
      When `mem_browse` is called with `project="bbrain"`
      Then only "bbrain" facts are returned, each as title+id+type (no full body)

AC-2  Given facts of type "decision" and type "preference" exist
      When `mem_browse` is called with `type="decision"`
      Then only "decision"-type facts are returned

AC-3  Given no `project`/`type` filter is passed
      When `mem_browse` is called
      Then all facts are returned (no filter = no restriction), same semantics as `store.ListFacts()` today

AC-4  Given a `project` filter for a project with zero facts
      When `mem_browse` is called with that `project`
      Then an empty list is returned (not an error)

AC-5  Given the `bbrain list` CLI subcommand
      When invoked with `--project`/`--type` flags
      Then it prints the filtered title+id+type list, matching `mem_browse`'s filtering semantics

AC-6  Given an empty string is passed as the `project` filter (as opposed to the param being omitted)
      When `mem_browse` is called
      Then it does not error and falls back to unfiltered semantics, consistent with AC-3

## Technical scope
- `internal/store` — reuse `ListFacts()` unchanged; confirm it already accepts (or add) project/type filter params without touching its 6 existing internal call sites' behavior.
- `internal/app/app.go` — new `Browse(project, type string)` method wrapping `store.ListFacts()`.
- `internal/mcp/tools.go` — new `mem_browse` MCP tool, schema with optional `project`/`type` params.
- `cmd/bbrain/main.go` — new `list` subcommand with `--project`/`--type` flags.
- `internal/app/app_test.go` / `cmd/bbrain/main_test.go` — new tests for AC-1 through AC-6.

## Constraints
- Go stdlib-first: only sqlite/yaml/atomic deps, no new dependency.
- Zero schema migration — reuses existing frontmatter fields already on disk.
- Decoupled from FTS index — reads directly from `store`, not from `internal/index`.
- Return shape is title+id+type only (not full fact body) — full body via a separate `mem_get` call, to avoid flooding agent context.
- Out of scope: `mem_search`/`ix.Search` scoping (sibling Story BBRAIN-4), tokenizer/recall, ranking, embeddings.

## Autonomy Guide
| | Actions |
|---|---|
| **Always** (just do it) | Add `Browse` method to `internal/app/app.go`; add `mem_browse` MCP tool in `internal/mcp/tools.go`; add `list` CLI subcommand in `cmd/bbrain/main.go`; add/update tests in `internal/app/app_test.go`/`cmd/bbrain/main_test.go`; run `go test ./internal/app ./cmd/bbrain`; run `go vet ./...` |
| **Ask first** (confirm with human/reviewer) | Any change to `store.ListFacts()`'s existing signature/behavior (used by 6 other call sites) beyond adding optional filter params |
| **Never** (out of scope) | Add a new runtime dependency; return full fact bodies from `mem_browse` (context-flooding risk); implement `mem_search`/`ix.Search` filtering (sibling Story BBRAIN-4); modify CI/CD pipelines |

## Definition of Done
1. `Browse` method + `mem_browse` MCP tool + `bbrain list` CLI subcommand implemented, filterable by `project`/`type` (AC-1, AC-2, AC-3, AC-4, AC-6).
2. CLI and MCP filtering semantics match (AC-5).
3. `internal/app/app_test.go` and/or `cmd/bbrain/main_test.go` cover AC-1 through AC-6.
4. `go test ./...` green; manual check: `bbrain list --project bbrain` lists BBrain facts without a query.
