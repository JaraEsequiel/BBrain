# UserPromptSubmit Hook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an active per-message hook to BBrain that forces tool loading on the first message of a session and nudges to save when a project has gone >15 min without a `mem_save`.

**Architecture:** A new `bbrain prompt-submit` subcommand reads the Claude Code UserPromptSubmit payload from stdin and emits a `{"systemMessage": ...}` (or `{}`) to stdout. A pure `decide()` function holds the policy; an IO wrapper resolves session statefiles (in `$TMPDIR`) and the project's last-save timestamp (from the FTS index, which gains an `updated_at` column). The hook is registered in `settings.json` alongside the existing `SessionStart` hook.

**Tech Stack:** Go 1.25, stdlib only (`encoding/json`, `os`, `time`, `database/sql` via the existing `modernc.org/sqlite`).

## Global Constraints

- Module `bbrain`, Go 1.25. Stdlib-first; add NO new dependencies.
- The hook MUST always `exit 0` and print valid JSON. Any internal error degrades to `{}` — never block the user's message.
- The `.md` files are the source of truth; the FTS index is derived and rebuildable. Never treat the index as truth.
- Link relations and existing public signatures are unchanged by this work.
- Branch first: we are on `master`. Do all work on `feat/user-prompt-submit-hook`.
- Injected message texts are frozen in the spec (`docs/superpowers/specs/2026-06-24-bbrain-userpromptsubmit-hook-design.md`) — copy them verbatim, no backticks (they ship as JSON string values).

---

## File Structure

- `internal/index/index.go` (modify) — add `updated_at`/`created_at` columns, `LastSavedAt(project)`, `Reset()`; delete now-dead `Clear()`.
- `internal/index/index_test.go` (modify) — tests for `LastSavedAt` and `Reset`.
- `internal/app/app.go` (modify) — `Reindex` calls `Reset()` instead of `Clear()`.
- `internal/prompthook/prompthook.go` (create) — `decide()` pure policy + message/threshold consts.
- `internal/prompthook/prompthook_test.go` (create) — table tests for `decide()`.
- `internal/prompthook/run.go` (create) — `Run()` IO wrapper, project detection, statefiles, index read.
- `internal/prompthook/run_test.go` (create) — first-message + project-detection tests.
- `internal/setup/setup.go` (modify) — `UserPromptSubmitHookEntry`, generalized `isBBrainHook`, `MergeSettingsHook`, `RemoveSettingsHook`.
- `internal/setup/setup_test.go` (modify) — both-hooks merge/remove/idempotency tests.
- `cmd/bbrain/main.go` (modify) — `prompt-submit` dispatch + `cmdPromptSubmit` + usage string.
- `cmd/bbrain/main_test.go` (modify) — end-to-end first-message test.

`internal/install/install.go` needs **no change**: it already calls `setup.MergeSettingsHook` / `setup.RemoveSettingsHook`, which Task 5 generalizes to manage both hooks.

---

### Task 1: Index gains `updated_at`/`created_at` + `LastSavedAt`

**Files:**
- Modify: `internal/index/index.go` (schema const ~L23-34; `IndexFact` ~L77-85)
- Test: `internal/index/index_test.go`

**Interfaces:**
- Produces: `func (ix *Index) LastSavedAt(project string) (string, bool, error)` — returns the max `updated_at` (raw RFC3339 string) among facts whose `project` matches exactly, and whether any exist.

- [ ] **Step 1: Write the failing test**

Add to `internal/index/index_test.go` (package `index`):

```go
func TestLastSavedAt(t *testing.T) {
	ix, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	mk := func(id, project, updated string) fact.Fact {
		return fact.Fact{ID: id, Title: "t", Project: project, UpdatedAt: updated, CreatedAt: updated}
	}
	for _, f := range []fact.Fact{
		mk("a", "BBrain", "2026-06-24T10:00:00Z"),
		mk("b", "BBrain", "2026-06-24T12:00:00Z"),
		mk("c", "Other", "2026-06-24T15:00:00Z"),
	} {
		if err := ix.IndexFact(f, f.ID+".md"); err != nil {
			t.Fatal(err)
		}
	}

	ts, ok, err := ix.LastSavedAt("BBrain")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || ts != "2026-06-24T12:00:00Z" {
		t.Fatalf("LastSavedAt(BBrain) = %q,%v; want 2026-06-24T12:00:00Z,true", ts, ok)
	}
	if _, ok, _ := ix.LastSavedAt("Nope"); ok {
		t.Fatal("LastSavedAt(Nope) ok=true; want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/index/ -run TestLastSavedAt`
