package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestArchiveLifecycleE2E exercises the full fact-lifecycle feature end to end
// through the CLI (run/runWithIn), against a synthetic vault, and asserts each
// of the PRD's 6 measurable success criteria
// (Projects/BBrain-fact-lifecycle/planning/prd.md, "Criterios de éxito
// medibles"). Criterion 2 (batch count scales with active facts, not
// active+archived) is NOT re-asserted here: it requires injecting a fake LLM
// Runner, which the CLI does not expose (cmdWikiBuild always builds a real
// CLIRunner) — it is already covered end to end at the App/wiki.Build layer by
// internal/wiki/wiki_test.go:TestBuildArchivedOutOfBatchesButCitable and
// internal/app/app_test.go:TestWikiBuildPassesArchivedAsCitationUniverse.
func TestArchiveLifecycleE2E(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)

	run1 := func(args ...string) (string, string, int) {
		var out, errOut bytes.Buffer
		code := run(args, &out, &errOut)
		return out.String(), errOut.String(), code
	}

	if out, errOut, code := run1("init"); code != 0 {
		t.Fatalf("init: code=%d out=%q err=%q", code, out, errOut)
	}

	// --- Fixtures -----------------------------------------------------
	// citedID: cited by a wiki page fixture -> qualifies under --distilled.
	citedID := memSave(t, "Episodic cited by wiki", "session-summary", "vexos")
	// uncitedID: NOT referenced by any page -> must never qualify under
	// --distilled (criterion 6), even though it matches --type.
	uncitedID := memSave(t, "Episodic never distilled", "session-summary", "vexos")
	// pinnedID: pinned facts are never archive candidates, regardless of filter.
	pinnedID := memSave(t, "Pinned episodic", "session-summary", "vexos")
	pinFact(t, home, pinnedID)

	// A wiki page fixture (hand-written frontmatter, per story-08 notes) that
	// cites citedID as its only source, so lint/distilled-coverage have real
	// signal to work with.
	wikiDir := filepath.Join(home, "wiki")
	if err := os.MkdirAll(filepath.Join(wikiDir, "projects", "vexos", "decisions"), 0o755); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(wikiDir, "projects", "vexos", "decisions", "episodic-recap.md")
	pageContent := "---\ntitle: Episodic recap\ncategory: decisions\nsources:\n  - " +
		citedID + "\ngenerated_at: " + time.Now().UTC().Format(time.RFC3339) + "\n---\n\n" +
		"# Episodic recap\n\nDistilled from [[" + citedID + "]].\n"
	if err := os.WriteFile(pagePath, []byte(pageContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Hash the raws/ tree before ANY `mem archive` call (dry-run or not), so
	// criterion 5 below has an untainted baseline.
	rawsBefore := hashTree(t, filepath.Join(home, "raws"))

	// ================================================================
	// Criterion 6 — Seguridad de cobertura: --distilled only ever selects
	// facts cited by >=1 page. uncitedID must never appear in the plan even
	// though it shares --type with citedID.
	// ================================================================
	planOut, _, code := run1("mem", "archive", "--distilled", "--type", "session-summary")
	if code != 0 {
		t.Fatalf("mem archive --distilled (dry-run): code=%d out=%q", code, planOut)
	}
	if !strings.Contains(planOut, citedID) {
		t.Fatalf("criterion 6: --distilled plan = %q, want it to select the cited fact %s", planOut, citedID)
	}
	if strings.Contains(planOut, uncitedID) {
		t.Fatalf("criterion 6: --distilled plan = %q, must NOT select the uncited fact %s", planOut, uncitedID)
	}
	// pinnedID is reported as skipped (not selected), never a candidate.
	if strings.Contains(planOut, "\t"+pinnedID+"\t") {
		t.Fatalf("criterion 6/pinned: --distilled plan = %q, pinned fact must not be a candidate", planOut)
	}

	// ================================================================
	// Criterion 5 — Reversibilidad operativa: without --apply, raws/ is
	// untouched (hash the tree before/after) and stdout lists what it would do.
	// ================================================================
	if !strings.Contains(planOut, "[dry-run] would archive") {
		t.Fatalf("criterion 5: dry-run stdout = %q, want the dry-run header", planOut)
	}
	rawsAfter := hashTree(t, filepath.Join(home, "raws"))
	if rawsBefore != rawsAfter {
		t.Fatalf("criterion 5: dry-run mutated raws/ (before=%s after=%s)", rawsBefore, rawsAfter)
	}

	// ================================================================
	// Criterion 4 — Lint verde post-archivado: same non-info issue count
	// before and after archiving a fact cited by a page; the only new issue
	// allowed is the informative archived-link (if the fixture had a fact-side
	// link into the archive — here we assert the simpler, PRD-stated bar:
	// missing-source/orphan-page/dangling-link attributable to the archived
	// fact stay at 0).
	// ================================================================
	lintBefore, _, code := run1("wiki", "lint")
	if code != 0 {
		t.Fatalf("wiki lint (pre-archive): code=%d out=%q", code, lintBefore)
	}
	if strings.Contains(lintBefore, "missing-source") || strings.Contains(lintBefore, "orphan-page") ||
		strings.Contains(lintBefore, "dangling-link") {
		t.Fatalf("criterion 4: lint before archiving is already dirty: %q", lintBefore)
	}
	nonInfoBefore := countNonInfoLintLines(lintBefore)

	// --- Now actually archive citedID (criterion 1/3/4 need it applied). ---
	if out, errOut, code := run1("mem", "archive", "--apply", citedID); code != 0 {
		t.Fatalf("mem archive --apply %s: code=%d out=%q err=%q", citedID, code, out, errOut)
	}

	lintAfter, _, code := run1("wiki", "lint")
	if code != 0 {
		t.Fatalf("wiki lint (post-archive): code=%d out=%q", code, lintAfter)
	}
	if strings.Contains(lintAfter, "missing-source") || strings.Contains(lintAfter, "orphan-page") ||
		strings.Contains(lintAfter, "dangling-link") {
		t.Fatalf("criterion 4: archiving a cited fact must not manufacture missing-source/orphan-page/dangling-link: %q", lintAfter)
	}
	nonInfoAfter := countNonInfoLintLines(lintAfter)
	if nonInfoBefore != nonInfoAfter {
		t.Fatalf("criterion 4: non-info lint issue count changed (before=%d after=%d); before=%q after=%q",
			nonInfoBefore, nonInfoAfter, lintBefore, lintAfter)
	}

	// ================================================================
	// Criterion 1 — Recall limpio: mem_search (bbrain search) with the
	// archived fact's exact term gives 0 hits once archived.
	// ================================================================
	searchOut, _, code := run1("search", "Episodic cited by wiki")
	if code != 0 {
		t.Fatalf("search after archive: code=%d out=%q", code, searchOut)
	}
	if strings.Contains(searchOut, citedID) {
		t.Fatalf("criterion 1: search for the archived fact's exact title = %q, want 0 hits", searchOut)
	}

	// ================================================================
	// Criterion 3 — Cero pérdida: archive->unarchive round-trips the .md
	// byte-identical, and the fact is searchable again.
	// ================================================================
	archivedPath := filepath.Join(home, "raws", "archive", citedID+".md")
	archivedBytes, err := os.ReadFile(archivedPath)
	if err != nil {
		t.Fatalf("criterion 3: archived .md missing: %v", err)
	}

	if out, errOut, code := run1("mem", "unarchive", citedID); code != 0 {
		t.Fatalf("mem unarchive %s: code=%d out=%q err=%q", citedID, code, out, errOut)
	}
	restoredPath := filepath.Join(home, "raws", "facts", citedID+".md")
	restoredBytes, err := os.ReadFile(restoredPath)
	if err != nil {
		t.Fatalf("criterion 3: restored .md missing: %v", err)
	}
	if !bytes.Equal(archivedBytes, restoredBytes) {
		t.Fatalf("criterion 3: round-trip is not byte-identical\narchived=%q\nrestored=%q", archivedBytes, restoredBytes)
	}

	searchAfterUnarchive, _, code := run1("search", "Episodic cited by wiki")
	if code != 0 {
		t.Fatalf("search after unarchive: code=%d out=%q", code, searchAfterUnarchive)
	}
	if !strings.Contains(searchAfterUnarchive, citedID) {
		t.Fatalf("criterion 3: search after unarchive = %q, want the fact to be findable again (id %s)",
			searchAfterUnarchive, citedID)
	}
}

// pinFact flips pinned:true on a saved fact's frontmatter directly, mirroring
// the pattern in TestMemArchiveDryRunReportsSkipped (no CLI flag to pin).
func pinFact(t *testing.T, home, id string) {
	t.Helper()
	p := filepath.Join(home, "raws", "facts", id+".md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	pinned := strings.Replace(string(b), "id: "+id, "id: "+id+"\npinned: true", 1)
	if err := os.WriteFile(p, []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hashTree returns a content fingerprint of every file under dir (relative
// path + bytes), so a caller can assert a subtree is byte-identical
// before/after an operation without depending on mtimes. An absent dir hashes
// to a stable sentinel.
func hashTree(t *testing.T, dir string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sb.WriteString(rel)
		sb.WriteByte(0)
		sb.Write(b)
		sb.WriteByte(0)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("hashTree(%s): %v", dir, err)
	}
	return sb.String()
}

// countNonInfoLintLines counts cmdWikiLint output lines that are real issues
// (not "fixed:" and not "info:" — see cmdWikiLint's stdout format).
func countNonInfoLintLines(out string) int {
	n := 0
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" || strings.HasPrefix(l, "info:") || strings.HasPrefix(l, "fixed:") {
			continue
		}
		n++
	}
	return n
}
