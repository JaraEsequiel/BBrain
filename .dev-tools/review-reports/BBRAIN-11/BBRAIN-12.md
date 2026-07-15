---
ticket: BBRAIN-12
epic: BBRAIN-11
plan: .dev-tools/plans/BBRAIN-11/BBRAIN-12-mcp-auto-reindex.md
test_report: .dev-tools/test-reports/BBRAIN-11/BBRAIN-12.md
generated_at: 2026-07-15
audited_commit: 1616f67
---

# BBRAIN-12: Review Report

## Doc Sync (Step 1.5)

Not structural (no manifest/lockfile change, no new top-level directory, no workspace added/removed)
— `.claude/context/` left untouched, no researcher dispatch. `docs-maintainer`: no `docs/` directory
exists in this repo — no-op. `readme-maintainer`: no VexForge-style marker blocks
(`vexforge:overview`/`vexforge:structure`) exist in this repo's `README.md` — it's fully human-authored
prose — no-op, nothing to regenerate. No commit made (nothing stale). HEAD unchanged from Step 1's
resolution.

## Dependency Audit (s09)

No dependency manifest files changed in this diff (`go.mod`/`go.sum` untouched) — skipped per the
skill's own rule. No dependency findings.

## AC Coverage + Code Quality (s10)

| AC | Status | Note |
|----|--------|------|
| AC-1 | ✅ Covered | `RunBackgroundReindex` + `cmdMCP` wiring; `TestAcceptance_AC1_...` drives a real MCP session, hand-edits mid-session, confirms `mem_search`/`mem_get` reflect it. |
| AC-2 | ✅ Covered | `App.withIndex` mutex + `RebuildAll`'s single transaction; `TestConcurrentReindexAndSaveDoNotRace` + acceptance test pass under `-race` across repeated runs. |
| AC-3 | ✅ Covered | Fingerprint-gate before any `Reindex()` call; acceptance test confirms zero rewrite when unchanged, rebuild when changed. |
| AC-4 | ✅ Covered | README `watch` bullet documents the CLI-only edge + dual-process risk; acceptance test checks both doc text and runtime behavior. |

**Quality findings:**
- [SUGGESTION] (WARN) `internal/mcp/reindex_loop.go:22-33` — errors from `FactsFingerprint`/`Reindex` are silently swallowed with no stderr diagnostic; a persistent failure (e.g. disk-full) would leave auto-reindex silently broken with no operator-visible signal.
- [SUGGESTION] (WARN) `internal/prompthook/run.go:108` — pre-existing, untouched code opens the index directly, bypassing the new mutex; a separate OS process, so out of this ticket's in-process-only scope. Not a regression.

No CRITICAL/WARNING findings. Reviewer independently re-ran the full suite 3x under `-race` for
flakiness (none found) and verified the transactional-rollback test genuinely forces a mid-tx
`SQLITE_BUSY`, not a `Begin()`-time failure.

## Security Review (s11)

No critical/high findings.
- LOW — the global mutex serializes a full index rebuild against all other index ops; a very
  large brain mid-rebuild could stall a concurrent `mem_search` for the rebuild's duration.
  Availability/UX only, not remotely triggerable, no data exposure. **Fix-loop round 1:** sharper
  evidence presented (the disk I/O — `ListFacts`, the part that scales with brain size — already
  runs outside the mutex; only the SQL transaction itself is locked, and `Server.Serve` processes
  one request at a time so contention is bounded to "the one active request vs. the one background
  tick"). **Classified NOT-ACCEPTABLE regardless** — the reviewer's Calibration has no
  proportionality exception for a security-tagged finding, at any severity, however narrow the
  window. A genuine fix (bounding the lock to O(1) via a shadow-table build-then-swap instead of
  build-in-place) is a real, disproportionate redesign for this ticket's scope — carries its own
  regression risk (FTS5 virtual-table rename semantics need verification) and deserves its own
  design/test cycle, not a same-session review-fix. **UNRESOLVED.**
- LOW (fixed, ACCEPTABLE) — the background goroutine had no shutdown `sync.WaitGroup`/join in
  `cmdMCP`. Fixed in `1616f67`: `reindexWG.Add(1)`/`defer reindexWG.Done()` around the goroutine,
  `defer reindexWG.Wait()` declared before `defer cancel()` (LIFO: `cancel()` fires first, then
  `Wait()` blocks until the goroutine has actually returned — verified no deadlock, no leak).
  Re-classified ACCEPTABLE.

No SQL injection (all parameterized), no secrets/env-var exposure, no new deserialization path, no
new auth-boundary or MCP-tool-surface change, no path-traversal (facts dir is not attacker-influenced
per-request input).

## Verification (s12)

A fix landed (`1616f67`, the WaitGroup fix), so the suite was re-run fresh:
```
go build ./... && go vet ./... && go test ./... -race -count=1
```
→ all 14 packages PASS.

## Human Gate

**Blocked (autonomous) — 2026-07-15.** Two rounds of `@agent-vex:branch-gate-reviewer` classification
over the accumulated WARN set:
- Round 1: 2 code-quality SUGGESTIONs → ACCEPTABLE; 2 security LOW findings → NOT-ACCEPTABLE (the
  reviewer's Calibration blocks any security-tagged finding regardless of severity — no
  proportionality exception).
- Round 2 (after fixing the goroutine-shutdown finding, commit `1616f67`): that finding →
  ACCEPTABLE (fixed, verified). The remaining finding (global mutex could stall `mem_search` during
  a large-brain full-rebuild transaction) → **NOT-ACCEPTABLE again**, even after presenting sharper
  evidence that the lock's held duration is already bounded (disk I/O excluded, single fast SQL
  transaction only, single-threaded request model). The reviewer correctly held the line: Calibration
  has no severity-based carve-out for security-tagged findings.

**Why this isn't fixed further, and why that's the right call, not a shortcut:** the only genuine
fix — rebuilding the index into shadow tables and swapping them in under the lock (bounding lock-hold
time to O(1) regardless of brain size) — is a real architectural change with its own regression risk
(FTS5 virtual-table rename semantics need verification against this repo's SQLite driver) and its own
design/test cycle. Forcing it into this review's fix loop, under time pressure, risks introducing an
untested concurrency bug into the exact subsystem (D2/AC-2) this ticket just made safe. This is
precisely the scenario the autonomy protocol's `blocked` branch exists for: a STOP-level finding that
survives the fix loop, where accepting the risk requires a human decision autonomy cannot make on its
own ("continuar" does not exist under autonomy).

**Recommendation for Esequiel:** either (a) accept the risk explicitly (it's a single-user local
tool; the practical exposure is a brief stall on `mem_search` while a large brain's background
reindex commits — bounded, non-remote, no data loss) and merge as-is, or (b) file a follow-up ticket
for the shadow-table-swap redesign as its own Story before merging. The branch is otherwise fully
green, all 4 ACs covered, no other findings.

## PR

Not opened — blocked at the human gate above. `git push` was not run.
