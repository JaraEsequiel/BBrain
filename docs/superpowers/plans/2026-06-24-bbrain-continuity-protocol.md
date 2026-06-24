# Continuity Protocol (Gap 1+2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make BBrain re-inject brain context after `clear`/`compact` and instruct the agent to save a handoff summary at session close and after compaction — all in `internal/setup/setup.go`, reusing `mem_save`/`mem_search`.

**Architecture:** Two edits to `setup.go`: widen the `SessionStart` hook matcher to also fire on `clear|compact`, and append two sections (SESSION CLOSE, POST-COMPACTION) to the managed `ClaudeMDBlock`. No new tools, no session model, no other files. (Track A — disjoint from Spec B, which lives in `internal/mcp/tools.go`.)

**Tech Stack:** Go 1.25, stdlib only.

## Global Constraints

- Module `bbrain`, Go 1.25, stdlib-only — add NO new dependencies.
- `ClaudeMDBlock` is a Go raw-string literal (backticks) — the protocol text MUST NOT contain backticks.
- Session summaries are plain `mem_save` facts with `type: "session-summary"` — do NOT add session tools or a session model.
- Do NOT touch any file other than `internal/setup/setup.go` and `internal/setup/setup_test.go` (frontier with Spec B).
- Branch first: we are on `master`. Do the work on `feat/continuity-protocol`, and include the two new spec files + this plan in the branch's groundwork commit.
- The two protocol texts are frozen in the spec (`docs/superpowers/specs/2026-06-24-bbrain-continuity-protocol-design.md`) — copy them verbatim.

---

### Task 1: Matcher widening + SESSION CLOSE / POST-COMPACTION text

**Files:**
- Modify: `internal/setup/setup.go` — `SessionStartHookEntry` matcher (line ~200); `ClaudeMDBlock` tail (lines ~152-154)
- Test: `internal/setup/setup_test.go` — `TestClaudeMDBlockMentionsToolsAndMarkers` (~line 78), `TestSessionStartHookAndMerge` (~line 157)

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: no new exported symbols — behavior change only (wider matcher + two protocol sections in the managed block).

- [ ] **Step 1: Write the failing test assertions**

In `internal/setup/setup_test.go`, extend the `want` slice in `TestClaudeMDBlockMentionsToolsAndMarkers` to require the two new sections:

```go
	for _, want := range []string{BlockBegin, BlockEnd, "mcp__bbrain__mem_save", "mcp__bbrain__wiki_build", "/adapter.sh", "ToolSearch", "SESSION CLOSE", "POST-COMPACTION"} {
```

And in `TestSessionStartHookAndMerge`, extend the hook-JSON `want` slice to require the wider matcher (the marshaled SessionStart entry includes the matcher string):

```go
	for _, want := range []string{`"bbrain"`, `"context"`, `"--home"`, `/v/memory`, "compact", "clear"} {
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/setup/ -run 'TestClaudeMDBlockMentionsToolsAndMarkers|TestSessionStartHookAndMerge'`
Expected: FAIL — `block missing "SESSION CLOSE"` and `hook missing "compact"`.

- [ ] **Step 3: Widen the SessionStart matcher**

In `internal/setup/setup.go`, in `SessionStartHookEntry`, change the matcher:

```go
		"matcher": "startup|resume|clear|compact",
```

- [ ] **Step 4: Append the two protocol sections to `ClaudeMDBlock`**

In `internal/setup/setup.go`, insert the two sections between the FORMAT block and the wiki-backend line. Replace this exact text:

```go
**Learned**: surprises, edge cases, or things to remember (omit if none)

The wiki LLM backend is $BBRAIN_AGENT_CLI -> ` + adapterPath + `. Workflow: build -> link -> rebuild; wiki_lint --fix for consistency.
```

with:

```go
**Learned**: surprises, edge cases, or things to remember (omit if none)

### SESSION CLOSE — before you say "done", if the session did real work (decisions, code, fixes, discoveries — not trivial chat or a single lookup):

Call mem_save ONCE, type "session-summary", with a handoff:
- Goal: what this session set out to do
- Accomplished: what got done, with key details
- Next steps: what's left for the next session
- Relevant files: paths touched and why

If nothing is worth handing off, skip it — do not save filler.

### POST-COMPACTION — if you see a context compaction or reset, do this FIRST, in order, before continuing:

1. mem_save the compacted summary as a session-summary fact (same fields as SESSION CLOSE) — preserve what was accomplished before the context was cut.
2. mem_search with keywords from the user's current task to pull back the facts you just lost.
3. Only THEN continue the user's request.

Skipping these means working blind.

The wiki LLM backend is $BBRAIN_AGENT_CLI -> ` + adapterPath + `. Workflow: build -> link -> rebuild; wiki_lint --fix for consistency.
```

(Note: no backticks anywhere in the inserted text — it lives inside a Go raw-string literal.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/setup/ -run 'TestClaudeMDBlockMentionsToolsAndMarkers|TestSessionStartHookAndMerge'`
Expected: PASS.

- [ ] **Step 6: Run the full setup + install suites (no regressions)**

Run: `go build ./... && go test ./internal/setup/ ./internal/install/`
Expected: build clean; both packages PASS. (Idempotency of the settings merge is unaffected — `isBBrainHook` matches by command+verb `context`, not by matcher.)

- [ ] **Step 7: Commit**

```bash
git add internal/setup/setup.go internal/setup/setup_test.go
git commit -m "feat(setup): re-inject context on clear/compact; add SESSION CLOSE + POST-COMPACTION protocol"
```

---

## Post-implementation: propagate to the live install

The change lives in the binary; an existing install still has the old block + matcher. Refresh both:

```bash
go build -o bbrain ./cmd/bbrain
./bbrain install            # rewrites the managed CLAUDE.md block + settings.json hook (new matcher)
# new Claude session picks up both
```

---

## Self-Review

**Spec coverage:**
- Gap 1 (re-inject on clear/compact) → Step 3 matcher widening. ✓
- Gap 1 "what to do" + Gap 2 (session close) → Step 4 SESSION CLOSE + POST-COMPACTION text. ✓
- Reuse mem_save/mem_search, type "session-summary", no session model → text only, no new tools. ✓
- Frontier (only setup.go) → all edits in setup.go/setup_test.go. ✓
- Verbatim protocol text → Step 4 copied from the spec. ✓

**Placeholder scan:** none — every step has exact code/commands.

**Type consistency:** no new symbols; the only identifiers touched (`SessionStartHookEntry`, `ClaudeMDBlock`, `isBBrainHook`, the two test functions) are pre-existing and used consistently.
