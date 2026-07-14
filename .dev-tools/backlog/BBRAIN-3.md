---
key: BBRAIN-3
type: Epic
title: "Search: Scoping and browse by project/type filter"
status: in-progress
area: search
labels: [index, mcp, cli]
points: 8
created: 2026-07-13T23:11:08Z
updated: 2026-07-14T15:46:22Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-13
---

## Context
BBrain is "one brain, many projects" — separation lives in each fact's frontmatter, not in the directory tree (per BBrain/CLAUDE.md). But the search layer does not honor that separation:
1. **No scoping.** `mem_search` takes only `query`+`limit`. The `project`/`type`/`scope` columns are stored as `UNINDEXED` in the FTS index (populated on every `IndexFact`) but `Search`/`SearchAny` never filter on them — searching inside one project returns facts from every other project mixed in.
2. **No browse.** `store.ListFacts()` exists but is internal-only; there is no CLI/MCP path to see "what does the brain know about this project" without guessing FTS keywords.

## Acceptance Criteria (child-story outline)
- Story A (Scoping) covers: optional `project`/`type` params on `ix.Search`/`ix.SearchAny` (`internal/index/index.go`) with a conditional WHERE clause, threaded through call sites (`app.Search`, `app.Candidates` x2, `cmd/bbrain/main.go`, `mcp/tools.go`), exposed in the `mem_search` MCP schema and as `bbrain search` CLI flags. Backward-compatible: no filter → identical behavior to today. No schema migration. Tests: filter coverage in `internal/index/index_test.go`.
- Story B (Browse) covers: `mem_browse` (MCP) + `bbrain list` (CLI, new subcommand) as a thin wrapper over `store.ListFacts()`, filterable by `project`/`type`, decoupled from FTS. Tests: browse coverage in `internal/app/app_test.go` and/or `cmd/bbrain/main_test.go`.

## Technical scope
- `internal/index/index.go` — `ix.Search`/`ix.SearchAny` WHERE clause.
- `internal/app/app.go` — `Search`, `Candidates` (x2) call sites.
- `cmd/bbrain/main.go` — CLI flags for `search` and new `list` subcommand.
- `internal/mcp/tools.go` — MCP tool schemas for `mem_search` and new `mem_browse`.
- `internal/store` — `ListFacts()` reuse for browse (no changes expected).

## Constraints
- Go stdlib-first: only sqlite/yaml/atomic deps.
- Backward-compatible: unfiltered calls behave identically to today.
- Zero schema migration — `project`/`type`/`scope` columns already exist and are populated.
- Out of scope: tokenizer/recall (separate Epic, BBRAIN-1/BBRAIN-2), ranking changes, embeddings.

## Definition of Done
- Both child Stories shipped.
- Test coverage per PRD success criterion #5: `internal/index/index_test.go` covers project/type filtering on `ix.Search`/`ix.SearchAny`; `internal/app/app_test.go` and/or `cmd/bbrain/main_test.go` covers `mem_browse`/`bbrain list` behavior.
- `go test ./...` green.
- Manual verification: `mem_search` scoped to `bbrain` on the live vault returns only BBrain facts; `bbrain list --project bbrain` lists without a query.

Source: PRD board slug `search-scoping-browse-filtro-project-type-en-mem-search-path-de-listado` (id 3), debate verdict GO — unanimous, cheap, no schema migration.
