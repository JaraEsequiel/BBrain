# BBrain — Plan 1: Core Memory Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the foundational BBrain engine: a Go binary that initializes a "brain", saves memories as `.md` files (source of truth) under `raws/facts/`, and maintains a derived, disposable FTS5 index for search — fully testable via CLI.

**Architecture:** New Go project (engram as reference, not a fork). The `.md` files are the source of truth; the SQLite/FTS5 index is derived and rebuildable from disk. Packages are split by responsibility: `fact` (parse/serialize the `.md` format), `brain` (locate/init the brain on disk), `store` (write/read fact files), `index` (FTS5 search), wired by `cmd/bbrain`.

**Tech Stack:** Go 1.25, `modernc.org/sqlite` (pure-Go SQLite + FTS5, no cgo), `gopkg.in/yaml.v3` (frontmatter), `github.com/natefinch/atomic` (atomic file writes). Single static binary, zero runtime deps.

## Global Constraints

- **Module path:** `bbrain` (local module; all imports are `bbrain/internal/...`).
- **Go version:** `go 1.25` in `go.mod`.
- **New project root:** `/home/vex/Projects/BBrain/` (the existing `engram/` subdir is reference only — never import from it).
- **`.md` is source of truth.** The index must be 100% reconstructable from the `.md` files. No data may live only in the index.
- **SQLite driver:** `modernc.org/sqlite`, registered driver name `"sqlite"`. FTS5 is built in.
- **Atomic writes:** every `.md` write goes through `atomic.WriteFile` so a crash never leaves a half-written memory.
- **Timestamps:** RFC3339 UTC (`time.Now().UTC().Format(time.RFC3339)`). All time access goes through an injectable `Now func() time.Time` so tests are deterministic.
- **Fact `type` vocabulary:** `decision|architecture|bugfix|pattern|config|discovery|learning`.
- **Fact `scope` vocabulary:** `project|personal|global`.
- **Commit after every task.** Run `go test ./...` green before each commit.

---

## Roadmap (this plan is #1 of a sequence)

Each plan produces working, testable software on its own. We write the next plan only when this one is done and reviewed.

1. **Plan 1 — Core memory engine (THIS PLAN):** brain init, save fact `.md`, FTS index, search, reindex. CLI only.
2. **Plan 2 — Reasoned wikilink graph:** `links:` parsing into the index, `mem_link`, graph queries ("why is A related to B"), conflict surfacing (FindCandidates by FTS).
3. **Plan 3 — Wiki layer + pluggable LLM routine:** `wiki/` ingest/lint orchestration, `wiki_build`/`wiki_lint`, `wiki/index.md` + `wiki/log.md` maintenance.
4. **Plan 4 — MCP server + tools:** `bbrain mcp` (stdio) exposing `mem_save`, `mem_search`, `mem_context`, `mem_get`, `mem_update`, `mem_delete`, sessions, `mem_current_project`, `wiki_*`.
5. **Plan 5 — TUI install + agent integration:** `bbrain init`/`bbrain setup <agent>` TUI, Claude Code hooks plugin, managed `BEGIN/END` block for hookless agents, watcher-driven auto-reindex.
6. **Plan 6 — Relocatable memory vault (after the TUI):** `bbrain vault move <dest>` moves the whole brain to a user-chosen location, updates the persistent location pointer the TUI introduced (Plan 5), reindexes (the FTS `path` column holds absolute paths, so a move requires a rebuild — clean because the index is disposable), and refreshes any agent-integration blocks/hooks that embed the old path. Depends on Plan 5 because it reuses that persistent location config and the agent-integration plumbing.

---

## File Structure (Plan 1)

- `go.mod` — module `bbrain`, Go 1.25, deps.
- `cmd/bbrain/main.go` — CLI entrypoint + command dispatch (`version`, `init`, `save`, `search`, `reindex`).
- `internal/fact/fact.go` — `Fact`/`Link` types, `Marshal`, `Parse`, `Slug`, `NewID`.
- `internal/fact/fact_test.go` — round-trip, parse, slug tests.
- `internal/brain/brain.go` — locate/resolve the brain dir, `Init` (create structure), path helpers.
- `internal/brain/brain_test.go` — init + path tests.
- `internal/store/store.go` — `Store` over the brain: `WriteFact` (upsert + dedup), `ListFacts`.
- `internal/store/store_test.go` — write/upsert/dedup/list tests.
- `internal/index/index.go` — `Index` (FTS5): `Open`, `IndexFact`, `Search`, `Clear`, `Close`.
- `internal/index/index_test.go` — index + search tests.
- `internal/app/app.go` — `Reindex` (rebuild index from store) + `App` glue used by CLI.
- `internal/app/app_test.go` — reindex integration test.

---

## Task 1: Project scaffolding + `version` command

**Files:**
- Create: `go.mod`
- Create: `cmd/bbrain/main.go`
- Test: `cmd/bbrain/main_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `main()` dispatching `os.Args[1]` to subcommands; `run(args []string, stdout, stderr io.Writer) int` for testability; constant `version = "0.1.0-dev"`.

- [ ] **Step 1: Write the failing test**

Create `cmd/bbrain/main_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"version"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), version) {
		t.Fatalf("stdout = %q, want it to contain version %q", out.String(), version)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"frobnicate"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero for unknown command")
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("stderr = %q, want it to mention 'unknown command'", errOut.String())
	}
}
```

- [ ] **Step 2: Create `go.mod`**

```
module bbrain

