# BBrain — Plan 2: Reasoned Wikilink Graph Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: This plan MUST be executed with superpowers:subagent-driven-development — a fresh subagent per task with two-stage review between tasks. Do **not** use inline `executing-plans` for this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the `links:` frontmatter that already round-trips through the `fact` package into a queryable, derived graph: mirror each fact's reasoned wikilinks into a `links` table in the index, add a `mem_link`-style engine operation that writes a reasoned edge into a fact's `.md`, and expose graph queries ("why is A related to B", neighbors) plus FTS-based correlation/conflict surfacing — all testable via CLI.

**Architecture:** Builds directly on the Plan 1 engine (`fact` → `store` → `index` → `app` → `cmd/bbrain`). The `.md` frontmatter `links:` stay the source of truth; a new ordinary SQL table `links` in the existing `index.db` is **derived and disposable**, rebuilt from the `.md` on every `reindex`. The graph engine treats relations as symmetric for *querying* (an edge A→B answers "why are A and B related" from either side) while only ever writing a single directed edge on the source fact — no auto-written reverse links.

**Tech Stack:** Go 1.25, `modernc.org/sqlite` (the existing FTS + a plain `links` table in the same db), `gopkg.in/yaml.v3` (frontmatter), `github.com/natefinch/atomic` (atomic `.md` rewrites). No new dependencies.

## Global Constraints

- **Module path:** `bbrain` (local module; all imports are `bbrain/internal/...`).
- **Go version:** `go 1.25` in `go.mod`.
- **Project root:** `/home/vex/Projects/BBrain/` (the `engram/` subdir is reference only — never import from it).
- **`.md` is source of truth.** The `links` table, like the FTS table, must be 100% reconstructable from the `.md` files. No edge may live only in the index.
- **SQLite driver:** `modernc.org/sqlite`, registered driver name `"sqlite"`. The `links` table lives in the same `index.db` as `facts_fts`.
- **Atomic writes:** every `.md` rewrite goes through `atomic.WriteFile`.
- **Timestamps:** RFC3339 UTC. All time access goes through `Store.Now func() time.Time` so tests are deterministic.
- **Relation vocabulary (exact):** `relates | depends-on | conflicts-with | supersedes | scoped | compatible`.
- **`why` is mandatory** on every reasoned wikilink.
- **Wikilink target form on disk:** `[[<fact-id>]]`. The bare id is `<date>-<slug>`.
- **Commit after every task.** Run `go test ./...` green before each commit.

---

## Roadmap (this plan is #2 of a sequence)

The full 1→6 roadmap lives in the header of Plan 1 (`2026-06-22-bbrain-plan-1-core-engine.md`). Recap of the boundaries that matter here:

- **Plan 1 (done):** brain init, save fact `.md`, FTS index, search, reindex. CLI only.
- **Plan 2 (THIS PLAN):** `links:` parsing into the index, a `mem_link`-style engine op (`AddLink`), graph queries ("why is A related to B", neighbors), and FTS-based candidate/conflict surfacing (`FindCandidates`). CLI only.
- **Plan 4:** exposes these as MCP tools (`mem_link`, etc.). **Out of scope here** — Plan 2 builds the engine + CLI, exactly as Plan 1 deferred MCP.

Deferred out of this plan: the actual MCP `mem_link` tool (Plan 4), the LLM routine that *auto-populates* links during `wiki build` (Plan 3 wires the routine; this plan gives it the `AddLink` primitive to call), and any auto-written reverse edges (YAGNI).

---

## File Structure (Plan 2)

All files already exist (from Plan 1); Plan 2 modifies them. No new files.

