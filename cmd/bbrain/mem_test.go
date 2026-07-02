package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memSave is a small test helper: saves a fact and returns its id ("saved <id>"
// -> "<id>"). Mirrors the `saved` helper used inline in other CLI tests.
func memSave(t *testing.T, title, typ, project string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run([]string{"save", "--title", title, "--project", project,
		"--type", typ, "--body", "b"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("save %q failed: %s", title, errOut.String())
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out.String()), "saved "))
}

func TestRunUsageMentionsMem(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "mem") {
		t.Fatalf("usage = %q, want it to mention 'mem'", errOut.String())
	}
}

func TestMemUnknownSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"mem", "frobnicate"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown subcommand") {
		t.Fatalf("stderr = %q, want it to mention unknown subcommand", errOut.String())
	}
}

func TestMemArchiveNoFilterOrIDIsUsageError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error); stderr=%q", code, errOut.String())
	}
}

// Dry-run default: no --apply must never touch disk, and must print the plan
// with id, type and reason.
func TestMemArchiveDryRunDoesNotTouchDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	id := memSave(t, "Old decision", "session-summary", "p")

	before, err := os.ReadFile(filepath.Join(home, "raws", "facts", id+".md"))
	if err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive", "--type", "session-summary"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("dry-run exit = %d, want 0; stderr=%q", code, errOut.String())
	}

	// Still present, byte-identical, in the active tier.
	after, err := os.ReadFile(filepath.Join(home, "raws", "facts", id+".md"))
	if err != nil {
		t.Fatalf("dry-run must not move the file: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("dry-run modified the fact on disk")
	}
	if _, err := os.Stat(filepath.Join(home, "raws", "archive", id+".md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create an archive file")
	}

	if !strings.Contains(out.String(), "[dry-run] would archive 1 fact(s); run with --apply") {
		t.Fatalf("dry-run output missing header: %q", out.String())
	}
	if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "session-summary") {
		t.Fatalf("dry-run output = %q, want it to mention id + type", out.String())
	}
	if !strings.Contains(out.String(), "type=session-summary") {
		t.Fatalf("dry-run output = %q, want the reason type=session-summary", out.String())
	}
}

// Skipped candidates (pinned, not found, already archived) are reported with
// their reason, and never cause the whole dry-run to fail.
func TestMemArchiveDryRunReportsSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	id := memSave(t, "Pinned fact", "decision", "p")
	out.Reset()
	errOut.Reset()
	// Flip pinned by rewriting the .md frontmatter directly (no CLI flag to pin).
	p := filepath.Join(home, "raws", "facts", id+".md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	pinned := strings.Replace(string(b), "id: "+id, "id: "+id+"\npinned: true", 1)
	if err := os.WriteFile(p, []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}

	code := run([]string{"mem", "archive", id, "missing-id-not-real"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("dry-run exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "pinned") {
		t.Fatalf("dry-run output = %q, want it to report skipped pinned", out.String())
	}
	if !strings.Contains(out.String(), "not found") {
		t.Fatalf("dry-run output = %q, want it to report skipped not found", out.String())
	}
}

// --apply archives the candidates via App.Archive and reports "archived <id>".
func TestMemArchiveApplyMovesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	id := memSave(t, "Old decision", "session-summary", "p")

	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive", "--type", "session-summary", "--apply"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("apply exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "archived "+id) {
		t.Fatalf("apply output = %q, want it to report archived %s", out.String(), id)
	}
	if _, err := os.Stat(filepath.Join(home, "raws", "facts", id+".md")); !os.IsNotExist(err) {
		t.Fatalf("apply must move the fact out of raws/facts/")
	}
	if _, err := os.Stat(filepath.Join(home, "raws", "archive", id+".md")); err != nil {
		t.Fatalf("apply must move the fact into raws/archive/: %v", err)
	}
}

// --older-than accepts both "Nd" and stdlib duration units.
func TestMemArchiveOlderThanFiltersOnUpdatedAt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	id := memSave(t, "Ancient fact", "decision", "p")
	// Rewrite updated_at to 60 days ago so --older-than 30d matches it.
	p := filepath.Join(home, "raws", "facts", id+".md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "updated_at:") {
			lines[i] = "updated_at: " + old
		}
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive", "--older-than", "30d"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), id) {
		t.Fatalf("dry-run output = %q, want it to select the old fact", out.String())
	}
	if !strings.Contains(out.String(), "older-than") {
		t.Fatalf("dry-run output = %q, want reason older-than", out.String())
	}

	// A fresh fact must NOT qualify.
	fresh := memSave(t, "Fresh fact", "decision", "p")
	out.Reset()
	errOut.Reset()
	if code := run([]string{"mem", "archive", "--older-than", "720h"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if strings.Contains(out.String(), fresh) {
		t.Fatalf("dry-run output = %q, must not select the fresh fact", out.String())
	}
}

func TestMemArchiveTypeRepeatableFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	a := memSave(t, "A", "discovery", "p")
	b := memSave(t, "B", "session-summary", "p")
	c := memSave(t, "C", "decision", "p")

	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive", "--type", "discovery", "--type", "session-summary"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), a) || !strings.Contains(out.String(), b) {
		t.Fatalf("output = %q, want it to select both a and b", out.String())
	}
	if strings.Contains(out.String(), c) {
		t.Fatalf("output = %q, must not select c (decision)", out.String())
	}
}

func TestMemArchiveDistilledFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	id := memSave(t, "Cited fact", "discovery", "p")

	// Fabricate a wiki page that cites id in its frontmatter sources, mirroring
	// wiki.SourceIDs' contract (ParsePageMeta reads `sources:` list).
	wikiDir := filepath.Join(home, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	page := "---\ntitle: Test Page\ncategory: discovery\nsources:\n  - " + id + "\n---\n\n# Test Page\n"
	if err := os.WriteFile(filepath.Join(wikiDir, "test-page.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive", "--distilled"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), id) {
		t.Fatalf("output = %q, want it to select the cited fact", out.String())
	}
	if !strings.Contains(out.String(), "distilled") {
		t.Fatalf("output = %q, want reason distilled", out.String())
	}
}

func TestMemArchiveProjectFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	a := memSave(t, "A", "decision", "alpha")
	b := memSave(t, "B", "decision", "beta")

	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive", "--type", "decision", "--project", "alpha"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), a) {
		t.Fatalf("output = %q, want it to select a", out.String())
	}
	if strings.Contains(out.String(), b) {
		t.Fatalf("output = %q, must not select b", out.String())
	}
}

func TestMemArchiveBadOlderThanIsUsageError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive", "--older-than", "basura"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a bad --older-than value; stderr=%q", code, errOut.String())
	}
}

// --home follows the same pattern as cmdWikiBuild: overrides BBRAIN_HOME.
func TestMemArchiveHomeFlag(t *testing.T) {
	realHome := t.TempDir()
	emptyHome := t.TempDir()

	t.Setenv("BBRAIN_HOME", realHome)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	id := memSave(t, "In real home", "decision", "p")

	// Now point BBRAIN_HOME at an empty brain: --home must override it.
	t.Setenv("BBRAIN_HOME", emptyHome)
	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "archive", "--type", "decision", "--home", realHome}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), id) {
		t.Fatalf("output = %q, want --home to point at realHome's fact %s", out.String(), id)
	}
}

func TestMemWarnsOnMissingBrain(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "no-brain-here")
	var out, errOut bytes.Buffer
	code := run([]string{"mem", "archive", "--type", "decision", "--home", missing}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (no facts to select, just a warning); stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "no brain at") {
		t.Fatalf("stderr = %q, want warning about missing brain", errOut.String())
	}
}

// mem unarchive.

func TestMemUnarchiveNoIDsIsUsageError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "unarchive"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestMemUnarchiveRestoresFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	id := memSave(t, "Archivable", "decision", "p")
	out.Reset()
	errOut.Reset()
	if code := run([]string{"mem", "archive", "--apply", id}, &out, &errOut); code != 0 {
		t.Fatalf("archive --apply: %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "unarchive", id}, &out, &errOut)
	if code != 0 {
		t.Fatalf("unarchive exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "unarchived "+id) {
		t.Fatalf("output = %q, want it to report unarchived %s", out.String(), id)
	}
	if _, err := os.Stat(filepath.Join(home, "raws", "facts", id+".md")); err != nil {
		t.Fatalf("unarchive must restore the fact to raws/facts/: %v", err)
	}
}

// One id failing (already active / not archived) prints to stderr and keeps
// processing the rest; exit 1 if any failed.
func TestMemUnarchivePartialFailureContinuesAndExitsOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	ok := memSave(t, "Archivable", "decision", "p")
	out.Reset()
	errOut.Reset()
	if code := run([]string{"mem", "archive", "--apply", ok}, &out, &errOut); code != 0 {
		t.Fatalf("archive --apply: %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code := run([]string{"mem", "unarchive", "not-a-real-id", ok}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (one id failed); stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "unarchived "+ok) {
		t.Fatalf("output = %q, want the valid id to still be unarchived", out.String())
	}
	if !strings.Contains(errOut.String(), "not-a-real-id") {
		t.Fatalf("stderr = %q, want it to mention the failing id", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, "raws", "facts", ok+".md")); err != nil {
		t.Fatalf("valid id must still be restored: %v", err)
	}
}