go 1.25
```

- [ ] **Step 3: Write minimal `cmd/bbrain/main.go`**

```go
package main

import (
	"fmt"
	"io"
	"os"
)

const version = "0.1.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a subcommand and returns a process exit code. It is the
// testable core of main: all I/O goes through the provided writers.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bbrain <command> [args]")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "bbrain "+version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./cmd/...`
Expected: PASS (both tests).

- [ ] **Step 5: Verify it builds and runs**

Run: `cd /home/vex/Projects/BBrain && go build ./cmd/bbrain && ./bbrain version`
Expected: prints `bbrain 0.1.0-dev`.

- [ ] **Step 6: Commit**

```bash
cd /home/vex/Projects/BBrain
echo "/bbrain" > .gitignore
git init -q 2>/dev/null || true
git add go.mod cmd/bbrain/main.go cmd/bbrain/main_test.go .gitignore
git commit -m "feat: scaffold bbrain CLI with version command"
```

---

## Task 2: `fact` package — `.md` parse/serialize + slug/id

**Files:**
- Create: `internal/fact/fact.go`
- Test: `internal/fact/fact_test.go`

**Interfaces:**
- Consumes: `gopkg.in/yaml.v3`.
- Produces:
  - `type Link struct { Target, Relation, Why string }`
  - `type Fact struct { ID, Type, Scope, Project, TopicKey string; Tags []string; Links []Link; CreatedAt, UpdatedAt string; RevisionCount int; Title, Body string }`
  - `func Marshal(f Fact) string` — serializes to frontmatter + `# Title` + body.
  - `func Parse(s string) (Fact, error)` — inverse of `Marshal`.
  - `func Slug(title string) string` — kebab-case slug.
  - `func NewID(date, title string) string` — `"<date>-<slug>"`.

- [ ] **Step 1: Add the yaml dependency**

Run: `cd /home/vex/Projects/BBrain && go get gopkg.in/yaml.v3@v3.0.1`
Expected: adds `gopkg.in/yaml.v3` to `go.mod`.

- [ ] **Step 2: Write the failing test**

Create `internal/fact/fact_test.go`:

```go
package fact

import "testing"

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Use JWT with refresh tokens for auth": "use-jwt-with-refresh-tokens-for-auth",
		"  Trim & Symbols!! ":                  "trim-symbols",
		"Postgres vs MySQL":                    "postgres-vs-mysql",
		"":                                     "untitled",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewID(t *testing.T) {
	got := NewID("2026-06-22", "Use JWT for auth")
	want := "2026-06-22-use-jwt-for-auth"
	if got != want {
		t.Fatalf("NewID = %q, want %q", got, want)
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	f := Fact{
		ID:       "2026-06-22-use-jwt-for-auth",
		Type:     "decision",
		Scope:    "project",
		Project:  "bbrain",
		TopicKey: "architecture/auth-model",
		Tags:     []string{"auth", "security"},
		Links: []Link{
			{Target: "[[postgres-vs-mysql]]", Relation: "supersedes", Why: "replaces prior DB choice"},
		},
		CreatedAt:     "2026-06-22T17:57:00Z",
		UpdatedAt:     "2026-06-22T17:57:00Z",
		RevisionCount: 1,
		Title:         "Use JWT with refresh tokens for auth",
		Body:          "**What:** use JWT\n**Why:** stateless",
	}

	got, err := Parse(Marshal(f))
	if err != nil {
		t.Fatalf("Parse(Marshal(f)) error: %v", err)
	}
	if got.Title != f.Title {
		t.Errorf("Title = %q, want %q", got.Title, f.Title)
	}
	if got.Body != f.Body {
		t.Errorf("Body = %q, want %q", got.Body, f.Body)
	}
	if got.TopicKey != f.TopicKey || got.Type != f.Type || got.Scope != f.Scope {
		t.Errorf("frontmatter scalars mismatch: %+v", got)
	}
	if len(got.Links) != 1 || got.Links[0].Why != "replaces prior DB choice" {
		t.Errorf("Links not round-tripped: %+v", got.Links)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "auth" {
		t.Errorf("Tags not round-tripped: %+v", got.Tags)
	}
}

func TestParseRejectsMissingFrontmatter(t *testing.T) {
	if _, err := Parse("# Just a title\n\nno frontmatter"); err == nil {
		t.Fatal("Parse should error when frontmatter delimiters are missing")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/fact/`
Expected: FAIL (package does not compile — `Slug`, `NewID`, `Marshal`, `Parse` undefined).

- [ ] **Step 4: Write `internal/fact/fact.go`**