- `internal/fact/fact.go` — **modify:** add link helpers `Relations`, `ValidRelation`, `FormatTarget`, `LinkTargetID`. (`Fact`/`Link` types unchanged.)
- `internal/fact/fact_test.go` — **modify:** tests for the new helpers.
- `internal/store/store.go` — **modify:** add `Get` (load one fact by id) and `AddLink` (write a reasoned wikilink into a fact's `.md`).
- `internal/store/store_test.go` — **modify:** `Get` + `AddLink` tests.
- `internal/index/index.go` — **modify:** add the `links` table to the schema; `IndexLinks`; extend `Clear` to also empty `links`; graph queries `Why`/`Neighbors` with `Edge`/`Neighbor` types; `SearchAny` (OR-match) for candidate discovery.
- `internal/index/index_test.go` — **modify:** link-index, graph-query, clear, and `SearchAny` tests.
- `internal/app/app.go` — **modify:** `Reindex` also indexes links; add `Link`, `Why`, `Related`, `Candidates`.
- `internal/app/app_test.go` — **modify:** link/why/related, reindex-rebuilds-edges, and candidates tests.
- `cmd/bbrain/main.go` — **modify:** add `link`, `why`, `related`, `candidates` subcommands + usage string.
- `cmd/bbrain/main_test.go` — **modify:** end-to-end link/why/related test.

---

## Task 1: `fact` package — reasoned-link helpers

**Files:**
- Modify: `internal/fact/fact.go`
- Test: `internal/fact/fact_test.go`

**Interfaces:**
- Consumes: `strings` (already imported); the existing `Link` type.
- Produces:
  - `var Relations []string` — the controlled relation vocabulary.
  - `func ValidRelation(r string) bool` — membership check against `Relations`.
  - `func FormatTarget(id string) string` — wraps a fact id as `"[[id]]"`.
  - `func LinkTargetID(target string) string` — strips `[[`/`]]` and surrounding whitespace, returning the bare id.

- [ ] **Step 1: Write the failing test**

Append to `internal/fact/fact_test.go`:

```go
func TestValidRelation(t *testing.T) {
	for _, r := range []string{"relates", "depends-on", "conflicts-with", "supersedes", "scoped", "compatible"} {
		if !ValidRelation(r) {
			t.Errorf("ValidRelation(%q) = false, want true", r)
		}
	}
	if ValidRelation("frobnicates") {
		t.Error("ValidRelation(frobnicates) = true, want false")
	}
	if ValidRelation("") {
		t.Error("ValidRelation(\"\") = true, want false")
	}
}

func TestLinkTargetRoundTrip(t *testing.T) {
	id := "2026-06-22-postgres-vs-mysql"
	if got := FormatTarget(id); got != "[[2026-06-22-postgres-vs-mysql]]" {
		t.Fatalf("FormatTarget = %q", got)
	}
	if got := LinkTargetID(FormatTarget(id)); got != id {
		t.Fatalf("LinkTargetID(FormatTarget(id)) = %q, want %q", got, id)
	}
	// Tolerates a bare slug and surrounding whitespace.
	if got := LinkTargetID("  [[session-model]] "); got != "session-model" {
		t.Fatalf("LinkTargetID(messy) = %q, want %q", got, "session-model")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/fact/`
Expected: FAIL (undefined `ValidRelation`, `FormatTarget`, `LinkTargetID`).

- [ ] **Step 3: Add the helpers to `internal/fact/fact.go`**

Append at the end of `internal/fact/fact.go` (after `NewID`):

```go
// Relations is the controlled vocabulary for reasoned wikilink relations,
// ported from engram. A link's relation must be one of these.
var Relations = []string{"relates", "depends-on", "conflicts-with", "supersedes", "scoped", "compatible"}

// ValidRelation reports whether r is one of the allowed relation types.
func ValidRelation(r string) bool {
	for _, x := range Relations {
		if x == r {
			return true
		}
	}
	return false
}

// FormatTarget wraps a fact id as an on-disk wikilink target ("[[id]]").
func FormatTarget(id string) string { return "[[" + id + "]]" }

// LinkTargetID extracts the bare fact id from a wikilink target, stripping the
// surrounding [[ ]] and any whitespace. A target that is already a bare slug is
// returned unchanged.
func LinkTargetID(target string) string {
	t := strings.TrimSpace(target)
	t = strings.TrimPrefix(t, "[[")
	t = strings.TrimSuffix(t, "]]")
	return strings.TrimSpace(t)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/fact/`
Expected: PASS (existing tests + the 2 new ones).

- [ ] **Step 5: Commit**

```bash
cd /home/vex/Projects/BBrain
git add internal/fact/
git commit -m "feat(fact): relation vocab + wikilink target helpers"
```

---

## Task 2: `store` package — `Get` + `AddLink` (write a reasoned wikilink)

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `bbrain/internal/fact` (`Fact`, `Link`, `Parse`, `Marshal`, `ValidRelation`, `FormatTarget`, `LinkTargetID`); `Store.Now`; `Store.write` (existing private helper); `github.com/natefinch/atomic` (via `write`).
- Produces:
  - `func (s *Store) Get(id string) (fact.Fact, bool, error)` — loads `<FactsDir>/<id>.md`; `ok=false` when absent.
  - `func (s *Store) AddLink(srcID, dstID, relation, why string) (fact.Fact, error)` — adds/updates a reasoned link from `srcID` to `dstID` on the source `.md` and returns the rewritten fact.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go` (reuses the existing `newTestStore` helper):

```go
func TestGetReturnsFactOrNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, ok, err := s.Get("nope"); err != nil || ok {
		t.Fatalf("Get(missing) = ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	f, _ := s.Save(SaveInput{Type: "decision", Title: "Use JWT", Body: "b",
		Project: "bbrain", Scope: "project"})
	got, ok, err := s.Get(f.ID)
	if err != nil || !ok {
		t.Fatalf("Get(existing) ok=%v err=%v", ok, err)
	}
	if got.Title != "Use JWT" {
		t.Fatalf("Get title = %q, want %q", got.Title, "Use JWT")
	}
}

func TestAddLinkWritesReasonedWikilink(t *testing.T) {
	s := newTestStore(t)
	src, _ := s.Save(SaveInput{Type: "architecture", Title: "Auth model", Body: "jwt",
		Project: "bbrain", Scope: "project"})
	// Advance time so the second save is distinct (and not deduped).
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	dst, _ := s.Save(SaveInput{Type: "decision", Title: "Session storage", Body: "redis",
		Project: "bbrain", Scope: "project"})

	updated, err := s.AddLink(src.ID, dst.ID, "depends-on", "auth model assumes the session storage")
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if len(updated.Links) != 1 {
		t.Fatalf("links = %+v, want 1", updated.Links)
	}
	l := updated.Links[0]
	if l.Target != "[["+dst.ID+"]]" || l.Relation != "depends-on" || l.Why == "" {
		t.Fatalf("link fields wrong: %+v", l)
	}
	// Persisted to disk, not just returned.
	reloaded, ok, _ := s.Get(src.ID)
	if !ok || len(reloaded.Links) != 1 || reloaded.Links[0].Relation != "depends-on" {
		t.Fatalf("link not persisted: ok=%v links=%+v", ok, reloaded.Links)
	}
}

