# BBrain — Tech-Debt Refinements (post-Plan 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pay down the three accepted Minor findings from the Plan 2 whole-branch review — without changing any observable behavior of the CLI or engine.

**Architecture:** Three small, independent refactors/coverage fills on code introduced by Plan 2 (the reasoned wikilink graph). Two touch `internal/index/index.go` (DRY the FTS match-string builders; make `Clear` atomic); one adds a missing characterization test to `internal/store/store_test.go` (the `AddLink` `updated_at`/`revision_count` invariant). All three are behavior-preserving, so each task's tests are *characterization* tests that pass **green before and after** the change — they lock the current contract rather than drive new behavior (red→green TDD does not apply to a pure refactor or a coverage-gap fill).

**Tech Stack:** Go 1.25, `modernc.org/sqlite` (FTS5 + the plain `links` table), `gopkg.in/yaml.v3`, `github.com/natefinch/atomic`. No new dependencies.

## Global Constraints

- **Module path:** `bbrain` (local module; all imports are `bbrain/internal/...`).
- **Go version:** `go 1.25` in `go.mod`.
- **Project root:** `BBrain/` (the `engram/` subdir is reference only — never import from it).
- **No behavior change.** These are tech-debt refinements. The output of `buildMatch`/`buildMatchAny`, the post-condition of `Clear`, and the behavior of `AddLink` must be byte-for-byte identical before and after. Every existing test must stay green.
- **`.md` is source of truth.** The `links` table and `facts_fts` remain derived and disposable.
- **SQLite driver:** `modernc.org/sqlite`, registered driver name `"sqlite"`.
- **Timestamps:** RFC3339 UTC, accessed through `Store.Now func() time.Time` for deterministic tests.
- **No new dependencies.**
- **Commit after every task.** Run `go test ./...` green before each commit.

---

## Provenance & Branch Context

