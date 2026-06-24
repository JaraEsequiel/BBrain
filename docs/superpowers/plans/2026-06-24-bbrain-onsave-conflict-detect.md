# On-save Conflict/Duplicate Hint (Gap 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When `mem_save` stores a fact, also return the existing, lexically-similar, not-yet-linked facts (if any) so the agent can link/reconcile instead of silently duplicating or contradicting.

**Architecture:** One change to `handleMemSave` in `internal/mcp/tools.go`: after saving, call the existing `app.Candidates(id, limit)` and, if it returns matches, attach them as a `related` field on the response. No LLM on the save path, no new mechanism, no session model. (Track B — disjoint from Spec A, which lives in `internal/setup/setup.go`.)

**Tech Stack:** Go 1.25, stdlib only.

## Global Constraints

- Module `bbrain`, Go 1.25, stdlib-only — add NO new dependencies.
- A hint failure MUST NEVER fail the save — ignore `Candidates`' error and just omit `related`.
- Do NOT call the LLM (`$BBRAIN_AGENT_CLI`) on the save path. Semantic classification stays in the existing on-demand `wiki_link`.
- **Frontier (hard):** touch ONLY `internal/mcp/tools.go` and `internal/mcp/tools_test.go`. Do NOT touch `internal/setup/setup.go`, the `ClaudeMDBlock`, `app.Save`'s signature, or `internal/llm` (keeps this disjoint from Spec A so the two can run in parallel sessions and merge conflict-free).
- This plan is independent of Spec A: branch off `master` as `feat/onsave-conflict-detect`. If Spec A merges first, this rebases trivially (disjoint files).

## Resolved design decisions (were open in the spec)

- **How many candidates:** top **5** (`a.Candidates(f.ID, 5)`).
- **Score floor:** none — `index.Result` exposes no score, and `Candidates` already returns by FTS relevance and excludes already-linked facts. Surface what it returns.
- **Shape of `related`:** the raw `[]index.Result` (`{fact_id, title, type, project, path}`), exactly as `mem_candidates` already returns. No reshaping.

---

### Task 1: Surface related candidates in the mem_save response

**Files:**
- Modify: `internal/mcp/tools.go` — `handleMemSave` (lines ~88-102)
- Test: `internal/mcp/tools_test.go`

**Interfaces:**
- Consumes (already exist): `func (a *App) Candidates(id string, limit int) ([]index.Result, error)`; `factView(f fact.Fact) map[string]any`; test helper `call(t *testing.T, a *app.App, name, args string) string` (runs a tool handler, returns the JSON string).
- Produces: an optional `related` key on the `mem_save` response (`[]index.Result`), present only when candidates exist.

- [ ] **Step 1: Write the failing test**

Add to `internal/mcp/tools_test.go` (package `mcp`):

```go
func TestMemSaveSurfacesRelatedCandidates(t *testing.T) {
	a := app.New(t.TempDir())

	// Save fact A.
	outA := call(t, a, "mem_save", `{"type":"decision","title":"Postgres connection pool tuning","body":"set max pool size to 20","project":"p"}`)
	var ra map[string]any
	if err := json.Unmarshal([]byte(outA), &ra); err != nil {
		t.Fatalf("A response not JSON: %v\n%s", err, outA)
	}
	idA, _ := ra["id"].(string)
	if idA == "" {
		t.Fatalf("A has no id: %s", outA)
	}

	// Save fact B — lexically similar to A (shared distinctive terms).
	outB := call(t, a, "mem_save", `{"type":"decision","title":"Postgres connection pool sizing","body":"reconsider the pool size","project":"p"}`)
	if !strings.Contains(outB, `"related"`) {
		t.Fatalf("B's save should surface related candidates:\n%s", outB)
	}
	if !strings.Contains(outB, idA) {
		t.Fatalf("B's related should include A's id %q:\n%s", idA, outB)
	}

	// Save fact C — disjoint vocabulary → no related.
	outC := call(t, a, "mem_save", `{"type":"decision","title":"Frontend teal palette","body":"pick teal accents","project":"p"}`)
	if strings.Contains(outC, `"related"`) {
		t.Fatalf("C is dissimilar; should have no related:\n%s", outC)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestMemSaveSurfacesRelatedCandidates`
Expected: FAIL — B's response has no `"related"` key (handler returns `factView(f)` only).

- [ ] **Step 3: Implement — attach candidates in `handleMemSave`**

In `internal/mcp/tools.go`, replace the tail of `handleMemSave`:

```go
	f, err := a.Save(store.SaveInput{
		Type: in.Type, Title: in.Title, Body: in.Body,
		Project: in.Project, Scope: in.Scope, TopicKey: in.TopicKey, Tags: in.Tags,
		Pinned: in.Pinned,
	})
	if err != nil {
		return nil, err
	}
	return factView(f), nil
}
```

with:

```go
	f, err := a.Save(store.SaveInput{
		Type: in.Type, Title: in.Title, Body: in.Body,
		Project: in.Project, Scope: in.Scope, TopicKey: in.TopicKey, Tags: in.Tags,
		Pinned: in.Pinned,
	})
	if err != nil {
		return nil, err
	}
	view := factView(f)
	// Lexical hint: surface existing similar, not-yet-linked facts so the agent can
	// link/reconcile (mem_link conflicts-with/supersedes, or re-save with the same
	// topic_key) instead of silently duplicating or contradicting. A hint failure
	// must never fail the save, so the error is intentionally ignored.
	if cands, cerr := a.Candidates(f.ID, 5); cerr == nil && len(cands) > 0 {
		view["related"] = cands
	}
	return view, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestMemSaveSurfacesRelatedCandidates`
Expected: PASS.

- [ ] **Step 5: Run the full mcp suite (no regressions)**

Run: `go build ./... && go test ./internal/mcp/`
Expected: build clean; package PASS. (Existing `mem_save` tests still pass — `related` is additive and absent when there are no candidates.)

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): mem_save surfaces related not-yet-linked facts (on-save dup/conflict hint)"
```

---

## Self-Review

**Spec coverage:**
- Return similar not-yet-linked facts on save → Step 3 `Candidates(f.ID, 5)` + `related`. ✓
- No LLM on save path → only `Candidates` (FTS), no `$BBRAIN_AGENT_CLI`. ✓
- Hint failure never fails save → error ignored, `related` omitted. ✓
- Frontier (only `mcp/tools.go` + test) → both edits there; `app.Save` signature untouched. ✓
- Reuse existing `Candidates` / shape as `mem_candidates` → `[]index.Result` raw. ✓

**Placeholder scan:** none — exact code and commands throughout. (Note: the "C has no related" assertion depends on FTS5 not matching disjoint vocabulary; if a future tokenizer change makes it match, tighten C's wording — the core A→B assertion is the load-bearing one.)

**Type consistency:** `Candidates(id string, limit int) ([]index.Result, error)` and `factView(...) map[string]any` are used exactly as defined in the codebase; `view` is the `map[string]any` from `factView`, so `view["related"] = cands` is valid.