func TestAddLinkUpsertsSameTarget(t *testing.T) {
	s := newTestStore(t)
	src, _ := s.Save(SaveInput{Type: "architecture", Title: "A", Body: "x", Project: "p", Scope: "project"})
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	dst, _ := s.Save(SaveInput{Type: "decision", Title: "B", Body: "y", Project: "p", Scope: "project"})

	if _, err := s.AddLink(src.ID, dst.ID, "relates", "first reason"); err != nil {
		t.Fatal(err)
	}
	updated, err := s.AddLink(src.ID, dst.ID, "conflicts-with", "second reason")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Links) != 1 {
		t.Fatalf("re-linking the same target must not duplicate: %+v", updated.Links)
	}
	if updated.Links[0].Relation != "conflicts-with" || updated.Links[0].Why != "second reason" {
		t.Fatalf("link not updated in place: %+v", updated.Links[0])
	}
}

func TestAddLinkValidates(t *testing.T) {
	s := newTestStore(t)
	src, _ := s.Save(SaveInput{Type: "architecture", Title: "A", Body: "x", Project: "p", Scope: "project"})
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	dst, _ := s.Save(SaveInput{Type: "decision", Title: "B", Body: "y", Project: "p", Scope: "project"})

	if _, err := s.AddLink(src.ID, dst.ID, "bogus-relation", "why"); err == nil {
		t.Fatal("AddLink should reject an invalid relation")
	}
	if _, err := s.AddLink(src.ID, dst.ID, "relates", ""); err == nil {
		t.Fatal("AddLink should require a non-empty why")
	}
	if _, err := s.AddLink(src.ID, "missing-fact", "relates", "why"); err == nil {
		t.Fatal("AddLink should reject a missing target fact")
	}
	if _, err := s.AddLink("missing-src", dst.ID, "relates", "why"); err == nil {
		t.Fatal("AddLink should reject a missing source fact")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/store/`
Expected: FAIL (undefined `Get`, `AddLink`).

- [ ] **Step 3: Add `fmt` to the imports of `internal/store/store.go`**

The import block currently is:

```go
import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/natefinch/atomic"

	"bbrain/internal/brain"
	"bbrain/internal/fact"
)
```

Change it to add `"fmt"`:

```go
import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/natefinch/atomic"

	"bbrain/internal/brain"
	"bbrain/internal/fact"
)
```

- [ ] **Step 4: Add `Get` and `AddLink` to `internal/store/store.go`**

Append after the `ListFacts` method (before the `contentHash` helpers):

```go
// Get loads a single fact by id. ok is false (with a nil error) when no .md with
// that id exists under the brain's facts dir.
func (s *Store) Get(id string) (fact.Fact, bool, error) {
	data, err := os.ReadFile(filepath.Join(s.Brain.FactsDir(), id+".md"))
	if err != nil {
		if os.IsNotExist(err) {
			return fact.Fact{}, false, nil
		}
		return fact.Fact{}, false, err
	}
	f, err := fact.Parse(string(data))
	if err != nil {
		return fact.Fact{}, false, err
	}
	return f, true, nil
}

// AddLink adds (or updates) a reasoned wikilink from srcID to dstID on the source
// fact's .md frontmatter. Both facts must exist and relation must be in the
// controlled vocabulary; why is mandatory. If a link to dstID already exists, its
// relation and why are overwritten in place (no duplicate edge). The source .md is
// rewritten atomically and its updated_at is bumped; revision_count is left
// untouched because a link edit is not a content revision.
func (s *Store) AddLink(srcID, dstID, relation, why string) (fact.Fact, error) {
	if !fact.ValidRelation(relation) {
		return fact.Fact{}, fmt.Errorf("store: invalid relation %q", relation)
	}
	if why == "" {
		return fact.Fact{}, fmt.Errorf("store: link why is required")
	}
	src, ok, err := s.Get(srcID)
	if err != nil {
		return fact.Fact{}, err
	}
	if !ok {
		return fact.Fact{}, fmt.Errorf("store: source fact %q not found", srcID)
	}
	if _, ok, err := s.Get(dstID); err != nil {
		return fact.Fact{}, err
	} else if !ok {
		return fact.Fact{}, fmt.Errorf("store: target fact %q not found", dstID)
	}

	updated := false
	for i := range src.Links {
		if fact.LinkTargetID(src.Links[i].Target) == dstID {
			src.Links[i].Relation = relation
			src.Links[i].Why = why
			updated = true
			break
		}
	}
	if !updated {
		src.Links = append(src.Links, fact.Link{
			Target:   fact.FormatTarget(dstID),
			Relation: relation,
			Why:      why,
		})
	}
	src.UpdatedAt = s.Now().UTC().Format(time.RFC3339)
	if err := s.write(src); err != nil {
		return fact.Fact{}, err
	}
	return src, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/store/`
Expected: PASS (existing tests + the 4 new ones).

- [ ] **Step 6: Commit**

```bash
cd /home/vex/Projects/BBrain
git add internal/store/
git commit -m "feat(store): Get by id + AddLink writes reasoned wikilink into .md"
```

---

## Task 3: `index` package — `links` table, graph queries, OR-search

**Files:**
- Modify: `internal/index/index.go`
- Test: `internal/index/index_test.go`

**Interfaces:**
- Consumes: `bbrain/internal/fact` (`Fact`, `Link`, `LinkTargetID`); the existing `Index` (`*sql.DB`), `Open`, `Search`.
- Produces:
  - `func (ix *Index) IndexLinks(f fact.Fact) error` — replaces all edges originating from `f.ID` with `f.Links` (targets stored as bare ids).
  - `func (ix *Index) Clear() error` — now empties **both** `facts_fts` and `links` (signature unchanged).
  - `type Edge struct { SrcID, DstID, Relation, Why string }`
  - `type Neighbor struct { FactID, Relation, Why, Direction string }` — `Direction` is `"out"` (this fact links to `FactID`) or `"in"` (`FactID` links to this fact).
  - `func (ix *Index) Why(aID, bID string) ([]Edge, error)` — edges directly connecting a and b in **either** direction.
  - `func (ix *Index) Neighbors(id string) ([]Neighbor, error)` — out- and in-edges touching id.
  - `func (ix *Index) SearchAny(query string, limit int) ([]Result, error)` — like `Search` but OR-matches terms.

- [ ] **Step 1: Write the failing test**

Append to `internal/index/index_test.go` (reuses the existing `openMem`, `sampleFact`, and `must` helpers):

```go
func TestIndexLinksAndWhy(t *testing.T) {
	ix := openMem(t)
	f := sampleFact("a", "Auth model", "body", "architecture", "p")
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(f))

	edges, err := ix.Why("a", "b")
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	if len(edges) != 1 || edges[0].SrcID != "a" || edges[0].DstID != "b" ||
		edges[0].Relation != "depends-on" || edges[0].Why != "needs b" {
		t.Fatalf("Why(a,b) = %+v", edges)
	}
	// The reverse query returns the same edge (relation is symmetric for querying).
	rev, err := ix.Why("b", "a")
	if err != nil {
		t.Fatalf("Why reverse: %v", err)
	}
	if len(rev) != 1 {
		t.Fatalf("Why(b,a) = %+v, want 1", rev)
	}
}

