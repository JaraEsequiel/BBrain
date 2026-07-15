---
key: BBRAIN-9
type: Story
title: "Search: Add snippet field to mem_search results"
status: in-review
parent: BBRAIN-8
area: search
labels: [index, mcp]
points: 3
pr: https://github.com/JaraEsequiel/BBrain/pull/34
created: 2026-07-15T03:17:02Z
updated: 2026-07-15T05:46:42Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-15
---

## Context
`mem_search` (`internal/mcp/tools.go:118 handleMemSearch`) returns `index.Result`
(`internal/index/index.go:271-277`: `FactID`, `Title`, `Type`, `Project`, `Path`) — no preview of
the fact body. To judge relevance the caller has to `mem_get` each hit. `facts_fts` already
indexes `body` and a `snippet(body, max)` helper already exists at `internal/app/app.go:323`,
used today only internally by `WikiLink`. `mem_candidates` (`handleMemCandidates`,
`internal/mcp/tools.go:277`) shares the same `Result` shape via `SearchAny`
(`internal/index/index.go:289`) and gets the field for free.

## Acceptance Criteria
AC-1  Given a fact whose body contains the term "archive"
      When `mem_search` is called with query "archive"
      Then each matching result includes a non-empty `snippet` field containing "archive"

AC-2  Given a fact body longer than the snippet cap (~160 chars)
      When it appears in `mem_search` results
      Then the snippet is truncated at a word boundary, never mid-word

AC-3  Given a fact body shorter than the snippet cap
      When it appears in `mem_search` results
      Then the snippet is the full (whitespace-collapsed) body, unchanged

AC-4  Given a query with zero matches
      When `mem_search` is called
      Then the result list is empty and no error is returned (existing behavior unchanged)

AC-5  Given `mem_candidates` (same `Result` shape via `SearchAny`)
      When it returns matches
      Then each result also includes the `snippet` field

## Technical scope
- `internal/index/index.go`: add `Snippet string` to `Result` (line 271-277); extend the `SELECT`
  in `search()` (line 293-309, `SELECT fact_id, title, type, project, path FROM facts_fts ...`) to
  also select `body` and populate `Snippet` per row — via the existing `snippet()` helper (moved
  or exported from `internal/app/app.go:323`) or the FTS5 builtin `snippet()` SQL function if it
  compiles under `modernc.org/sqlite` (no cgo) — spike this first, prefer the builtin if it works.
- `internal/mcp/tools.go`: `handleMemSearch` (line 118) and `handleMemCandidates` (line 277)
  already marshal `Result` as-is — no handler change needed if `Result` gains the field and JSON
  tag `snippet`.
- Existing callers of `Search`/`SearchAny` beyond MCP (CLI, `WikiLink` candidate gathering) keep
  working unchanged — `Snippet` is an additive field.

## Constraints
- Pure-Go, no cgo (`modernc.org/sqlite`). If FTS5 builtin `snippet()` doesn't compile cleanly,
  fall back to the existing Go helper — do not reimplement truncation logic.
- No new dependencies, no schema migration.
- Cap ~160 chars; never cut mid-word.
- Out of scope: semantic/embedding search, ranking/scoring changes, pagination or result-limit
  changes.

## Autonomy Guide
| | Actions |
|---|---|
| **Always** (just do it) | Edit `internal/index/index.go` (`Result`, `search()`), export/reuse `snippet()` from `internal/app/app.go`, add/update unit tests in `internal/index/index_test.go` and `internal/mcp/tools_test.go`, run `go build ./...` / `go test ./...` / `go vet ./...` |
| **Ask first** (confirm with human) | n/a under autonomy — reviewer gate covers this |
| **Never** (out of scope) | Change the FTS5 schema/columns, change ranking/BM25 behavior, add pagination, add a new MCP tool, touch `mem_related`/`mem_why` (Story BBRAIN-10) |

## Definition of Done
- `Result.Snippet` populated for `mem_search` and `mem_candidates`, surfaced via existing MCP
  handlers with zero schema/handler signature changes beyond the new field.
- Tests cover AC-1 through AC-5, green in CI.
- `go build ./... && go vet ./... && go test ./...` clean.
