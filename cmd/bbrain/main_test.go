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
