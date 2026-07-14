---
ticket: BBRAIN-4
epic: BBRAIN-3
plan: .dev-tools/plans/BBRAIN-3/BBRAIN-4-search-scoping.md
test_report: .dev-tools/test-reports/BBRAIN-3/BBRAIN-4.md
generated_at: 2026-07-14
audited_commit: de0cfa41f7a41b2392c9e06bb60ef83167815127
---

# BBRAIN-4: Review Report

## Doc Sync (Step 1.5)
Not structural (no dependency/manifest changes, no new top-level directory, no workspace changes). `.claude/context/` left untouched. `docs-maintainer`: no-op — `docs/` doesn't exist in this repo. `readme-maintainer`: no-op — this worktree is BBrain, not the VexForge plugin repo, so the README's managed-block sources (`.claude-plugin/plugin.json`, `skills/how-to-use-vexforge/SKILL.md`) don't exist here; correctly failed safe rather than inventing content. No commit made.

## Dependency Audit (s09)
No dependency manifest (`go.mod`/`go.sum`) changed between BASE (274efe7) and HEAD — skipped, nothing to audit.

## AC Coverage + Code Quality (s10)
| AC | Status | Note |
|----|--------|------|
| AC-1 | ✅ Covered | `index.go` WHERE clause + fallback leg test |
| AC-2 | ✅ Covered | type filter, `index_test.go` |
| AC-3 | ✅ Covered | backward-compat, 24 call sites verified unchanged |
| AC-4 | ✅ Covered | zero-match → empty, not error |
| AC-5 | ✅ Covered | `mem_search` MCP schema + handler |
| AC-6 | ✅ Covered | `bbrain search --project/--type` |

**Quality findings:**
- 🛑 `[WARNING]` `reorderFlagsFirst` silently swallowed a known flag as another flag's value when no explicit value was given (e.g. `--project --type bar`) → **fixed**, commit `de0cfa4`, re-confirmed by a fresh `code-reviewer` dispatch (root finding resolved, full suite green, no new CRITICAL/WARNING introduced).
- ⚠ `[SUGGESTION]` `cmdList` doesn't validate/report leftover positional args — minor inconsistency with `cmdSearch`, not a spec violation.
- ⚠ `[SUGGESTION]` (post-fix) the `--limit`-followed-by-a-flag case now hard-fails with Go's own `flag` package error message rather than a friendlier one — acceptable, strictly better than the prior silent bug.

## Security Review (s11)
No security findings. SQL params bound throughout (no injection vector); `Browse`/`mem_browse` read a fixed, non-user-controlled directory (no path traversal); no secrets, no auth-boundary changes (none exist to violate — local single-user tool), no unsafe deserialization.

## Verification (s12)
Baseline (Step 1, before doc-sync/s09-s11): `go build ./... && go test ./... && go vet ./...` → all 14 packages green, vet clean.
Final (Step 5, after the s10 fix commit `de0cfa4`): same commands → all 14 packages green, vet clean.

## Human Gate
Accepted — branch-gate-reviewer (autonomous), 2026-07-14. Both accumulated WARNs (cmdList positional-arg handling, --limit hard-fail messaging) classified ACCEPTABLE.

## PR
https://github.com/JaraEsequiel/BBrain/pull/32
