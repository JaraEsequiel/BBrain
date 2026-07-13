---
key: BBRAIN-1
type: Epic
title: Search recall: porter stemming for FTS5 tokenizer
status: todo
area: search
labels: [index, fts5, search]
points: 8
created: 2026-07-13T19:14:50Z
updated: 2026-07-13T19:14:50Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-13
---

## Context
`mem_search` is BBrain's most-used tool per session — it is the reason BBrain exists. The FTS5 index (`internal/index/index.go`, `CREATE VIRTUAL TABLE ... USING fts5(...)`) runs today with the default `unicode61` tokenizer: no stemming. A search for "archiving" does not match a fact saved as "archive"; "decisiones" does not match "decisión". The failure mode is silent — zero results, no error — so the agent assumes the fact was never saved and re-explains context that was already in the brain. This is exactly the cross-session context loss BBrain exists to kill, reintroduced by its own search layer.

Debate verdict: GO — porter-only, de-risked with a regression set over the real corpus; #1 of the run. Tension resolved: porter-only + regression set over real corpus turns a speculative bet into something evidence-backed.

## Acceptance Criteria (child-story outline)
- Story BBRAIN-2 covers the full scope: FTS5 tokenizer switched to `porter unicode61`, a loud staleness signal for un-reindexed old-tokenizer indexes, a before/after regression query set over the real corpus (`memory/raws/facts/`) with zero new false negatives and strictly higher recall on inflected/plural queries, the existing OR-based fallback search path (`SearchAny`) still working, and updated word-boundary assertions in `index_test.go`.

## Technical scope
- `internal/index/index.go` — FTS5 schema `tokenize` constant.
- `internal/index/index_test.go` — word-boundary assertions to rewrite under stemming semantics.
- `internal/index` `Open()` — candidate site for a schema-version staleness check.
- Migration path: existing `Reset()`/reindex (drop-and-rebuild) — the index is documented as derived/disposable, no data migration needed.

## Constraints
- Go stdlib-first: only sqlite/yaml/atomic deps. No new dependency — porter is an FTS5 built-in tokenizer, not an external dep.
- Single binary, portable.
- The index is derived; the `.md` files under `raws/facts/` are the source of truth. Any schema change is resolved via reindex, never data migration.
- Out of scope: trigram/fuzzy/typo-tolerance (unverified in the `modernc.org/sqlite` build), embeddings/vector search (hard project constraint), BM25 ranking/scoring changes.

## Definition of Done
- Story BBRAIN-2 shipped: porter tokenizer live, staleness signal in place, regression set green in CI, word-boundary tests updated, `go test ./internal/index` passing.
- Manual verification: `bbrain reindex` on the live vault + spot-check of 3-4 inflected queries.
