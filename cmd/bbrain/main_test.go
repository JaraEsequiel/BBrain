package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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

// BBRAIN-4 AC-6: bbrain search CLI --project/--type flags reach ix.Search;
// TC-6.1 filtered, TC-6.2 no flags → identical to pre-change unfiltered behavior.
func TestEndToEndSearchProjectFilter(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	var out, errOut bytes.Buffer

	if code := run([]string{"save", "--title", "alpha shared", "--body", "b", "--type", "decision", "--project", "bbrain"}, &out, &errOut); code != 0 {
		t.Fatalf("save bbrain fact: code=%d stderr=%s", code, errOut.String())
	}
	out.Reset()
	if code := run([]string{"save", "--title", "beta shared", "--body", "b", "--type", "decision", "--project", "vexforge"}, &out, &errOut); code != 0 {
		t.Fatalf("save vexforge fact: code=%d stderr=%s", code, errOut.String())
	}
	out.Reset()

	if code := run([]string{"search", "shared", "--project", "bbrain"}, &out, &errOut); code != 0 {
		t.Fatalf("search: code=%d stderr=%s", code, errOut.String())
	}
	// AC-6 TC-6.1
	if !strings.Contains(out.String(), "alpha shared") {
		t.Fatalf("AC-6 TC-6.1 expected bbrain fact in output, got: %s", out.String())
	}
	if strings.Contains(out.String(), "beta shared") {
		t.Fatalf("AC-6 TC-6.1 project filter leaked vexforge fact: %s", out.String())
	}

	out.Reset()
	// AC-6 combined: --project + --type together
	if code := run([]string{"search", "shared", "--project", "bbrain", "--type", "decision"}, &out, &errOut); code != 0 {
		t.Fatalf("search combined: code=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "alpha shared") {
		t.Fatalf("AC-6 combined filter: expected bbrain/decision fact, got: %s", out.String())
	}

	out.Reset()
	// AC-6 TC-6.2: no flags → identical to pre-change (unfiltered) behavior
	if code := run([]string{"search", "shared"}, &out, &errOut); code != 0 {
		t.Fatalf("search unfiltered: code=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "alpha shared") || !strings.Contains(out.String(), "beta shared") {
		t.Fatalf("AC-6 TC-6.2 unfiltered search should show both facts, got: %s", out.String())
	}
}

// BBRAIN-5 AC-3/AC-5: bbrain list CLI --project flag matches mem_browse
// filtering semantics; no filter lists all facts (project+type, title only,
// per App.Browse's shape).
func TestEndToEndList(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	var out, errOut bytes.Buffer

	if code := run([]string{"save", "--title", "bbrain fact", "--body", "b", "--type", "decision", "--project", "bbrain"}, &out, &errOut); code != 0 {
		t.Fatalf("save: code=%d stderr=%s", code, errOut.String())
	}
	out.Reset()
	if code := run([]string{"save", "--title", "vexforge fact", "--body", "b", "--type", "decision", "--project", "vexforge"}, &out, &errOut); code != 0 {
		t.Fatalf("save: code=%d stderr=%s", code, errOut.String())
	}
	out.Reset()

	if code := run([]string{"list", "--project", "bbrain"}, &out, &errOut); code != 0 {
		t.Fatalf("list: code=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "bbrain fact") {
		t.Fatalf("expected bbrain fact in output, got: %s", out.String())
	}
	if strings.Contains(out.String(), "vexforge fact") {
		t.Fatalf("project filter leaked vexforge fact: %s", out.String())
	}

	out.Reset()
	if code := run([]string{"list"}, &out, &errOut); code != 0 {
		t.Fatalf("list (no filter): code=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "bbrain fact") || !strings.Contains(out.String(), "vexforge fact") {
		t.Fatalf("no-filter list should show both facts, got: %s", out.String())
	}
}

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

func TestEndToEndWikiBuild(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer

	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"save", "--title", "Use JWT", "--project", "shopapp",
		"--type", "decision", "--body", "jwt"}, &out, &errOut); code != 0 {
		t.Fatalf("save: %s", errOut.String())
	}
	id := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out.String()), "saved "))

	// Fake agent CLI: a script that emits a page citing the saved fact id.
	jsonOut := `{"pages":[{"slug":"auth-model","category":"decisions","title":"Auth model","sources":["` + id + `"],"body":"# Auth model\n\nSee [[` + id + `]]","change_reason":"created"}]}`
	script := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'JSON'\n"+jsonOut+"\nJSON\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BBRAIN_AGENT_CLI", script)

	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "build"}, &out, &errOut); code != 0 {
		t.Fatalf("wiki build: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "projects/shopapp/decisions/auth-model.md") {
		t.Fatalf("wiki build output = %q", out.String())
	}
	page := filepath.Join(home, "wiki", "projects", "shopapp", "decisions", "auth-model.md")
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("page not written: %v", err)
	}
	if !strings.Contains(string(b), "title: Auth model") {
		t.Fatalf("page content = %s", b)
	}
	idx, _ := os.ReadFile(filepath.Join(home, "wiki", "index.md"))
	if !strings.Contains(string(idx), "auth-model.md") {
		t.Fatalf("index = %s", idx)
	}
}