```go
// Package fact defines the on-disk Markdown memory format and its (de)serialization.
// The .md file is the source of truth; this package is the only place that knows
// how a Fact is laid out as frontmatter + an H1 title + a body.
package fact

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Link is a reasoned, typed wikilink between two facts.
type Link struct {
	Target   string `yaml:"target"`
	Relation string `yaml:"relation"`
	Why      string `yaml:"why"`
}

// Fact is one memory. Frontmatter fields carry the structured metadata; Title and
// Body are derived from the markdown content below the frontmatter and are never
// serialized into YAML (yaml:"-").
type Fact struct {
	ID            string   `yaml:"id"`
	Type          string   `yaml:"type"`
	Scope         string   `yaml:"scope"`
	Project       string   `yaml:"project"`
	TopicKey      string   `yaml:"topic_key,omitempty"`
	Tags          []string `yaml:"tags,omitempty"`
	Links         []Link   `yaml:"links,omitempty"`
	CreatedAt     string   `yaml:"created_at"`
	UpdatedAt     string   `yaml:"updated_at"`
	RevisionCount int      `yaml:"revision_count"`

	Title string `yaml:"-"`
	Body  string `yaml:"-"`
}

const delim = "---"

// Marshal renders a Fact as: frontmatter, then "# Title", then the body.
func Marshal(f Fact) string {
	fm, _ := yaml.Marshal(f) // Fact has no unmarshalable fields
	var sb strings.Builder
	sb.WriteString(delim + "\n")
	sb.Write(fm)
	sb.WriteString(delim + "\n\n")
	sb.WriteString("# " + f.Title + "\n\n")
	sb.WriteString(strings.TrimRight(f.Body, "\n"))
	sb.WriteString("\n")
	return sb.String()
}

// Parse is the inverse of Marshal. It requires leading frontmatter delimited by
// "---" lines, then reads the first "# " line as the title and the remainder as
// the body.
func Parse(s string) (Fact, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, delim+"\n") {
		return Fact{}, fmt.Errorf("fact: missing opening frontmatter delimiter")
	}
	rest := s[len(delim)+1:]
	end := strings.Index(rest, "\n"+delim+"\n")
	if end < 0 {
		return Fact{}, fmt.Errorf("fact: missing closing frontmatter delimiter")
	}
	fmText := rest[:end]
	body := rest[end+len("\n"+delim+"\n"):]

	var f Fact
	if err := yaml.Unmarshal([]byte(fmText), &f); err != nil {
		return Fact{}, fmt.Errorf("fact: bad frontmatter yaml: %w", err)
	}

	body = strings.TrimLeft(body, "\n")
	if strings.HasPrefix(body, "# ") {
		nl := strings.IndexByte(body, '\n')
		if nl < 0 {
			f.Title = strings.TrimSpace(body[2:])
			f.Body = ""
			return f, nil
		}
		f.Title = strings.TrimSpace(body[2:nl])
		body = body[nl+1:]
	}
	f.Body = strings.Trim(body, "\n")
	return f, nil
}

// Slug converts a title into a filesystem-safe kebab-case slug.
func Slug(title string) string {
	var sb strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && sb.Len() > 0 {
				sb.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(sb.String(), "-")
	if out == "" {
		return "untitled"
	}
	return out
}

// NewID builds a stable id of the form "<date>-<slug>".
func NewID(date, title string) string {
	return date + "-" + Slug(title)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/fact/`
Expected: PASS (all 4 tests).

- [ ] **Step 6: Commit**

```bash
cd /home/vex/Projects/BBrain
git add go.mod go.sum internal/fact/
git commit -m "feat: fact package with markdown parse/serialize, slug, id"
```

---

## Task 3: `brain` package — locate + init the brain on disk

**Files:**
- Create: `internal/brain/brain.go`
- Test: `internal/brain/brain_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type Brain struct { Root string }`
  - `func New(root string) Brain`
  - `func (b Brain) FactsDir() string` → `<root>/raws/facts`
  - `func (b Brain) UserRawsDir() string` → `<root>/raws/user-raws`
  - `func (b Brain) WikiDir() string` → `<root>/wiki`
  - `func (b Brain) IndexPath() string` → `<root>/.bbrain/index.db`
  - `func (b Brain) Init() error` — creates the full structure (idempotent), writing `CLAUDE.md`, `wiki/index.md`, `wiki/log.md` only if absent.

- [ ] **Step 1: Write the failing test**

Create `internal/brain/brain_test.go`:

```go
package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitCreatesStructure(t *testing.T) {
	root := t.TempDir()
	b := New(root)
	if err := b.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	mustDir(t, b.FactsDir())
	mustDir(t, b.UserRawsDir())
	mustDir(t, b.WikiDir())
	mustFile(t, filepath.Join(root, "CLAUDE.md"))
	mustFile(t, filepath.Join(b.WikiDir(), "index.md"))
	mustFile(t, filepath.Join(b.WikiDir(), "log.md"))
}

func TestInitIsIdempotentAndPreservesContent(t *testing.T) {
	root := t.TempDir()
	b := New(root)
	if err := b.Init(); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	claude := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte("EDITED BY USER"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	got, _ := os.ReadFile(claude)
	if string(got) != "EDITED BY USER" {
		t.Fatalf("Init overwrote user-edited CLAUDE.md: %q", got)
	}
}

func TestPaths(t *testing.T) {
	b := New("/brains/x")
	if b.IndexPath() != filepath.Join("/brains/x", ".bbrain", "index.db") {
		t.Fatalf("IndexPath = %q", b.IndexPath())
	}
}

func mustDir(t *testing.T, p string) {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected dir at %s (err: %v)", p, err)
	}
}

func mustFile(t *testing.T, p string) {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		t.Fatalf("expected file at %s (err: %v)", p, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/brain/`
Expected: FAIL (undefined `New`, etc.).

