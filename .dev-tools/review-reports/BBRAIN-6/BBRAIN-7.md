---
ticket: BBRAIN-7
epic: BBRAIN-6
plan: .dev-tools/plans/BBRAIN-6/BBRAIN-7-mcp-archive-unarchive.md
test_report: .dev-tools/test-reports/BBRAIN-6/BBRAIN-7.md
generated_at: 2026-07-15
audited_commit: 3179fab1e75762e14f2b593deb58569807aa1952
---

# BBRAIN-7: Review Report

## Doc Sync (Step 1.5)

Not structural (no manifest/lockfile/workspace change — same monolithic package). Context untouched
per design. `docs-maintainer`: no `docs/` directory in this repo — no-op. `readme-maintainer`: this
repo has neither of the VexForge-specific managed-section sources (`.claude-plugin/plugin.json`,
`skills/how-to-use-vexforge/SKILL.md`) — no markers in `README.md` — no-op, byte-identical. Nothing
staged, no `docs:` commit made.

## Dependency Audit (s09)

No manifest files changed (`go.mod`/`go.sum` untouched, confirmed via `git diff BASE..HEAD --name-only`).
Skipped — no dependency changes to audit.

## AC Coverage + Code Quality (s10)

| AC | Status | Note |
|----|--------|------|
| AC-1 | ✅ Covered | `handleMemArchive` calls `a.Archive`; `TestMemArchiveKnownId` |
| AC-2 | ✅ Covered | response includes archived id; same test |
| AC-3 | ✅ Covered | `count` = `len(archived)`; `TestMemArchiveBatchCount` |
| AC-4 | ✅ Covered | `handleMemUnarchive` calls `a.Unarchive`; `TestMemUnarchiveKnownId` |
| AC-5 | ✅ Covered | same response shape; same test |
| AC-6 | ✅ Covered | `schemaIDs` is `{"ids":[...]}`-only; both schema tests |
| AC-7 | ✅ Covered | any error `continue`s, never propagates; unknown-id tests |
| AC-8 | ✅ Covered | unknown id absent from response; same tests |
| AC-9 | ✅ Covered | `handleMemGet`'s archive-fallback untouched; regression test |
| AC-10 | ✅ Covered | `toolSearchMsg` lists both tools; `TestToolSearchMsgListsArchiveTools` |

**Quality findings** (both `[SUGGESTION]`, WARN tier — classified ACCEPTABLE, see Human Gate):
1. `internal/mcp/tools.go:184-216` — `handleMemArchive`/`handleMemUnarchive` are near-identical
   (unmarshal-loop-append-count). A shared `applyByID` helper is optional polish for only 2 call sites
   — reviewer recommended leave as-is unless a 3rd id-batch tool appears.
2. `internal/mcp/tools.go:184-216` — per-id errors are silently swallowed with no reason surfaced in
   the response (deliberate per design D1/D2 and AC-8's no-false-positive requirement) — flagged only
   as a future debuggability note, not a defect.

## Security Review (s11)

No critical/high/medium findings. Path-traversal guard (`fact.ValidID`) confirmed to hold per-id even
through the new batch entrypoint (each id independently re-validated inside `store.getFrom`, no
shared/cached shortcut). Id-list-only schema confirmed unable to reach `PlanArchive`/bulk-by-filter —
extra fields a caller might send are silently dropped by the typed `json.Unmarshal`, never reach
`app.Archive`/`app.Unarchive`.

**Low finding (fixed)**: `internal/mcp/tools.go:184-212` — no upper bound on `ids` array length
(unbounded loop of file syscalls on a very large batch). **Resolution**: commit `3179fab` adds
`maxBatchIDs = 1000`, checked before the loop in both handlers, rejecting an oversized batch with an
error before any filesystem work; test `TestMemArchiveRejectsOversizedBatch` added. Re-dispatched
`security-reviewer` confirmed: resolved, no new issues introduced by the fix (no off-by-one, no
info-leak in the error message, no new DoS surface via the reject path itself).

## Verification (s12)

Fix landed in this round (commit `3179fab`) → full suite re-run: `go test ./...`: 14/14 packages `ok`.
`go vet ./...`: clean. `go build ./...`: succeeds.

## Human Gate

Accepted — branch-gate-reviewer (autonomous), 2026-07-15.

Round 1 classification: 2 `[SUGGESTION]` WARNs → ACCEPTABLE (documented design trade-offs, no AC
impact). 1 `Low` security WARN → NOT-ACCEPTABLE per Calibration ("a security finding of ANY severity
always blocks"). Fixed in commit `3179fab` (batch-size bound). Round 2 classification (this round):
all 3 WARNs ACCEPTABLE/resolved — zero findings remain open.

## PR

https://github.com/JaraEsequiel/BBrain/pull/33