// A backend that always emits malformed JSON skips every batch: nothing is
// written, so the CLI exits 1 and warns on stderr (re-run to retry). This pins
// the exit-code contract: skips + zero pages written == failure.
func TestWikiBuildAllBatchesSkippedExitsOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer

	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"save", "--title", "Use JWT", "--project", "shopapp",
		"--type", "decision", "--body", "jwt"}, &out, &errOut); code != 0 {
		t.Fatalf("save: %s", errOut.String())
	}
	// Agent emits invalid JSON every call => batch exhausts retries and is skipped.
	script := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'not json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BBRAIN_AGENT_CLI", script)

	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "build"}, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1 (nothing written but batches skipped)", code)
	}
	if !strings.Contains(errOut.String(), "skipped") || !strings.Contains(errOut.String(), "re-run") {
		t.Fatalf("stderr should warn about skipped batches, got: %q", errOut.String())
	}
}

// A backend that emits a well-formed page citing a fact id that does not exist
// (the LLM hallucinated a source): the page fails validation and is dropped, not
// aborted. With only that page, nothing is written => exit 1 with an invalid-page
// warning on stderr. Pins the CLI side of the skip-invalid-page contract.
func TestWikiBuildInvalidPageDroppedExitsOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer

	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"save", "--title", "Use JWT", "--project", "shopapp",
		"--type", "decision", "--body", "jwt"}, &out, &errOut); code != 0 {
		t.Fatalf("save: %s", errOut.String())
	}
	// Valid JSON, but the page cites a fact id that was never saved.
	jsonOut := `{"pages":[{"slug":"ghost","category":"decisions","title":"Ghost","sources":["hallucinated-id"],"body":"b","change_reason":"x"}]}`
	script := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'JSON'\n"+jsonOut+"\nJSON\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BBRAIN_AGENT_CLI", script)

	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "build"}, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1 (nothing written, page dropped); stderr=%q", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "invalid page") || !strings.Contains(errOut.String(), "re-run") {
		t.Fatalf("stderr should warn about the dropped invalid page, got: %q", errOut.String())
	}
}

func TestWikiBuildUnconfiguredFails(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	t.Setenv("BBRAIN_AGENT_CLI", "")
	var out, errOut bytes.Buffer
	run([]string{"init"}, &out, &errOut)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "build"}, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "BBRAIN_AGENT_CLI") {
		t.Fatalf("err = %q", errOut.String())
	}
}

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

	// Fake agent: emits a link to the first candidate ONLY when the source-fact
	// section is srcID, so the candidate fact's own prompt yields nothing (and we
	// never produce a self-link). awk reads the ids robustly regardless of
	// surrounding whitespace.
	script := filepath.Join(t.TempDir(), "agent.sh")
	body := "#!/bin/sh\n" +
		"in=$(cat)\n" +
		"src=$(printf '%s\\n' \"$in\" | awk '/^## Source fact$/{f=1;next} f&&/^### /{sub(/^### /,\"\"); print; exit}')\n" +
		"dst=$(printf '%s\\n' \"$in\" | awk '/^## Candidate facts$/{f=1;next} f&&/^### /{sub(/^### /,\"\"); print; exit}')\n" +
		"if [ \"$src\" = \"" + srcID + "\" ] && [ -n \"$dst\" ]; then\n" +
		"  printf '{\"links\":[{\"dst\":\"%s\",\"relation\":\"relates\",\"why\":\"both jwt\"}]}' \"$dst\"\n" +
		"else\n" +
		"  printf '{\"links\":[]}'\n" +
		"fi\n"
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