func TestNeighborsReturnsInAndOutEdges(t *testing.T) {
	ix := openMem(t)
	fa := sampleFact("a", "A", "x", "decision", "p")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "relates", Why: "r"}}
	must(t, ix.IndexLinks(fa))
	fc := sampleFact("c", "C", "z", "decision", "p")
	fc.Links = []fact.Link{{Target: "[[a]]", Relation: "supersedes", Why: "s"}}
	must(t, ix.IndexLinks(fc))

	ns, err := ix.Neighbors("a")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 2 {
		t.Fatalf("Neighbors(a) = %+v, want 2 (out to b, in from c)", ns)
	}
	var dirs = map[string]string{}
	for _, n := range ns {
		dirs[n.FactID] = n.Direction
	}
	if dirs["b"] != "out" || dirs["c"] != "in" {
		t.Fatalf("directions wrong: %+v", dirs)
	}
}

func TestIndexLinksIsUpsert(t *testing.T) {
	ix := openMem(t)
	f := sampleFact("a", "A", "x", "decision", "p")
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "relates", Why: "first"}}
	must(t, ix.IndexLinks(f))
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "conflicts-with", Why: "second"}}
	must(t, ix.IndexLinks(f))

	edges, _ := ix.Why("a", "b")
	if len(edges) != 1 || edges[0].Relation != "conflicts-with" || edges[0].Why != "second" {
		t.Fatalf("re-indexing must replace edges: %+v", edges)
	}
}

func TestClearAlsoEmptiesLinks(t *testing.T) {
	ix := openMem(t)
	f := sampleFact("a", "A", "x", "decision", "p")
	f.Links = []fact.Link{{Target: "[[b]]", Relation: "relates", Why: "r"}}
	must(t, ix.IndexLinks(f))
	must(t, ix.Clear())
	if edges, _ := ix.Why("a", "b"); len(edges) != 0 {
		t.Fatalf("after Clear, Why = %+v, want empty", edges)
	}
}

