package main

import (
	"bytes"
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
