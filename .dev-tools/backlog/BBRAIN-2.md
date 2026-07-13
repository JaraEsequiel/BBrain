---
key: BBRAIN-2
type: Story
title: "Search: Enable porter stemming with regression tests"
status: in-review
parent: BBRAIN-1
area: search
labels: [index, fts5, search]
points: 5
pr: https://github.com/JaraEsequiel/BBrain/pull/30
created: 2026-07-13T19:14:50Z
updated: 2026-07-13T21:04:30Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-13
---

## Context
The FTS5 virtual table in `internal/index/index.go` is created with the default `unicode61` tokenizer (no stemming). Searches for inflected/plural forms ("archiving" vs "archive", "decisiones" vs "decisión") return zero results with no error — a silent recall gap that looks identical to "this fact was never saved." This Story switches the tokenizer to `porter unicode61`, adds a loud staleness signal for indexes built with the old tokenizer, and proves the change with a before/after regression set over the real corpus.

Before any schema change, this Story must confirm `modernc.org/sqlite`'s FTS5 build actually exposes the `porter` tokenizer (see Definition of Done, step 1) — there is no stdlib fallback if it doesn't, and that outcome would send this Epic back to backlog per its open question.

## Acceptance Criteria
AC-1  Given the FTS5 schema in `internal/index/index.go`
      When the index is created or rebuilt via `bbrain reindex`
      Then the virtual table uses `tokenize = 'porter unicode61'`

AC-2  Given a regression query set built from ~12 real search terms drawn from `memory/raws/facts/`
      When each query is run against the old (`unicode61`) index and then against the new (`porter unicode61`) index
      Then recall under the new tokenizer is greater than or equal to recall under the old tokenizer on every query in the set

AC-3  Given the same regression query set, restricted to the inflected/plural queries that returned zero results under the old tokenizer
      When those queries are run against the new (`porter unicode61`) index
      Then recall is strictly higher than zero on every one of those queries

AC-4  Given the existing OR-based fallback search path (`SearchAny`)
      When it runs against a porter-tokenized index
      Then it still returns correct results (no regression in the fallback path)

AC-5  Given an index built before this change (old tokenizer)
      When the binary is upgraded to a version with the porter schema but `bbrain reindex` has not been run
      Then `Open()` detects the schema-version mismatch and surfaces a loud signal (error or warning) instead of silently serving stale-tokenizer results

AC-6  Given the word-boundary assertions in `internal/index/index_test.go` written against `unicode61` semantics
      When they are rewritten for porter stemming semantics
      Then `go test ./internal/index` passes with no assertion silently weakened to always-pass

## Technical scope
- `internal/index/index.go` — change the `tokenize` value in the FTS5 `CREATE VIRTUAL TABLE` schema constant to `porter unicode61`; add/bump a schema-version marker checked in `Open()`.
- `internal/index/index_test.go` — rewrite word-boundary assertions that assumed `unicode61` no-stemming behavior; add the before/after regression test using a fixed query set.
- Migration: reuse the existing `Reset()` (drop-and-rebuild) as the reindex path — no data migration, the index is derived.

## Constraints
- Go stdlib-first: only sqlite/yaml/atomic deps, no new dependency (porter is FTS5 built-in).
- Single binary, portable.
- Risk gate: if `modernc.org/sqlite`'s FTS5 build does not expose the `porter` tokenizer, this Story cannot proceed as scoped — no stdlib fallback exists; stop and report back to the Epic rather than substituting a different tokenizer.
- No trigram/fuzzy/typo-tolerance, no embeddings/vector search, no BM25 ranking/scoring changes — out of scope.
- Schema changes resolve via reindex only, never data migration (index is derived, `.md` facts are source of truth).

## Autonomy Guide
| | Actions |
|---|---|
| **Always** (just do it) | Edit `internal/index/index.go` tokenizer constant and schema-version check; edit/add tests in `internal/index/index_test.go`; run `go test ./internal/index`; run `go vet ./internal/index`; run `bbrain reindex` against a local/test vault |
| **Ask first** (confirm with human) | Any change to the FTS5 schema beyond the tokenizer clause; any change to `Open()`'s public error/return contract that other packages depend on |
| **Never** (out of scope) | Add trigram/fuzzy matching; add embeddings/vector search; change BM25 ranking/scoring; add a new runtime dependency; touch `internal/store` or `internal/fact` (out of this Story's scope); modify CI/CD pipelines |

## Definition of Done
1. Confirm `modernc.org/sqlite`'s FTS5 build exposes the `porter` tokenizer (minimal test: create `fts5(x, tokenize='porter unicode61')`, insert "archiving", query "archive", assert a match). If unavailable, stop here and report — do not proceed with the schema change.
2. `internal/index/index.go` uses `porter unicode61`; schema-version check in `Open()` detects mismatches and surfaces a loud signal (as required by AC-5) — this check is mandatory, not optional.
3. Regression query set (before/after) passes with the properties in AC-2/AC-3, checked into the test suite and run in CI.
4. `internal/index/index_test.go` word-boundary assertions rewritten and green.
5. `go test ./internal/index` green; `bbrain reindex` run manually against the live vault with 3-4 inflected queries spot-checked.