func TestSearchAnyMatchesAnyTerm(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for auth", "stateless tokens", "decision", "p"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Postgres choice", "relational database", "decision", "p"), "/x/f2.md"))

	// AND search (Search) for two terms in different facts matches nothing.
	if res, _ := ix.Search("jwt database", 10); len(res) != 0 {
		t.Fatalf("Search(AND) = %+v, want 0", res)
	}
	// OR search (SearchAny) matches both.
	res, err := ix.SearchAny("jwt database", 10)
	if err != nil {
		t.Fatalf("SearchAny: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("SearchAny = %+v, want 2", res)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/index/`
Expected: FAIL (undefined `IndexLinks`, `Why`, `Neighbors`, `SearchAny`, `Edge`, `Neighbor`).

- [ ] **Step 3: Add the `links` schema and exec it in `Open`**

In `internal/index/index.go`, after the existing `const schema = ...` block, add:

```go
// linksSchema is a plain (non-FTS) table mirroring each fact's reasoned wikilinks.
// Like facts_fts it is derived from the .md and rebuilt on reindex. Targets are
// stored as bare fact ids (the [[ ]] wrapping is stripped on the way in).
const linksSchema = `
CREATE TABLE IF NOT EXISTS links (
	src_id   TEXT NOT NULL,
	dst_id   TEXT NOT NULL,
	relation TEXT NOT NULL,
	why      TEXT NOT NULL,
	PRIMARY KEY (src_id, dst_id, relation)
);`
```

Then replace the body of `Open` so it execs both statements. Change:

```go
func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db}, nil
}
```

to:

```go
func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{schema, linksSchema} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Index{db: db}, nil
}
```

- [ ] **Step 4: Extend `Clear` to also empty `links`**

Replace the existing `Clear`:

```go
func (ix *Index) Clear() error {
	_, err := ix.db.Exec(`DELETE FROM facts_fts`)
	return err
}
```

with:

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

- [ ] **Step 5: Add `IndexLinks` and the graph queries**

Append after `IndexFact` (or anywhere at package scope) in `internal/index/index.go`:

```go
// IndexLinks mirrors a fact's reasoned wikilinks into the links table: it removes
// any existing edges originating from f.ID, then inserts one row per link. Targets
// are normalized to bare fact ids; empty targets are skipped. Dangling edges (a
// dst_id with no indexed fact) are allowed — they still answer graph queries.
func (ix *Index) IndexLinks(f fact.Fact) error {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM links WHERE src_id = ?`, f.ID); err != nil {
		tx.Rollback()
		return err
	}
	for _, l := range f.Links {
		dst := fact.LinkTargetID(l.Target)
		if dst == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO links (src_id, dst_id, relation, why) VALUES (?, ?, ?, ?)`,
			f.ID, dst, l.Relation, l.Why,
		); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Edge is one reasoned graph edge.
type Edge struct {
	SrcID    string
	DstID    string
	Relation string
	Why      string
}

// Neighbor is a fact connected to a given fact, with the relation, its why, and
// the direction relative to the queried fact ("out": this fact links to FactID;
// "in": FactID links to this fact).
type Neighbor struct {
	FactID    string
	Relation  string
	Why       string
	Direction string
}

// Why returns the reasoned edges directly connecting a and b, in either direction
// — this answers "why is A related to B". Empty when there is no direct link.
func (ix *Index) Why(aID, bID string) ([]Edge, error) {
	rows, err := ix.db.Query(
		`SELECT src_id, dst_id, relation, why FROM links
		 WHERE (src_id = ? AND dst_id = ?) OR (src_id = ? AND dst_id = ?)
		 ORDER BY src_id, dst_id, relation`,
		aID, bID, bID, aID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.SrcID, &e.DstID, &e.Relation, &e.Why); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Neighbors returns every fact linked to or from id, with direction. Out-edges
// (id -> X) come first, then in-edges (Y -> id), each ordered by the other fact's
// id for deterministic output.
func (ix *Index) Neighbors(id string) ([]Neighbor, error) {
	rows, err := ix.db.Query(
		`SELECT dst_id, relation, why, 'out' AS dir FROM links WHERE src_id = ?
		 UNION ALL
		 SELECT src_id, relation, why, 'in' AS dir FROM links WHERE dst_id = ?
		 ORDER BY 4, 1`,
		id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Neighbor
	for rows.Next() {
		var n Neighbor
		if err := rows.Scan(&n.FactID, &n.Relation, &n.Why, &n.Direction); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
```

- [ ] **Step 6: Refactor `Search` to share a core and add `SearchAny`**

Replace the existing `Search` method:

```go
func (ix *Index) Search(query string, limit int) ([]Result, error) {
	match := buildMatch(query)
	if match == "" {
		return nil, nil
	}
	rows, err := ix.db.Query(
		`SELECT fact_id, title, type, project, path
		 FROM facts_fts
		 WHERE facts_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.FactID, &r.Title, &r.Type, &r.Project, &r.Path); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

with this trio (the shared `search` core plus the two public entry points):

```go
// Search runs an FTS5 MATCH over title/body/tags/topic_key (all query terms
// AND-ed), ranked by BM25.
func (ix *Index) Search(query string, limit int) ([]Result, error) {
	return ix.search(buildMatch(query), limit)
}

// SearchAny is like Search but matches facts containing ANY of the query terms
// (OR semantics), ranked by BM25. It powers candidate/correlation discovery, where
// a strict AND would miss facts that only partially overlap.
func (ix *Index) SearchAny(query string, limit int) ([]Result, error) {
	return ix.search(buildMatchAny(query), limit)
}

func (ix *Index) search(match string, limit int) ([]Result, error) {
	if match == "" {
		return nil, nil
	}
	rows, err := ix.db.Query(
		`SELECT fact_id, title, type, project, path
		 FROM facts_fts
		 WHERE facts_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.FactID, &r.Title, &r.Type, &r.Project, &r.Path); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

Then add `buildMatchAny` next to the existing `buildMatch`:

```go
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

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/index/`
Expected: PASS (existing tests + the 5 new ones).

- [ ] **Step 8: Commit**

```bash
cd /home/vex/Projects/BBrain
git add internal/index/
git commit -m "feat(index): derived links table, Why/Neighbors graph queries, SearchAny"
```

---

## Task 4: `app` package — wire linking, graph queries, and candidates

**Files:**
- Modify: `internal/app/app.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: `bbrain/internal/store` (`Store.AddLink`, `Store.Get`), `bbrain/internal/index` (`IndexLinks`, `Why`, `Neighbors`, `SearchAny`, `Edge`, `Neighbor`, `Result`), `bbrain/internal/fact` (`LinkTargetID`), the existing `App`, `ensureIndexDir`, `Search`.
- Produces:
  - `func (a *App) Link(srcID, dstID, relation, why string) (fact.Fact, error)` — `Store.AddLink` then re-index that fact's edges.
  - `func (a *App) Why(aID, bID string) ([]index.Edge, error)`
  - `func (a *App) Related(id string) ([]index.Neighbor, error)`
  - `func (a *App) Candidates(id string, limit int) ([]index.Result, error)` — FTS-OR neighbors of a fact, excluding itself and facts it already links to.
  - `Reindex` now also calls `IndexLinks` for every fact (signature unchanged).

- [ ] **Step 1: Write the failing test**

Append to `internal/app/app_test.go`. Add the `index` import to the test file's import block (it currently imports only `testing` and `bbrain/internal/store`):

```go
import (
	"testing"

	"bbrain/internal/index"
	"bbrain/internal/store"
)
```

Then append the tests and helpers:

```go
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func containsID(rs []index.Result, id string) bool {
	for _, r := range rs {
		if r.FactID == id {
			return true
		}
	}
	return false
}