These refinements target code added by **Plan 2** (`2026-06-23-bbrain-plan-2-wikilink-graph.md`), which currently lives on branch `plan-2-wikilink-graph` (open PR #1, **not yet merged to `master`**). The cleanest landing is to add these commits onto that same branch so they refine the open PR. Confirm the branch at execution time; do not start on `master` if Plan 2 is still unmerged there, or the refactors will reference code that isn't present.

Findings explicitly **out of scope** (the human triaged these as "leave as-is"): the `Candidates` over-fetch sizing and the `cmdWhy` arrow orientation (both already match the spec), the `LinkTargetID` local-variable rename, and the `os.IsNotExist` vs `errors.Is` style note (the current usage is correct).

---

## File Structure

No files are created. Three existing files are modified:

- `internal/index/index.go` — **modify:** add a private `quoteTokens` helper and rewrite `buildMatch`/`buildMatchAny` to share it (Task 1); wrap `Clear`'s two `DELETE`s in a single transaction (Task 2).
- `internal/index/index_test.go` — **modify:** add `TestBuildMatchHelpers` characterization test (Task 1).
- `internal/store/store_test.go` — **modify:** add `TestAddLinkBumpsUpdatedAtNotRevisionCount` (Task 3).

---

## Task 1: `index` — DRY the FTS match builders behind `quoteTokens`

**Why:** `buildMatch` and `buildMatchAny` are identical except for the join separator. The duplicated half is the FTS5 quote-escaping logic, which is security-sensitive (it neutralizes FTS special characters). Two copies can silently drift; one shared helper cannot.

**Files:**
- Modify: `internal/index/index.go` (`buildMatch`, `buildMatchAny`; add `quoteTokens`)
- Test: `internal/index/index_test.go`

**Interfaces:**
- Consumes: `strings` (already imported).
- Produces:
  - `func quoteTokens(q string) []string` — splits `q` on whitespace and wraps each token in double quotes, doubling any embedded `"`. Returns an empty slice for a blank query.
  - `func buildMatch(q string) string` — unchanged signature/output: `quoteTokens` joined by `" "` (AND).
  - `func buildMatchAny(q string) string` — unchanged signature/output: `quoteTokens` joined by `" OR "`.

- [ ] **Step 1: Write the characterization test**

Append to `internal/index/index_test.go` (the test is in `package index`, so it can call the unexported helpers directly):

```go
func TestBuildMatchHelpers(t *testing.T) {
	cases := []struct {
		name, in, and, or string
	}{
		{"two terms", "jwt database", `"jwt" "database"`, `"jwt" OR "database"`},
		{"single term", "postgres", `"postgres"`, `"postgres"`},
		{"embedded quote", `a"b`, `"a""b"`, `"a""b"`},
		{"collapses whitespace", "  jwt   auth  ", `"jwt" "auth"`, `"jwt" OR "auth"`},
		{"blank query", "   ", "", ""},
	}
	for _, c := range cases {
		if got := buildMatch(c.in); got != c.and {
			t.Errorf("%s: buildMatch(%q) = %q, want %q", c.name, c.in, got, c.and)
		}
		if got := buildMatchAny(c.in); got != c.or {
			t.Errorf("%s: buildMatchAny(%q) = %q, want %q", c.name, c.in, got, c.or)
		}
	}
}
```

- [ ] **Step 2: Run the test against the CURRENT (un-refactored) code — it must already PASS**

Run: `cd BBrain && go test ./internal/index/ -run TestBuildMatchHelpers -v`
Expected: PASS. This proves the test captures the *current* contract before we touch the implementation. (This is a refactor safety net, not red→green.)

- [ ] **Step 3: Add `quoteTokens` and rewrite the two builders**

In `internal/index/index.go`, replace the two existing functions:

```go
// buildMatch turns a raw user query into a safe FTS5 expression: each whitespace
// token is wrapped in double quotes (with internal quotes doubled), so FTS5
// special characters are treated as literals and tokens are AND-ed together.
func buildMatch(q string) string {
	fields := strings.Fields(q)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, `""`)
		quoted = append(quoted, `"`+f+`"`)
	}
	return strings.Join(quoted, " ")
}

// buildMatchAny is like buildMatch but OR-joins the quoted tokens, so a fact
// matching any single term is returned.
func buildMatchAny(q string) string {
	fields := strings.Fields(q)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, `""`)
		quoted = append(quoted, `"`+f+`"`)
	}
	return strings.Join(quoted, " OR ")
}
```

with this trio (one shared helper plus two thin joiners):

```go
// quoteTokens splits q into whitespace-delimited tokens and wraps each in double
// quotes, doubling any embedded quote so FTS5 treats every token as a literal
// (neutralizing FTS special characters). A blank query yields an empty slice, so
// strings.Join over the result returns "" — the empty match the search core
// short-circuits on.
func quoteTokens(q string) []string {
	fields := strings.Fields(q)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, `""`)
		quoted = append(quoted, `"`+f+`"`)
	}
	return quoted
}

// buildMatch turns a raw user query into a safe FTS5 expression: each token is
// quoted via quoteTokens, then AND-ed together.
func buildMatch(q string) string {
	return strings.Join(quoteTokens(q), " ")
}