- [ ] **Step 3: Write `internal/brain/brain.go`**

```go
// Package brain locates and initializes a BBrain "brain" on disk: a single
// directory that holds raws/ (source-of-truth memories) and wiki/ (distilled),
// plus a derived index under .bbrain/.
package brain

import (
	"os"
	"path/filepath"
)

// Brain is a brain rooted at a directory.
type Brain struct {
	Root string
}

// New returns a Brain rooted at root.
func New(root string) Brain { return Brain{Root: root} }

func (b Brain) FactsDir() string    { return filepath.Join(b.Root, "raws", "facts") }
func (b Brain) UserRawsDir() string { return filepath.Join(b.Root, "raws", "user-raws") }
func (b Brain) WikiDir() string     { return filepath.Join(b.Root, "wiki") }
func (b Brain) IndexPath() string   { return filepath.Join(b.Root, ".bbrain", "index.db") }

// Init creates the brain structure. It is idempotent: directories are created
// with MkdirAll, and seed files are written only when they do not already exist,
// so re-running never clobbers user edits.
func (b Brain) Init() error {
	dirs := []string{
		b.FactsDir(),
		b.UserRawsDir(),
		b.WikiDir(),
		filepath.Join(b.Root, ".bbrain"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	seeds := map[string]string{
		filepath.Join(b.Root, "CLAUDE.md"):        claudeSchema,
		filepath.Join(b.WikiDir(), "index.md"):    "# Wiki Index\n\n_Pages will be cataloged here._\n",
		filepath.Join(b.WikiDir(), "log.md"):      "# Wiki Log\n\n_Append-only record of ingests, queries, and lints._\n",
	}
	for path, content := range seeds {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

const claudeSchema = `# BBrain Brain — Schema

This directory is a **BBrain brain**: cross-session, cross-project memory.

## Structure
- ` + "`raws/facts/`" + ` — atomic memories as ` + "`.md`" + ` (source of truth). Flat layout;
  metadata lives in YAML frontmatter (type, scope, project, topic_key, tags, links).
- ` + "`raws/user-raws/`" + ` — your own raw notes.
- ` + "`wiki/`" + ` — distilled pages maintained by an LLM routine; ` + "`index.md`" + ` catalogs
  pages, ` + "`log.md`" + ` records ingests/queries/lints.
- ` + "`.bbrain/`" + ` — derived FTS index. Disposable; rebuild with ` + "`bbrain reindex`" + `.

## Conventions
- A fact's frontmatter ` + "`links:`" + ` are reasoned wikilinks: each has ` + "`target`" + `,
  ` + "`relation`" + ` (relates|depends-on|conflicts-with|supersedes|scoped|compatible) and a
  required ` + "`why`" + `.
- ` + "`topic_key`" + ` (family/description) makes a save an upsert: it rewrites the same file.

This file is only for opening a cowork directly inside this folder. Agents use
BBrain through its MCP tools, not this file.
`
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/brain/`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
cd /home/vex/Projects/BBrain
git add internal/brain/
git commit -m "feat: brain package with init + path helpers"
```

---

## Task 4: `store` package — write facts with upsert + dedup

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `bbrain/internal/fact` (`Fact`, `Marshal`, `Parse`, `NewID`), `bbrain/internal/brain` (`Brain`), `github.com/natefinch/atomic`.
- Produces:
  - `type Store struct { Brain brain.Brain; Now func() time.Time }`
  - `func New(b brain.Brain) *Store` — `Now` defaults to `time.Now`.
  - `type SaveInput struct { Type, Title, Body, Project, Scope, TopicKey string; Tags []string }`
  - `func (s *Store) Save(in SaveInput) (fact.Fact, error)` — resolves the target file (topic_key upsert vs new), applies a 15-minute exact-duplicate guard, writes the `.md` atomically, returns the persisted Fact.
  - `func (s *Store) ListFacts() ([]fact.Fact, error)` — parses every `.md` under `FactsDir()`.
  - `func (s *Store) PathFor(f fact.Fact) string` — `<FactsDir>/<id>.md`.

- [ ] **Step 1: Add the atomic-write dependency**

Run: `cd /home/vex/Projects/BBrain && go get github.com/natefinch/atomic@v1.0.1`
Expected: adds `github.com/natefinch/atomic` to `go.mod`.

- [ ] **Step 2: Write the failing test**

Create `internal/store/store_test.go`:

```go
package store

import (
	"testing"
	"time"

	"bbrain/internal/brain"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	b := brain.New(t.TempDir())
	if err := b.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(b)
	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }
	return s
}

