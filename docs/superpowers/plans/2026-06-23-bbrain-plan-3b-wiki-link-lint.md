# BBrain — Plan 3b: `wiki link` (LLM-assisted graph population) + `wiki lint` (consistency checks) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver `bbrain wiki link` (autonomously grow the reasoned **fact** graph: for each fact, read its FTS `Candidates`, ask the LLM which are related and how, validate, and write the links) and `bbrain wiki lint` (deterministic consistency checks over facts + the derived `wiki/` layer, with `--fix` for the mechanically-safe issues).

**Architecture:** Same layering as Plans 1–3 (`fact` → `wiki`/`llm` → `app` → `cmd`). `internal/wiki` **decides** (LLM prompt assembly, JSON parse, structural validation — pure and fake-runner-testable) and never imports `store`/`index`. `internal/app` **fetches** data (`ListFacts`, `Candidates`, `Store.Get`) and performs **writes** (`a.Link` → `store.AddLink`, the new `store.RemoveLink`, `RegenerateIndex`), so `--dry-run` is just "app skips the write." Reuses the `internal/llm` runner from Plan 3.

**Tech Stack:** Go 1.25, stdlib (`context`, `encoding/json`, `regexp`, `time`, `strings`, `flag`), reusing `internal/llm`, `internal/wiki` (Plan 3) and `internal/store`/`internal/index`/`internal/fact` (Plans 1–2). No new dependencies.

## Global Constraints

- **Module path:** `bbrain` (all imports `bbrain/internal/...`). **Go:** `go 1.25`. **Root:** `/home/vex/Projects/BBrain/` (`engram/` is reference only — never import it).
- **`.md` is source of truth.** `wiki link` writes links into **fact** `.md` files via `store.AddLink` (which bumps `updated_at`, leaves `revision_count` untouched, and dedups by target). `wiki lint --fix` mutates only via existing/tested write paths plus the new `store.RemoveLink`, and regenerates the derived `wiki/index.md`.
- **BBrain orchestrates; the LLM is a pure text→JSON function.** Same Plan 3 contract (prompt on stdin, one JSON object on stdout, via `$BBRAIN_AGENT_CLI`). `wiki link` requires it (unset → error mentioning `BBRAIN_AGENT_CLI`, exit 1). **`wiki lint` does NOT use the LLM** and runs with `BBRAIN_AGENT_CLI` unset.
- **Clean layering:** `internal/wiki` imports only `fact`, `llm`, stdlib. It never imports `store`/`index`. All data access and all writes live in `internal/app`.
- **Relation vocabulary is fixed (NOT extensible):** `fact.Relations` = `relates, depends-on, conflicts-with, supersedes, scoped, compatible`; validate with `fact.ValidRelation`.
- **Single directional edge.** `wiki link` writes one `src → dst` edge per proposal; never the reciprocal. Plan 2's `Why`/`Neighbors` already resolve reverse lookups.
- **Idempotency.** `a.Candidates` already excludes the fact itself and anything it already links to, so re-runs never re-propose existing edges. `app.WikiLink` additionally skips a proposal whose `dst` is already linked from the source (belt-and-suspenders), counting it as `Skipped`.
- **Validation before writing; abort the whole run on any structural failure** (no partial writes), mirroring `wiki build`.
- **`wiki lint` is whole-brain** (no `--project`/`--scope` filter): filtering the fact set would make cross-project links falsely look dangling. Lint judges existence against ALL facts.
- **Timestamps:** RFC3339 UTC via the injectable clock (`Store.Now`).
- **Tests assume a POSIX `/bin/sh`** for the runner/e2e tests (Linux dev platform).
- **Commit after every task.** `go test ./...` green and `go vet ./...` clean before each commit.

Design spec: `docs/superpowers/specs/2026-06-23-bbrain-wiki-link-lint-design.md`.
Carryover follow-ups from the Plan 3 review: `docs/superpowers/plans/carryover-from-plan-3-review.md`.

---

## Verified existing interfaces (Plans 1–3, do not re-implement)

- `fact.Fact{ ID, Type, Scope, Project, TopicKey string; Tags []string; Links []fact.Link; CreatedAt, UpdatedAt string; RevisionCount int; Title, Body string }`.
- `fact.Link{ Target, Relation, Why string }` (Target is the `[[id]]` form).
- `fact.Relations []string`, `fact.ValidRelation(r string) bool`, `fact.LinkTargetID(target string) string`, `fact.FormatTarget(id string) string`.
- `store.Get(id string) (fact.Fact, bool, error)`, `store.AddLink(srcID, dstID, relation, why string) (fact.Fact, error)` (dedups by target; bumps `updated_at`), `Store.Now func() time.Time`, `Store.write` (unexported).
- `index.Result{ FactID, Title, Type, Project, Path string }`.
- `app.New(root) *App`; `App{ Store *store.Store; Brain brain.Brain; Runner llm.Runner }`; `a.Candidates(id string, limit int) ([]index.Result, error)` (already drops self + already-linked); `a.Link(srcID, dstID, relation, why string) (fact.Fact, error)` (AddLink + re-index edges); `a.Store.ListFacts()`; `a.Brain.WikiDir()`, `a.Brain.IndexPath()`; `a.ensureIndexDir()`.
- `wiki.DefaultCategories []string`, `wiki.ErrInvalidJSON error`, `wiki.PageMeta{ Title, Category string; Sources []string; GeneratedAt string }`, `wiki.ParsePageMeta(content string) (PageMeta, error)`, `wiki.readPages(wikiDir string) ([]pageOnDisk, error)` (unexported; `pageOnDisk{ RelPath, Content string }`), `wiki.RegenerateIndex(wikiDir string) error`, `wiki.AppendLog(wikiDir, entry string) error`.
- `cmd/bbrain/main.go` has `run`, `brainRoot()`, and `cmdWiki` dispatching `case "build"`. `cmd/bbrain/main_test.go` imports `bytes, os, path/filepath, strings, testing`. `internal/app/app_test.go` imports `context, strings, testing, bbrain/internal/index, bbrain/internal/store` and defines `must(t, err)`.

---

## File Structure (Plan 3b)

- `internal/wiki/link.go` — **create:** link types (`Candidate`, `ProposedLink`, `FactProposals`, `Edge`, `LinkResult`, `LinkOptions`), `BuildLinkPrompt`, `ParseLinkResponse`, `ValidateProposals`, `Link` (per-fact LLM loop). Pure; imports `context`, `encoding/json`, `fmt`, `strings`, `bbrain/internal/fact`, `bbrain/internal/llm`.
- `internal/wiki/link_test.go` — **create:** prompt/parse/validate/loop tests with a fake runner.
- `internal/wiki/lint.go` — **create:** `Issue`, `LintResult`, `scanTargets`, `Lint`. Pure; imports `fmt`, `regexp`, `strings`, `time`, `bbrain/internal/fact`; reuses `readPages`/`ParsePageMeta` from `wiki.go`.
- `internal/wiki/lint_test.go` — **create:** check-matrix + fixability tests over fixture trees.
- `internal/store/store.go` — **modify:** add `RemoveLink`.
- `internal/store/store_test.go` — **modify:** `RemoveLink` tests.
- `internal/app/app.go` — **modify:** add `snippet` helper, `WikiLinkOptions`/`WikiLink`, `WikiLintOptions`/`WikiLint`, `RemoveLink`.
- `internal/app/app_test.go` — **modify:** `WikiLink` + `WikiLint` wiring tests with a per-prompt fake runner.
- `cmd/bbrain/main.go` — **modify:** `cmdWiki` dispatch (`link`, `lint`) + `cmdWikiLink` + `cmdWikiLint` + usage line.
- `cmd/bbrain/main_test.go` — **modify:** e2e `wiki link` (fake agent) + `wiki link` unconfigured + `wiki lint --fix` fixture-tree tests.

