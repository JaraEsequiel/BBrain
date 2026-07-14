---
key: BBRAIN-4
type: Story
title: "Search - Scoping: Add project/type filter to mem_search"
status: in-review
pr: https://github.com/JaraEsequiel/BBrain/pull/32
parent: BBRAIN-3
area: search
labels: [index, mcp, cli]
points: 5
created: 2026-07-13T23:11:08Z
updated: 2026-07-14T15:46:22Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-13
---

## Context
`mem_search` (MCP tool backed by `ix.Search`/`ix.SearchAny` in `internal/index/index.go`) takes only `query`+`limit`. The `project`/`type`/`scope` columns are stored `UNINDEXED` in the FTS5 virtual table and populated on every `IndexFact`, but no query path filters on them. Searching inside one project (e.g. `bbrain`) returns facts from every other project (`vexforge`, `vexos`, ...) mixed into the same result set — the agent filters mentally on every search. This Story adds optional `project`/`type` filters, threaded from the MCP/CLI surface down through the index layer, with zero schema migration since the columns already exist and are populated.

## Acceptance Criteria
AC-1  Given an index with facts from multiple projects
      When `ix.Search` is called with `project="bbrain"` and a query matching facts in both `bbrain` and another project
      Then only `bbrain` facts are returned

AC-2  Given an index with facts of multiple types
      When `ix.Search` is called with `type="decision"` and a query matching facts of multiple types
      Then only `decision`-type facts are returned

AC-3  Given an index with facts from multiple projects
      When `ix.Search` is called with no `project`/`type` filter (as today)
      Then results are identical to pre-change behavior (backward compatibility, no regression)

AC-4  Given a `project` filter for a project with zero matching facts
      When `ix.Search` is called with that `project` and a query that matches facts in other projects
      Then zero results are returned (not an error, not silently ignoring the filter)

AC-5  Given the `mem_search` MCP tool
      When invoked with `project` and/or `type` params
      Then the params are passed through to `ix.Search` unchanged and reflected in the results

AC-6  Given the `bbrain search` CLI command
      When invoked with `--project`/`--type` flags
      Then the underlying `ix.Search` call receives those filters

## Technical scope
- `internal/index/index.go` — add `project`/`type` params to `ix.Search`/`ix.SearchAny`; conditional WHERE clause (only applied when non-empty, to preserve AC-3).
- `internal/app/app.go` — thread params through `Search` and both `Candidates` call sites.
- `cmd/bbrain/main.go` — add `--project`/`--type` flags to `bbrain search`.
- `internal/mcp/tools.go` — add `project`/`type` optional params to the `mem_search` MCP tool schema.
- `internal/index/index_test.go` — new tests for AC-1 through AC-4.

## Constraints
- Go stdlib-first: only sqlite/yaml/atomic deps, no new dependency.
- Backward-compatible: unfiltered calls behave identically to today (AC-3).
- Zero schema migration — `project`/`type`/`scope` columns already exist and are populated.
- Do NOT default `project` to `mem_current_project` — this is an open question in the source PRD, left unresolved; filter is opt-in only, no implicit default in this Story.
- Out of scope: tokenizer/recall, ranking changes, embeddings, the browse/list path (covered by sibling Story BBRAIN-5).

## Autonomy Guide
| | Actions |
|---|---|
| **Always** (just do it) | Edit `internal/index/index.go`, `internal/app/app.go`, `cmd/bbrain/main.go`, `internal/mcp/tools.go` within stated scope; add/update tests in `internal/index/index_test.go`; run `go test ./internal/index ./internal/app ./cmd/bbrain`; run `go vet ./...` |
| **Ask first** (confirm with human/reviewer) | Defaulting `project` to `mem_current_project` (explicitly out per Constraints — flag if tempted); any change to the FTS5 schema itself beyond adding a WHERE clause |
| **Never** (out of scope) | Add a new runtime dependency; change ranking/BM25 behavior; touch tokenizer config (BBRAIN-1/2 scope); implement `mem_browse`/`bbrain list` (sibling Story BBRAIN-5); modify CI/CD pipelines |

## Definition of Done
1. `ix.Search`/`ix.SearchAny` accept optional `project`/`type` and filter correctly (AC-1, AC-2, AC-4).
2. Unfiltered calls unchanged (AC-3) — verified by existing tests still passing.
3. `mem_search` MCP schema and `bbrain search` CLI flags expose the filters (AC-5, AC-6).
4. `internal/index/index_test.go` covers AC-1 through AC-4.
5. `go test ./...` green; manual check: `mem_search` scoped to `bbrain` on the live vault returns only BBrain facts.
