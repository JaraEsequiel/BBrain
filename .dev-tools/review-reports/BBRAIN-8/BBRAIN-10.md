---
ticket: BBRAIN-10
epic: BBRAIN-8
plan: .dev-tools/plans/BBRAIN-8/BBRAIN-10-graph-snippet.md
test_report: .dev-tools/test-reports/BBRAIN-8/BBRAIN-10.md
generated_at: 2026-07-15
audited_commit: ffe666a1c62c98c234aa30adbaceb6322130c919
---

# BBRAIN-10: Review Report

## Doc Sync (Step 1.5)
Not structural. `.claude/context/` left untouched. `docs-maintainer`: no-op — `docs/` doesn't exist
(gitignored by design). `readme-maintainer`: no-op — README's managed-block sources don't exist in
this repo (BBrain, not the VexForge plugin repo); failed safe. No commit made.

## Dependency Audit (s09)
No dependency/manifest files changed in this diff — skipped.

## AC Coverage + Code Quality (s10)
| AC | Status | Note |
|----|--------|------|
| AC-1: mem_related returns the linked fact's title | ✅ Covered | `Neighbor.Title` via `LEFT JOIN`; `acceptance_bbrain10_test.go:19` |
| AC-2: mem_related returns a snippet of the linked fact's body | ✅ Covered | `Neighbor.Snippet`; `acceptance_bbrain10_test.go:51,69` |
| AC-3: mem_why returns title+snippet for both sides of an edge | ✅ Covered | `Why()`/`factPreview()`; `acceptance_bbrain10_test.go:87` |
| AC-4: fact with zero neighbors → empty list, no error | ✅ Covered (was ⚠ Partial, fixed this round) | `Neighbors`/`Why` now `make([]T, 0)`, not nil — `tools_test.go` `TestMemRelatedReturnsEmptyArrayNotNullOnZeroNeighbors`/`TestMemWhyReturnsEmptyArrayNotNullOnZeroEdges`; `acceptance_bbrain10_test.go:119,132` |
| AC-5: dangling link doesn't fail the call | ✅ Covered | `LEFT JOIN` + `factPreview` `sql.ErrNoRows`→empty; `acceptance_bbrain10_test.go:144,159`; MCP regression `tools_test.go:179-215` |

**Quality findings:**
- `[WARNING]` **found and RESOLVED** this round: `internal/index/index.go:242,283` — `Why()`/`Neighbors()` returned nil slices on zero results, serializing to JSON `null` instead of `[]`, directly contradicting this ticket's own AC-4 ("empty list, no error" — a caller receiving `null` where `[]` is promised is a real API-shape defect, not cosmetic). Fixed with `make([]Edge, 0)` / `make([]Neighbor, 0)`, matching the pre-existing `search()` convention. Two new regression tests assert the literal `"neighbors":[]`/`"edges":[]` JSON. Re-confirmed resolved by a second `code-reviewer` dispatch.
- `[SUGGESTION]` `internal/index/index.go` (`factPreview`) — `err == sql.ErrNoRows` vs `errors.Is`. Cosmetic, **WARN**, not fixed.
- **Design fidelity confirmed by reviewer:** the `factPreview` two-lookup workaround for `Why()` (avoiding the double-aliased-`facts_fts`-JOIN `snippet()` failure documented in the plan) and the `LEFT JOIN` in `Neighbors()` were both verified correct across multi-neighbor scenarios, not just the single-neighbor happy path.

## Security Review (s11)
No security findings. Verified in particular: the dangling-link path (`factPreview`, the `LEFT JOIN`
+ `CASE WHEN` guard in `Neighbors`) resolves a deleted/archived fact's id to empty title/snippet
rather than leaking stale/cached content — `TestMemRelatedToleratesArchivedNeighbor` explicitly
asserts the old title does not leak through `mem_related` post-archive.

## Verification (s12)
**Baseline (Step 1):** `go build ./... && go vet ./... && go test ./...` — all 14 packages `ok`.
**Final (Step 5, after the nil-slice fix landed):** same command, re-run — all 14 packages `ok`,
zero failures, `go vet` clean.

## Human Gate
Accepted — branch-gate-reviewer (autonomous), 2026-07-15T00:00:00Z. Both remaining WARNs
(`errors.Is` idiom suggestion; acceptance-test naming/assertion mismatch, both originating in
BBRAIN-9's diff but touching shared file `internal/index/index.go`/`acceptance_bbrain9_test.go`)
classified ACCEPTABLE — pure style/clarity, no correctness defect, no AC left partial, suite green.

## PR
(filled in after Step 7)