---

## Task 1: `internal/wiki/link.go` — link prompt, parse, validation, per-fact loop (pure core)

**Files:**
- Create: `internal/wiki/link.go`
- Test: `internal/wiki/link_test.go`

**Interfaces:**
- Consumes: `bbrain/internal/fact` (`Fact`, `Relations`, `ValidRelation`), `bbrain/internal/llm` (`Runner`); stdlib `context`, `encoding/json`, `fmt`, `strings`. Reuses `wiki.ErrInvalidJSON` (already defined in `wiki.go`).
- Produces:
  - `type Candidate struct { ID, Title, Type, Project, Snippet string }`
  - `type ProposedLink struct { Dst, Relation, Why string }` (JSON-tagged)
  - `type FactProposals struct { Src string; Links []ProposedLink }`
  - `type Edge struct { Src, Dst, Relation, Why string }`
  - `type LinkResult struct { Written []Edge; Skipped int; DryRun bool }`
  - `type LinkOptions struct { Facts []fact.Fact; Candidates map[string][]Candidate; Runner llm.Runner }`
  - `func BuildLinkPrompt(src fact.Fact, candidates []Candidate, relations []string) string`
  - `func ParseLinkResponse(stdout string) ([]ProposedLink, error)`
  - `func ValidateProposals(src fact.Fact, props []ProposedLink, candidateIDs map[string]bool) error`
  - `func Link(ctx context.Context, opts LinkOptions) ([]FactProposals, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/wiki/link_test.go`:

```go
package wiki

import (
	"context"
	"strings"
	"testing"

	"bbrain/internal/fact"
)

type linkFakeRunner struct {
	srcID, dstID string
	calls        int
}

// Run emits a link proposal only for the source fact's prompt; every other
// fact's prompt (where it appears as a candidate, not the source) yields none.
func (f *linkFakeRunner) Run(ctx context.Context, prompt string) (string, error) {
	f.calls++
	if strings.Contains(prompt, "## Source fact\n### "+f.srcID+"\n") {
		return `{"links":[{"dst":"` + f.dstID + `","relation":"relates","why":"both about jwt"}]}`, nil
	}
	return `{"links":[]}`, nil
}

func TestBuildLinkPromptContainsSourceCandidatesAndSchema(t *testing.T) {
	src := fact.Fact{ID: "f-src", Title: "JWT access", Type: "decision", Project: "shopapp", Body: "access token body"}
	cands := []Candidate{{ID: "f-cand", Title: "JWT refresh", Type: "decision", Project: "shopapp", Snippet: "refresh token snippet"}}
	p := BuildLinkPrompt(src, cands, fact.Relations)
	for _, want := range []string{"## Source fact", "f-src", "access token body", "## Candidate facts", "f-cand", "refresh token snippet", "relates, depends-on", "json", "dst"} {
		if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
}

func TestParseLinkResponse(t *testing.T) {
	props, err := ParseLinkResponse(`  {"links":[{"dst":"f2","relation":"relates","why":"x"}]}  `)
	must(t, err)
	if len(props) != 1 || props[0].Dst != "f2" || props[0].Relation != "relates" {
		t.Fatalf("props = %+v", props)
	}
}

func TestParseLinkResponseInvalid(t *testing.T) {
	if _, err := ParseLinkResponse("not json"); err == nil {
		t.Fatal("want error on malformed JSON")
	}
}

func TestValidateProposals(t *testing.T) {
	src := fact.Fact{ID: "s"}
	cands := map[string]bool{"a": true, "b": true}
	good := []ProposedLink{{Dst: "a", Relation: "relates", Why: "ok"}}
	must(t, ValidateProposals(src, good, cands))

	bad := [][]ProposedLink{
		{{Dst: "a", Relation: "nope", Why: "x"}},        // invalid relation
		{{Dst: "s", Relation: "relates", Why: "x"}},     // self-link
		{{Dst: "zzz", Relation: "relates", Why: "x"}},   // non-candidate
		{{Dst: "a", Relation: "relates", Why: " "}},     // empty why
		{{Dst: "a", Relation: "relates", Why: "x"}, {Dst: "a", Relation: "relates", Why: "y"}}, // intra dup
	}
	for i, props := range bad {
		if err := ValidateProposals(src, props, cands); err == nil {
			t.Fatalf("bad proposal set %d accepted", i)
		}
	}
}

func TestLinkLoopSkipsFactsWithNoCandidatesAndValidates(t *testing.T) {
	facts := []fact.Fact{
		{ID: "f-src", Title: "JWT access", Project: "shopapp", Body: "a"},
		{ID: "f-cand", Title: "JWT refresh", Project: "shopapp", Body: "b"},
		{ID: "f-lonely", Title: "Unrelated", Project: "shopapp", Body: "c"},
	}
	fr := &linkFakeRunner{srcID: "f-src", dstID: "f-cand"}
	opts := LinkOptions{
		Facts: facts,
		Candidates: map[string][]Candidate{
			"f-src":  {{ID: "f-cand", Title: "JWT refresh", Project: "shopapp"}},
			"f-cand": {{ID: "f-src", Title: "JWT access", Project: "shopapp"}},
			// f-lonely has no candidates -> no LLM call
		},
		Runner: fr,
	}
	out, err := Link(context.Background(), opts)
	must(t, err)
	if len(out) != 1 || out[0].Src != "f-src" || len(out[0].Links) != 1 || out[0].Links[0].Dst != "f-cand" {
		t.Fatalf("proposals = %+v", out)
	}
	if fr.calls != 2 { // f-src and f-cand were called; f-lonely was skipped
		t.Fatalf("runner calls = %d, want 2", fr.calls)
	}
}

func TestLinkLoopAbortsOnInvalidProposal(t *testing.T) {
	facts := []fact.Fact{{ID: "f-src", Title: "JWT", Project: "p", Body: "a"}}
	// Runner returns a dst that is not in f-src's candidate set -> validation aborts.
	fr := &staticRunner{out: `{"links":[{"dst":"not-a-candidate","relation":"relates","why":"x"}]}`}
	opts := LinkOptions{
		Facts:      facts,
		Candidates: map[string][]Candidate{"f-src": {{ID: "f-cand"}}},
		Runner:     fr,
	}
	if _, err := Link(context.Background(), opts); err == nil {
		t.Fatal("Link should abort on non-candidate dst")
	}
}

type staticRunner struct{ out string }

func (s *staticRunner) Run(ctx context.Context, prompt string) (string, error) { return s.out, nil }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/wiki/`
Expected: FAIL (undefined `Candidate`, `ProposedLink`, `BuildLinkPrompt`, `ParseLinkResponse`, `ValidateProposals`, `Link`, `LinkOptions`).

- [ ] **Step 3: Implement the link core**

Create `internal/wiki/link.go`:

