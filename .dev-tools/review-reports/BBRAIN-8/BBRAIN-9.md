---
ticket: BBRAIN-9
epic: BBRAIN-8
plan: .dev-tools/plans/BBRAIN-8/BBRAIN-9-search-snippet.md
test_report: .dev-tools/test-reports/BBRAIN-8/BBRAIN-9.md
generated_at: 2026-07-15
audited_commit: ffe666a1c62c98c234aa30adbaceb6322130c919
---

# BBRAIN-9: Review Report

## Doc Sync (Step 1.5)
Not structural (no dependency/manifest changes, no new top-level directory, no workspace changes).
`.claude/context/` left untouched. `docs-maintainer`: no-op — `docs/` doesn't exist in this repo
(gitignored by design per `.gitignore`: "Design docs... are kept local, not tracked").
`readme-maintainer`: no-op — this repo is BBrain, not the VexForge plugin repo, so the README's
managed-block sources (`.claude-plugin/plugin.json`, `skills/how-to-use-vexforge/SKILL.md`) don't
exist here; correctly failed safe rather than inventing content. No commit made.

## Dependency Audit (s09)
No dependency/manifest files changed in this diff (`go.mod`/`go.sum` untouched) — skipped per the
skill's own no-changed-manifests short-circuit.

## AC Coverage + Code Quality (s10)
| AC | Status | Note |
|----|--------|------|
| AC-1: mem_search returns non-empty snippet containing the search term | ✅ Covered | `index.go` `search()` snippet column; `acceptance_bbrain9_test.go:18,33` |
| AC-2: long body truncated at a word boundary, never mid-word | ✅ Covered | FTS5 `snippet()` builtin; `acceptance_bbrain9_test.go:48,74`, `index_test.go:521` |
| AC-3: short body returned in full, whitespace-collapsed | ✅ Covered | whitespace-collapse post-process; `acceptance_bbrain9_test.go:91`, `index_test.go:553` |
| AC-4: zero matches → empty list, no error | ✅ Covered | `search()`'s existing `make([]Result, 0)`; `acceptance_bbrain9_test.go:119,133` |
| AC-5: mem_candidates (SearchAny) also returns snippet | ✅ Covered | shared `search()` path; `acceptance_bbrain9_test.go:147`, `tools_test.go:561` |

**Quality findings:**
- `[SUGGESTION]` `internal/index/index.go` (`factPreview`, BBRAIN-10 territory but same file) — `err == sql.ErrNoRows` uses direct equality instead of `errors.Is`. Harmless (the driver returns the sentinel unwrapped) but `errors.Is` is the more future-proof idiom. **WARN**, not fixed — cosmetic.
- `[SUGGESTION]` `internal/index/acceptance_bbrain9_test.go:74` (`TestAcceptance_AC2_TC2_2_..._RepeatabilityCheck`) — only asserts no trailing space before the ellipsis, doesn't re-verify the word-boundary property its name implies (that's TC-2.1's job). Naming/assertion mismatch, not a functional problem. **WARN**, not fixed — cosmetic.
- `[WARNING]` (found and **RESOLVED** during this review round) `internal/index/index.go:242,283` (`Why`/`Neighbors`) — nil-slice → `null` JSON on zero results, inconsistent with `search()`'s `make([]Result, 0)` pattern. Fixed: `make([]Edge, 0)` / `make([]Neighbor, 0)`, proven by `TestMemRelatedReturnsEmptyArrayNotNullOnZeroNeighbors` / `TestMemWhyReturnsEmptyArrayNotNullOnZeroEdges`. Re-confirmed resolved by a second `code-reviewer` dispatch.

## Security Review (s11)
No security findings. Verified: all new queries use `?` placeholders for every user/derived value
(no string concatenation into SQL); the FTS5 `snippet()` column index and table name are
compile-time literals; `sql.ErrNoRows` maps to empty strings, no raw driver error surfaced to
callers; not applicable — no HTTP surface, no crypto, no deserialization of untrusted formats.

## Verification (s12)
**Baseline (Step 1):** `go build ./... && go vet ./... && go test ./...` — all 14 packages `ok`,
zero failures, `go vet` clean.
**Final (Step 5, after the nil-slice fix landed):** same command, re-run — all 14 packages `ok`,
zero failures, `go vet` clean.

## Human Gate
Accepted — branch-gate-reviewer (autonomous), 2026-07-15T00:00:00Z. Both remaining WARNs
(`errors.Is` idiom suggestion; acceptance-test naming/assertion mismatch) classified ACCEPTABLE —
pure style/clarity, no correctness defect, no AC left partial, suite green.

## PR
(filled in after Step 7)
