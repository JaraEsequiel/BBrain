# BBrain — Plan 3b Design: `wiki link` (LLM-assisted graph population) + `wiki lint` (consistency checks)

**Status:** Approved design — ready for implementation planning.
**Date:** 2026-06-23
**Predecessors:** Plan 2 (reasoned wikilink graph: `links:`, `AddLink`, graph queries, `Candidates`), Plan 3 (pluggable `internal/llm` runner + `wiki build`).
**Roadmap slot:** Plan 3b — "`bbrain wiki link` (populate reasoned wikilinks via `Candidates` → LLM → `store.AddLink`) and `bbrain wiki lint` (consistency checks). Reuses the `internal/llm` runner."

---

## 1. Goal

Deliver two CLI subcommands under the existing `wiki` namespace:

- **`bbrain wiki link`** — autonomously grow the **fact graph** (Plan 2's `links:`) using the LLM: for each fact, read its FTS `Candidates`, ask the LLM which are genuinely related and how, validate, and write the reasoned typed links via `store.AddLink`. Automates the manual `bbrain link` from Plan 2.
- **`bbrain wiki lint`** — deterministic (no-LLM) consistency checks across facts + the derived `wiki/` layer, with `--fix` to repair the mechanically-safe issues. CI-friendly exit codes.

Both reuse the Plan 1–3 layering and the `internal/llm` runner. No new dependencies.

---

## 2. Global Constraints (inherited + new)

- **Module:** `bbrain`; **Go:** 1.25; **root:** `BBrain/` (`engram/` is reference only, never imported).
- **`.md` is the source of truth.** `wiki link` writes links into the **fact** `.md` files (through `store.AddLink`, which already bumps `updated_at` / preserves `revision_count` invariants from Plan 2). `wiki lint --fix` mutates only via existing, tested write paths (`store` for facts, `RegenerateIndex` for the derived index).
- **BBrain orchestrates; the LLM is a pure text→JSON function.** Same contract as Plan 3: prompt on stdin, one JSON object on stdout, reached via `$BBRAIN_AGENT_CLI`. `wiki link` requires it (unset → clear error, exit 1). **`wiki lint` does NOT use the LLM** and works with `BBRAIN_AGENT_CLI` unset.
- **Clean layering:** `internal/wiki` decides (LLM prompt + parse + structural validation, pure and fake-runner-testable) and **never imports `store`/`index`**. `internal/app` fetches data (`ListFacts`, `Candidates`, existing `Links`) and performs writes (`store.AddLink`, `RegenerateIndex`). `--dry-run` = app skips the write.
- **Relation vocabulary** is Plan 2's controlled set: `relates, depends-on, conflicts-with, supersedes, scoped, compatible` (`fact.Relations` / `fact.ValidRelation`). The LLM must pick from it.
- **Single directional edge.** `wiki link` stores one `src → dst` edge per proposed link; it does NOT also write the reciprocal `dst → src`. Plan 2's `Why`/`Neighbors` already resolve reverse lookups, so a single stored edge is sufficient.
- **Idempotency.** Re-running `wiki link` must not duplicate links: a proposal is skipped when an edge with the same `{src → dst, relation}` already exists on the source fact.
- **Validation before writing; abort the whole run on any structural failure** (no partial writes), mirroring `wiki build`.
- **Timestamps:** RFC3339 UTC via the injectable clock (`Store.Now`).
- **Tests** assume a POSIX `/bin/sh` for the runner/e2e tests (Linux dev platform).

---

## 3. `bbrain wiki link`

### 3.1 CLI

```
bbrain wiki link [--project P] [--scope S] [--limit N] [--dry-run]
```

- `--project` / `--scope`: restrict which facts are processed (same filtering as `wiki build`).
- `--limit N`: max FTS candidates considered per fact (default 8).
- `--dry-run`: print the proposed edges without writing.
- Unset `BBRAIN_AGENT_CLI` → error mentioning `BBRAIN_AGENT_CLI`, exit 1.

### 3.2 Flow (per-fact pass)

For each fact `F` after `--project`/`--scope` filtering:

1. `app.Candidates(F.ID, limit)` → candidate facts (Plan 2 FTS; already excludes `F` itself).
2. `wiki.BuildLinkPrompt(F, candidates, fact.Relations)` assembles a focused prompt: `F`'s id/title/type/project/scope/body, the candidate facts (id/title/snippet), the relation vocabulary, and the required JSON schema.
3. `Runner.Run(ctx, prompt)` → stdout.
4. `wiki.ParseLinkResponse(stdout)` → `[]ProposedLink{ Dst, Relation, Why string }` (wraps a sentinel `ErrInvalidJSON` on malformed output).

### 3.3 Structural validation (in `internal/wiki`, abort on failure)

For every proposed link of `F`:
- `Relation` ∈ `fact.Relations` (`fact.ValidRelation`).
- `Dst` is the id of one of `F`'s candidates (the LLM may not invent ids or link outside the candidate set).
- `Dst != F.ID` (no self-link).
- `Why` non-empty after trim.
- No duplicate `{Dst, Relation}` within `F`'s own proposal list.

Any failure aborts the entire `wiki link` run before any write.

### 3.4 Application & idempotency (in `internal/app`)

- `wiki.Link(...)` returns the validated proposals per source fact; `app.WikiLink` is responsible for the writes.
- For each validated proposal, `app` skips it when `F` already has an edge `{→ Dst, Relation}` (read from `F.Links`). Skipped proposals are reported as skipped, not written.
- Otherwise `app` calls `store.AddLink(F.ID, Dst, Relation, Why)`.
- `--dry-run`: `app` performs filtering + idempotency classification and returns the would-write set, but calls no `AddLink`.
- After a non-dry run, append a `## <ts> — wiki link` block to `wiki/log.md` listing written edges (`src -[relation]-> dst: why`) and a count of skipped-existing — consistent with `wiki build`'s log discipline.

### 3.5 Result type

```
LinkResult{ Written []Edge; Skipped int; DryRun bool }   // Edge{Src, Dst, Relation, Why}
```

`cmd` prints written edges (with a `[dry-run] would write:` banner when dry), and the skipped count.

---

## 4. `bbrain wiki lint`

### 4.1 CLI

```
bbrain wiki lint [--categories a,b,c] [--fix]
```

- **Whole-brain only — no `--project`/`--scope` filter.** Lint judges link/source existence against the *entire* fact set; filtering would make cross-project links falsely appear dangling. (Revised from the original draft, which listed these flags.)
- No LLM; runs with `BBRAIN_AGENT_CLI` unset.
- `--categories`: extra categories merged into the default vocabulary (same merge as `wiki build`) so a page using a custom category isn't falsely flagged.
- `--fix`: apply the mechanically-safe repairs (§4.3).
- **Exit code:** `0` when clean (or every detected issue was fixed); non-zero when any unfixed issue remains.

### 4.2 Checks (deterministic, pure over facts + `readPages`)

Let `byID` = all facts; `pages` = `readPages(wikiDir)` with parsed `PageMeta`.

1. **Dangling targets** — every `[[id]]` wikilink target (via `fact.LinkTargetID`) found in a **fact body**, a **fact's `links:`**, or a **wiki page body** must reference an existing fact. (Plan 3b has no page→page links, so all `[[id]]` targets are fact ids.) Report each dangling target with its location.
2. **Page source → missing fact** — every id in a page's `sources:` must exist in `byID`.
3. **Invalid category** — every page's `category` must be in the active vocabulary (defaults + `--categories`).
4. **Orphan page** — a page whose **every** `sources:` fact is missing (nothing left to distill).
5. **Stale page** — some source fact's `updated_at` is strictly newer than the page's `generated_at` (the distilled page lags its raws).

Each issue carries: kind, location (fact id / page relpath / line), and a human-readable message.

### 4.3 `--fix` policy

Fixable (mechanically safe, via existing tested write paths):
- **Stale/derived index** — always regenerate `index.md` via `RegenerateIndex` (cheap, idempotent, derived).
- **Dangling link to a deleted fact** — drop that `{→ id}` entry from the source fact's `links:` (through `store`), since the target is gone.

Reported but NOT auto-fixed (need judgment / belong elsewhere):
- **Stale page** → hint: run `bbrain wiki build` to re-distill.
- **Orphan page** → hint: review and delete manually (deletion is out of scope here).
- **Invalid category** → hint: fix the page or extend `--categories`.
- **Dangling `[[id]]` inside a fact/page body** (prose citation) → reported, not auto-edited (editing prose is unsafe to mechanize).

### 4.4 Result type

```
LintResult{ Issues []Issue; Fixed []Issue; DryRunFix bool }
Issue{ Kind, Location, Message string; Fixable bool }
```

`cmd` prints a grouped report; with `--fix`, prints what was fixed and what remains; sets the exit code from the remaining (unfixed) issue count.

---

## 5. File structure

- `internal/wiki/link.go` — **create:** `ProposedLink`, `Edge`, `LinkResult`, `BuildLinkPrompt`, `ParseLinkResponse`, `ValidateProposals`. Pure; imports `fact`, `llm`, stdlib.
- `internal/wiki/link_test.go` — **create:** prompt-assembly, parse, validation-matrix tests (fake runner).
- `internal/wiki/lint.go` — **create:** `Issue`, `LintResult`, `Lint(facts, pages, validCategories)` (pure check engine returning issues + fixability), plus a `[[id]]` target scanner. Imports `fact`, stdlib; reuses `readPages`/`ParsePageMeta`/`RegenerateIndex` from `wiki.go`.
- `internal/wiki/lint_test.go` — **create:** check-matrix + fix-classification tests over fixture trees.
- `internal/app/app.go` — **modify:** add `WikiLink`/`WikiLinkOptions` (filter → candidates → `wiki` decide → idempotency skip → `AddLink` → log) and `WikiLint`/`WikiLintOptions` (gather facts+pages → `wiki.Lint` → apply `--fix` via `store`/`RegenerateIndex`).
- `internal/app/app_test.go` — **modify:** wiring tests with a fake runner (link filter + idempotency skip + dry-run) and fix-application tests (lint).
- `cmd/bbrain/main.go` — **modify:** add `link` / `lint` cases to `cmdWiki`; usage line.
- `cmd/bbrain/main_test.go` — **modify:** e2e `wiki link` (fake agent script) + `wiki lint --fix` (fixture tree) + `wiki link` unconfigured-error test.

---

## 6. Scope boundaries

**In scope:** `wiki link` (autonomous, validated, idempotent, dry-run) and `wiki lint` (deterministic checks + safe `--fix`), CLI only.

**Deferred:**
- LLM-assisted *semantic* lint (contradiction / duplicate-page / mis-categorization detection) — a later plan.
- Page deletion / orphan auto-removal and stale-page re-distillation automation (the latter is `wiki build`'s job).
- MCP exposure of `wiki_link` / `wiki_lint` — Plan 4.
- TUI config of the agent CLI — Plan 5.

**Carryover follow-ups from the Plan 3 review** (see `docs/superpowers/plans/carryover-from-plan-3-review.md`) to fold into this plan where cheap:
- Add a same-build/same-run **slug/edge collision guard** so reported output never misrepresents disk (the `wiki link` "no duplicate `{Dst,Relation}` within a fact's proposals" rule is the analog here).
- The `wiki build` mid-write partial-state wording is a documentation fix, not behavioral; `wiki lint`'s derived-index regeneration further mitigates index drift.

---

## 7. Testing strategy

- **Pure core (`internal/wiki`):** link prompt contains the fact, candidates, relation vocab, and JSON schema; `ParseLinkResponse` wraps `ErrInvalidJSON`; validation rejects bad relation / non-candidate dst / self-link / empty why / intra-proposal dup; lint detects each of the five issue kinds and classifies fixability correctly.
- **App wiring:** `--project`/`--scope` filter; idempotency skip of an already-existing edge; `--dry-run` writes nothing; `--fix` regenerates the index and drops a dangling fact link, leaving unfixable issues reported and exit non-zero.
- **E2E (`cmd`):** real shell-script fake agent drives `wiki link` end-to-end (a link appears in the source fact's `.md`); a fixture wiki tree drives `wiki lint --fix`; `wiki link` with `BBRAIN_AGENT_CLI` unset exits 1.
- `go test ./...` green and `go vet` clean before each commit.