// The agent emits malformed JSON on every call, so every fact's linking exhausts
// its retries and is dropped. Nothing is written => exit 1 with a re-run warning
// on stderr. Pins the CLI side of the link skip-on-exhaust contract (mirrors
// TestWikiBuildAllBatchesSkippedExitsOne).
func TestWikiLinkAllFactsFailExitsOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer

	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	// Two related facts so at least one has a candidate and the runner is invoked.
	run([]string{"save", "--title", "JWT access", "--project", "p", "--type", "decision", "--body", "jwt access token"}, &out, &errOut)
	run([]string{"save", "--title", "JWT refresh", "--project", "p", "--type", "decision", "--body", "jwt refresh token"}, &out, &errOut)

	script := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'not json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BBRAIN_AGENT_CLI", script)

	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "link"}, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1 (nothing written but facts dropped); stderr=%q", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "dropped") || !strings.Contains(errOut.String(), "re-run") {
		t.Fatalf("stderr should warn about dropped facts, got: %q", errOut.String())
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
	if code := run([]string{"link", "--from", x, "--to", y, "--relation", "relates", "--why", "x"}, &out, &errOut); code != 0 {
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

func TestWikiLintArchivedLinkIsInfoAndExitsZero(t *testing.T) {
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
	out.Reset()
	errOut.Reset()
	if code := run([]string{"link", "--from", x, "--to", y, "--relation", "relates", "--why", "x"}, &out, &errOut); code != 0 {
		t.Fatalf("link: %s", errOut.String())
	}
	// Archive y by moving its file to the archive tier (rename semantics, story-01).
	if err := os.MkdirAll(filepath.Join(home, "raws", "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(home, "raws", "facts", y+".md"),
		filepath.Join(home, "raws", "archive", y+".md")); err != nil {
		t.Fatal(err)
	}

	// The only finding is informative -> printed with "info: " prefix, exit 0.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "lint"}, &out, &errOut); code != 0 {
		t.Fatalf("wiki lint exit = %d, want 0; out=%q err=%q", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "info: archived-link") {
		t.Fatalf("lint report = %q", out.String())
	}

	// --fix must NOT remove the active->archived link.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"wiki", "lint", "--fix"}, &out, &errOut); code != 0 {
		t.Fatalf("wiki lint --fix exit = %d, want 0; out=%q err=%q", code, out.String(), errOut.String())
	}
	b, err := os.ReadFile(filepath.Join(home, "raws", "facts", x+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), y) {
		t.Fatalf("--fix dropped the link to the archived fact:\n%s", b)
	}
}

// An archived-link (informative) must not mask a real, non-info issue in the
// same run: the exit code has to reflect the real issue, not just the
// info-only case.
func TestWikiLintMixedArchivedAndRealIssueExitsNonZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	t.Setenv("BBRAIN_AGENT_CLI", "")
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
	out.Reset()
	errOut.Reset()
	if code := run([]string{"link", "--from", x, "--to", y, "--relation", "relates", "--why", "x"}, &out, &errOut); code != 0 {
		t.Fatalf("link: %s", errOut.String())
	}
	// Archive y: leaves an informative archived-link on x->y.
	if err := os.MkdirAll(filepath.Join(home, "raws", "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(home, "raws", "facts", y+".md"),
		filepath.Join(home, "raws", "archive", y+".md")); err != nil {
		t.Fatal(err)
	}
	// A real dangling-link: x -> an id that exists in neither tier.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"link", "--from", x, "--to", "does-not-exist", "--relation", "relates", "--why", "x"}, &out, &errOut); code == 0 {
		t.Fatalf("expected link to a nonexistent fact to fail, got exit 0: %s", out.String())
	}
	// link validates existence up front, so forge the dangling edge directly
	// on disk instead — that's the only way to get a real dangling-link past
	// the CLI's own guard.
	xPath := filepath.Join(home, "raws", "facts", x+".md")
	b, err := os.ReadFile(xPath)
	if err != nil {
		t.Fatal(err)
	}
	// Match the existing list item's indentation (4/6 spaces, as written by
	// the store) and append after it rather than splicing into the middle
	// of the "links:" block, so the YAML stays well-formed.
	marker := "      why: x\n"
	patched := strings.Replace(string(b), marker,
		marker+"    - target: '[[does-not-exist]]'\n      relation: relates\n      why: x\n", 1)
	if patched == string(b) {
		t.Fatalf("could not patch links: block into %s", xPath)
	}
	if err := os.WriteFile(xPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	code := run([]string{"wiki", "lint"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("wiki lint exit = 0, want non-zero when a real dangling-link coexists with an archived-link; out=%q", out.String())
	}
	if !strings.Contains(out.String(), "info: archived-link") {
		t.Fatalf("lint report missing archived-link: %q", out.String())
	}
	if !strings.Contains(out.String(), "dangling-link") || strings.Contains(out.String(), "info: dangling-link") {
		t.Fatalf("lint report missing a non-info dangling-link: %q", out.String())
	}
}

func TestEndToEndInstallUninstallProject(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	vault := filepath.Join(t.TempDir(), "vault")
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer

	args := []string{"install", "--non-interactive", "--agent", "claude-code", "--scope", "project",
		"--vault", vault, "--project", proj}
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("install: %s", errOut.String())
	}
	for _, p := range []string{
		filepath.Join(vault, "memory", "raws", "facts"),
		filepath.Join(vault, "CLAUDE.md"),
		filepath.Join(proj, ".mcp.json"),
		filepath.Join(proj, "CLAUDE.md"),
		filepath.Join(proj, ".claude", "settings.json"),
		filepath.Join(proj, ".claude", "skills", "bbrain-recall", "SKILL.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("install did not create %s: %v", p, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(proj, ".mcp.json")); !strings.Contains(string(b), filepath.Join(vault, "memory")) {
		t.Fatalf(".mcp.json BBRAIN_HOME wrong:\n%s", b)
	}

	// uninstall reverses (vault kept)
	out.Reset()
	errOut.Reset()
	uargs := []string{"uninstall", "--scope", "project", "--project", proj, "--vault", vault}
	if code := run(uargs, &out, &errOut); code != 0 {
		t.Fatalf("uninstall: %s", errOut.String())
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "CLAUDE.md")); strings.Contains(string(b), "BBRAIN:BEGIN") {
		t.Fatal("uninstall left the managed block")
	}
	if _, err := os.Stat(filepath.Join(vault, "memory")); err != nil {
		t.Fatal("uninstall without --purge deleted the vault")
	}
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	proj := t.TempDir()
	vault := filepath.Join(t.TempDir(), "vault")
	t.Setenv("HOME", t.TempDir())
	var out, errOut bytes.Buffer
	args := []string{"install", "--non-interactive", "--scope", "project", "--vault", vault, "--project", proj, "--dry-run"}
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("install --dry-run: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "[dry-run]") {
		t.Fatalf("missing dry-run banner: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(proj, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote .mcp.json")
	}
	if _, err := os.Stat(filepath.Join(proj, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote CLAUDE.md")
	}
	if _, err := os.Stat(filepath.Join(proj, ".claude")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote .claude/")
	}
}

func TestInstallReindexes(t *testing.T) {
	proj := t.TempDir()
	vault := filepath.Join(t.TempDir(), "vault")
	t.Setenv("HOME", t.TempDir())
	var out, errBuf bytes.Buffer
	code := runWithIn([]string{"install", "--non-interactive", "--scope", "project", "--vault", vault, "--project", proj},
		strings.NewReader(""), &out, &errBuf)
	if code != 0 {
		t.Fatalf("install exit %d: %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "reindexed") {
		t.Fatalf("install output missing 'reindexed': %q", out.String())
	}
}

func TestSetupCommandRemoved(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"setup", "claude-code"}, &out, &errOut); code == 0 {
		t.Fatal("`setup` should be removed (non-zero exit expected)")
	}
}

func TestEndToEndWatchOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	run([]string{"init"}, &out, &errOut)
	run([]string{"save", "--title", "Watched fact", "--project", "p", "--type", "decision", "--body", "b"}, &out, &errOut)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"watch", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("watch --once: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "reindexed") {
		t.Fatalf("watch output = %q", out.String())
	}
}

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
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("old brain root still present after move")
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