func TestSaveCreatesFactFile(t *testing.T) {
	s := newTestStore(t)
	f, err := s.Save(SaveInput{
		Type: "decision", Title: "Use JWT for auth", Body: "**What:** jwt",
		Project: "bbrain", Scope: "project",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f.ID != "2026-06-22-use-jwt-for-auth" {
		t.Fatalf("ID = %q", f.ID)
	}
	facts, err := s.ListFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Title != "Use JWT for auth" {
		t.Fatalf("ListFacts = %+v", facts)
	}
}

func TestSaveWithTopicKeyUpserts(t *testing.T) {
	s := newTestStore(t)
	first, _ := s.Save(SaveInput{
		Type: "architecture", Title: "Auth model", Body: "v1",
		Project: "bbrain", Scope: "project", TopicKey: "architecture/auth-model",
	})
	// Advance time so the dedup window does not swallow the second save.
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	second, err := s.Save(SaveInput{
		Type: "architecture", Title: "Auth model", Body: "v2 — now stateless",
		Project: "bbrain", Scope: "project", TopicKey: "architecture/auth-model",
	})
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("upsert should reuse id: first=%q second=%q", first.ID, second.ID)
	}
	if second.RevisionCount != 2 {
		t.Fatalf("RevisionCount = %d, want 2", second.RevisionCount)
	}
	facts, _ := s.ListFacts()
	if len(facts) != 1 {
		t.Fatalf("upsert must not add a new file: got %d facts", len(facts))
	}
	if facts[0].Body != "v2 — now stateless" {
		t.Fatalf("body not updated: %q", facts[0].Body)
	}
}

func TestSaveDedupesIdenticalWithinWindow(t *testing.T) {
	s := newTestStore(t)
	in := SaveInput{Type: "bugfix", Title: "Nil panic", Body: "guard nil",
		Project: "bbrain", Scope: "project"}
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	// Same content, 5 minutes later (inside the 15-min window): no new file.
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 12, 5, 0, 0, time.UTC) }
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	facts, _ := s.ListFacts()
	if len(facts) != 1 {
		t.Fatalf("dedup failed: got %d facts, want 1", len(facts))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/store/`
Expected: FAIL (undefined `New`, `Save`, `SaveInput`, etc.).

- [ ] **Step 4: Write `internal/store/store.go`**

```go
// Package store writes and reads facts as .md files under a brain's raws/facts/.
// The files are the source of truth; this package never caches them elsewhere.
package store

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

// dedupeWindow is how long an exact-duplicate save is folded into the existing
// file instead of creating a new one.
const dedupeWindow = 15 * time.Minute

// Store persists facts for one brain.
type Store struct {
	Brain brain.Brain
	Now   func() time.Time
}

// New returns a Store with Now defaulting to time.Now.
func New(b brain.Brain) *Store {
	return &Store{Brain: b, Now: time.Now}
}

// SaveInput is the data needed to create or upsert a fact.
type SaveInput struct {
	Type     string
	Title    string
	Body     string
	Project  string
	Scope    string
	TopicKey string
	Tags     []string
}

// PathFor returns the .md path for a fact.
func (s *Store) PathFor(f fact.Fact) string {
	return filepath.Join(s.Brain.FactsDir(), f.ID+".md")
}

// Save writes a fact to disk. With a TopicKey it upserts the matching file
// (bumping RevisionCount); otherwise it creates a new file. Exact duplicates
// saved within dedupeWindow are skipped (the existing file is returned).
func (s *Store) Save(in SaveInput) (fact.Fact, error) {
	now := s.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	existing, err := s.ListFacts()
	if err != nil {
		return fact.Fact{}, err
	}

	// Topic-key upsert: rewrite the existing file in place.
	if in.TopicKey != "" {
		for _, e := range existing {
			if e.TopicKey == in.TopicKey && e.Project == in.Project && e.Scope == in.Scope {
				e.Type = in.Type
				e.Title = in.Title
				e.Body = in.Body
				e.Tags = in.Tags
				e.UpdatedAt = nowStr
				e.RevisionCount++
				if err := s.write(e); err != nil {
					return fact.Fact{}, err
				}
				return e, nil
			}
		}
	}

	// Exact-duplicate guard within the rolling window.
	h := contentHash(in)
	for _, e := range existing {
		if contentHashOf(e) == h {
			if t, perr := time.Parse(time.RFC3339, e.UpdatedAt); perr == nil {
				if now.Sub(t.UTC()) <= dedupeWindow {
					return e, nil
				}
			}
		}
	}

	f := fact.Fact{
		ID:            fact.NewID(now.Format("2006-01-02"), in.Title),
		Type:          in.Type,
		Scope:         in.Scope,
		Project:       in.Project,
		TopicKey:      in.TopicKey,
		Tags:          in.Tags,
		CreatedAt:     nowStr,
		UpdatedAt:     nowStr,
		RevisionCount: 1,
		Title:         in.Title,
		Body:          in.Body,
	}
	if err := s.write(f); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

func (s *Store) write(f fact.Fact) error {
	if err := os.MkdirAll(s.Brain.FactsDir(), 0o755); err != nil {
		return err
	}
	return atomic.WriteFile(s.PathFor(f), strings.NewReader(fact.Marshal(f)))
}

// ListFacts parses every .md file directly under the brain's facts dir.
func (s *Store) ListFacts() ([]fact.Fact, error) {
	dir := s.Brain.FactsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []fact.Fact
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		f, err := fact.Parse(string(data))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func contentHash(in SaveInput) string {
	return hashParts(in.Project, in.Scope, in.Type, in.Title, in.Body)
}

func contentHashOf(f fact.Fact) string {
	return hashParts(f.Project, f.Scope, f.Type, f.Title, f.Body)
}

func hashParts(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/store/`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
cd /home/vex/Projects/BBrain
git add go.mod go.sum internal/store/
git commit -m "feat: store with fact write, topic_key upsert, dedup window"
```

---

## Task 5: `index` package — FTS5 search over facts

**Files:**
- Create: `internal/index/index.go`
- Test: `internal/index/index_test.go`

**Interfaces:**
- Consumes: `bbrain/internal/fact` (`Fact`), `modernc.org/sqlite`.
- Produces:
  - `type Index struct { ... }`
  - `func Open(path string) (*Index, error)` — opens/creates the SQLite db and FTS5 schema. `path` may be `":memory:"`.
  - `func (ix *Index) Close() error`
  - `func (ix *Index) IndexFact(f fact.Fact, path string) error` — upserts one fact (delete-by-id then insert).
  - `func (ix *Index) Clear() error` — empties the index (for full reindex).
  - `type Result struct { FactID, Title, Type, Project, Path string }`
  - `func (ix *Index) Search(query string, limit int) ([]Result, error)` — FTS5 MATCH ranked by BM25.

- [ ] **Step 1: Add the sqlite dependency**

Run: `cd /home/vex/Projects/BBrain && go get modernc.org/sqlite@v1.45.0`
Expected: adds `modernc.org/sqlite` (+ transitive `modernc.org/*`) to `go.mod`.

- [ ] **Step 2: Write the failing test**

Create `internal/index/index_test.go`:

```go
package index

import (
	"testing"

	"bbrain/internal/fact"
)

func openMem(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

func sampleFact(id, title, body, typ, project string) fact.Fact {
	return fact.Fact{ID: id, Title: title, Body: body, Type: typ,
		Scope: "project", Project: project}
}

func TestSearchFindsByTitleAndBody(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT for auth", "stateless tokens", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f2", "Postgres choice", "relational database", "decision", "bbrain"), "/x/f2.md"))

	res, err := ix.Search("jwt", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].FactID != "f1" {
		t.Fatalf("Search(jwt) = %+v, want only f1", res)
	}
	if res[0].Path != "/x/f1.md" || res[0].Title != "Use JWT for auth" {
		t.Fatalf("result fields wrong: %+v", res[0])
	}
}

func TestIndexFactIsUpsert(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Old title", "old body", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.IndexFact(sampleFact("f1", "New title carrot", "new body", "decision", "bbrain"), "/x/f1.md"))

	if res, _ := ix.Search("carrot", 10); len(res) != 1 {
		t.Fatalf("Search(carrot) = %+v, want 1 (new content)", res)
	}
	if res, _ := ix.Search("old", 10); len(res) != 0 {
		t.Fatalf("Search(old) = %+v, want 0 (old content gone)", res)
	}
}

func TestSearchQueryWithSpecialCharsDoesNotError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Auth (v2) AND tokens", "body", "decision", "bbrain"), "/x/f1.md"))
	if _, err := ix.Search(`auth (v2) AND "tokens`, 10); err != nil {
		t.Fatalf("Search with FTS5 special chars should not error: %v", err)
	}
}

func TestClearEmptiesIndex(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Use JWT", "body", "decision", "bbrain"), "/x/f1.md"))
	must(t, ix.Clear())
	if res, _ := ix.Search("jwt", 10); len(res) != 0 {
		t.Fatalf("after Clear, Search = %+v, want empty", res)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/index/`
Expected: FAIL (undefined `Open`, `Index`, etc.).

- [ ] **Step 4: Write `internal/index/index.go`**

```go
// Package index is BBrain's derived, disposable search index. It mirrors the
// .md facts into a SQLite FTS5 table for fast lexical (BM25) search and can be
// rebuilt from disk at any time.
package index

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"

	"bbrain/internal/fact"
)

// Index wraps a SQLite connection holding the FTS5 facts table.
type Index struct {
	db *sql.DB
}

// schema: a single standalone FTS5 table. Searchable columns (title, body, tags,
// topic_key) are tokenized; identifiers/filters (fact_id, path, type, scope,
// project) are UNINDEXED so they are stored verbatim and usable in WHERE.
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
	project UNINDEXED
);`

// Open opens (or creates) the index at path. Use ":memory:" for tests.
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

// Close closes the underlying database.
func (ix *Index) Close() error { return ix.db.Close() }

// IndexFact upserts a fact: it removes any existing row with the same fact_id
// then inserts the current content.
func (ix *Index) IndexFact(f fact.Fact, path string) error {
	if _, err := ix.db.Exec(`DELETE FROM facts_fts WHERE fact_id = ?`, f.ID); err != nil {
		return err
	}
	_, err := ix.db.Exec(
		`INSERT INTO facts_fts (fact_id, path, title, body, tags, topic_key, type, scope, project)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, path, f.Title, f.Body, strings.Join(f.Tags, " "), f.TopicKey,
		f.Type, f.Scope, f.Project,
	)
	return err
}