// buildMatchAny is like buildMatch but OR-joins the quoted tokens, so a fact
// matching any single term is returned.
func buildMatchAny(q string) string {
	return strings.Join(quoteTokens(q), " OR ")
}
```

- [ ] **Step 4: Run the index tests — all must still PASS**

Run: `cd BBrain && go test ./internal/index/ -v`
Expected: PASS — `TestBuildMatchHelpers` plus all pre-existing index tests (search, search-any, graph, clear). Identical output before and after proves the refactor preserved behavior.

- [ ] **Step 5: Vet**

Run: `cd BBrain && go vet ./internal/index/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
cd BBrain
git add internal/index/index.go internal/index/index_test.go
git commit -m "refactor(index): extract quoteTokens helper shared by buildMatch/buildMatchAny"
```

---

## Task 2: `index` — make `Clear` atomic

**Why:** `Clear` runs `DELETE FROM facts_fts` then `DELETE FROM links` as two separate statements. If the second fails, the FTS table is already empty while `links` still holds data — a half-cleared index. Wrapping both in one transaction removes that window. (It is self-healing in practice because `Reindex` repopulates both immediately, but a single transaction is trivially cheap and correct.)

**Note on testing:** the rollback path requires forcing the second `DELETE` to fail, which has no injection seam here, so there is no new unit test — the existing `TestClearEmptiesIndex` and `TestClearAlsoEmptiesLinks` cover the (unchanged) happy-path post-condition, and atomicity is a structural guarantee of the transaction. Do not add a contrived test or a fault-injection hook for this.

**Files:**
- Modify: `internal/index/index.go` (`Clear`)
- Test: none added; existing `internal/index/index_test.go` Clear tests must stay green.

**Interfaces:**
- Consumes: `ix.db` (`*sql.DB`), already used by `IndexFact`/`IndexLinks` with the same `Begin`/`Rollback`/`Commit` pattern.
- Produces: `func (ix *Index) Clear() error` — unchanged signature and post-condition (both tables empty on success).

- [ ] **Step 1: Confirm the existing Clear tests pass before the change**

Run: `cd BBrain && go test ./internal/index/ -run 'TestClear' -v`
Expected: PASS (`TestClearEmptiesIndex`, `TestClearAlsoEmptiesLinks`). Baseline before touching `Clear`.

- [ ] **Step 2: Wrap `Clear`'s two deletes in a transaction**

In `internal/index/index.go`, replace the existing `Clear`:

```go
// Clear empties the index (used before a full reindex): both the FTS table and
// the derived links table.
func (ix *Index) Clear() error {
	for _, stmt := range []string{`DELETE FROM facts_fts`, `DELETE FROM links`} {
		if _, err := ix.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
```

with:

```go
// Clear empties the index (used before a full reindex): both the FTS table and
// the derived links table, in a single transaction so the index is never left
// half-cleared if the second delete fails.
func (ix *Index) Clear() error {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	for _, stmt := range []string{`DELETE FROM facts_fts`, `DELETE FROM links`} {
		if _, err := tx.Exec(stmt); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 3: Run the index tests — all must still PASS**

Run: `cd BBrain && go test ./internal/index/ -v`
Expected: PASS — the Clear tests and every other index test, unchanged.

- [ ] **Step 4: Vet**

Run: `cd BBrain && go vet ./internal/index/`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
cd BBrain
git add internal/index/index.go
git commit -m "refactor(index): make Clear atomic via a single transaction"
```

---

## Task 3: `store` — lock the `AddLink` `updated_at`/`revision_count` invariant with a test

**Why:** `AddLink`'s doc comment promises it "bumps `updated_at`" but "leaves `revision_count` untouched because a link edit is not a content revision." That is the one behavioral promise in the package with no test. A future refactor could drop the `updated_at` bump or accidentally increment `revision_count` with nothing failing. This is a coverage-gap fill — no production code changes.

**Files:**
- Modify: `internal/store/store_test.go`
- (No change to `internal/store/store.go`.)

**Interfaces:**
- Consumes: the existing `newTestStore` helper (sets `s.Now` to a fixed base), `Store.Save`, `Store.AddLink`, `Store.Get`; `time` (already imported in the test file).
- Produces: nothing public — a new test function only.

- [ ] **Step 1: Write the characterization test**

Append to `internal/store/store_test.go`:

```go
func TestAddLinkBumpsUpdatedAtNotRevisionCount(t *testing.T) {
	s := newTestStore(t)
	src, err := s.Save(SaveInput{Type: "architecture", Title: "A", Body: "x", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	// Advance time so the second save is a distinct file (not deduped).
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	dst, err := s.Save(SaveInput{Type: "decision", Title: "B", Body: "y", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}

	// Link at a later, distinct timestamp so the bump is observable.
	linkTime := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return linkTime }
	updated, err := s.AddLink(src.ID, dst.ID, "relates", "because")
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}

	// updated_at is bumped to the link time...
	if want := linkTime.Format(time.RFC3339); updated.UpdatedAt != want {
		t.Fatalf("updated_at = %q, want %q", updated.UpdatedAt, want)
	}
	// ...and it actually moved (the source was saved at the base time).
	if updated.UpdatedAt == src.UpdatedAt {
		t.Fatalf("updated_at was not bumped: still %q", updated.UpdatedAt)
	}
	// revision_count is unchanged — a link edit is not a content revision.
	if updated.RevisionCount != src.RevisionCount {
		t.Fatalf("revision_count = %d, want %d (unchanged)", updated.RevisionCount, src.RevisionCount)
	}

	// Both invariants survive a reload from disk.
	reloaded, ok, err := s.Get(src.ID)
	if err != nil || !ok {
		t.Fatalf("reload: ok=%v err=%v", ok, err)
	}
	if reloaded.UpdatedAt != updated.UpdatedAt {
		t.Fatalf("persisted updated_at = %q, want %q", reloaded.UpdatedAt, updated.UpdatedAt)
	}
	if reloaded.RevisionCount != src.RevisionCount {
		t.Fatalf("persisted revision_count = %d, want %d", reloaded.RevisionCount, src.RevisionCount)
	}
}
```

- [ ] **Step 2: Run it against the CURRENT code — it must already PASS**

Run: `cd BBrain && go test ./internal/store/ -run TestAddLinkBumpsUpdatedAtNotRevisionCount -v`
Expected: PASS. The behavior already exists; this test documents and protects it. (If it FAILS, the implementation contradicts its own doc comment — stop and escalate rather than editing the test to match.)

- [ ] **Step 3: Run the full store package**

Run: `cd BBrain && go test ./internal/store/ -v`
Expected: PASS — the new test plus all existing store tests.

- [ ] **Step 4: Vet**

Run: `cd BBrain && go vet ./internal/store/`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
cd BBrain
git add internal/store/store_test.go
git commit -m "test(store): lock AddLink updated_at bump + revision_count invariance"
```

---

## Final verification

- [ ] **Run the whole suite uncached**

Run: `cd BBrain && go test -count=1 ./...`
Expected: every package `ok`. No behavior changed; the diff is one new helper, one transaction wrapper, and two new tests.

---

## Self-Review

**1. Spec coverage (the three accepted Minor findings the human selected):**
- DRY `buildMatch`/`buildMatchAny` via a shared `quoteTokens` → Task 1. ✓
- `Clear` atomic (single transaction) → Task 2. ✓
- `AddLink` `updated_at`-bump / `revision_count`-invariance test → Task 3. ✓
- Out-of-scope items (Candidates over-fetch, cmdWhy arrow, `LinkTargetID` var rename, `os.IsNotExist` style) are explicitly listed as excluded and have no task. ✓

**2. Placeholder scan:** No TBD/TODO. Every code step shows the complete code; every test step shows the command and the expected result. ✓

**3. Type consistency:** `quoteTokens(q string) []string` is defined in Task 1 and consumed only by `buildMatch`/`buildMatchAny` in the same task — signatures match. `Clear() error` keeps its exact signature and post-condition (Task 2). The Task 3 test uses only existing symbols (`newTestStore`, `Save`, `SaveInput`, `AddLink`, `Get`, `time`) with their real signatures. No new public API is introduced anywhere. ✓

**4. Behavior-preservation check:** Tasks 1 and 2 are refactors whose tests pass green before and after; Task 3 adds no production code. `strings.Join(nil, sep) == ""`, so `quoteTokens("")` → `""` preserves the `search()` empty-match short-circuit. The transactional `Clear` has the identical success post-condition (both tables empty). ✓