func TestLinkThenWhyAndRelated(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f1, err := a.Save(store.SaveInput{Type: "architecture", Title: "Auth model", Body: "jwt",
		Project: "bbrain", Scope: "project"})
	must(t, err)
	f2, err := a.Save(store.SaveInput{Type: "decision", Title: "Session storage", Body: "redis",
		Project: "bbrain", Scope: "project"})
	must(t, err)

	if _, err := a.Link(f1.ID, f2.ID, "depends-on", "auth model assumes the session storage"); err != nil {
		t.Fatalf("Link: %v", err)
	}

	edges, err := a.Why(f1.ID, f2.ID)
	must(t, err)
	if len(edges) != 1 || edges[0].Relation != "depends-on" || edges[0].Why == "" {
		t.Fatalf("Why = %+v", edges)
	}
	// Symmetric for querying.
	rev, err := a.Why(f2.ID, f1.ID)
	must(t, err)
	if len(rev) != 1 {
		t.Fatalf("Why is not symmetric: %+v", rev)
	}

	out, err := a.Related(f1.ID)
	must(t, err)
	if len(out) != 1 || out[0].FactID != f2.ID || out[0].Direction != "out" {
		t.Fatalf("Related(f1) = %+v", out)
	}
	in, err := a.Related(f2.ID)
	must(t, err)
	if len(in) != 1 || in[0].FactID != f1.ID || in[0].Direction != "in" {
		t.Fatalf("Related(f2) = %+v", in)
	}
}

func TestReindexRebuildsEdges(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f1, err := a.Save(store.SaveInput{Type: "architecture", Title: "A", Body: "x", Project: "p", Scope: "project"})
	must(t, err)
	f2, err := a.Save(store.SaveInput{Type: "architecture", Title: "B", Body: "y", Project: "p", Scope: "project"})
	must(t, err)
	if _, err := a.Link(f1.ID, f2.ID, "relates", "linked"); err != nil {
		t.Fatal(err)
	}

	// A fresh App over the same root rebuilds the edge table from the .md alone.
	a2 := New(a.Brain.Root)
	if _, err := a2.Reindex(); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	edges, err := a2.Why(f1.ID, f2.ID)
	must(t, err)
	if len(edges) != 1 {
		t.Fatalf("edges after reindex = %+v, want 1 (links must rebuild from .md)", edges)
	}
}