```go
package wiki

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"bbrain/internal/fact"
	"bbrain/internal/llm"
)

// Candidate is a fact offered to the LLM as a possible link target for a source
// fact. It carries just enough context (title/type/project + a short snippet) to
// judge relatedness without sending whole bodies.
type Candidate struct {
	ID      string
	Title   string
	Type    string
	Project string
	Snippet string
}

// ProposedLink is one reasoned link the LLM proposes from the source fact.
type ProposedLink struct {
	Dst      string `json:"dst"`
	Relation string `json:"relation"`
	Why      string `json:"why"`
}

// FactProposals groups one source fact's validated proposals.
type FactProposals struct {
	Src   string
	Links []ProposedLink
}

// Edge is a link actually written (or, in dry-run, that would be written).
type Edge struct {
	Src      string
	Dst      string
	Relation string
	Why      string
}

// LinkResult reports what a wiki link run wrote.
type LinkResult struct {
	Written []Edge
	Skipped int
	DryRun  bool
}

// LinkOptions configures the per-fact LLM linking loop.
type LinkOptions struct {
	Facts      []fact.Fact
	Candidates map[string][]Candidate // fact id -> its candidate facts
	Runner     llm.Runner
}

type linkResponse struct {
	Links []ProposedLink `json:"links"`
}

// BuildLinkPrompt assembles the prompt for one source fact: instructions, the
// relation vocabulary, the JSON schema, the source fact, and its candidates.
func BuildLinkPrompt(src fact.Fact, candidates []Candidate, relations []string) string {
	var sb strings.Builder
	sb.WriteString("You are BBrain's link reasoner. Decide which candidate facts are genuinely related to the source fact, and how.\n")
	sb.WriteString("Return ONLY a single JSON object: {\"links\":[{\"dst\",\"relation\",\"why\"}]}.\n")
	sb.WriteString("- dst: the id of a candidate fact below (never invent ids).\n")
	sb.WriteString("- relation: one of: " + strings.Join(relations, ", ") + ".\n")
	sb.WriteString("- why: one sentence explaining the relationship.\n")
	sb.WriteString("Only include genuinely related candidates; return an empty list if none apply.\n\n")

	sb.WriteString("## Source fact\n")
	sb.WriteString("### " + src.ID + "\n")
	sb.WriteString(fmt.Sprintf("title: %s | type: %s | project: %s | scope: %s | tags: %s\n",
		src.Title, src.Type, src.Project, src.Scope, strings.Join(src.Tags, ",")))
	sb.WriteString(strings.TrimSpace(src.Body) + "\n\n")

	sb.WriteString("## Candidate facts\n")
	for _, c := range candidates {
		sb.WriteString("### " + c.ID + "\n")
		sb.WriteString(fmt.Sprintf("title: %s | type: %s | project: %s\n", c.Title, c.Type, c.Project))
		if c.Snippet != "" {
			sb.WriteString(c.Snippet + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseLinkResponse parses the LLM stdout into the proposed links.
func ParseLinkResponse(stdout string) ([]ProposedLink, error) {
	var r linkResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return r.Links, nil
}

// ValidateProposals rejects anything unsafe before BBrain writes links: relation
// must be in the controlled vocabulary, dst must be one of the source's
// candidates (never an invented id), no self-links, why is mandatory, and no
// duplicate {dst,relation} within the same source's proposals.
func ValidateProposals(src fact.Fact, props []ProposedLink, candidateIDs map[string]bool) error {
	seen := map[string]bool{}
	for _, p := range props {
		if !fact.ValidRelation(p.Relation) {
			return fmt.Errorf("wiki: fact %q proposes invalid relation %q", src.ID, p.Relation)
		}
		if p.Dst == src.ID {
			return fmt.Errorf("wiki: fact %q proposes a self-link", src.ID)
		}
		if !candidateIDs[p.Dst] {
			return fmt.Errorf("wiki: fact %q proposes non-candidate target %q", src.ID, p.Dst)
		}
		if strings.TrimSpace(p.Why) == "" {
			return fmt.Errorf("wiki: fact %q link to %q has empty why", src.ID, p.Dst)
		}
		key := p.Dst + "\x00" + p.Relation
		if seen[key] {
			return fmt.Errorf("wiki: fact %q has a duplicate proposal %q/%q", src.ID, p.Dst, p.Relation)
		}
		seen[key] = true
	}
	return nil
}

// Link runs the per-fact linking pass: for each fact with candidates, prompt the
// LLM, parse, and validate. It writes nothing (writes go through the app layer).
// Any parse/validation failure aborts the whole run.
func Link(ctx context.Context, opts LinkOptions) ([]FactProposals, error) {
	var out []FactProposals
	for _, f := range opts.Facts {
		cands := opts.Candidates[f.ID]
		if len(cands) == 0 {
			continue // nothing to link against; skip the LLM call
		}
		candIDs := make(map[string]bool, len(cands))
		for _, c := range cands {
			candIDs[c.ID] = true
		}
		stdout, err := opts.Runner.Run(ctx, BuildLinkPrompt(f, cands, fact.Relations))
		if err != nil {
			return nil, err
		}
		props, err := ParseLinkResponse(stdout)
		if err != nil {
			return nil, err
		}
		if err := ValidateProposals(f, props, candIDs); err != nil {
			return nil, err
		}
		if len(props) > 0 {
			out = append(out, FactProposals{Src: f.ID, Links: props})
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/wiki/`
Expected: PASS (all prior wiki tests + the new link tests).

- [ ] **Step 5: Commit**

```bash
cd /home/vex/Projects/BBrain
go vet ./internal/wiki/
git add internal/wiki/link.go internal/wiki/link_test.go
git commit -m "feat(wiki): link prompt, parse, validation, and per-fact LLM loop"
```

---

## Task 2: `internal/app` — `WikiLink` wiring (filter → candidates → decide → write)

**Files:**
- Modify: `internal/app/app.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: `wiki.Link`, `wiki.LinkOptions`, `wiki.LinkResult`, `wiki.Candidate`, `wiki.Edge`, `wiki.AppendLog` (Task 1 + Plan 3); `a.Candidates`, `a.Store.Get`, `a.Store.ListFacts`, `a.Link`, `a.Brain.WikiDir`, `a.Store.Now`; stdlib `context`, `os`, `strings`, `time`.
- Produces:
  - `type WikiLinkOptions struct { Project, Scope string; Limit int; DryRun bool }`
  - `func (a *App) WikiLink(ctx context.Context, opts WikiLinkOptions) (wiki.LinkResult, error)`
  - `func snippet(body string, max int) string` (unexported helper)

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/app_test.go`. Add `"context"` is already imported and `"time"` to the import block (it currently imports `context, strings, testing, index, store`; add `time`). Then append:

