# BBrain — Plan 6: `bbrain vault move` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bbrain vault move <dest>` relocates the whole brain to `<dest>`, rebuilds the derived index there (the FTS `path` column stores absolute paths), regenerates the brain's `env.sh` to the moved adapter, and optionally refreshes a project's integration. Stdlib only.

**Architecture:** New pure package `internal/vault` (`Move` = atomic `os.Rename`, with a cross-device copy-tree-then-remove fallback; refuses unsafe destinations). `internal/app` gains `VaultMove` (move → rebuild index at dest → regen `env.sh` → optional project refresh via the existing `SetupClaudeCode`). `cmd/bbrain` adds `vault move`.

**Tech Stack:** Go 1.25, stdlib (`io`, `io/fs`, `os`, `path/filepath`, `fmt`, `flag`). No new dependencies.

## Global Constraints

- **Module:** `bbrain`. **Go:** `go 1.25`. **Root:** `/home/vex/Projects/BBrain/`. **No new dependencies.**
- **`.md` is source of truth; the index is disposable** — rebuilt at the new root after a move, never moved-and-trusted (its `path` column would be stale).
- **Safe/non-destructive:** refuse when `dest == src` or `dest` exists and is non-empty. Prefer atomic `os.Rename`; on a cross-device failure, copy the tree then remove the source **only after a verified copy**; never leave a partial copy.
- **Layering:** `internal/vault` imports only stdlib; `internal/app` orchestrates and reuses `internal/setup.EnvExportLine` + `App.SetupClaudeCode`; `cmd/bbrain` wires the command.
- **Path resolution:** the brain root is `brainRoot()` (`$BBRAIN_HOME` or `~/.bbrain/default`). After a move the user must `export BBRAIN_HOME=<dest>` (the command prints this); we never edit shell rc.
- `go test ./...` green + `go vet` clean before each commit.

Design spec: `docs/superpowers/specs/2026-06-23-bbrain-vault-move-design.md`.

---

## File Structure (Plan 6)

- `internal/vault/vault.go` + `internal/vault/vault_test.go` — **create:** `Move` + `copyTree`.
- `internal/app/app.go` + `internal/app/app_test.go` — **modify:** `VaultMoveOptions`, `VaultMove`.
- `cmd/bbrain/main.go` + `cmd/bbrain/main_test.go` — **modify:** `vault` command + usage + e2e.

---

## Task 1: `internal/vault` — `Move` (relocate the brain tree)

**Files:** Create `internal/vault/vault.go`, `internal/vault/vault_test.go`.

**Interfaces:**
- Consumes: stdlib `fmt`, `io`, `io/fs`, `os`, `path/filepath`.
- Produces: `func Move(src, dest string) error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/vault/vault_test.go`:

