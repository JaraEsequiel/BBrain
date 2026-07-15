---
ticket: BBRAIN-12
epic: BBRAIN-11
plan: .dev-tools/plans/BBRAIN-11/BBRAIN-12-mcp-auto-reindex.md
generated_at: 2026-07-15
audited_commit: d939a5a6f5af993aed3f25ce1dc764b282dce11d
---

# BBRAIN-12: Test Coverage Report

## Coverage Scan

| AC | TC | Status | Test |
|----|----|--------|------|
| AC-1 | TC-1.1 | ✅ covered | `cmd/bbrain/acceptance_bbrain12_test.go:119` (`TestAcceptance_AC1_HandEditedFactReflectedInMemSearchViaLiveMCPSession`) |
| AC-1 | TC-1.2 | ✅ covered | `internal/mcp/acceptance_bbrain12_test.go` (`TestAcceptance_AC3_NoFullReindexCostWhenFactsDirUnchanged`, no-op-tick guarantee shared with AC-3) |
| AC-1 | TC-1.3 | ✅ covered | `cmd/bbrain/acceptance_bbrain12_test.go:122` (same test, `mem_get` confirmatory check) |
| AC-2 | TC-2.1 | ✅ covered | `internal/app/acceptance_bbrain12_test.go:83` (`TestAcceptance_AC2_ConcurrentReindexAndSaveDoNotCorruptIndex`) |
| AC-2 | TC-2.2 | ✅ covered | `internal/app/acceptance_bbrain12_test.go:71` (same test) + `internal/index/index_test.go` (`TestRebuildAllRollsBackOnFailure`, genuine mid-tx failure) |
| AC-3 | TC-3.1 | ✅ covered | `internal/mcp/acceptance_bbrain12_test.go:72` (`TestAcceptance_AC3_NoFullReindexCostWhenFactsDirUnchanged`) |
| AC-3 | TC-3.2 | ✅ covered | `internal/mcp/acceptance_bbrain12_test.go:88` (same test) |
| AC-4 | TC-4.1 | ✅ covered | `cmd/bbrain/acceptance_bbrain12_test.go:187,193` (`TestAcceptance_AC4_CLIOnlyEditWithNoLiveSessionStaysStaleAndIsDocumented`) |
| AC-4 | TC-4.2 | ✅ covered | `cmd/bbrain/acceptance_bbrain12_test.go:235,250` (same test) |

No gaps found. All 9 TCs the plan's `AC → Task Coverage` table promises are backed by real,
passing tests in the acceptance suite (`e97233d`, refined through the implementation waves) plus
each task's own unit tests (`TestConcurrentReindexAndSaveDoNotRace`,
`TestRunBackgroundReindexTicksOnlyOnChange`, `TestRebuildAllHappyPath`/
`TestRebuildAllRollsBackOnFailure`).

(Note: a naive grep for bare `TC-n.m` strings also matches unrelated TC ids from other tickets —
BBRAIN-1/2's porter-stemming regression suite and generic project/type-filter tests in
`internal/app/app_test.go`/`internal/index/index_test.go` reuse the same `TC-n.m` numbering
convention for their own ACs. Those are false positives from string matching alone; the file:line
references above are the actual BBRAIN-12-scoped tests, verified by reading each one directly.)

## Gaps Filled

None — no gaps found at Step 3, so Step 4 (gap-fill) was skipped entirely.

## Suite Evidence

**Baseline (Step 2):**
```
go build ./... && go vet ./... && go test ./... -race
```
→ all 14 packages PASS (cached, previously run clean at the same commit).

**Final gate (Step 5):** skipped — no gaps found, baseline already proved the suite green.

## Human Gate

Autonomy ON — `@agent-vex:branch-gate-reviewer` dispatched in place of the human review gate.
**Verdict: APPROVE** (2026-07-15). Independently re-verified all 9 TC↔test mappings by reading each
file (not just grepping the id) and re-ran the full suite fresh (`go test ./... -race -count=1`) —
all 14 packages pass. One cosmetic note: a couple of file:line references in the Coverage Scan table
above are off by 1-3 lines from the exact assertion line (same test function in every case, not a
coverage gap) — left as-is, non-blocking.
