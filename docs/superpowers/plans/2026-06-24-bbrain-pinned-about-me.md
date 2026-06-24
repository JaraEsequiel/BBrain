# Pinned context & about-me Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a generic `pinned` boolean to facts so any fact can be injected (full body, framed) at the top of every session digest; ship about-me as the first pinned fact.

**Architecture:** `pinned` is one new frontmatter field threaded fact → store → app → mcp. `App.Context()` grows from one memory section to two: a Pinned section (full bodies, global-visible, deduped) plus the unchanged Recent-facts bullets. Same change fixes a latent bug where global facts (`Project==""`) were dropped under a project filter. about-me is just a pinned fact with a `topic_key` (idempotent upsert) — no special code path.

**Tech Stack:** Go 1.25, `gopkg.in/yaml.v3`, stdlib testing.

## Global Constraints

- `.md` is the source of truth; index is derived — all writes go through `store`/`app`.
- Idempotency is required: re-saving about-me (same `topic_key`) rewrites one file.
- Stdlib-first, near-zero deps. No new dependencies.
- `Pinned bool` uses `yaml:"pinned,omitempty"` — byte-stable: the key only appears on disk when `true`.
- `limit` bounds the Recent-facts section ONLY; pinned facts are never truncated.
- Pinned section preamble copy (verbatim):
  > This is always-on context: who the user is, their preferences, and how to
  > work with them. Keep it in mind throughout the session — it's background to
  > factor into your work, not a task to act on.

## Dependency graph & parallelism

```
Task A (foundation: fact.Pinned + store threading)
        │
        ├──────────────┬──────────────┐
        ▼              ▼               │
   Task B          Task C    ← PARALLEL (no dependency on each other)
   (app.Context)   (mcp mem_save)
        └──────┬───────┘
               ▼
           Task D (build + vet + test gate / join)
```

| Task | Depends on | Parallel? |
|------|-----------|-----------|
| **A** — `fact.Pinned` field + `store.SaveInput` threading | — | No (foundation; must land first) |
| **B** — `App.Context()` two-section digest | A | **Yes — run concurrently with C** |
| **C** — MCP `mem_save` schema/threading/output | A | **Yes — run concurrently with B** |
| **D** — build + vet + full test suite | B, C | No (join point) |

**Why B and C are independent:** both only read `fact.Pinned` (A) and write through `store.SaveInput.Pinned` (A). B touches `internal/app/app.go`; C touches `internal/mcp/tools.go`. No shared file, no shared symbol beyond A. C's test verifies persistence via `a.Get`, **not** via B's digest output — so C does not wait on B.

**Execution:** with subagent-driven-development, dispatch A alone first (review gate), then dispatch **B and C as two concurrent subagents in a single message**, then D as the join.

---

### Task A: `fact.Pinned` field + `store.SaveInput` threading  ·  *(foundation — no deps)*

**Files:**
- Modify: `internal/fact/fact.go:23-37` (the `Fact` struct)
- Modify: `internal/store/store.go:36-44` (SaveInput), `:66-78` (upsert path), `:92-104` (new-fact path)
- Test: `internal/fact/fact_test.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces: `fact.Fact.Pinned bool` and `store.SaveInput.Pinned bool` — read/set by Tasks B and C.

- [ ] **Step 1: Write the failing `fact` test**

Add to `internal/fact/fact_test.go` (add `"strings"` to imports if missing):

```go
func TestPinnedRoundTrip(t *testing.T) {
	f := Fact{
		ID: "x", Type: "about-me", Scope: "global",
		CreatedAt: "2026-06-24T00:00:00Z", UpdatedAt: "2026-06-24T00:00:00Z",
		RevisionCount: 1, Pinned: true,
		Title: "About", Body: "hi",
	}
	got, err := Parse(Marshal(f))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Pinned {
		t.Fatalf("pinned lost in round-trip: %+v", got)
	}
}