```go
package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMoveRelocatesTree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustMkdir(t, filepath.Join(src, "raws", "facts"))
	mustWrite(t, filepath.Join(src, "raws", "facts", "a.md"), "alpha")
	mustWrite(t, filepath.Join(src, "CLAUDE.md"), "doc")
	dest := filepath.Join(root, "dest")

	if err := Move(src, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source still exists after move")
	}
	b, err := os.ReadFile(filepath.Join(dest, "raws", "facts", "a.md"))
	if err != nil || string(b) != "alpha" {
		t.Fatalf("moved file = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "CLAUDE.md")); err != nil {
		t.Fatalf("CLAUDE.md not moved: %v", err)
	}
}

func TestMoveRefusesSameAndNonEmptyDest(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustMkdir(t, src)
	if err := Move(src, src); err == nil {
		t.Fatal("Move should refuse dest == src")
	}
	dest := filepath.Join(root, "dest")
	mustMkdir(t, dest)
	mustWrite(t, filepath.Join(dest, "x"), "occupied")
	if err := Move(src, dest); err == nil {
		t.Fatal("Move should refuse a non-empty destination")
	}
}

func TestCopyTreePreservesContentAndMode(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustMkdir(t, filepath.Join(src, "sub"))
	mustWrite(t, filepath.Join(src, "sub", "f.sh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(src, "sub", "f.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "copy")
	if err := copyTree(src, dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "sub", "f.sh"))
	if err != nil || string(b) != "#!/bin/sh\n" {
		t.Fatalf("copied content = %q, %v", b, err)
	}
	info, err := os.Stat(filepath.Join(dest, "sub", "f.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("mode not preserved: %v", info.Mode())
	}
}

func TestMoveMissingSource(t *testing.T) {
	root := t.TempDir()
	if err := Move(filepath.Join(root, "nope"), filepath.Join(root, "dest")); err == nil {
		t.Fatal("Move should error on missing source")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/vault/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement `vault.go`**

Create `internal/vault/vault.go`:

```go
// Package vault relocates a brain directory. Stdlib only; the caller rebuilds the
// derived index after a move (it stores absolute paths that a move invalidates).
package vault

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Move relocates the tree at src to dest. It refuses when dest equals src or when
// dest already exists as a non-empty directory. It prefers an atomic os.Rename and
// falls back to a copy-then-remove when src and dest are on different filesystems;
// on a copy failure it removes the partial destination and leaves src intact.
func Move(src, dest string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("vault: source %q is not a directory", src)
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if absSrc == absDest {
		return fmt.Errorf("vault: destination equals source")
	}
	if nonEmptyDir(absDest) {
		return fmt.Errorf("vault: destination %q already exists and is not empty", dest)
	}
	if err := os.MkdirAll(filepath.Dir(absDest), 0o755); err != nil {
		return err
	}
	// Fast path: atomic rename within the same filesystem.
	if err := os.Rename(absSrc, absDest); err == nil {
		return nil
	}
	// Fallback: copy the tree, then remove the source only on success.
	if err := copyTree(absSrc, absDest); err != nil {
		os.RemoveAll(absDest) // don't leave a partial copy
		return err
	}
	return os.RemoveAll(absSrc)
}

// nonEmptyDir reports whether path is a directory containing at least one entry.
func nonEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// copyTree copies the directory tree at src into dest, preserving file modes.
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(p, target, d)
	})
}

