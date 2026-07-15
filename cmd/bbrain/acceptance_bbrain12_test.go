package main

// Acceptance suite for BBRAIN-12 (MCP auto-reindex of hand-edited facts),
// AC-1 and AC-4 halves. See
// .dev-tools/plans/BBRAIN-11/BBRAIN-12-mcp-auto-reindex.md for the full
// AC → Task Coverage table. Per this suite's own dispatch: one test per AC
// (not one per TC-n.m) — every TC-n.m for these two ACs is covered as a
// sub-check inside its AC's single test function.
//
// AC-2 (concurrency guard) lives in internal/app/acceptance_bbrain12_test.go
// and AC-3 (zero-cost-when-unchanged) lives in
// internal/mcp/acceptance_bbrain12_test.go — each grounded at the interface
// the plan actually produces for that AC.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestAcceptance_AC1_HandEditedFactReflectedInMemSearchViaLiveMCPSession drives
// a real `bbrain mcp` session over stdin/stdout — the exact surface an agent
// uses — via an io.Pipe so a hand-edit can happen mid-session between
// requests, matching AC-1's "sesión bbrain mcp viva" framing.
//
//   - TC-1.1 (positive): a fact hand-edited on disk mid-session — bypassing
//     mem_save entirely — is reflected by a later mem_search call, with no
//     manual command run by the caller.
//   - TC-1.2 (negative, no-op ticks don't disturb search) is exercised by
//     AC-3's dedicated test (internal/mcp), which asserts no reindex fires
//     absent a real change — the same guarantee from the fingerprint-gate
//     angle rather than re-deriving it here.
//   - TC-1.3 (confirmatory): mem_get on the same hand-edited fact, issued
//     immediately (no extra wait beyond what already elapsed for TC-1.1),
//     already reflects the edit — it reads the .md straight from disk and was
//     never affected by this staleness class. This proves the new mechanism
//     didn't regress that existing behavior, not that it produces it.
//
// The test also confirms the mcp process exits cleanly on stdin EOF (no
// goroutine leak from whatever background mechanism AC-1 introduces).
func TestAcceptance_AC1_HandEditedFactReflectedInMemSearchViaLiveMCPSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var initOut, initErr bytes.Buffer
	if code := run([]string{"init"}, &initOut, &initErr); code != 0 {
		t.Fatalf("init: %s", initErr.String())
	}

	pr, pw := io.Pipe()
	var mcpOut, mcpErr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runWithIn([]string{"mcp"}, pr, &mcpOut, &mcpErr)
	}()

	write := func(line string) {
		if _, err := io.WriteString(pw, line+"\n"); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mem_save","arguments":{"type":"note","title":"before","body":"before body","project":"e2e","scope":"project"}}}`)
	// The pipe write only guarantees the bytes reached the server's scanner,
	// not that Serve has finished handling the request yet — give mem_save a
	// moment to actually write the .md file before reading the facts dir.
	time.Sleep(200 * time.Millisecond)

	factsDir := filepath.Join(home, "raws", "facts")
	entries, err := os.ReadDir(factsDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a saved fact file, dir read err=%v entries=%d", err, len(entries))
	}
	factPath := filepath.Join(factsDir, entries[0].Name())
	body, err := os.ReadFile(factPath)
	if err != nil {
		t.Fatalf("read fact: %v", err)
	}
	// The hand-edit: bypasses mem_save/mem_delete entirely, exactly the
	// scenario AC-1 is about.
	edited := strings.Replace(string(body), "before body", "handedited-marker body", 1)
	if err := os.WriteFile(factPath, []byte(edited), 0o644); err != nil {
		t.Fatalf("hand-edit fact: %v", err)
	}
	// Give whatever background mechanism AC-1 introduces a window to notice
	// and reindex before the next mem_search.
	time.Sleep(2500 * time.Millisecond)

	write(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mem_search","arguments":{"query":"handedited-marker"}}}`)

	entries2, err := os.ReadDir(factsDir)
	if err != nil || len(entries2) == 0 {
		t.Fatalf("expected the fact file to still be there for mem_get: err=%v entries=%d", err, len(entries2))
	}
	factID := strings.TrimSuffix(entries2[0].Name(), ".md")
	write(fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"mem_get","arguments":{"id":%q}}}`, factID))
	pw.Close()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("mcp exit=%d err=%s", code, mcpErr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AC-1: mcp process did not exit after stdin EOF — background mechanism may have leaked")
	}

	lines := strings.Split(strings.TrimSpace(mcpOut.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 responses, got %d:\n%s", len(lines), mcpOut.String())
	}
	if !strings.Contains(lines[2], "handedited-marker") {
		t.Fatalf("AC-1 TC-1.1: mem_search did not reflect the hand-edited fact with no manual command: %s", lines[2])
	}
	if !strings.Contains(lines[3], "handedited-marker") {
		t.Fatalf("AC-1 TC-1.3: mem_get did not reflect the hand-edited fact: %s", lines[3])
	}
}

// repoRoot resolves the repo root from this test file's own location so
// README.md can be read regardless of the working directory `go test` runs
// from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// this file lives at <root>/cmd/bbrain/acceptance_bbrain12_test.go
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestAcceptance_AC4_CLIOnlyEditWithNoLiveSessionStaysStaleAndIsDocumented
// covers AC-4, which is deliberately an out-of-scope, documented-only edge
// (no code resolves it):
//
//   - TC-4.1 (positive): README.md carries a one-line note about this edge —
//     a hand-edit made with no live `bbrain mcp`/`bbrain watch` session stays
//     stale until a manual `bbrain reindex`.
//   - TC-4.2 (negative, scope guard): a fact hand-edited on disk with no live
//     `bbrain mcp` (nor `bbrain watch`) session running must NOT be picked up
//     by a plain `bbrain search` — proving no code anywhere tries to auto-fix
//     this case. This sub-check is expected to already pass today (and must
//     keep passing): it pins down the boundary this Story deliberately does
//     not cross, not a new behavior the Story adds.
func TestAcceptance_AC4_CLIOnlyEditWithNoLiveSessionStaysStaleAndIsDocumented(t *testing.T) {
	// TC-4.1. The plan (Task 5) extends the existing "watch" bullet under
	// Architecture in place, so the new sentences must live in that same
	// bullet's paragraph — matching the two required substrings anywhere in
	// the whole README would also match unrelated pre-existing mentions of
	// "bbrain mcp"/"bbrain reindex" elsewhere in the doc (line ~15, ~69, ~98),
	// which would pass without the note ever being written.
	readme, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	text := string(readme)
	marker := "**`watch`**"
	idx := strings.Index(text, marker)
	if idx == -1 {
		t.Fatalf("AC-4 TC-4.1: README no longer has the baseline watch bullet to extend")
	}
	// The bullet's paragraph runs until the next bullet ("\n- ") or a markdown
	// heading, whichever comes first.
	rest := text[idx:]
	end := len(rest)
	for _, sep := range []string{"\n- ", "\n#"} {
		if i := strings.Index(rest[1:], sep); i != -1 && i+1 < end {
			end = i + 1
		}
	}
	watchParagraph := rest[:end]
	if !strings.Contains(watchParagraph, "reindex on change") {
		t.Fatalf("AC-4 TC-4.1: watch bullet paragraph changed shape unexpectedly: %q", watchParagraph)
	}
	hasCLIOnlyEdge := strings.Contains(watchParagraph, "bbrain mcp") &&
		strings.Contains(watchParagraph, "bbrain reindex") &&
		(strings.Contains(watchParagraph, "no live") || strings.Contains(watchParagraph, "no **") ||
			strings.Contains(watchParagraph, "without a live") || strings.Contains(watchParagraph, "no session"))
	if !hasCLIOnlyEdge {
		t.Fatalf("AC-4 TC-4.1: watch bullet is missing the documented CLI-only staleness edge (a hand-edit with no live bbrain mcp/watch session stays stale until a manual bbrain reindex); bullet paragraph: %q", watchParagraph)
	}
	hasDualProcessRisk := strings.Contains(watchParagraph, "bbrain watch") && strings.Contains(watchParagraph, "bbrain mcp") &&
		(strings.Contains(watchParagraph, "same time") || strings.Contains(watchParagraph, "alongside") ||
			strings.Contains(watchParagraph, "redundant") || strings.Contains(watchParagraph, "concurrent"))
	if !hasDualProcessRisk {
		t.Fatalf("AC-4/D6 TC-4.1: watch bullet is missing the documented risk of running bbrain watch and bbrain mcp at the same time; bullet paragraph: %q", watchParagraph)
	}

	// TC-4.2: no live bbrain mcp/watch session — a hand-edited fact must
	// remain unreachable via a plain search until a manual reindex.
	home := t.TempDir()
	t.Setenv("BBRAIN_HOME", home)
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"save", "--title", "cli-only edit target", "--project", "p", "--type", "note", "--body", "original body"}, &out, &errOut); code != 0 {
		t.Fatalf("save: %s", errOut.String())
	}

	factsDir := filepath.Join(home, "raws", "facts")
	entries, err := os.ReadDir(factsDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a saved fact file: err=%v entries=%d", err, len(entries))
	}
	factID := strings.TrimSuffix(entries[0].Name(), ".md")
	factPath := filepath.Join(factsDir, entries[0].Name())
	body, err := os.ReadFile(factPath)
	if err != nil {
		t.Fatalf("read fact: %v", err)
	}
	edited := strings.Replace(string(body), "original body", "cli-only-marker body", 1)
	if err := os.WriteFile(factPath, []byte(edited), 0o644); err != nil {
		t.Fatalf("hand-edit fact: %v", err)
	}

	// bbrain search's plain-text output is fact_id/type/title only (never the
	// body/snippet), so presence/absence of factID in the results is the
	// observable signal here — the query term itself only lives in the body.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"search", "cli-only-marker"}, &out, &errOut); code != 0 {
		t.Fatalf("search: %s", errOut.String())
	}
	if strings.Contains(out.String(), factID) {
		t.Fatalf("AC-4 TC-4.2: search found the hand-edited fact with no live session and no manual reindex — this case must stay stale by design: %s", out.String())
	}

	// A manual `bbrain reindex` is still the documented way out.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"reindex"}, &out, &errOut); code != 0 {
		t.Fatalf("reindex: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"search", "cli-only-marker"}, &out, &errOut); code != 0 {
		t.Fatalf("search after reindex: %s", errOut.String())
	}
	if !strings.Contains(out.String(), factID) {
		t.Fatalf("AC-4 TC-4.2: manual bbrain reindex should surface the hand-edit: %s", out.String())
	}
}