```go
// linkRunner emits a link proposal only for the source fact's prompt.
type linkRunner struct{ srcID, dstID string }

func (r *linkRunner) Run(ctx context.Context, prompt string) (string, error) {
	if strings.Contains(prompt, "## Source fact\n### "+r.srcID+"\n") {
		return `{"links":[{"dst":"` + r.dstID + `","relation":"relates","why":"both about jwt"}]}`, nil
	}
	return `{"links":[]}`, nil
}

func TestWikiLinkWritesEdge(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	src, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT access tokens", Body: "access", Project: "shopapp", Scope: "project"})
	must(t, err)
	dst, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT refresh tokens", Body: "refresh", Project: "shopapp", Scope: "project"})
	must(t, err)
	a.Runner = &linkRunner{srcID: src.ID, dstID: dst.ID}

	res, err := a.WikiLink(context.Background(), WikiLinkOptions{})
	must(t, err)
	if len(res.Written) != 1 || res.Written[0].Src != src.ID || res.Written[0].Dst != dst.ID || res.Written[0].Relation != "relates" {
		t.Fatalf("written = %+v", res.Written)
	}
	// The link must be on the source fact's .md.
	got, ok, err := a.Store.Get(src.ID)
	must(t, err)
	if !ok || len(got.Links) != 1 || fact.LinkTargetID(got.Links[0].Target) != dst.ID {
		t.Fatalf("source links = %+v", got.Links)
	}
}

func TestWikiLinkDryRunWritesNothing(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	src, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT access tokens", Body: "access", Project: "shopapp", Scope: "project"})
	must(t, err)
	dst, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT refresh tokens", Body: "refresh", Project: "shopapp", Scope: "project"})
	must(t, err)
	a.Runner = &linkRunner{srcID: src.ID, dstID: dst.ID}

	res, err := a.WikiLink(context.Background(), WikiLinkOptions{DryRun: true})
	must(t, err)
	if !res.DryRun || len(res.Written) != 1 {
		t.Fatalf("dry-run result = %+v", res)
	}
	got, _, _ := a.Store.Get(src.ID)
	if len(got.Links) != 0 {
		t.Fatalf("dry-run wrote a link: %+v", got.Links)
	}
}

func TestWikiLinkIsIdempotent(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	src, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT access tokens", Body: "access", Project: "shopapp", Scope: "project"})
	must(t, err)
	dst, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT refresh tokens", Body: "refresh", Project: "shopapp", Scope: "project"})
	must(t, err)
	a.Runner = &linkRunner{srcID: src.ID, dstID: dst.ID}

	if _, err := a.WikiLink(context.Background(), WikiLinkOptions{}); err != nil {
		t.Fatal(err)
	}
	// Second run: dst is already linked, so a.Candidates drops it -> no proposal,
	// nothing written, and (if it were re-proposed) it would be skipped.
	res, err := a.WikiLink(context.Background(), WikiLinkOptions{})
	must(t, err)
	if len(res.Written) != 0 {
		t.Fatalf("second run wrote %+v, want nothing", res.Written)
	}
	got, _, _ := a.Store.Get(src.ID)
	if len(got.Links) != 1 {
		t.Fatalf("idempotency broken: source links = %+v", got.Links)
	}
}
```

(Note: `fact` must be imported in `app_test.go`. If it is not already, add `"bbrain/internal/fact"` to its import block — `TestWikiLinkWritesEdge` uses `fact.LinkTargetID`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/app/`
Expected: FAIL (undefined `WikiLink`, `WikiLinkOptions`).

- [ ] **Step 3: Implement `WikiLink` + `snippet`**

In `internal/app/app.go`, add `"time"` to the import block (it already imports `context`, `os`, `strings`, `fmt`, `path/filepath`, and the internal packages). Then append at the end of the file:

```go
// WikiLinkOptions configures App.WikiLink.
type WikiLinkOptions struct {
	Project string
	Scope   string
	Limit   int // max FTS candidates per fact; <=0 means 8
	DryRun  bool
}

// snippet collapses whitespace in body and returns at most max runes — enough
// context for the LLM to judge relatedness without sending the whole body.
func snippet(body string, max int) string {
	s := strings.Join(strings.Fields(body), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}

// WikiLink grows the reasoned fact graph: for each fact (optionally filtered by
// project/scope) it gathers FTS candidates, asks the LLM which are related and
// how, validates, and writes the new links via a.Link. Re-runs are idempotent
// (Candidates already excludes already-linked facts). On --dry-run nothing is
// written.
func (a *App) WikiLink(ctx context.Context, opts WikiLinkOptions) (wiki.LinkResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 8
	}
	facts, err := a.Store.ListFacts()
	if err != nil {
		return wiki.LinkResult{}, err
	}
	var filtered []fact.Fact
	for _, f := range facts {
		if opts.Project != "" && f.Project != opts.Project {
			continue
		}
		if opts.Scope != "" && f.Scope != opts.Scope {
			continue
		}
		filtered = append(filtered, f)
	}

	candMap := map[string][]wiki.Candidate{}
	for _, f := range filtered {
		res, err := a.Candidates(f.ID, limit)
		if err != nil {
			return wiki.LinkResult{}, err
		}
		var cs []wiki.Candidate
		for _, r := range res {
			snip := ""
			if cf, ok, err := a.Store.Get(r.FactID); err != nil {
				return wiki.LinkResult{}, err
			} else if ok {
				snip = snippet(cf.Body, 240)
			}
			cs = append(cs, wiki.Candidate{ID: r.FactID, Title: r.Title, Type: r.Type, Project: r.Project, Snippet: snip})
		}
		candMap[f.ID] = cs
	}

	proposals, err := wiki.Link(ctx, wiki.LinkOptions{Facts: filtered, Candidates: candMap, Runner: a.Runner})
	if err != nil {
		return wiki.LinkResult{}, err
	}

	var written []wiki.Edge
	skipped := 0
	for _, fp := range proposals {
		src, ok, err := a.Store.Get(fp.Src)
		if err != nil {
			return wiki.LinkResult{}, err
		}
		linked := map[string]bool{}
		if ok {
			for _, l := range src.Links {
				linked[fact.LinkTargetID(l.Target)] = true
			}
		}
		for _, p := range fp.Links {
			if linked[p.Dst] {
				skipped++
				continue
			}
			if !opts.DryRun {
				if _, err := a.Link(fp.Src, p.Dst, p.Relation, p.Why); err != nil {
					return wiki.LinkResult{}, err
				}
			}
			written = append(written, wiki.Edge{Src: fp.Src, Dst: p.Dst, Relation: p.Relation, Why: p.Why})
		}
	}

	if !opts.DryRun && len(written) > 0 {
		now := a.Store.Now().UTC().Format(time.RFC3339)
		var sb strings.Builder
		sb.WriteString("\n## " + now + " — wiki link\n")
		for _, e := range written {
			sb.WriteString(fmt.Sprintf("- %s -[%s]-> %s: %s\n", e.Src, e.Relation, e.Dst, e.Why))
		}
		if skipped > 0 {
			sb.WriteString(fmt.Sprintf("- (skipped %d already-linked)\n", skipped))
		}
		if err := os.MkdirAll(a.Brain.WikiDir(), 0o755); err != nil {
			return wiki.LinkResult{}, err
		}
		if err := wiki.AppendLog(a.Brain.WikiDir(), sb.String()); err != nil {
			return wiki.LinkResult{}, err
		}
	}

	return wiki.LinkResult{Written: written, Skipped: skipped, DryRun: opts.DryRun}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/app/`
Expected: PASS (existing app tests + the 3 new ones).

- [ ] **Step 5: Commit**

```bash
cd /home/vex/Projects/BBrain
go vet ./internal/app/
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat(app): WikiLink — candidates -> LLM -> validated, idempotent link writes"
```

---

## Task 3: CLI — `bbrain wiki link`

**Files:**
- Modify: `cmd/bbrain/main.go`
- Test: `cmd/bbrain/main_test.go`

**Interfaces:**
- Consumes: `app.WikiLink`, `app.WikiLinkOptions`, `wiki.Edge` (via `res.Written`); existing `cmdWiki`, `brainRoot`, `run`, `flag`, `fmt`, `io`, `context`, `strings`.
- Produces: `wiki link` subcommand: `bbrain wiki link [--project P] [--scope S] [--limit N] [--dry-run]`.

- [ ] **Step 1: Write the failing end-to-end tests**

Append to `cmd/bbrain/main_test.go`:

```go
func TestEndToEndWikiLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer

	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	save := func(title, body string) string {
		out.Reset()
		errOut.Reset()
		if code := run([]string{"save", "--title", title, "--project", "shopapp", "--type", "decision", "--body", body}, &out, &errOut); code != 0 {
			t.Fatalf("save: %s", errOut.String())
		}
		return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out.String()), "saved "))
	}
	srcID := save("JWT access tokens", "access token decision")
	dstID := save("JWT refresh tokens", "refresh token decision")

	// Fake agent: emits a link to dstID only when the prompt is for srcID.
	script := filepath.Join(t.TempDir(), "agent.sh")
	body := "#!/bin/sh\n" +
		"in=$(cat)\n" +
		"case \"$in\" in\n" +
		"*\"## Source fact\"*\"### " + srcID + "\"*) printf '{\"links\":[{\"dst\":\"" + dstID + "\",\"relation\":\"relates\",\"why\":\"both jwt\"}]}' ;;\n" +
		"*) printf '{\"links\":[]}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BBRAIN_AGENT_CLI", script)

	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "link"}, &out, &errOut); code != 0 {
		t.Fatalf("wiki link: %s", errOut.String())
	}
	if !strings.Contains(out.String(), srcID) || !strings.Contains(out.String(), dstID) || !strings.Contains(out.String(), "relates") {
		t.Fatalf("wiki link output = %q", out.String())
	}
	// The link landed on the source fact's .md.
	b, err := os.ReadFile(filepath.Join(home, "raws", "facts", srcID+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), dstID) || !strings.Contains(string(b), "relation: relates") {
		t.Fatalf("source fact .md = %s", b)
	}
}

func TestWikiLinkUnconfiguredFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	t.Setenv("BBRAIN_AGENT_CLI", "")
	var out, errOut bytes.Buffer
	run([]string{"init"}, &out, &errOut)
	// One fact with no candidates would skip the LLM; create two related facts so
	// the runner is actually invoked and the unset-CLI error surfaces.
	run([]string{"save", "--title", "JWT access", "--project", "p", "--type", "decision", "--body", "a"}, &out, &errOut)
	run([]string{"save", "--title", "JWT refresh", "--project", "p", "--type", "decision", "--body", "b"}, &out, &errOut)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "link"}, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "BBRAIN_AGENT_CLI") {
		t.Fatalf("err = %q", errOut.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./cmd/...`
Expected: FAIL (`wiki link` unknown → assertions fail).

- [ ] **Step 3: Add the subcommand**

In `cmd/bbrain/main.go`, update `cmdWiki` to dispatch `link`, and update its usage line:

```go
func cmdWiki(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "wiki: usage: bbrain wiki <build|link|lint> [args]")
		return 2
	}
	switch args[0] {
	case "build":
		return cmdWikiBuild(args[1:], stdout, stderr)
	case "link":
		return cmdWikiLink(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "wiki: unknown subcommand %q\n", args[0])
		return 2
	}
}
```

(Leave the `lint` case out for now — Task 6 adds it. The `<build|link|lint>` usage text is forward-looking and harmless.)

Append `cmdWikiLink` at the end of the file:

```go
func cmdWikiLink(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wiki link", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "only link facts in this project")
	scope := fs.String("scope", "", "only link facts in this scope")
	limit := fs.Int("limit", 8, "max FTS candidates considered per fact")
	dryRun := fs.Bool("dry-run", false, "print proposed links without writing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a := app.New(brainRoot())
	res, err := a.WikiLink(context.Background(), app.WikiLinkOptions{
		Project: *project, Scope: *scope, Limit: *limit, DryRun: *dryRun,
	})
	if err != nil {
		fmt.Fprintf(stderr, "wiki link: %v\n", err)
		return 1
	}
	if res.DryRun {
		fmt.Fprintln(stdout, "[dry-run] would write:")
	}
	for _, e := range res.Written {
		fmt.Fprintf(stdout, "%s -[%s]-> %s: %s\n", e.Src, e.Relation, e.Dst, e.Why)
	}
	if res.Skipped > 0 {
		fmt.Fprintf(stdout, "(skipped %d already-linked)\n", res.Skipped)
	}
	return 0
}
```

- [ ] **Step 4: Run the full suite**

Run: `cd /home/vex/Projects/BBrain && go test ./...`
Expected: PASS (all packages).

- [ ] **Step 5: Commit**

```bash
cd /home/vex/Projects/BBrain
go vet ./...
git add cmd/bbrain/main.go cmd/bbrain/main_test.go
git commit -m "feat(cli): wiki link command (project/scope/limit/dry-run) with e2e test"
```

---

## Task 4: `internal/wiki/lint.go` — deterministic check engine (pure core)

**Files:**
- Create: `internal/wiki/lint.go`
- Test: `internal/wiki/lint_test.go`

**Interfaces:**
- Consumes: `bbrain/internal/fact` (`Fact`, `LinkTargetID`); stdlib `fmt`, `regexp`, `strings`, `time`. Reuses `readPages`/`ParsePageMeta` from `wiki.go`.
- Produces:
  - `type Issue struct { Kind, Location, Message string; Fixable bool; Src, Dst string }`
  - `type LintResult struct { Issues []Issue; Fixed []Issue }`
  - `func Lint(wikiDir string, facts []fact.Fact, validCategories map[string]bool) ([]Issue, error)`

Issue `Kind` values: `dangling-link` (a fact's `links:` entry → missing fact; **fixable**, carries `Src`/`Dst`), `dangling-ref` (a `[[id]]` in a fact/page body → missing fact; not fixable — editing prose is unsafe), `missing-source`, `invalid-category`, `orphan-page`, `stale-page`, `bad-page` (un-parseable frontmatter). Only `dangling-link` is `Fixable`.

- [ ] **Step 1: Write the failing tests**

Create `internal/wiki/lint_test.go`:

```go
package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bbrain/internal/fact"
)

func lintWritePage(t *testing.T, dir, rel, cat, gen string, sources []string, body string) {
	t.Helper()
	s := "---\ntitle: T\ncategory: " + cat + "\nsources:\n"
	for _, src := range sources {
		s += "  - " + src + "\n"
	}
	s += "generated_at: " + gen + "\n---\n\n# T\n\n" + body + "\n"
	p := filepath.Join(dir, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(s), 0o644))
}

func hasIssue(issues []Issue, kind, locContains string) bool {
	for _, is := range issues {
		if is.Kind == kind && strings.Contains(is.Location, locContains) {
			return true
		}
	}
	return false
}

func TestLintDetectsDanglingLinkAndRef(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{
		{ID: "f1", Title: "F1", Body: "see [[ghost]] here", Links: []fact.Link{
			{Target: "[[missing]]", Relation: "relates", Why: "x"},
		}},
	}
	issues, err := Lint(dir, facts, map[string]bool{"decisions": true})
	must(t, err)
	if !hasIssue(issues, "dangling-link", "f1") {
		t.Fatalf("missing dangling-link:\n%+v", issues)
	}
	if !hasIssue(issues, "dangling-ref", "f1") {
		t.Fatalf("missing dangling-ref:\n%+v", issues)
	}
	// the dangling-link must be fixable and carry src/dst
	for _, is := range issues {
		if is.Kind == "dangling-link" {
			if !is.Fixable || is.Src != "f1" || is.Dst != "missing" {
				t.Fatalf("dangling-link issue = %+v", is)
			}
		}
		if is.Kind == "dangling-ref" && is.Fixable {
			t.Fatalf("dangling-ref must not be fixable: %+v", is)
		}
	}
}

func TestLintDetectsPageIssues(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "f1", Title: "F1", Body: "b", UpdatedAt: "2026-06-23T18:00:00Z"}}
	// invalid category + a missing source ("gone")
	lintWritePage(t, dir, "global/nope/bad.md", "nope", "2026-06-23T16:00:00Z", []string{"f1", "gone"}, "body")
	// orphan: its only source is missing
	lintWritePage(t, dir, "global/people/orphan.md", "people", "2026-06-23T16:00:00Z", []string{"gone"}, "body")
	// stale: source f1 updated_at (18:00) > generated_at (16:00)
	lintWritePage(t, dir, "global/people/stale.md", "people", "2026-06-23T16:00:00Z", []string{"f1"}, "body")

	valid := map[string]bool{"people": true}
	issues, err := Lint(dir, facts, valid)
	must(t, err)
	if !hasIssue(issues, "invalid-category", "bad.md") {
		t.Fatalf("missing invalid-category:\n%+v", issues)
	}
	if !hasIssue(issues, "missing-source", "bad.md") {
		t.Fatalf("missing missing-source:\n%+v", issues)
	}
	if !hasIssue(issues, "orphan-page", "orphan.md") {
		t.Fatalf("missing orphan-page:\n%+v", issues)
	}
	if !hasIssue(issues, "stale-page", "stale.md") {
		t.Fatalf("missing stale-page:\n%+v", issues)
	}
}