// Clear empties the index (used before a full reindex).
func (ix *Index) Clear() error {
	_, err := ix.db.Exec(`DELETE FROM facts_fts`)
	return err
}

// Result is one search hit.
type Result struct {
	FactID  string
	Title   string
	Type    string
	Project string
	Path    string
}

// Search runs an FTS5 MATCH over title/body/tags/topic_key, ranked by BM25.
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/index/`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
cd /home/vex/Projects/BBrain
git add go.mod go.sum internal/index/
git commit -m "feat: FTS5 index with upsert, search, clear"
```

---

## Task 6: `app` package + CLI wiring — `init`, `save`, `search`, `reindex`

**Files:**
- Create: `internal/app/app.go`
- Test: `internal/app/app_test.go`
- Modify: `cmd/bbrain/main.go`
- Test: `cmd/bbrain/main_test.go` (add an end-to-end test)

**Interfaces:**
- Consumes: `bbrain/internal/brain`, `bbrain/internal/store`, `bbrain/internal/index`, `bbrain/internal/fact`.
- Produces:
  - `type App struct { Store *store.Store; Brain brain.Brain }`
  - `func New(root string) *App`
  - `func (a *App) Init() error` — `brain.Init()` then a first `Reindex()`.
  - `func (a *App) Reindex() (int, error)` — opens the index, `Clear()`s it, re-adds every fact from `Store.ListFacts()`, returns the count.
  - `func (a *App) Save(in store.SaveInput) (fact.Fact, error)` — `Store.Save` then index just that fact.
  - `func (a *App) Search(query string, limit int) ([]index.Result, error)` — opens the index and searches.