func runStdin(t *testing.T, args []string, stdin string, out, errOut *bytes.Buffer) int {
	t.Helper()
	return runWithIn(args, strings.NewReader(stdin), out, errOut)
}

func TestEndToEndMCP(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	// Drive the server over stdin/stdout via run(): initialize, then save+search.
	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mem_save","arguments":{"type":"decision","title":"Use JWT","body":"stateless","project":"shopapp","scope":"project"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mem_search","arguments":{"query":"jwt"}}}`,
	}, "\n") + "\n"

	out.Reset()
	errOut.Reset()
	code := runStdin(t, []string{"mcp"}, reqs, &out, &errOut)
	if code != 0 {
		t.Fatalf("mcp exit=%d err=%s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Expect 3 responses (initialize, mem_save, mem_search); the notification yields none.
	if len(lines) != 3 {
		t.Fatalf("want 3 responses, got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"protocolVersion"`) {
		t.Fatalf("initialize resp = %s", lines[0])
	}
	if !strings.Contains(lines[2], "Use JWT") {
		t.Fatalf("search resp = %s", lines[2])
	}
}

// TestMCPHomeFlag locks in the fix for the silent-null bug: `bbrain mcp --home`
// must serve the brain at that path even when BBRAIN_HOME points elsewhere (or is
// unset). Before the flag existed, cmdMCP ignored its args and always fell back to
// brainRoot(), so a wrong/empty BBRAIN_HOME yielded {"results": null}.
func TestMCPHomeFlag(t *testing.T) {
	realHome := t.TempDir()
	emptyHome := t.TempDir()

	// Seed the real brain via BBRAIN_HOME (save/init resolve only via the env).
	t.Setenv("BBRAIN_HOME", realHome)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	if code := run([]string{"save", "--title", "Juan Jara", "--project", "fuel-cx", "--type", "note", "--body", "works on fuel-cx"}, &out, &errOut); code != 0 {
		t.Fatalf("save: %s", errOut.String())
	}

	// Now point BBRAIN_HOME at an empty brain: --home must override the env fallback.
	t.Setenv("BBRAIN_HOME", emptyHome)

	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mem_search","arguments":{"query":"Juan Jara"}}}`,
	}, "\n") + "\n"

	out.Reset()
	errOut.Reset()
	if code := runStdin(t, []string{"mcp", "--home", realHome}, reqs, &out, &errOut); code != 0 {
		t.Fatalf("mcp exit=%d err=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Juan Jara") {
		t.Fatalf("mcp --home did not serve the brain at realHome; out=%s", out.String())
	}
}