func TestLintClean(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "f1", Title: "F1", Body: "b", UpdatedAt: "2026-06-23T16:00:00Z"}}
	lintWritePage(t, dir, "global/people/ok.md", "people", "2026-06-23T18:00:00Z", []string{"f1"}, "see [[f1]]")
	issues, err := Lint(dir, facts, map[string]bool{"people": true})
	must(t, err)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got:\n%+v", issues)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/wiki/`
Expected: FAIL (undefined `Issue`, `LintResult`, `Lint`).

- [ ] **Step 3: Implement the lint engine**

Create `internal/wiki/lint.go`:

```go
package wiki

import (
	"fmt"
	"regexp"
	"time"

	"bbrain/internal/fact"
)

// Issue is one consistency problem found by Lint.
type Issue struct {
	Kind     string // dangling-link | dangling-ref | missing-source | invalid-category | orphan-page | stale-page | bad-page
	Location string
	Message  string
	Fixable  bool
	Src, Dst string // populated for dangling-link so --fix can act without re-parsing
}

// LintResult reports the issues found and (after --fix) the ones repaired.
type LintResult struct {
	Issues []Issue // remaining (unfixed) issues
	Fixed  []Issue
}

var targetRE = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

// scanTargets returns the bare fact ids referenced as [[id]] in s.
func scanTargets(s string) []string {
	var out []string
	for _, m := range targetRE.FindAllString(s, -1) {
		if id := fact.LinkTargetID(m); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// Lint runs the deterministic consistency checks over all facts and every wiki
// page under wikiDir, judging existence against the full fact set. It never
// mutates anything. validCategories is the active vocabulary.
func Lint(wikiDir string, facts []fact.Fact, validCategories map[string]bool) ([]Issue, error) {
	byID := make(map[string]fact.Fact, len(facts))
	for _, f := range facts {
		byID[f.ID] = f
	}
	var issues []Issue

	// Fact-side checks.
	for _, f := range facts {
		for _, l := range f.Links {
			dst := fact.LinkTargetID(l.Target)
			if dst == "" {
				continue
			}
			if _, ok := byID[dst]; !ok {
				issues = append(issues, Issue{
					Kind: "dangling-link", Location: "fact " + f.ID,
					Message: fmt.Sprintf("link %s -[%s]-> %s targets a missing fact", f.ID, l.Relation, dst),
					Fixable: true, Src: f.ID, Dst: dst,
				})
			}
		}
		for _, dst := range scanTargets(f.Body) {
			if _, ok := byID[dst]; !ok {
				issues = append(issues, Issue{
					Kind: "dangling-ref", Location: "fact " + f.ID,
					Message: fmt.Sprintf("body of %s references missing fact [[%s]]", f.ID, dst),
				})
			}
		}
	}

	// Page-side checks.
	pages, err := readPages(wikiDir)
	if err != nil {
		return nil, err
	}
	for _, pg := range pages {
		meta, err := ParsePageMeta(pg.Content)
		if err != nil {
			issues = append(issues, Issue{Kind: "bad-page", Location: pg.RelPath, Message: err.Error()})
			continue
		}
		if !validCategories[meta.Category] {
			issues = append(issues, Issue{Kind: "invalid-category", Location: pg.RelPath,
				Message: fmt.Sprintf("page category %q is not in the active vocabulary", meta.Category)})
		}
		missing := 0
		for _, src := range meta.Sources {
			if _, ok := byID[src]; !ok {
				missing++
				issues = append(issues, Issue{Kind: "missing-source", Location: pg.RelPath,
					Message: fmt.Sprintf("page source %q does not exist", src)})
			}
		}
		if len(meta.Sources) > 0 && missing == len(meta.Sources) {
			issues = append(issues, Issue{Kind: "orphan-page", Location: pg.RelPath,
				Message: "every source fact for this page is missing"})
		}
		if gen, perr := time.Parse(time.RFC3339, meta.GeneratedAt); perr == nil {
			for _, src := range meta.Sources {
				sf, ok := byID[src]
				if !ok {
					continue
				}
				if upd, uerr := time.Parse(time.RFC3339, sf.UpdatedAt); uerr == nil && upd.After(gen) {
					issues = append(issues, Issue{Kind: "stale-page", Location: pg.RelPath,
						Message: fmt.Sprintf("source %s was updated after the page's generated_at", src)})
				}
			}
		}
		for _, dst := range scanTargets(pg.Content) {
			if _, ok := byID[dst]; !ok {
				issues = append(issues, Issue{Kind: "dangling-ref", Location: pg.RelPath,
					Message: fmt.Sprintf("page %s references missing fact [[%s]]", pg.RelPath, dst)})
			}
		}
	}
	return issues, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/wiki/`
Expected: PASS (all prior wiki tests + the 3 new lint tests).

- [ ] **Step 5: Commit**

```bash
cd /home/vex/Projects/BBrain
go vet ./internal/wiki/
git add internal/wiki/lint.go internal/wiki/lint_test.go
git commit -m "feat(wiki): deterministic lint engine (dangling/missing/orphan/stale checks)"
```

---

## Task 5: `internal/store` `RemoveLink` + `internal/app` `WikiLint`/`RemoveLink` wiring

**Files:**
- Modify: `internal/store/store.go`, `internal/store/store_test.go`
- Modify: `internal/app/app.go`, `internal/app/app_test.go`

**Interfaces:**
- Consumes: `wiki.Lint`, `wiki.LintResult`, `wiki.Issue`, `wiki.DefaultCategories`, `wiki.RegenerateIndex`; `a.Store.ListFacts`, `a.Store.RemoveLink` (new), `a.Brain.WikiDir`, `index.IndexLinks`; stdlib `os`, `strings`.
- Produces:
  - `func (s *Store) RemoveLink(srcID, dstID string) (fact.Fact, error)`
  - `func (a *App) RemoveLink(srcID, dstID string) (fact.Fact, error)`
  - `type WikiLintOptions struct { Categories []string; Fix bool }`
  - `func (a *App) WikiLint(opts WikiLintOptions) (wiki.LintResult, error)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/store_test.go`:

```go
func TestRemoveLink(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Save(SaveInput{Type: "decision", Title: "Alpha", Body: "a", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Save(SaveInput{Type: "decision", Title: "Beta", Body: "b", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLink(a.ID, b.ID, "relates", "x"); err != nil {
		t.Fatal(err)
	}
	got, err := s.RemoveLink(a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Links) != 0 {
		t.Fatalf("links after remove = %+v", got.Links)
	}
	// Removing a non-existent link is a no-op, not an error.
	if _, err := s.RemoveLink(a.ID, "does-not-exist"); err != nil {
		t.Fatalf("no-op remove errored: %v", err)
	}
}
```

(Note: `newTestStore(t)` is the existing helper used by the other store tests. If the existing helper has a different name, use whatever the file already uses to construct a `*Store` with a temp brain and a fixed clock.)

Append to `internal/app/app_test.go`:

```go
func TestWikiLintReportsAndFixes(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	x, err := a.Save(store.SaveInput{Type: "decision", Title: "Alpha", Body: "a", Project: "p", Scope: "project"})
	must(t, err)
	y, err := a.Save(store.SaveInput{Type: "decision", Title: "Beta", Body: "b", Project: "p", Scope: "project"})
	must(t, err)
	// A real link, then delete the target fact's .md so the link dangles.
	if _, err := a.Link(x.ID, y.ID, "relates", "x"); err != nil {
		t.Fatal(err)
	}
	must(t, os.Remove(filepath.Join(a.Brain.FactsDir(), y.ID+".md")))

	// Report-only: the dangling link is reported and remains.
	res, err := a.WikiLint(WikiLintOptions{})
	must(t, err)
	found := false
	for _, is := range res.Issues {
		if is.Kind == "dangling-link" && is.Src == x.ID && is.Dst == y.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("dangling-link not reported:\n%+v", res.Issues)
	}

	// --fix: the dangling link is dropped from the source fact.
	res, err = a.WikiLint(WikiLintOptions{Fix: true})
	must(t, err)
	if len(res.Fixed) != 1 || res.Fixed[0].Kind != "dangling-link" {
		t.Fatalf("fixed = %+v", res.Fixed)
	}
	got, _, _ := a.Store.Get(x.ID)
	if len(got.Links) != 0 {
		t.Fatalf("link not dropped: %+v", got.Links)
	}
}
```

(`os` and `path/filepath` are needed in `app_test.go`; add them to the import block if not present.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/store/ ./internal/app/`
Expected: FAIL (undefined `RemoveLink`, `WikiLint`, `WikiLintOptions`).

- [ ] **Step 3: Implement `store.RemoveLink`**

In `internal/store/store.go`, append after `AddLink`:

```go
// RemoveLink removes any reasoned wikilink from srcID to dstID on the source
// fact's .md. It is a no-op (returning the unchanged fact) when no such link
// exists. The source .md is rewritten atomically and updated_at is bumped only
// when a link was actually removed.
func (s *Store) RemoveLink(srcID, dstID string) (fact.Fact, error) {
	src, ok, err := s.Get(srcID)
	if err != nil {
		return fact.Fact{}, err
	}
	if !ok {
		return fact.Fact{}, fmt.Errorf("store: source fact %q not found", srcID)
	}
	kept := make([]fact.Link, 0, len(src.Links))
	removed := false
	for _, l := range src.Links {
		if fact.LinkTargetID(l.Target) == dstID {
			removed = true
			continue
		}
		kept = append(kept, l)
	}
	if !removed {
		return src, nil
	}
	src.Links = kept
	src.UpdatedAt = s.Now().UTC().Format(time.RFC3339)
	if err := s.write(src); err != nil {
		return fact.Fact{}, err
	}
	return src, nil
}
```

- [ ] **Step 4: Implement `app.RemoveLink` + `app.WikiLint`**

In `internal/app/app.go`, append at the end of the file:

```go
// RemoveLink drops the reasoned wikilink from srcID to dstID on the source
// fact's .md, then re-indexes that fact's edges.
func (a *App) RemoveLink(srcID, dstID string) (fact.Fact, error) {
	f, err := a.Store.RemoveLink(srcID, dstID)
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

// WikiLintOptions configures App.WikiLint.
type WikiLintOptions struct {
	Categories []string // extra categories merged into the default vocabulary
	Fix        bool
}

// WikiLint runs the deterministic consistency checks over the whole brain. With
// Fix, it drops dangling fact links (via RemoveLink) and always regenerates the
// derived wiki/index.md; everything else is reported for the human to resolve.
func (a *App) WikiLint(opts WikiLintOptions) (wiki.LintResult, error) {
	facts, err := a.Store.ListFacts()
	if err != nil {
		return wiki.LintResult{}, err
	}
	valid := map[string]bool{}
	for _, c := range wiki.DefaultCategories {
		valid[c] = true
	}
	for _, c := range opts.Categories {
		if c = strings.TrimSpace(c); c != "" {
			valid[c] = true
		}
	}
	issues, err := wiki.Lint(a.Brain.WikiDir(), facts, valid)
	if err != nil {
		return wiki.LintResult{}, err
	}
	if !opts.Fix {
		return wiki.LintResult{Issues: issues}, nil
	}

	var remaining, fixed []wiki.Issue
	for _, is := range issues {
		if is.Kind == "dangling-link" && is.Fixable {
			if _, err := a.RemoveLink(is.Src, is.Dst); err != nil {
				return wiki.LintResult{}, err
			}
			fixed = append(fixed, is)
			continue
		}
		remaining = append(remaining, is)
	}
	// The index is derived: always regenerate it on --fix.
	if err := os.MkdirAll(a.Brain.WikiDir(), 0o755); err != nil {
		return wiki.LintResult{}, err
	}
	if err := wiki.RegenerateIndex(a.Brain.WikiDir()); err != nil {
		return wiki.LintResult{}, err
	}
	return wiki.LintResult{Issues: remaining, Fixed: fixed}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/store/ ./internal/app/`
Expected: PASS (existing tests + the new `RemoveLink` and `WikiLint` tests).

- [ ] **Step 6: Commit**

```bash
cd /home/vex/Projects/BBrain
go vet ./internal/store/ ./internal/app/
git add internal/store/store.go internal/store/store_test.go internal/app/app.go internal/app/app_test.go
git commit -m "feat(store,app): RemoveLink + WikiLint (report + safe --fix, derived index regen)"
```

---

## Task 6: CLI — `bbrain wiki lint`

**Files:**
- Modify: `cmd/bbrain/main.go`
- Test: `cmd/bbrain/main_test.go`

**Interfaces:**
- Consumes: `app.WikiLint`, `app.WikiLintOptions`, `wiki.Issue` (via `res.Issues`/`res.Fixed`); existing `cmdWiki`, `brainRoot`, `flag`, `fmt`, `io`, `strings`.
- Produces: `wiki lint` subcommand: `bbrain wiki lint [--categories a,b,c] [--fix]`; exit `0` when clean/all-fixed, `1` when unfixed issues remain.

- [ ] **Step 1: Write the failing end-to-end tests**

Append to `cmd/bbrain/main_test.go`:

```go
func TestEndToEndWikiLintFix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	t.Setenv("BBRAIN_AGENT_CLI", "") // lint needs no agent
	var out, errOut bytes.Buffer

	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	save := func(title string) string {
		out.Reset()
		errOut.Reset()
		if code := run([]string{"save", "--title", title, "--project", "p", "--type", "decision", "--body", "b"}, &out, &errOut); code != 0 {
			t.Fatalf("save: %s", errOut.String())
		}
		return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out.String()), "saved "))
	}
	x := save("Alpha")
	y := save("Beta")

	// Link x -> y, then delete y's fact so the link dangles.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"link", x, y, "--relation", "relates", "--why", "x"}, &out, &errOut); code != 0 {
		t.Fatalf("link: %s", errOut.String())
	}
	if err := os.Remove(filepath.Join(home, "raws", "facts", y+".md")); err != nil {
		t.Fatal(err)
	}

	// Report-only: a dangling-link is reported and the command exits non-zero.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "lint"}, &out, &errOut); code != 1 {
		t.Fatalf("wiki lint exit = %d, want 1; out=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "dangling-link") {
		t.Fatalf("lint report = %q", out.String())
	}

	// --fix: the dangling link is dropped and the command exits 0.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "lint", "--fix"}, &out, &errOut); code != 0 {
		t.Fatalf("wiki lint --fix exit = %d, want 0; out=%q err=%q", code, out.String(), errOut.String())
	}
	b, err := os.ReadFile(filepath.Join(home, "raws", "facts", x+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), y) {
		t.Fatalf("dangling link not dropped from source:\n%s", b)
	}
}
```

(Note: the `link` subcommand and its `--relation`/`--why` flags are the Plan 2 CLI. If the exact flag names differ, match what `cmd/bbrain/main.go`'s existing `link` command defines.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./cmd/...`
Expected: FAIL (`wiki lint` unknown → assertions fail).

