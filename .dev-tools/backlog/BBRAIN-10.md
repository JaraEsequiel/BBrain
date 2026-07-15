---
key: BBRAIN-10
type: Story
title: "Graph: Add title+snippet to mem_related/mem_why"
status: todo
parent: BBRAIN-8
area: search
labels: [index, mcp]
points: 5
created: 2026-07-15T03:17:02Z
updated: 2026-07-15T03:17:02Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-15
---

## Context
`mem_related` (`handleMemRelated`, `internal/mcp/tools.go:263`) and `mem_why`
(`handleMemWhy`, `internal/mcp/tools.go:248`) call `app.Related`/`app.Why`, which wrap
`index.Neighbors`/`index.Why` (`internal/index/index.go:221`/`197`). Both `Neighbor`
(line 188-193: `FactID`, `Relation`, `Why`, `Direction`) and `Edge` (line 178-183: `SrcID`,
`DstID`, `Relation`, `Why`) query the flat `links` table only — no title, no body preview. The
caller has to `mem_get` every neighbor/edge to know what it even is. Unlike Story BBRAIN-9 (a
same-table `SELECT` addition), this requires a JOIN to `facts_fts` since `links` carries no
fact metadata.

## Acceptance Criteria
AC-1  Given a fact with at least one linked neighbor
      When `mem_related` is called with that fact's id
      Then each returned neighbor includes the linked fact's `title`

AC-2  Given a fact with at least one linked neighbor
      When `mem_related` is called with that fact's id
      Then each returned neighbor includes a `snippet` of the linked fact's body (same
      truncation rules as BBRAIN-9: ~160 chars, word-boundary safe)

AC-3  Given two facts directly linked to each other
      When `mem_why` is called with both ids
      Then each returned edge includes `title` and `snippet` for both `src` and `dst`

AC-4  Given a fact id with zero neighbors
      When `mem_related` is called
      Then an empty neighbor list is returned, no error (existing behavior unchanged)

AC-5  Given a neighbor fact that no longer exists on disk (stale link, dangling reference)
      When `mem_related` is called
      Then the neighbor is still returned with its `fact_id` but `title`/`snippet` are empty
      strings rather than the call failing

## Technical scope
- `internal/index/index.go`: add `Title string` + `Snippet string` to `Neighbor` (line 188-193);
  add `SrcTitle`/`SrcSnippet`/`DstTitle`/`DstSnippet` (or a nested struct) to `Edge` (line
  178-183) — match the shape Story BBRAIN-9 lands for `Result.Snippet` reuse.
  - `Neighbors(id)` (line 221-241): change the `UNION ALL` query to `LEFT JOIN facts_fts` on
    `dst_id`/`src_id` respectively, selecting `title`, `body` (for snippet) alongside the
    existing columns. `LEFT JOIN` (not inner) so a dangling link still returns the neighbor row
    (AC-5) with empty title/snippet.
  - `Why(aID, bID)` (line 197-216): similarly `LEFT JOIN facts_fts` twice (once per side) to
    resolve title/snippet for both `src_id` and `dst_id`.
- Reuse the same snippet-building path Story BBRAIN-9 introduces (exported `snippet()` or FTS5
  builtin) — do not duplicate truncation logic.
- `internal/mcp/tools.go`: `handleMemRelated` (line 263) and `handleMemWhy` (line 248) already
  marshal the structs as-is — no handler change needed beyond the new fields.

## Constraints
- Pure-Go, no cgo. No new dependencies, no schema migration (the JOIN reads existing
  `facts_fts`/`links` tables, no new tables/columns beyond the Go struct fields).
- `LEFT JOIN`, not `INNER JOIN` — a dangling link (fact deleted after linking) must not vanish
  from results or error the call.
- Depends on Story BBRAIN-9 landing the shared snippet-building path first (sequential, not
  parallel-safe with BBRAIN-9 if both touch `search()`/snippet helper signature — coordinate or
  land BBRAIN-9 first).
- Out of scope: semantic/embedding search, ranking changes, pagination.

## Autonomy Guide
| | Actions |
|---|---|
| **Always** (just do it) | Edit `internal/index/index.go` (`Neighbor`, `Edge`, `Neighbors`, `Why`), reuse the snippet helper from BBRAIN-9, add/update unit tests in `internal/index/index_test.go` and `internal/mcp/tools_test.go`, run `go build ./...` / `go test ./...` / `go vet ./...` |
| **Ask first** (confirm with human) | n/a under autonomy — reviewer gate covers this |
| **Never** (out of scope) | Change the `links` table schema, change edge/relation semantics, touch `mem_search`/`mem_candidates` (Story BBRAIN-9), add a new MCP tool |

## Definition of Done
- `Neighbor` and `Edge` carry `title`+`snippet` (or per-side equivalents for `Edge`), populated
  via `LEFT JOIN` to `facts_fts`, surfaced via existing `mem_related`/`mem_why` handlers with no
  signature changes beyond the new fields.
- Tests cover AC-1 through AC-5 (including the dangling-link case), green in CI.
- `go build ./... && go vet ./... && go test ./...` clean.