func TestPinnedOmittedWhenFalse(t *testing.T) {
	out := Marshal(Fact{ID: "x", Type: "decision", Title: "T", Body: "b"})
	if strings.Contains(out, "pinned") {
		t.Fatalf("pinned:false must not appear on disk:\n%s", out)
	}
}
```

- [ ] **Step 2: Write the failing `store` test**

Add to `internal/store/store_test.go` (reuse this file's existing store constructor — mirror how the other tests build a `*Store`):

```go
func TestSavePinnedNewAndUpsert(t *testing.T) {
	s := newTestStore(t) // reuse this file's existing store constructor
	f, err := s.Save(SaveInput{Type: "about-me", Title: "About", Body: "v1",
		Scope: "global", TopicKey: "profile/about-me", Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Pinned {
		t.Fatalf("new save lost pinned: %+v", f)
	}
	f2, err := s.Save(SaveInput{Type: "about-me", Title: "About", Body: "v2",
		Scope: "global", TopicKey: "profile/about-me", Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	if !f2.Pinned || f2.Body != "v2" || f2.ID != f.ID {
		t.Fatalf("upsert wrong: %+v", f2)
	}
}
```

- [ ] **Step 3: Run both tests to verify they fail**

Run: `go test ./internal/fact ./internal/store -run 'Pinned' -v`
Expected: FAIL — unknown field `Pinned` on `Fact` / `SaveInput`.

- [ ] **Step 4: Add the `fact` field**

`internal/fact/fact.go`, in the `Fact` struct, after the `Links` line:

```go
	Links         []Link   `yaml:"links,omitempty"`
	Pinned        bool     `yaml:"pinned,omitempty"`
	CreatedAt     string   `yaml:"created_at"`
```

- [ ] **Step 5: Add `Pinned` to `SaveInput`**

`internal/store/store.go`, in `SaveInput`:

```go
	TopicKey string
	Tags     []string
	Pinned   bool
}
```

- [ ] **Step 6: Thread it on the upsert path**

`internal/store/store.go`, in the topic-key upsert block, after `e.Tags = in.Tags`:

```go
				e.Tags = in.Tags
				e.Pinned = in.Pinned
				e.UpdatedAt = nowStr
```

- [ ] **Step 7: Thread it on the new-fact path**

`internal/store/store.go`, in the `f := fact.Fact{...}` literal, after `Tags: in.Tags,`:

```go
		Tags:          in.Tags,
		Pinned:        in.Pinned,
		CreatedAt:     nowStr,
```

- [ ] **Step 8: Run both tests to verify they pass**

Run: `go test ./internal/fact ./internal/store -run 'Pinned' -v`
Expected: PASS (all).

- [ ] **Step 9: Commit**

```bash
git add internal/fact/fact.go internal/fact/fact_test.go internal/store/store.go internal/store/store_test.go
git commit -m "feat(fact,store): add pinned field + thread through SaveInput"
```

---

### Task B: `App.Context()` two-section digest  ·  *(PARALLEL with C — depends on A)*

**Files:**
- Modify: `internal/app/app.go:584-618` (entire `Context` method)
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: `fact.Fact.Pinned`, `store.SaveInput.Pinned` (Task A).
- Produces: digest with a `## About you & pinned context` section (full bodies + preamble) when ≥1 pinned fact is visible; pinned facts excluded from `## Recent facts`; global non-pinned facts now visible under a project filter.

- [ ] **Step 1: Write the failing tests**

Add to `internal/app/app_test.go`:

```go
func TestContextPinnedGlobalShownUnderAnyProject(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "about-me", Title: "About you",
		Body: "User is Vex. Prefers terse answers.", Scope: "global",
		TopicKey: "profile/about-me", Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Proj note",
		Body: "x", Project: "shopapp", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("shopapp", 10)
	must(t, err)
	if !strings.Contains(out, "## About you & pinned context") {
		t.Fatalf("missing pinned heading: %s", out)
	}
	if !strings.Contains(out, "Prefers terse answers") {
		t.Fatalf("pinned full body missing: %s", out)
	}
	if !strings.Contains(out, "background to") {
		t.Fatalf("preamble missing: %s", out)
	}
}

func TestContextPinnedNotDuplicatedInRecent(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "about-me", Title: "About you",
		Body: "body", Scope: "global", TopicKey: "profile/about-me",
		Pinned: true}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("", 10)
	must(t, err)
	if strings.Contains(out, "[about-me] About you") {
		t.Fatalf("pinned fact leaked into Recent bullets: %s", out)
	}
}

func TestContextProjectScopedPinnedHiddenElsewhere(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "note", Title: "Shop pin",
		Body: "only shop", Project: "shopapp", Scope: "project",
		TopicKey: "shop/pin", Pinned: true}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("datacli", 10)
	must(t, err)
	if strings.Contains(out, "only shop") || strings.Contains(out, "## About you & pinned context") {
		t.Fatalf("project-scoped pin leaked to other project: %s", out)
	}
}

func TestContextGlobalNonPinnedVisibleUnderProject(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Global rule",
		Body: "g", Scope: "global"}); err != nil { // Project == ""
		t.Fatal(err)
	}
	out, err := a.Context("shopapp", 10)
	must(t, err)
	if !strings.Contains(out, "Global rule") {
		t.Fatalf("global non-pinned fact dropped under project filter: %s", out)
	}
}

func TestContextNoPinnedHeadingWhenNonePinned(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	if _, err := a.Save(store.SaveInput{Type: "decision", Title: "Plain",
		Body: "p", Project: "p", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Context("", 10)
	must(t, err)
	if strings.Contains(out, "## About you & pinned context") {
		t.Fatalf("pinned heading shown with no pinned facts: %s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app -run TestContext -v`
Expected: FAIL — no pinned heading / global fact dropped / pin leaks into recent.

- [ ] **Step 3: Rewrite `Context`**

Replace the entire body of `func (a *App) Context(project string, limit int) (string, error)` in `internal/app/app.go` with:

```go
func (a *App) Context(project string, limit int) (string, error) {
	if limit <= 0 {
		limit = 10
	}
	facts, err := a.Store.ListFacts()
	if err != nil {
		return "", err
	}

	// visible: a fact passes the project filter when it is global (no project)
	// or its project matches the requested one. Empty `project` shows everything.
	visible := func(f fact.Fact) bool {
		return project == "" || f.Project == "" || f.Project == project
	}

	// Pinned: full body, always-on, global-visible. Not bounded by `limit`.
	var pinned []fact.Fact
	pinnedID := map[string]bool{}
	for _, f := range facts {
		if f.Pinned && visible(f) {
			pinned = append(pinned, f)
			pinnedID[f.ID] = true
		}
	}
	sort.Slice(pinned, func(i, j int) bool { return pinned[i].UpdatedAt > pinned[j].UpdatedAt })

	// Recent: project-filtered bullets, excluding anything already pinned.
	var recent []fact.Fact
	for _, f := range facts {
		if pinnedID[f.ID] || !visible(f) {
			continue
		}
		recent = append(recent, f)
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].UpdatedAt > recent[j].UpdatedAt })
	if len(recent) > limit {
		recent = recent[:limit]
	}

	var sb strings.Builder
	sb.WriteString("# BBrain memory context\n")

	if len(pinned) > 0 {
		sb.WriteString("\n## About you & pinned context\n\n")
		sb.WriteString("This is always-on context: who the user is, their preferences, and how to\n")
		sb.WriteString("work with them. Keep it in mind throughout the session — it's background to\n")
		sb.WriteString("factor into your work, not a task to act on.\n")
		for _, f := range pinned {
			sb.WriteString(fmt.Sprintf("\n### %s\n\n", f.Title))
			sb.WriteString(strings.TrimRight(f.Body, "\n"))
			sb.WriteString("\n")
		}
	}

	if b, err := os.ReadFile(filepath.Join(a.Brain.WikiDir(), "index.md")); err == nil {
		sb.WriteString("\n## Wiki index\n")
		sb.Write(b)
		sb.WriteString("\n")
	}

	sb.WriteString("\n## Recent facts\n")
	if len(recent) == 0 {
		sb.WriteString("(none yet)\n")
	}
	for _, f := range recent {
		sb.WriteString(fmt.Sprintf("- [%s] %s (%s) — id %s\n", f.Type, f.Title, f.Project, f.ID))
	}
	return sb.String(), nil
}
```

- [ ] **Step 4: Run the app Context tests**

Run: `go test ./internal/app -run TestContext -v`
Expected: PASS — including pre-existing `TestContextRecentFactsAndFilter` and `TestContextEmptyBrain` (unaffected: their facts are project-scoped or absent).

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat(app): pinned digest section + preamble; show global facts under project filter"
```

---

### Task C: MCP `mem_save` — schema, threading, output  ·  *(PARALLEL with B — depends on A)*

**Files:**
- Modify: `internal/mcp/tools.go:37` (schemaMemSave), `:50-58` (factView), `:63-82` (memSaveArgs + UnmarshalJSON), `:84-99` (handleMemSave)
- Test: `internal/mcp/tools_test.go` (create if absent)

**Interfaces:**
- Consumes: `store.SaveInput.Pinned`, `fact.Fact.Pinned` (Task A); `app.App.Get(id) (fact.Fact, bool, error)` (existing).
- Produces: `mem_save` accepts optional `"pinned": bool`, persists it, echoes `"pinned"` in its JSON output via `factView`.

- [ ] **Step 1: Write the failing test (persistence verified via `a.Get`, not the digest — keeps C independent of B)**

Create/append `internal/mcp/tools_test.go`. Match how other handler tests in this package build an `*app.App` (mirror `app_test.go`'s `New(t.TempDir())` + `Init()` if no helper exists):

```go
package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"bbrain/internal/app"
)

func TestHandleMemSavePinned(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"about-me","title":"About","body":"hi","scope":"global","topic_key":"profile/about-me","pinned":true}`)
	out, err := handleMemSave(context.Background(), a, raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["pinned"] != true {
		t.Fatalf("output did not echo pinned:true: %#v", out)
	}
	// Persisted — reload by id (independent of the digest / Task B).
	got, found, err := a.Get(m["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !found || !got.Pinned {
		t.Fatalf("pinned not persisted: found=%v fact=%+v", found, got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp -run TestHandleMemSavePinned -v`
Expected: FAIL — `m["pinned"]` is nil (not threaded / not in factView).

- [ ] **Step 3: Add `pinned` to the schema**

`internal/mcp/tools.go`, in `schemaMemSave`, add `"pinned":{"type":"boolean"}` to `properties` (leave `required` unchanged):

```go
	schemaMemSave = json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"},"project":{"type":"string"},"scope":{"type":"string"},"topic_key":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}},"pinned":{"type":"boolean"}},"required":["type","title","body"]}`)
```

- [ ] **Step 4: Thread `pinned` through `memSaveArgs`**

`internal/mcp/tools.go`. Add the field to the struct:

```go
type memSaveArgs struct {
	Type, Title, Body, Project, Scope, TopicKey string
	Tags                                        []string
	Pinned                                      bool
}
```

In `UnmarshalJSON`, add to the `raw` struct and the assignment:

```go
		Tags     []string `json:"tags"`
		Pinned   bool     `json:"pinned"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Type, m.Title, m.Body = raw.Type, raw.Title, raw.Body
	m.Project, m.Scope, m.TopicKey, m.Tags = raw.Project, raw.Scope, raw.TopicKey, raw.Tags
	m.Pinned = raw.Pinned
```

- [ ] **Step 5: Pass `pinned` into `Save`**

`internal/mcp/tools.go`, in `handleMemSave`:

```go
	f, err := a.Save(store.SaveInput{
		Type: in.Type, Title: in.Title, Body: in.Body,
		Project: in.Project, Scope: in.Scope, TopicKey: in.TopicKey, Tags: in.Tags,
		Pinned: in.Pinned,
	})
```

- [ ] **Step 6: Echo `pinned` in `factView`**

`internal/mcp/tools.go`, in `factView`, add to the returned map:

```go
		"revision_count": f.RevisionCount, "links": f.Links,
		"pinned": f.Pinned,
	}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/mcp -run TestHandleMemSavePinned -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): mem_save accepts and echoes pinned flag"
```

---

### Task D: build + vet + full test suite  ·  *(join — depends on B and C)*

**Files:** none (verification only).

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 3: Full test suite**

Run: `go test ./...`
Expected: all packages PASS (`fact`, `store`, `app`, `mcp`, …).

---

## Self-Review

**Spec coverage:**
- §1 `pinned` primitive → Task A (fact + store), Task C (mcp schema/output). ✔
- §2 two-section digest, full body, dedup, project visibility, no `limit` on pinned, empty-heading guard → Task B. ✔
- §2 "Bug noted in passing" (global facts under filter) → folded into Task B (accepted in debate). ✔
- §3 about-me as a pinned fact via `topic_key` upsert → exercised by Task A upsert test + Task C (`profile/about-me`); no code path needed (just data). ✔
- Preamble / framing → Task B Step 3 + Global Constraints. ✔
- Testing matrix (§Testing) → Tasks A, B, C. ✔

**Placeholders:** none — every code step shows full code; test steps reference existing in-file constructors with a fallback instruction.

**Type consistency:** `Pinned bool` consistent across `fact.Fact`, `store.SaveInput`, `memSaveArgs`; `visible(f)` closure drives both sections; `pinnedID` map drives dedup; `a.Get(id) (fact.Fact, bool, error)` matches the existing signature used in `handleMemGet`. `Context(project, limit)` signature unchanged.

**Parallelism:** A is the sole foundation; B and C share no file or symbol beyond A and are dispatched concurrently; D joins. About-me has no "create file" task by design (§Non-goals) — authored at runtime via `mem_save {pinned:true, topic_key:"profile/about-me"}`, proven by Tasks A and C.