// TestMCPWarnsOnMissingBrain locks in the loud-failure signal: pointing mcp at a
// home with no brain must emit a warning to stderr (not exit non-zero, and not
// stay silent — silence is what made the original bug invisible).
func TestMCPWarnsOnMissingBrain(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "no-brain-here")

	var out, errOut bytes.Buffer
	// Empty stdin: the server reads to EOF and exits cleanly.
	code := runStdin(t, []string{"mcp", "--home", missing}, "", &out, &errOut)
	if code != 0 {
		t.Fatalf("mcp exit=%d err=%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "warning") || !strings.Contains(errOut.String(), missing) {
		t.Fatalf("expected a stderr warning naming %q, got: %q", missing, errOut.String())
	}
}

// TestWikiWarnsOnMissingBrain locks in the loud-failure signal for the CLI wiki
// commands: pointing --home at a home with no brain must warn on stderr instead
// of running silently against an empty vault (the real bug: BBRAIN_HOME unset ->
// brainRoot() falls back to ~/.bbrain/default -> wiki exits 0 doing nothing).
func TestWikiWarnsOnMissingBrain(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	t.Setenv("BBRAIN_AGENT_CLI", "")
	missing := filepath.Join(t.TempDir(), "no-brain-here")
	for _, sub := range []string{"build", "link", "lint"} {
		var out, errOut bytes.Buffer
		run([]string{"wiki", sub, "--home", missing}, &out, &errOut)
		if !strings.Contains(errOut.String(), "warning") || !strings.Contains(errOut.String(), missing) {
			t.Fatalf("wiki %s: expected a stderr warning naming %q, got: %q", sub, missing, errOut.String())
		}
	}
}

func TestEndToEndContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	run([]string{"init"}, &out, &errOut)
	run([]string{"save", "--title", "Context fact", "--project", "p", "--type", "decision", "--body", "jwt"}, &out, &errOut)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"context", "--home", home}, &out, &errOut); code != 0 {
		t.Fatalf("context: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "Context fact") {
		t.Fatalf("context output = %q", out.String())
	}
}

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
