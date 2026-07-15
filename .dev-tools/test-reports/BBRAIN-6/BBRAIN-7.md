---
ticket: BBRAIN-7
epic: BBRAIN-6
plan: .dev-tools/plans/BBRAIN-6/BBRAIN-7-mcp-archive-unarchive.md
generated_at: 2026-07-15
audited_commit: 40f25ea61e7a564bddd510bd16457e98951d5a23
---

# BBRAIN-7: Test Coverage Report

## Coverage Scan

**WARN**: the automated `scripts/coverage-scan` (mechanical grep for literal `TC-n.m` tokens inside
the plan's `## AC → Task Coverage` table) returned an empty table — this plan's coverage table cites
test **function names** (e.g. `` Task 1 (`TestMemArchiveKnownId`) ``) rather than embedding literal
`TC-1.1`-style ids, so the string-matching script found nothing to scan. This is a plan-formatting gap
in `/vex:write-plan`'s output for this ticket, not a real coverage gap — verified manually below by
cross-referencing the requirements digest's `AC → Test Cases` section (`.dev-tools/requirements/BBRAIN-6/BBRAIN-7.md`)
against the real test source.

| AC | Covered by (real test, grep-verified) |
|----|-----------|
| AC-1: mem_archive with a known id archives the fact | `TestMemArchiveKnownId` (`internal/mcp/tools_test.go:303`) |
| AC-2: response includes the archived id | `TestMemArchiveKnownId` (`internal/mcp/tools_test.go:303`) |
| AC-3: batch archive reports correct count | `TestMemArchiveBatchCount` (`internal/mcp/tools_test.go:332`) |
| AC-4: mem_unarchive with a known id unarchives the fact | `TestMemUnarchiveKnownId` (`internal/mcp/tools_test.go:436`) |
| AC-5: unarchive response confirms id + count | `TestMemUnarchiveKnownId` (`internal/mcp/tools_test.go:436`) |
| AC-6: schema is id-list-only, no filter/bulk fields | `TestMemArchiveSchemaIsIdListOnly` (`:405`), `TestMemUnarchiveSchemaIsIdListOnly` (`:476`) |
| AC-7: unknown id doesn't crash the tool | `TestMemArchiveUnknownIdSkippedNotCrashed` (`:352`), `TestMemUnarchiveUnknownIdSkippedNotCrashed` (`:461`) |
| AC-8: unknown id is never falsely reported as archived | same two tests as AC-7 |
| AC-9: mem_get on an archived id still returns archived:true | `TestMemGetAfterArchiveReturnsArchivedTrue` (`:418`) |
| AC-10: toolSearchMsg lists the new tools | `TestToolSearchMsgListsArchiveTools` (`internal/prompthook/prompthook_test.go:35`) |
| ⚠ candidate AC — pinned fact skipped, not aborted | `TestMemArchivePinnedFactSkipped` (`:376`) |
| ⚠ candidate AC — empty `ids` array | `TestMemArchiveEmptyIdsReturnsZeroCount` (`:393`) |

All 10 ACs + both candidate ACs have a real, passing, AC-tagged test (verified via
`grep -noE 'AC-[0-9]+' internal/mcp/tools_test.go internal/prompthook/prompthook_test.go` — every
AC-1 through AC-10 appears at least once in the test source). **No gaps found.**

## Gaps Filled

None — coverage is complete as implemented; no gap-fill dispatch was needed.

## Suite Evidence

- **Baseline** (Step 2, before the scan): `go test ./...` — all 13 packages `ok`, no failures.
- **Final** (Step 5): skipped per the skill's own rule — Step 3 found no gaps, so the baseline
  green from Step 2 already stands as the final evidence.
- `go vet ./...` — clean.
- `go build ./...` — succeeds.

## Human Gate

Autonomy ON — `@agent-vex:branch-gate-reviewer` dispatched in place of the human. **APPROVE**
(2026-07-15): independently re-ran `go test ./...`/`go vet ./...` (fresh green), read both test files
line-by-line and confirmed every AC-1..AC-10 + 2 candidate ACs have a real assertion (not just a
comment), cross-checked against the requirements digest. No findings.