- [ ] **Step 1: Write the failing app test**

Create `internal/app/app_test.go`:

```go
package app

import (
	"testing"

	"bbrain/internal/store"
)

func TestSaveThenSearch(t *testing.T) {
	a := New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{
		Type: "decision", Title: "Use JWT for auth", Body: "stateless tokens",
		Project: "bbrain", Scope: "project",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := a.Search("jwt", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Title != "Use JWT for auth" {
		t.Fatalf("Search = %+v", res)
	}
}

func TestReindexRebuildsFromDisk(t *testing.T) {
	a := New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save(store.SaveInput{
		Type: "decision", Title: "Use Postgres", Body: "relational",
		Project: "bbrain", Scope: "project",
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a thrown-away index: a fresh App over the same root reindexes.
	a2 := New(a.Brain.Root)
	n, err := a2.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if n != 1 {
		t.Fatalf("Reindex count = %d, want 1", n)
	}
	res, _ := a2.Search("postgres", 10)
	if len(res) != 1 {
		t.Fatalf("Search after reindex = %+v, want 1", res)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/app/`
Expected: FAIL (undefined `New`, `App`, etc.).

- [ ] **Step 3: Write `internal/app/app.go`**

```go
// Package app wires the brain, store, and index together and exposes the
// operations the CLI (and later the MCP server) drive.
package app

import (
	"bbrain/internal/brain"
	"bbrain/internal/fact"
	"bbrain/internal/index"
	"bbrain/internal/store"
)

// App is the high-level façade over one brain.
type App struct {
	Store *store.Store
	Brain brain.Brain
}

// New builds an App rooted at a brain directory.
func New(root string) *App {
	b := brain.New(root)
	return &App{Store: store.New(b), Brain: b}
}

// Init creates the brain structure and builds an initial (empty) index.
func (a *App) Init() error {
	if err := a.Brain.Init(); err != nil {
		return err
	}
	_, err := a.Reindex()
	return err
}

// Reindex rebuilds the FTS index from the .md files on disk and returns how many
// facts were indexed. The index is fully derived: it is cleared first.
func (a *App) Reindex() (int, error) {
	facts, err := a.Store.ListFacts()
	if err != nil {
		return 0, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return 0, err
	}
	defer ix.Close()
	if err := ix.Clear(); err != nil {
		return 0, err
	}
	for _, f := range facts {
		if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
			return 0, err
		}
	}
	return len(facts), nil
}

// Save persists a fact and incrementally indexes it.
func (a *App) Save(in store.SaveInput) (fact.Fact, error) {
	f, err := a.Store.Save(in)
	if err != nil {
		return fact.Fact{}, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return fact.Fact{}, err
	}
	defer ix.Close()
	if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

// Search runs a lexical search over the index.
func (a *App) Search(query string, limit int) ([]index.Result, error) {
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	return ix.Search(query, limit)
}
```