func copyFile(srcPath, destPath string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/vault/` → PASS. Then `go vet ./internal/vault/`.

- [ ] **Step 5: Commit**

```bash
cd /home/vex/Projects/BBrain
git add internal/vault/
git commit -m "feat(vault): Move — relocate a brain tree (atomic rename + cross-device copy fallback)"
```

---

## Task 2: `internal/app` — `VaultMove`

**Files:** Modify `internal/app/app.go`, `internal/app/app_test.go`.

**Interfaces:**
- Consumes: `bbrain/internal/vault` (Task 1), `bbrain/internal/setup` (Plan 5), `App.Reindex`, `App.SetupClaudeCode`; stdlib `os`, `path/filepath` (already imported).
- Produces: `type VaultMoveOptions struct { ProjectDir string }`; `func (a *App) VaultMove(dest string, opts VaultMoveOptions) (newRoot string, indexed int, err error)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/app_test.go` (imports `os`, `path/filepath`, `store` are already present):

```go
func TestVaultMoveRelocatesAndReindexes(t *testing.T) {
	src := t.TempDir()
	a := New(src)
	must(t, a.Init())
	f, err := a.Save(store.SaveInput{Type: "decision", Title: "Movable JWT", Body: "tokens", Project: "p", Scope: "project"})
	must(t, err)
	dest := filepath.Join(t.TempDir(), "moved")

	newRoot, n, err := a.VaultMove(dest, VaultMoveOptions{})
	must(t, err)
	if newRoot != dest || n < 1 {
		t.Fatalf("VaultMove = %q, %d", newRoot, n)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source brain still present after move")
	}
	// Facts survive at dest and the index was rebuilt there.
	nb := New(dest)
	got, ok, err := nb.Get(f.ID)
	must(t, err)
	if !ok || got.Title != "Movable JWT" {
		t.Fatalf("fact missing at dest: %+v ok=%v", got, ok)
	}
	res, err := nb.Search("jwt", 10)
	must(t, err)
	if len(res) == 0 {
		t.Fatal("search returns nothing after move (index not rebuilt)")
	}
}

func TestVaultMoveRefreshesProject(t *testing.T) {
	src := t.TempDir()
	a := New(src)
	must(t, a.Init())
	proj := t.TempDir()
	dest := filepath.Join(t.TempDir(), "moved")

	_, _, err := a.VaultMove(dest, VaultMoveOptions{ProjectDir: proj})
	must(t, err)
	mcp, err := os.ReadFile(filepath.Join(proj, ".mcp.json"))
	must(t, err)
	if !strings.Contains(string(mcp), dest) {
		t.Fatalf(".mcp.json not pointed at new home %q:\n%s", dest, mcp)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/app/`
Expected: FAIL (undefined `VaultMove`, `VaultMoveOptions`).

- [ ] **Step 3: Implement `VaultMove`**

In `internal/app/app.go`, add `"bbrain/internal/vault"` to the import block (`setup`, `os`, `path/filepath` are already imported). Append:

```go
// VaultMoveOptions configures App.VaultMove.
type VaultMoveOptions struct {
	ProjectDir string // optional: refresh this project's integration at the new home
}

// VaultMove relocates the brain to dest, rebuilds the index there, regenerates the
// brain's env.sh to point at the moved adapter (when setup was run), and optionally
// refreshes a project's integration. Returns the new root and the reindexed count.
func (a *App) VaultMove(dest string, opts VaultMoveOptions) (string, int, error) {
	if err := vault.Move(a.Brain.Root, dest); err != nil {
		return "", 0, err
	}
	nb := New(dest)
	indexed, err := nb.Reindex()
	if err != nil {
		return "", 0, err
	}
	// Point the brain's env.sh at the moved adapter, if setup was run before the move.
	adapter := filepath.Join(dest, ".bbrain", "agents", "claude-code.sh")
	if _, statErr := os.Stat(adapter); statErr == nil {
		envPath := filepath.Join(dest, ".bbrain", "env.sh")
		if err := os.WriteFile(envPath, []byte(setup.EnvExportLine(adapter)+"\n"), 0o644); err != nil {
			return "", 0, err
		}
	}
	if opts.ProjectDir != "" {
		if _, err := nb.SetupClaudeCode(SetupOptions{ProjectDir: opts.ProjectDir, BrainHome: dest}); err != nil {
			return "", 0, err
		}
	}
	return dest, indexed, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/vex/Projects/BBrain && go test ./internal/app/` → PASS. Then `go vet ./internal/app/`.

- [ ] **Step 5: Commit**

```bash
cd /home/vex/Projects/BBrain
git add internal/app/
git commit -m "feat(app): VaultMove — relocate brain, rebuild index, refresh env.sh + optional project"
```

---

## Task 3: `cmd/bbrain` — `vault move`

**Files:** Modify `cmd/bbrain/main.go`, `cmd/bbrain/main_test.go`.

**Interfaces:**
- Consumes: `app.VaultMove`/`VaultMoveOptions`, `brainRoot`; stdlib `flag`, `fmt`, `io` (all already imported).
- Produces: `bbrain vault move [--project DIR] <dest>`.

- [ ] **Step 1: Write the failing e2e test**

Append to `cmd/bbrain/main_test.go`:

```go
func TestEndToEndVaultMove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	if code := run([]string{"save", "--title", "Relocate me", "--project", "p", "--type", "decision", "--body", "jwt body"}, &out, &errOut); code != 0 {
		t.Fatalf("save: %s", errOut.String())
	}
	dest := filepath.Join(t.TempDir(), "newhome")

	out.Reset()
	errOut.Reset()
	if code := run([]string{"vault", "move", dest}, &out, &errOut); code != 0 {
		t.Fatalf("vault move: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "moved brain to "+dest) {
		t.Fatalf("vault move output = %q", out.String())
	}
	// The old home is gone; the moved brain is searchable.
	if _, err := os.Stat(filepath.Join(home, "raws")); !os.IsNotExist(err) {
		t.Fatal("old brain still present after move")
	}
	t.Setenv("BBRAIN_HOME", dest)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"search", "jwt"}, &out, &errOut); code != 0 {
		t.Fatalf("search at dest: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "Relocate me") {
		t.Fatalf("search at moved brain = %q", out.String())
	}
}

func TestVaultUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"vault"}, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "vault move") {
		t.Fatalf("usage = %q", errOut.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/vex/Projects/BBrain && go test ./cmd/...`
Expected: FAIL (`vault` unknown).

- [ ] **Step 3: Wire the command**

In `cmd/bbrain/main.go`, add a case to the `runWithIn` switch (before `default`):

```go
	case "vault":
		return cmdVault(args[1:], stdout, stderr)
```

Update the usage line to include `vault`. Append:

```go
func cmdVault(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "move" {
		fmt.Fprintln(stderr, "vault: usage: bbrain vault move [--project DIR] <dest>")
		return 2
	}
	fs := flag.NewFlagSet("vault move", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "also refresh this project's .mcp.json + CLAUDE.md at the new home")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "vault move: exactly one destination is required (flags before <dest>)")
		return 2
	}
	a := app.New(brainRoot())
	newRoot, n, err := a.VaultMove(rest[0], app.VaultMoveOptions{ProjectDir: *project})
	if err != nil {
		fmt.Fprintf(stderr, "vault move: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "moved brain to %s (reindexed %d facts)\n", newRoot, n)
	fmt.Fprintf(stdout, "next: export BBRAIN_HOME=%s\n", newRoot)
	if *project != "" {
		fmt.Fprintf(stdout, "refreshed integration in %s\n", *project)
	}
	return 0
}
```

- [ ] **Step 4: Run the full suite**

Run: `cd /home/vex/Projects/BBrain && go test ./...` → PASS. Then `go vet ./...`.

- [ ] **Step 5: Manual smoke test**

```bash
cd /home/vex/Projects/BBrain
go build ./cmd/bbrain
rm -rf /tmp/bbrain-vault-smoke && export BBRAIN_HOME=/tmp/bbrain-vault-smoke/brain
./bbrain init
./bbrain save --title "Vault fact" --project p --type decision --body "jwt move me" >/dev/null
echo "--- move ---"; ./bbrain vault move /tmp/bbrain-vault-smoke/moved
echo "--- old gone? ---"; ls /tmp/bbrain-vault-smoke/brain 2>&1 || echo "(old home gone)"
echo "--- search at new home ---"; BBRAIN_HOME=/tmp/bbrain-vault-smoke/moved ./bbrain search jwt
unset BBRAIN_HOME
```
Expected: `vault move` prints `moved brain to /tmp/bbrain-vault-smoke/moved (reindexed N facts)` + the `export BBRAIN_HOME=...` reminder; the old home is gone; searching the moved brain finds "Vault fact".

- [ ] **Step 6: Commit**

```bash
cd /home/vex/Projects/BBrain
git add cmd/bbrain/
git commit -m "feat(cli): bbrain vault move <dest> (relocate brain) with e2e"
```

---

## Task 4 (runtime, not SDD): validate a moved brain still drives Claude Code

After merge: create a brain, `bbrain setup claude-code`, `bbrain vault move <dest>`, then with `BBRAIN_HOME=<dest>` (sourcing the regenerated `<dest>/.bbrain/env.sh`) run `bbrain wiki build` and confirm live Claude still distils. Record in `docs/runtime-validation-claude-code.md`. (Controller-performed, inline.)

---

## Self-Review

**1. Spec coverage:** relocate (Task 1 `Move`, atomic + copy fallback, refuse unsafe dest); rebuild index at dest + regen env.sh + optional project refresh (Task 2 `VaultMove`); `bbrain vault move` CLI + e2e (Task 3); runtime (Task 4). ✓

**2. Placeholder scan:** Every step has complete code + commands + expected output. ✓

**3. Type consistency:** `vault.Move(src,dest)` defined Task 1, consumed Task 2. `app.VaultMove(dest, VaultMoveOptions) (string,int,error)` defined Task 2, consumed Task 3. Reuses `setup.EnvExportLine`, `App.SetupClaudeCode`, `App.Reindex` (existing). ✓

**4. Import/dependency sanity:** `internal/vault` imports only stdlib; `app` adds `vault` (already has `setup`); `cmd` unchanged imports. No cycles, no `go.mod` change. ✓