Expected: FAIL — `ix.LastSavedAt undefined` (compile error).

- [ ] **Step 3: Add the columns to the schema**

In `internal/index/index.go`, replace the `facts_fts` schema body's last column line so the table ends with the two new UNINDEXED columns:

```go
const schema = `
CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(
	fact_id UNINDEXED,
	path UNINDEXED,
	title,
	body,
	tags,
	topic_key,
	type UNINDEXED,
	scope UNINDEXED,
	project UNINDEXED,
	updated_at UNINDEXED,
	created_at UNINDEXED
);`
```

- [ ] **Step 4: Write the new columns in `IndexFact`**

Replace the `INSERT INTO facts_fts (...)` statement in `IndexFact`:

```go
	if _, err := tx.Exec(
		`INSERT INTO facts_fts (fact_id, path, title, body, tags, topic_key, type, scope, project, updated_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, path, f.Title, f.Body, strings.Join(f.Tags, " "), f.TopicKey,
		f.Type, f.Scope, f.Project, f.UpdatedAt, f.CreatedAt,
	); err != nil {
		tx.Rollback()
		return err
	}
```

- [ ] **Step 5: Add `LastSavedAt`**

Add after `IndexFact` (the `database/sql` package is already imported):

```go
// LastSavedAt returns the most recent updated_at among facts in project, and
// whether any exist. The timestamp is the raw RFC3339 string stored on the
// fact. project is matched exactly (a global/empty-project fact does not count).
func (ix *Index) LastSavedAt(project string) (string, bool, error) {
	var ts sql.NullString
	err := ix.db.QueryRow(
		`SELECT max(updated_at) FROM facts_fts WHERE project = ?`, project,
	).Scan(&ts)
	if err != nil {
		return "", false, err
	}
	if !ts.Valid || ts.String == "" {
		return "", false, nil
	}
	return ts.String, true, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/index/ -run TestLastSavedAt`
Expected: PASS.

- [ ] **Step 7: Run the full index suite (no regressions)**

Run: `go test ./internal/index/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/index/index.go internal/index/index_test.go
git commit -m "feat(index): store updated_at/created_at, add LastSavedAt(project)"
```

---

### Task 2: `Reset()` recreates the table; `Reindex` migrates schema

**Files:**
- Modify: `internal/index/index.go` (add `Reset`, delete `Clear`)
- Modify: `internal/app/app.go:67` (`Reindex` uses `Reset`)
- Test: `internal/index/index_test.go`

**Interfaces:**
- Produces: `func (ix *Index) Reset() error` — drops and recreates both derived tables so a schema change in `schema` takes effect; callers repopulate with `IndexFact`.

- [ ] **Step 1: Write the failing test**

Add to `internal/index/index_test.go`:

```go
func TestResetRecreatesSchema(t *testing.T) {
	ix, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	if err := ix.IndexFact(fact.Fact{ID: "a", Project: "P", UpdatedAt: "2026-06-24T10:00:00Z"}, "a.md"); err != nil {
		t.Fatal(err)
	}
	if err := ix.Reset(); err != nil {
		t.Fatal(err)
	}
	// Reset clears all rows...
	if _, ok, _ := ix.LastSavedAt("P"); ok {
		t.Fatal("after Reset, expected no rows")
	}
	// ...and leaves a usable, current-schema table behind.
	if err := ix.IndexFact(fact.Fact{ID: "b", Project: "P", UpdatedAt: "2026-06-24T11:00:00Z"}, "b.md"); err != nil {
		t.Fatalf("IndexFact after Reset: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/index/ -run TestResetRecreatesSchema`
Expected: FAIL — `ix.Reset undefined`.

- [ ] **Step 3: Add `Reset` and delete `Clear`**

First confirm `Clear` has exactly one external caller (it becomes dead after this task):

Run: `grep -rn "\.Clear()" internal/ cmd/`
Expected: only `internal/app/app.go:67`.

Add `Reset` in `internal/index/index.go` and remove the `Clear` method:

```go
// Reset drops and recreates the derived tables so a schema change in `schema`
// takes effect. Unlike a row-wipe, this migrates the table definition; callers
// repopulate via IndexFact. The index is derived from the .md files, so dropping
// it loses nothing.
func (ix *Index) Reset() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS facts_fts`,
		`DROP TABLE IF EXISTS links`,
		schema, linksSchema,
	} {
		if _, err := ix.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
```

Delete the entire `func (ix *Index) Clear() error { ... }` method.

- [ ] **Step 4: Point `Reindex` at `Reset`**

In `internal/app/app.go`, inside `Reindex`, replace the clear call:

```go
	if err := ix.Reset(); err != nil {
		return 0, err
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/index/ ./internal/app/`
Expected: PASS (index Reset test passes; app Reindex tests still pass via Reset).

- [ ] **Step 6: Commit**

```bash
git add internal/index/index.go internal/index/index_test.go internal/app/app.go
git commit -m "feat(index): Reset() recreates tables; Reindex migrates schema"
```

---

### Task 3: `decide()` pure policy

**Files:**
- Create: `internal/prompthook/prompthook.go`
- Test: `internal/prompthook/prompthook_test.go`

**Interfaces:**
- Produces:
  - `type DecideInput struct { FirstMessage bool; SessionAge, SinceLastSave, SinceLastNudge time.Duration; HasLastSave, HasLastNudge bool }`
  - `type DecideOutput struct { Message string; DidNudge bool }`
  - `func decide(in DecideInput) DecideOutput` (unexported; used by `run.go` and tests in-package)
  - consts `toolSearchMsg`, `nudgeMsg` (unexported)

- [ ] **Step 1: Write the failing test**

Create `internal/prompthook/prompthook_test.go`:

```go
package prompthook

import (
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		name      string
		in        DecideInput
		wantMsg   string
		wantNudge bool
	}{
		{"first message forces toolsearch", DecideInput{FirstMessage: true}, toolSearchMsg, false},
		{"young session is silent", DecideInput{SessionAge: 2 * time.Minute}, "", false},
		{"within cooldown is silent", DecideInput{SessionAge: 10 * time.Minute, HasLastNudge: true, SinceLastNudge: 5 * time.Minute, HasLastSave: true, SinceLastSave: 30 * time.Minute}, "", false},
		{"no facts is silent", DecideInput{SessionAge: 10 * time.Minute, HasLastSave: false}, "", false},
		{"recent save is silent", DecideInput{SessionAge: 10 * time.Minute, HasLastSave: true, SinceLastSave: 5 * time.Minute}, "", false},
		{"stale save nudges", DecideInput{SessionAge: 10 * time.Minute, HasLastSave: true, SinceLastSave: 30 * time.Minute}, nudgeMsg, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decide(c.in)
			if got.Message != c.wantMsg || got.DidNudge != c.wantNudge {
				t.Fatalf("decide(%+v) = {%q,%v}; want {%q,%v}", c.in, got.Message, got.DidNudge, c.wantMsg, c.wantNudge)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompthook/`
Expected: FAIL — package/symbols do not exist.

- [ ] **Step 3: Write the implementation**

Create `internal/prompthook/prompthook.go`:

```go
// Package prompthook implements `bbrain prompt-submit`, the Claude Code
// UserPromptSubmit hook: it forces tool loading on the first message of a
// session and nudges to save when the current project has gone stale.
package prompthook

import "time"

const (
	sessionMinAge = 5 * time.Minute  // below this, the session is too new to nudge
	saveStale     = 15 * time.Minute // no save in this long → eligible to nudge
	nudgeCooldown = 15 * time.Minute // do not repeat a nudge within this window
)

const toolSearchMsg = "FIRST ACTION — before responding, run this ToolSearch once to load BBrain's memory tools (they are deferred and not yet callable):\nselect:mcp__bbrain__mem_save,mcp__bbrain__mem_search,mcp__bbrain__mem_get,mcp__bbrain__mem_delete,mcp__bbrain__mem_link,mcp__bbrain__mem_why,mcp__bbrain__mem_related,mcp__bbrain__mem_candidates,mcp__bbrain__mem_current_project,mcp__bbrain__wiki_build,mcp__bbrain__wiki_link,mcp__bbrain__wiki_lint"

const nudgeMsg = "MEMORY CHECK — over 15 minutes since your last save to this project. If anything since then is worth remembering (a decision, discovery, fixed bug, or fact about the user), call mem_save now. If nothing is, ignore this and continue."

// DecideInput is everything the IO layer resolved; decide() does no IO.
type DecideInput struct {
	FirstMessage   bool
	SessionAge     time.Duration
	SinceLastSave  time.Duration
	HasLastSave    bool
	SinceLastNudge time.Duration
	HasLastNudge   bool
}

// DecideOutput is the systemMessage to emit (empty = inject nothing) and whether
// the caller must record a fresh nudge timestamp.
type DecideOutput struct {
	Message  string
	DidNudge bool
}

// decide applies the policy in cheapest-gate-first order. Gates 1-3 require no
// brain read, so the caller can skip resolving SinceLastSave until they pass.
func decide(in DecideInput) DecideOutput {
	if in.FirstMessage {
		return DecideOutput{Message: toolSearchMsg}
	}
	if in.SessionAge < sessionMinAge {
		return DecideOutput{}
	}
	if in.HasLastNudge && in.SinceLastNudge < nudgeCooldown {
		return DecideOutput{}
	}
	if !in.HasLastSave {
		return DecideOutput{}
	}
	if in.SinceLastSave <= saveStale {
		return DecideOutput{}
	}
	return DecideOutput{Message: nudgeMsg, DidNudge: true}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/prompthook/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/prompthook/prompthook.go internal/prompthook/prompthook_test.go
git commit -m "feat(prompthook): pure decide() policy for the prompt-submit hook"
```

---

### Task 4: `Run()` IO wrapper + project detection

**Files:**
- Create: `internal/prompthook/run.go`
- Test: `internal/prompthook/run_test.go`

**Interfaces:**
- Consumes: `decide`, `DecideInput` (Task 3); `index.Open`, `(*Index).LastSavedAt` (Task 1); `brain.New(root).IndexPath()`.
- Produces: `func Run(r io.Reader, w io.Writer, home string, now time.Time)` — reads the hook payload, writes the JSON result, never returns an error.

- [ ] **Step 1: Write the failing test**

Create `internal/prompthook/run_test.go`:

```go
package prompthook

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDetectProjectPrefersEnv(t *testing.T) {
	t.Setenv("BBRAIN_PROJECT", "explicit")
	if got := detectProject("/home/u/whatever"); got != "explicit" {
		t.Fatalf("detectProject = %q; want explicit", got)
	}
}

func TestDetectProjectFromCwd(t *testing.T) {
	os.Unsetenv("BBRAIN_PROJECT")
	if got := detectProject("/home/u/BBrain"); got != "BBrain" {
		t.Fatalf("detectProject = %q; want BBrain", got)
	}
}

func TestRunFirstMessageForcesToolSearch(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("BBRAIN_PROJECT", "P")

	var out bytes.Buffer
	Run(strings.NewReader(`{"session_id":"sess-1","cwd":"/x/P","prompt":"hi"}`), &out, t.TempDir(), time.Now())
	if !strings.Contains(out.String(), "FIRST ACTION") {
		t.Fatalf("first message must force ToolSearch; got %q", out.String())
	}

	// Second message of the same session, session still young → no injection.
	var out2 bytes.Buffer
	Run(strings.NewReader(`{"session_id":"sess-1","cwd":"/x/P","prompt":"more"}`), &out2, t.TempDir(), time.Now())
	if strings.TrimSpace(out2.String()) != "{}" {
		t.Fatalf("young session must be silent; got %q", out2.String())
	}
}

func TestRunBadJSONIsSafe(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	var out bytes.Buffer
	Run(strings.NewReader("not json"), &out, t.TempDir(), time.Now())
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("bad JSON must still emit valid JSON, not empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompthook/ -run TestRun`
Expected: FAIL — `Run`/`detectProject` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/prompthook/run.go`:

```go
package prompthook

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"bbrain/internal/brain"
	"bbrain/internal/index"
)

type hookInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`
}

// Run reads the UserPromptSubmit payload from r, decides whether to inject a
// systemMessage, and writes the JSON result to w. It never blocks the user's
// message: any failure degrades to "{}". home is the brain root; now is injected
// for testability.
func Run(r io.Reader, w io.Writer, home string, now time.Time) {
	out := "{}"
	defer func() { io.WriteString(w, out) }()

	data, err := io.ReadAll(r)
	if err != nil {
		return
	}
	var in hookInput
	_ = json.Unmarshal(data, &in) // bad JSON → zero values → safe defaults

	project := detectProject(in.Cwd)
	key := sessionKey(in.SessionID, project)
	toolsFile := filepath.Join(os.TempDir(), "bbrain-claude-"+key+"-tools-loaded")
	nudgeFile := filepath.Join(os.TempDir(), "bbrain-claude-"+key+"-last-nudge")

	// First message: the marker does not exist yet. Create it and force tools.
	fi, statErr := os.Stat(toolsFile)
	if statErr != nil {
		_ = os.WriteFile(toolsFile, nil, 0o644)
		out = encode(decide(DecideInput{FirstMessage: true}).Message)
		return
	}

	di := DecideInput{SessionAge: now.Sub(fi.ModTime())}

	if b, err := os.ReadFile(nudgeFile); err == nil {
		if epoch, perr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); perr == nil {
			di.SinceLastNudge = now.Sub(time.Unix(epoch, 0))
			di.HasLastNudge = true
		}
	}

	if ts, ok := lastSave(home, project); ok {
		if t, perr := time.Parse(time.RFC3339, ts); perr == nil {
			di.SinceLastSave = now.Sub(t)
			di.HasLastSave = true
		}
	}

	res := decide(di)
	if res.DidNudge {
		_ = os.WriteFile(nudgeFile, []byte(strconv.FormatInt(now.Unix(), 10)), 0o644)
	}
	out = encode(res.Message)
}

// detectProject mirrors mem_current_project: BBRAIN_PROJECT wins, else the cwd's
// basename.
func detectProject(cwd string) string {
	if p := os.Getenv("BBRAIN_PROJECT"); p != "" {
		return p
	}
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// sessionKey is a filesystem-safe key: the session id if present, else a
// project+pid fallback. Non [a-zA-Z0-9_-] runes become '_'.
func sessionKey(sessionID, project string) string {
	raw := sessionID
	if raw == "" {
		raw = project + "-" + strconv.Itoa(os.Getpid())
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// lastSave reads the project's most recent updated_at from the derived index in
// read-only spirit. Any error (db busy, column absent on a not-yet-reindexed
// brain) is treated as "unknown" so the hook stays silent.
func lastSave(home, project string) (string, bool) {
	ix, err := index.Open(brain.New(home).IndexPath())
	if err != nil {
		return "", false
	}
	defer ix.Close()
	ts, ok, err := ix.LastSavedAt(project)
	if err != nil {
		return "", false
	}
	return ts, ok
}

// encode wraps a message as the hook's JSON output. Empty message → "{}".
func encode(msg string) string {
	if msg == "" {
		return "{}"
	}
	b, err := json.Marshal(map[string]string{"systemMessage": msg})
	if err != nil {
		return "{}"
	}
	return string(b)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/prompthook/`
Expected: PASS (all decide + run tests).

- [ ] **Step 5: Commit**

```bash
git add internal/prompthook/run.go internal/prompthook/run_test.go
git commit -m "feat(prompthook): Run() IO wrapper, statefiles, project detection"
```

---

### Task 5: Register the hook in `settings.json`

**Files:**
- Modify: `internal/setup/setup.go` (`isBBrainHook` ~L209, `MergeSettingsHook` ~L236, `RemoveSettingsHook` ~L263; add `UserPromptSubmitHookEntry`)
- Test: `internal/setup/setup_test.go`

**Interfaces:**
- Produces: `func UserPromptSubmitHookEntry(memoryDir string) map[string]any`. `MergeSettingsHook`/`RemoveSettingsHook` now manage BOTH `SessionStart` and `UserPromptSubmit`.

- [ ] **Step 1: Write the failing test**

Add to `internal/setup/setup_test.go` (package `setup`):

```go
func TestMergeSettingsInstallsBothHooks(t *testing.T) {
	out, err := MergeSettingsHook(nil, "/mem")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"SessionStart", "UserPromptSubmit", "prompt-submit", "context"} {
		if !strings.Contains(s, want) {
			t.Fatalf("merged settings missing %q:\n%s", want, s)
		}
	}
	// Idempotent: a second merge must not duplicate the UserPromptSubmit entry.
	out2, err := MergeSettingsHook(out, "/mem")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out2), "prompt-submit"); n != 1 {
		t.Fatalf("re-merge produced %d prompt-submit entries; want 1:\n%s", n, out2)
	}
}

func TestRemoveSettingsStripsBothHooksKeepsForeign(t *testing.T) {
	seed := []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"other","args":["keepme"]}]}]}}`)
	merged, err := MergeSettingsHook(seed, "/mem")
	if err != nil {
		t.Fatal(err)
	}
	out, err := RemoveSettingsHook(merged)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "prompt-submit") || strings.Contains(s, `"context"`) {
		t.Fatalf("remove left BBrain hooks:\n%s", s)
	}
	if !strings.Contains(s, "keepme") {
		t.Fatalf("remove dropped a foreign hook:\n%s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/setup/ -run TestMergeSettingsInstallsBothHooks`
Expected: FAIL — merged settings has no `UserPromptSubmit`/`prompt-submit`.

- [ ] **Step 3: Add `UserPromptSubmitHookEntry`**

Add in `internal/setup/setup.go`, next to `SessionStartHookEntry`:

```go
// UserPromptSubmitHookEntry is the Claude Code UserPromptSubmit hook that runs
// "bbrain prompt-submit --home <memoryDir>" on every user message.
func UserPromptSubmitHookEntry(memoryDir string) map[string]any {
	return map[string]any{
		"hooks": []map[string]any{
			{"type": "command", "command": "bbrain", "args": []string{"prompt-submit", "--home", memoryDir}, "timeout": 10},
		},
	}
}
```

- [ ] **Step 4: Generalize `isBBrainHook` to match either verb**

Replace the verb check inside `isBBrainHook` (the inner `for _, a := range args` loop body) so it matches both subcommands:

```go
		if args, ok := hm["args"].([]any); ok {
			for _, a := range args {
				if a == "context" || a == "prompt-submit" {
					return true
				}
			}
		}
```

- [ ] **Step 5: Rewrite `MergeSettingsHook` to manage both arrays**

Replace the body of `MergeSettingsHook` from the `var kept []any` block to the end of the function with a helper-based version, and add the helper:

```go
	hooks["SessionStart"] = appendBBrainHook(hooks["SessionStart"], SessionStartHookEntry(memoryDir))
	hooks["UserPromptSubmit"] = appendBBrainHook(hooks["UserPromptSubmit"], UserPromptSubmitHookEntry(memoryDir))
	root["hooks"] = hooks
	return json.MarshalIndent(root, "", "  ")
}

// appendBBrainHook strips any existing BBrain entry from a hook array and appends
// entry, so re-running install never duplicates BBrain's hook.
func appendBBrainHook(arr any, entry map[string]any) []any {
	var kept []any
	if a, ok := arr.([]any); ok {
		for _, e := range a {
			if !isBBrainHook(e) {
				kept = append(kept, e)
			}
		}
	}
	return append(kept, entry)
}
```

(The lines being replaced are the old `var kept []any … return json.MarshalIndent(...)` tail of `MergeSettingsHook`; keep the function's `root`/`hooks` setup above it unchanged.)

- [ ] **Step 6: Rewrite `RemoveSettingsHook` to strip both arrays**

Replace the single `if arr, ok := hooks["SessionStart"].([]any); ok { ... }` block in `RemoveSettingsHook` with a loop over both hook names:

```go
	for _, name := range []string{"SessionStart", "UserPromptSubmit"} {
		arr, ok := hooks[name].([]any)
		if !ok {
			continue
		}
		var kept []any
		for _, e := range arr {
			if !isBBrainHook(e) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(hooks, name)
		} else {
			hooks[name] = kept
		}
	}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/setup/ ./internal/install/`
Expected: PASS (new both-hooks tests + existing SessionStart tests still green; install round-trips both).

- [ ] **Step 8: Commit**

```bash
git add internal/setup/setup.go internal/setup/setup_test.go
git commit -m "feat(setup): register UserPromptSubmit hook alongside SessionStart"
```

---

### Task 6: Wire the `prompt-submit` subcommand

**Files:**
- Modify: `cmd/bbrain/main.go` (usage string ~L45; dispatch switch ~L91-94; add `cmdPromptSubmit`)
- Test: `cmd/bbrain/main_test.go`

**Interfaces:**
- Consumes: `prompthook.Run` (Task 4).

- [ ] **Step 1: Write the failing test**

Add to `cmd/bbrain/main_test.go` (package `main`):

```go
func TestPromptSubmitFirstMessage(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("BBRAIN_HOME", t.TempDir())
	t.Setenv("BBRAIN_PROJECT", "P")

	var out bytes.Buffer
	in := strings.NewReader(`{"session_id":"cli-1","cwd":"/x/P"}`)
	code := runWithIn([]string{"prompt-submit"}, in, &out, io.Discard)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if !strings.Contains(out.String(), "FIRST ACTION") {
		t.Fatalf("want forced ToolSearch; got %q", out.String())
	}
}
```

(If `bytes`, `io`, or `strings` are not already imported in `main_test.go`, add them.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bbrain/ -run TestPromptSubmitFirstMessage`
Expected: FAIL — `unknown command: prompt-submit` (exit 2, no "FIRST ACTION").

- [ ] **Step 3: Add the dispatch case**

In `runWithIn`'s switch, add alongside the other stdin-consuming cases:

```go
	case "prompt-submit":
		return cmdPromptSubmit(args[1:], stdin, stdout)
```

- [ ] **Step 4: Add `prompt-submit` to the usage string**

Replace the usage line so it lists the new verb:

```go
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex|link|why|related|candidates|wiki|install|uninstall|context|prompt-submit|watch|vault|mcp> [args]")
```

- [ ] **Step 5: Implement `cmdPromptSubmit`**

Add near `cmdContext`, and add `"bbrain/internal/prompthook"` to the import block:

```go
func cmdPromptSubmit(args []string, stdin io.Reader, stdout io.Writer) int {
	fs := flag.NewFlagSet("prompt-submit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	if err := fs.Parse(args); err != nil {
		// Never block the message on a flag error: emit a no-op and exit 0.
		io.WriteString(stdout, "{}")
		return 0
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	prompthook.Run(stdin, stdout, root, time.Now())
	return 0
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./cmd/bbrain/`
Expected: PASS.

- [ ] **Step 7: Full build + suite**

Run: `go build ./... && go test ./...`
Expected: build clean; all packages PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/bbrain/main.go cmd/bbrain/main_test.go
git commit -m "feat(cli): wire bbrain prompt-submit subcommand"
```

---

## Post-implementation: migrate the live brain

After the binary is rebuilt, the existing `~/.bbrain/default/.bbrain/index.db` still has the old schema (no `updated_at`). One command migrates it (Task 2 made `reindex` recreate the table):

```bash
go build -o bbrain ./cmd/bbrain
./bbrain reindex            # recreates facts_fts with the new columns
./bbrain install            # re-writes settings.json with both hooks
```

Until `reindex` runs, the hook degrades to `{}` on the nudge path (column absent → unknown) — it never errors.

---

## Self-Review

**Spec coverage:**
- First-message forced ToolSearch → Task 3 (`toolSearchMsg`) + Task 4 (`FirstMessage` path) + Task 6 (subcommand). ✓
- Project-scoped nudge, strict match → Task 1 (`LastSavedAt` exact project) + Task 3 (gates). ✓
- Thresholds 5/15/900 hardcoded → Task 3 consts. ✓
- Statefiles in `$TMPDIR`, session_id key + fallback → Task 4 (`sessionKey`). ✓
- `updated_at`+`created_at` columns in one migration → Task 1. ✓
- `reindex` recreates table → Task 2. ✓
- Registration generalized (both hooks, idempotent, remove) → Task 5. ✓
- Always exit 0 / valid JSON / never block → Task 4 (`Run` deferred write, degrade-to-`{}`) + Task 6 (flag-error path). ✓
- Exact message texts (no backticks, JSON string) → Task 3 consts copied verbatim from spec; `encode` JSON-escapes. ✓

**Placeholder scan:** none — every step has runnable code/commands.

**Type consistency:** `DecideInput`/`DecideOutput` fields (`Message`, `DidNudge`, `HasLastSave`, `HasLastNudge`, `SinceLastSave`, `SinceLastNudge`, `SessionAge`, `FirstMessage`) are used identically in Tasks 3, 4, 6. `LastSavedAt(project) (string, bool, error)` defined in Task 1, consumed in Task 4. `Reset()` defined Task 2, consumed by `Reindex`. `UserPromptSubmitHookEntry` defined Task 5, used by `MergeSettingsHook`.
