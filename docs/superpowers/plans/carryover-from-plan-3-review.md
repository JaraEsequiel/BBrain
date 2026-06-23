# Carryover from Plan 3 whole-branch review (Opus) — inputs for Plan 3b

Source: final whole-branch review of `plan-3-wiki-build` (merged as PR #2, master @ f986e29).
Verdict was **Ready to merge: Yes** — no Critical, no implementer defects. These are the
deferred follow-ups to fold into Plan 3b (`wiki link` / `wiki lint`) or a tech-debt pass.

## 1. Build write loop is not all-or-nothing on a mid-loop filesystem failure (Important, plan-owned design)
- Where: `internal/wiki/wiki.go`, `Build` write loop.
- Behavior: **Validation** is all-or-nothing (a bad LLM page writes nothing — the load-bearing
  guarantee). But the **write** phase is not: if page N's `os.MkdirAll`/`atomic.WriteFile` fails,
  pages 1..N-1 are already on disk, and `RegenerateIndex`/`AppendLog` never run, so the index
  won't even list the partial pages.
- Why acceptable now: single-user local CLI; mid-build FS failure is rare; the next successful
  build self-heals (index is derived by scanning `wiki/`).
- Discrepancy to fix: Plan 3's self-review wording "nothing is written" is only true for
  validation errors. Either (a) correct the wording, or (b) for true multi-file atomicity, stage
  pages into a temp dir and rename into place at the end.

## 2. Silent same-build slug collision (Minor, genuine)
- Where: `internal/wiki/wiki.go`, `Build`.
- Behavior: if the LLM returns two pages that derive the same `bucket/category/slug`, the second
  `atomic.WriteFile` silently overwrites the first — yet BOTH appear in `res.Written` and in
  `log.md`. Not a safety issue (still inside the wiki dir), but `BuildResult.Written`/`log.md`
  then misrepresent what's actually on disk.
- Fix: a one-line dedupe-or-reject check in the validate loop (reject duplicate
  bucket/category/slug within a single build), so the reported output never lies.
  `wiki lint` (Plan 3b) is also a natural home for detecting cross-build collisions.

## Already-triaged cosmetic Minors (no action needed, recorded for completeness)
- `TestCLIRunnerTimeout` ~5s wall-clock: `sleep 5` is a forked child of /bin/sh; SIGKILL hits the
  shell, the orphaned sleep holds the stdout pipe so `cmd.Run` blocks ~5s. Test is correct
  (returns `ErrTimeout`). Fix when convenient: `exec sleep 5` in the script, or a compiled sleeper.
- `runner.go` error wrap appends stderr as a string after `%w` (only `*exec.ExitError` is
  unwrappable). Cosmetic — callers match the sentinel errors which are returned directly.
- `os.IsNotExist` vs `errors.Is(err, fs.ErrNotExist)` — modern idiom, no behavior change.
- `TestDeriveBucketSanitizesProject` only asserts absence of `..`, not the exact `projects/etc`
  bucket. Production path verified correct via `fact.Slug("../etc") = "etc"`.
- A couple of tests `idx, _ := os.ReadFile(...)` ignore the read error before asserting contents;
  self-correcting (the contains-assertion fails anyway).