func TestCandidatesExcludesSelfAndLinked(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f1, err := a.Save(store.SaveInput{Type: "decision", Title: "Use JWT for auth", Body: "stateless tokens",
		Project: "bbrain", Scope: "project"})
	must(t, err)
	f2, err := a.Save(store.SaveInput{Type: "decision", Title: "Auth token rotation", Body: "rotate jwt tokens",
		Project: "bbrain", Scope: "project"})
	must(t, err)

	cands, err := a.Candidates(f1.ID, 10)
	must(t, err)
	if !containsID(cands, f2.ID) {
		t.Fatalf("candidates should include the similar f2: %+v", cands)
	}
	if containsID(cands, f1.ID) {
		t.Fatalf("candidates must exclude the fact itself: %+v", cands)
	}

	if _, err := a.Link(f1.ID, f2.ID, "relates", "both about auth"); err != nil {
		t.Fatal(err)
	}
	cands2, err := a.Candidates(f1.ID, 10)
	must(t, err)
	if containsID(cands2, f2.ID) {
		t.Fatalf("candidates must exclude an already-linked fact: %+v", cands2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/app/`
Expected: FAIL (undefined `Link`, `Why`, `Related`, `Candidates`).

- [ ] **Step 3: Add `fmt` and `strings` to the imports of `internal/app/app.go`**

The import block currently is:

```go
import (
	"os"
	"path/filepath"

	"bbrain/internal/brain"
	"bbrain/internal/fact"
	"bbrain/internal/index"
	"bbrain/internal/store"
)
```

Change it to:

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bbrain/internal/brain"
	"bbrain/internal/fact"
	"bbrain/internal/index"
	"bbrain/internal/store"
)
```

- [ ] **Step 4: Make `Reindex` also build edges**

In `internal/app/app.go`, in `Reindex`, replace the indexing loop:

```go
	for _, f := range facts {
		if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
			return 0, err
		}
	}
```

with:

```go
	for _, f := range facts {
		if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
			return 0, err
		}
		if err := ix.IndexLinks(f); err != nil {
			return 0, err
		}
	}
```

- [ ] **Step 5: Add `Link`, `Why`, `Related`, `Candidates`**

Append at the end of `internal/app/app.go`:

```go
// Link adds (or updates) a reasoned wikilink from srcID to dstID on the source
// fact's .md, then incrementally re-indexes that fact's edges.
func (a *App) Link(srcID, dstID, relation, why string) (fact.Fact, error) {
	f, err := a.Store.AddLink(srcID, dstID, relation, why)
	if err != nil {
		return fact.Fact{}, err
	}
	if err := a.ensureIndexDir(); err != nil {
		return fact.Fact{}, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return fact.Fact{}, err
	}
	defer ix.Close()
	if err := ix.IndexLinks(f); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

// Why returns the reasoned edges directly connecting two facts (either direction).
func (a *App) Why(aID, bID string) ([]index.Edge, error) {
	if err := a.ensureIndexDir(); err != nil {
		return nil, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	return ix.Why(aID, bID)
}

// Related returns every fact linked to or from id, with direction.
func (a *App) Related(id string) ([]index.Neighbor, error) {
	if err := a.ensureIndexDir(); err != nil {
		return nil, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	return ix.Neighbors(id)
}

// Candidates surfaces facts lexically similar to the given fact but not yet linked
// to it — the raw material for spotting correlations and conflicts. It OR-matches
// the fact's title and tags against the FTS index, then drops the fact itself and
// anything it already links to. Returns at most limit results.
func (a *App) Candidates(id string, limit int) ([]index.Result, error) {
	f, ok, err := a.Store.Get(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("candidates: fact %q not found", id)
	}
	linked := map[string]bool{id: true}
	for _, l := range f.Links {
		linked[fact.LinkTargetID(l.Target)] = true
	}
	terms := f.Title
	if len(f.Tags) > 0 {
		terms += " " + strings.Join(f.Tags, " ")
	}

	if err := a.ensureIndexDir(); err != nil {
		return nil, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	// Over-fetch so that, after dropping self + already-linked, we can still return
	// up to limit results.
	res, err := ix.SearchAny(terms, limit+len(linked))
	if err != nil {
		return nil, err
	}
	out := make([]index.Result, 0, limit)
	for _, r := range res {
		if linked[r.FactID] {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
```

- [ ] **Step 6: Run app tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/app/`
Expected: PASS (existing tests + the 3 new ones).

- [ ] **Step 7: Commit**

```bash
cd /home/vex/Projects/BBrain
git add internal/app/
git commit -m "feat(app): Link/Why/Related/Candidates + reindex rebuilds edges"
```

---

## Task 5: CLI wiring — `link`, `why`, `related`, `candidates`

**Files:**
- Modify: `cmd/bbrain/main.go`
- Test: `cmd/bbrain/main_test.go`

**Interfaces:**
- Consumes: `bbrain/internal/app` (`App.Link`, `App.Why`, `App.Related`, `App.Candidates`), the existing `run`, `brainRoot`, `flag`, `fmt`, `io`.
- Produces: four new subcommands dispatched from `run`:
  - `bbrain link --from <id> --to <id> [--relation relates] --why <text>`
  - `bbrain why <factA> <factB>`
  - `bbrain related <factID>`
  - `bbrain candidates [--limit 10] <factID>`

- [ ] **Step 1: Write the failing end-to-end test**

Append to `cmd/bbrain/main_test.go` (`bytes` and `strings` are already imported from Plan 1):

```go
func TestEndToEndLinkWhyRelated(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	var out, errOut bytes.Buffer

	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init failed: %s", errOut.String())
	}

	// save prints "saved <id>"; capture the id back.
	saved := func(title, typ, body string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		code := run([]string{"save", "--title", title, "--project", "bbrain",
			"--type", typ, "--body", body}, &out, &errOut)
		if code != 0 {
			t.Fatalf("save %q failed: %s", title, errOut.String())
		}
		return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out.String()), "saved "))
	}

	id1 := saved("Auth model", "architecture", "jwt")
	id2 := saved("Session storage", "decision", "redis")

	out.Reset()
	errOut.Reset()
	if code := run([]string{"link", "--from", id1, "--to", id2,
		"--relation", "depends-on", "--why", "auth assumes session storage"}, &out, &errOut); code != 0 {
		t.Fatalf("link failed: %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"why", id1, id2}, &out, &errOut); code != 0 {
		t.Fatalf("why failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "depends-on") ||
		!strings.Contains(out.String(), "auth assumes session storage") {
		t.Fatalf("why output = %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"related", id1}, &out, &errOut); code != 0 {
		t.Fatalf("related failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), id2) {
		t.Fatalf("related output = %q, want it to mention %s", out.String(), id2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./cmd/...`
Expected: FAIL (the `link`/`why`/`related` commands are unknown → non-zero exit).

- [ ] **Step 3: Add the subcommand cases and usage line in `cmd/bbrain/main.go`**

In `run`, update the usage line:

```go
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex> [args]")
		return 2
	}
```

to:

```go
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex|link|why|related|candidates> [args]")
		return 2
	}
```

Then add four cases to the `switch args[0]` (before the `default`):

```go
	case "link":
		return cmdLink(args[1:], stdout, stderr)
	case "why":
		return cmdWhy(args[1:], stdout, stderr)
	case "related":
		return cmdRelated(args[1:], stdout, stderr)
	case "candidates":
		return cmdCandidates(args[1:], stdout, stderr)
```

- [ ] **Step 4: Add the four command functions**

Append at the end of `cmd/bbrain/main.go`:

```go
func cmdLink(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "source fact id (required)")
	to := fs.String("to", "", "target fact id (required)")
	rel := fs.String("relation", "relates", "relation type (relates|depends-on|conflicts-with|supersedes|scoped|compatible)")
	why := fs.String("why", "", "why the two facts are related (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || *to == "" || *why == "" {
		fmt.Fprintln(stderr, "link: --from, --to and --why are required")
		return 2
	}
	a := app.New(brainRoot())
	f, err := a.Link(*from, *to, *rel, *why)
	if err != nil {
		fmt.Fprintf(stderr, "link: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "linked %s -[%s]-> %s\n", f.ID, *rel, *to)
	return 0
}

func cmdWhy(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "why: usage: bbrain why <factA> <factB>")
		return 2
	}
	a := app.New(brainRoot())
	edges, err := a.Why(args[0], args[1])
	if err != nil {
		fmt.Fprintf(stderr, "why: %v\n", err)
		return 1
	}
	if len(edges) == 0 {
		fmt.Fprintf(stdout, "no direct link between %s and %s\n", args[0], args[1])
		return 0
	}
	for _, e := range edges {
		fmt.Fprintf(stdout, "%s -[%s]-> %s: %s\n", e.SrcID, e.Relation, e.DstID, e.Why)
	}
	return 0
}

func cmdRelated(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "related: usage: bbrain related <factID>")
		return 2
	}
	a := app.New(brainRoot())
	ns, err := a.Related(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "related: %v\n", err)
		return 1
	}
	for _, n := range ns {
		arrow := "->"
		if n.Direction == "in" {
			arrow = "<-"
		}
		fmt.Fprintf(stdout, "%s %s [%s] %s\n", arrow, n.FactID, n.Relation, n.Why)
	}
	return 0
}

func cmdCandidates(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("candidates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 10, "max candidates")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "candidates: usage: bbrain candidates [--limit N] <factID>")
		return 2
	}
	a := app.New(brainRoot())
	res, err := a.Candidates(fs.Arg(0), *limit)
	if err != nil {
		fmt.Fprintf(stderr, "candidates: %v\n", err)
		return 1
	}
	for _, r := range res {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", r.FactID, r.Type, r.Title)
	}
	return 0
}
```

- [ ] **Step 5: Run the full suite**

Run: `cd /home/vex/Projects/BBrain && go test ./...`
Expected: PASS (all packages).

- [ ] **Step 6: Manual smoke test**

```bash
cd /home/vex/Projects/BBrain
go build ./cmd/bbrain
rm -rf /tmp/bbrain-smoke2
BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain init
A=$(BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain save --title "Auth model" --project demo --type architecture --body "jwt" | sed 's/^saved //')
B=$(BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain save --title "Session storage" --project demo --type decision --body "redis" | sed 's/^saved //')
BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain link --from "$A" --to "$B" --relation depends-on --why "auth assumes the session storage"
BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain why "$A" "$B"
BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain related "$A"
BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain candidates "$A"
cat /tmp/bbrain-smoke2/raws/facts/"$A".md            # the link lives in the .md frontmatter
rm -rf /tmp/bbrain-smoke2/.bbrain && BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain reindex  # edges rebuild from .md
BBRAIN_HOME=/tmp/bbrain-smoke2 ./bbrain why "$A" "$B"  # still answers after the index was thrown away
```
Expected: `why` prints the `depends-on` edge with its reason; `related` shows `$B` as an out-edge; the `.md` contains a `links:` entry with `target`/`relation`/`why`; after deleting `.bbrain/` and reindexing, `why` still returns the edge (proving the graph is derived from the `.md`).

- [ ] **Step 7: Commit**

```bash
cd /home/vex/Projects/BBrain
git add cmd/bbrain/
git commit -m "feat(cli): link/why/related/candidates commands with e2e test"
```

---

## Self-Review

**1. Spec coverage (Plan 2 scope — "Reasoned wikilink graph"):**
- `links:` parsing into the index → Task 3 `IndexLinks` + Task 4 `Reindex` rebuilds the `links` table from every `.md`. ✓
- Reasoned wikilink = `target` + `relation` + `why` (why **mandatory**, relation vocabulary) → Task 1 vocab/helpers + Task 2 `AddLink` enforces both. ✓
- `mem_link`-style operation (create/edit a reasoned link between two facts) → Task 2 `Store.AddLink` (upsert by target, both facts must exist) + Task 4 `App.Link` + Task 5 `bbrain link`. MCP exposure is Plan 4 (documented in Roadmap). ✓
- Graph query "why is A related to B" → Task 3 `Why` (either direction) + Task 4/5 `why`. ✓
- Neighbors / backlinks → Task 3 `Neighbors` (in + out) + Task 4/5 `related`. ✓
- Conflict surfacing / `FindCandidates` by FTS → Task 3 `SearchAny` (OR-match) + Task 4 `Candidates` (excludes self + already-linked) + Task 5 `candidates`. ✓
- `.md` is source of truth; index derived & disposable → `links` lives in the same disposable `index.db`, `Clear` empties it, `Reindex` rebuilds it; Task 4 `TestReindexRebuildsEdges` and the Task 5 smoke test (delete `.bbrain/`, reindex, query) prove it. ✓
- No new dependencies; single binary preserved → only `modernc.org/sqlite` (already present) for the plain `links` table. ✓
- Out of scope and tracked: MCP `mem_link` tool (Plan 4), LLM auto-population of links during `wiki build` (Plan 3 — `AddLink` is the primitive it will call), auto-written reverse edges (YAGNI). ✓

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code; every test step shows the command and expected result. ✓

**3. Type consistency:** `AddLink(srcID, dstID, relation, why string) (fact.Fact, error)` is used identically in store (Task 2), app (Task 4), and CLI (Task 5). `index.Edge{SrcID,DstID,Relation,Why}` and `index.Neighbor{FactID,Relation,Why,Direction}` match between Task 3 (definition + queries), Task 4 (`Why`/`Related` return types), and Task 5 (`cmdWhy`/`cmdRelated` field access). `IndexLinks(f fact.Fact)`, `SearchAny(query string, limit int) ([]Result, error)`, `Why(aID,bID)`, `Neighbors(id)` are called with the same signatures in Task 4 as defined in Task 3. `fact.ValidRelation`/`FormatTarget`/`LinkTargetID` (Task 1) are consumed unchanged by store (Task 2) and app (Task 4). `Direction` values `"out"`/`"in"` produced in Task 3 are exactly what Task 4 tests and Task 5's `cmdRelated` branch on. ✓