- [ ] **Step 4: Run app tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/app/`
Expected: PASS (2 tests).

- [ ] **Step 5: Wire the CLI — rewrite `cmd/bbrain/main.go`**

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"bbrain/internal/app"
	"bbrain/internal/store"
)

const version = "0.1.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// brainRoot resolves where the brain lives: $BBRAIN_HOME or ~/.bbrain/default.
func brainRoot() string {
	if v := os.Getenv("BBRAIN_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".bbrain"
	}
	return home + "/.bbrain/default"
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex> [args]")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "bbrain "+version)
		return 0
	case "init":
		a := app.New(brainRoot())
		if err := a.Init(); err != nil {
			fmt.Fprintf(stderr, "init: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "initialized brain at %s\n", a.Brain.Root)
		return 0
	case "reindex":
		a := app.New(brainRoot())
		n, err := a.Reindex()
		if err != nil {
			fmt.Fprintf(stderr, "reindex: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "reindexed %d facts\n", n)
		return 0
	case "save":
		return cmdSave(args[1:], stdout, stderr)
	case "search":
		return cmdSearch(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

func cmdSave(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("save", flag.ContinueOnError)
	fs.SetOutput(stderr)
	typ := fs.String("type", "discovery", "fact type")
	title := fs.String("title", "", "fact title (required)")
	body := fs.String("body", "", "fact body")
	project := fs.String("project", "", "project (required)")
	scope := fs.String("scope", "project", "scope")
	topic := fs.String("topic-key", "", "optional topic key for upsert")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *title == "" || *project == "" {
		fmt.Fprintln(stderr, "save: --title and --project are required")
		return 2
	}
	a := app.New(brainRoot())
	f, err := a.Save(store.SaveInput{
		Type: *typ, Title: *title, Body: *body,
		Project: *project, Scope: *scope, TopicKey: *topic,
	})
	if err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "saved %s\n", f.ID)
	return 0
}

func cmdSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 20, "max results")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	query := ""
	for i, a := range fs.Args() {
		if i > 0 {
			query += " "
		}
		query += a
	}
	if query == "" {
		fmt.Fprintln(stderr, "search: provide a query")
		return 2
	}
	a := app.New(brainRoot())
	res, err := a.Search(query, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "search: %v\n", err)
		return 1
	}
	for _, r := range res {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", r.FactID, r.Type, r.Title)
	}
	return 0
}
```

- [ ] **Step 6: Add an end-to-end CLI test**

Append to `cmd/bbrain/main_test.go`:

```go
func TestEndToEndSaveAndSearch(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())

	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init failed: %s", errOut.String())
	}

	out.Reset(); errOut.Reset()
	code := run([]string{"save", "--title", "Use JWT for auth",
		"--project", "bbrain", "--type", "decision", "--body", "stateless tokens"},
		&out, &errOut)
	if code != 0 {
		t.Fatalf("save failed: %s", errOut.String())
	}

	out.Reset(); errOut.Reset()
	if code := run([]string{"search", "jwt"}, &out, &errOut); code != 0 {
		t.Fatalf("search failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "Use JWT for auth") {
		t.Fatalf("search output = %q, want it to contain the saved title", out.String())
	}
}
```

(Add `"bytes"` and `"strings"` to the imports if not already present.)

- [ ] **Step 7: Run the full suite**

Run: `cd /home/vex/Projects/BBrain && go test ./...`
Expected: PASS (all packages).

- [ ] **Step 8: Manual smoke test**

```bash
cd /home/vex/Projects/BBrain
go build ./cmd/bbrain
BBRAIN_HOME=/tmp/bbrain-smoke ./bbrain init
BBRAIN_HOME=/tmp/bbrain-smoke ./bbrain save --title "Use JWT" --project demo --type decision --body "stateless"
BBRAIN_HOME=/tmp/bbrain-smoke ./bbrain search jwt
cat /tmp/bbrain-smoke/raws/facts/*.md   # confirm the .md is the source of truth
rm -rf /tmp/bbrain-smoke/.bbrain && BBRAIN_HOME=/tmp/bbrain-smoke ./bbrain reindex  # index is disposable
```
Expected: search prints the saved fact; deleting `.bbrain/` and reindexing restores search.

- [ ] **Step 9: Commit**

```bash
cd /home/vex/Projects/BBrain
git add internal/app/ cmd/bbrain/
git commit -m "feat: app glue + CLI init/save/search/reindex with e2e test"
```

---

## Self-Review

**1. Spec coverage (Plan 1 scope):**
- `.md` as source of truth → Task 4 writes `.md`; Task 6 proves the index is disposable (smoke test deletes `.bbrain/` and reindexes). ✓
- Flat facts with rich frontmatter, `<date>-<slug>` naming → Tasks 2 + 4. ✓
- FTS5 derived index, rebuildable → Tasks 5 + 6. ✓
- `topic_key` upsert + dedup window → Task 4. ✓
- Brain structure (`CLAUDE.md`, `raws/facts`, `raws/user-raws`, `wiki/index.md`, `wiki/log.md`) → Task 3. ✓
- Reasoned wikilinks (`links:` with `why`) → round-tripped in the format (Task 2) but graph queries/`mem_link` are **deferred to Plan 2** (documented in Roadmap). ✓
- Single binary, zero-deps, pure-Go SQLite → Tech Stack + Task 5 driver. ✓
- Out of scope here and tracked in Roadmap: wiki LLM routine (Plan 3), MCP tools (Plan 4), TUI install + agent hooks (Plan 5). ✓

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code; every test step shows the command and expected result. ✓

**3. Type consistency:** `SaveInput`, `fact.Fact`, `index.Result`, `App.Reindex() (int, error)`, `Index.IndexFact(f, path)` are used identically across Tasks 4–6. `brain.Brain.IndexPath()`/`FactsDir()` match their definitions in Task 3. ✓