- [ ] **Step 3: Add the subcommand**

In `cmd/bbrain/main.go`, add the `lint` case to `cmdWiki`'s switch (before `default`):

```go
	case "lint":
		return cmdWikiLint(args[1:], stdout, stderr)
```

Append `cmdWikiLint` at the end of the file:

```go
func cmdWikiLint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wiki lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cats := fs.String("categories", "", "extra wiki categories (comma-separated)")
	fix := fs.Bool("fix", false, "apply mechanically-safe repairs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var extra []string
	if *cats != "" {
		for _, c := range strings.Split(*cats, ",") {
			if c = strings.TrimSpace(c); c != "" {
				extra = append(extra, c)
			}
		}
	}
	a := app.New(brainRoot())
	res, err := a.WikiLint(app.WikiLintOptions{Categories: extra, Fix: *fix})
	if err != nil {
		fmt.Fprintf(stderr, "wiki lint: %v\n", err)
		return 1
	}
	for _, is := range res.Fixed {
		fmt.Fprintf(stdout, "fixed: %s — %s\n", is.Kind, is.Message)
	}
	for _, is := range res.Issues {
		fmt.Fprintf(stdout, "%s: %s — %s\n", is.Kind, is.Location, is.Message)
	}
	if len(res.Issues) > 0 {
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run the full suite**

Run: `cd /home/vex/Projects/BBrain && go test ./...`
Expected: PASS (all packages).

- [ ] **Step 5: Manual smoke test**

```bash
cd /home/vex/Projects/BBrain
go build ./cmd/bbrain
rm -rf /tmp/bbrain-smoke3b
export BBRAIN_HOME=/tmp/bbrain-smoke3b
./bbrain init
# A fake agent that links the source fact to the FIRST candidate id it sees.
cat > /tmp/fake-link-agent.sh <<'SH'
#!/bin/sh
in=$(cat)
# pull the first candidate id (first "### <id>" under "## Candidate facts")
dst=$(printf '%s\n' "$in" | awk '/## Candidate facts/{f=1;next} f&&/^### /{print $2; exit}')
src=$(printf '%s\n' "$in" | awk '/## Source fact/{f=1;next} f&&/^### /{print $2; exit}')
if [ -n "$dst" ]; then
  printf '{"links":[{"dst":"%s","relation":"relates","why":"shared topic"}]}' "$dst"
else
  printf '{"links":[]}'
fi
SH
chmod +x /tmp/fake-link-agent.sh
A=$(./bbrain save --title "JWT access tokens" --project shopapp --type decision --body "access" | sed 's/^saved //')
B=$(./bbrain save --title "JWT refresh tokens" --project shopapp --type decision --body "refresh" | sed 's/^saved //')
echo "--- wiki link (dry-run) ---"; BBRAIN_AGENT_CLI=/tmp/fake-link-agent.sh ./bbrain wiki link --dry-run
echo "--- wiki link ---";           BBRAIN_AGENT_CLI=/tmp/fake-link-agent.sh ./bbrain wiki link
echo "--- source fact .md ---";     cat "$BBRAIN_HOME/raws/facts/$A.md"
echo "--- wiki link again (idempotent: should write nothing) ---"; BBRAIN_AGENT_CLI=/tmp/fake-link-agent.sh ./bbrain wiki link
echo "--- break a link, then lint ---"; rm "$BBRAIN_HOME/raws/facts/$B.md"
echo "--- wiki lint (should report dangling-link, exit 1) ---"; ./bbrain wiki lint; echo "exit=$?"
echo "--- wiki lint --fix (should drop it, exit 0) ---"; ./bbrain wiki lint --fix; echo "exit=$?"
echo "--- source fact after fix ---"; cat "$BBRAIN_HOME/raws/facts/$A.md"
unset BBRAIN_HOME
```
Expected: `wiki link --dry-run` prints the proposed edge without writing; `wiki link` writes a `relates` link into the source fact's `.md`; the second `wiki link` writes nothing (idempotent); after deleting B's fact, `wiki lint` reports `dangling-link` and exits 1; `wiki lint --fix` drops the link and exits 0.

- [ ] **Step 6: Commit**

```bash
cd /home/vex/Projects/BBrain
go vet ./...
git add cmd/bbrain/main.go cmd/bbrain/main_test.go
git commit -m "feat(cli): wiki lint command (report + --fix, CI exit codes) with e2e test"
```

---

## Self-Review

**1. Spec coverage (design spec §1–§7):**
- `wiki link` autonomous, validated, idempotent, dry-run, per-fact pass, requires `BBRAIN_AGENT_CLI` → Tasks 1–3. ✓
- Validation matrix (relation in vocab, dst ∈ candidates, no self-link, why required, intra-proposal dedup; abort on failure) → Task 1 `ValidateProposals`/`Link`. ✓
- Single directional edge; idempotency via `Candidates` exclusion + app skip → Task 2. ✓
- `wiki lint` five deterministic checks (dangling targets, missing source, invalid category, orphan, stale) + `bad-page` robustness → Task 4. ✓
- `--fix` drops dangling fact links (new `store.RemoveLink`) and always regenerates the derived index; stale/orphan/category reported not fixed → Task 5. ✓
- CI-friendly exit codes (0 clean/all-fixed, 1 remaining) → Task 6. ✓
- Clean layering (`wiki` decides; `app` fetches & writes) → Tasks 1/2/4/5. ✓
- No new dependencies; `.md` source of truth; relation vocab fixed → Global Constraints, honored throughout. ✓
- **Deviation from spec §4.1 (flagged):** `wiki lint` drops `--project`/`--scope` (filtering would create false dangling reports); keeps `--categories`. ✓

**2. Placeholder scan:** No TBD/TODO. Every code step shows complete code; every test step shows the command and expected result. The only "match the existing name" notes (`newTestStore`, the Plan 2 `link` flags, `fact` import in `app_test.go`) are explicit verification instructions, not missing logic. ✓

**3. Type consistency:** `wiki.Candidate{ID,Title,Type,Project,Snippet}`, `ProposedLink{Dst,Relation,Why}`, `FactProposals{Src,Links}`, `Edge{Src,Dst,Relation,Why}`, `LinkResult{Written,Skipped,DryRun}`, `LinkOptions{Facts,Candidates,Runner}` are defined in Task 1 and consumed identically in Tasks 2–3. `Issue{Kind,Location,Message,Fixable,Src,Dst}` and `LintResult{Issues,Fixed}` defined in Task 4, consumed in Tasks 5–6. `store.RemoveLink(srcID,dstID)(fact.Fact,error)` defined Task 5, called by `app.RemoveLink` (Task 5) and indirectly by `WikiLint`. `app.WikiLink(ctx, WikiLinkOptions)` / `app.WikiLint(WikiLintOptions)` called by `cmdWikiLink`/`cmdWikiLint` exactly as defined. ✓

**4. Import/dependency sanity:** `wiki/link.go` imports `fact`, `llm`, stdlib; `wiki/lint.go` imports `fact`, stdlib; neither imports `store`/`index` (layering preserved). `app` adds `time` (WikiLink log timestamp). `store.RemoveLink` uses already-imported `fmt`/`time`. `cmd` uses already-imported `context`/`strings`/`flag`. No cycles, no `go.mod` change. ✓
