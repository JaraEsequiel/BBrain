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
