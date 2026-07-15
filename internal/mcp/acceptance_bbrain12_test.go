package mcp

// Acceptance suite for BBRAIN-12 (MCP auto-reindex of hand-edited facts),
// AC-3 half. See .dev-tools/plans/BBRAIN-11/BBRAIN-12-mcp-auto-reindex.md for
// the full AC → Task Coverage table. Per this suite's own dispatch: one test
// per AC (not one per TC-n.m) — TC-3.1/TC-3.2 are sub-checks inside this
// single test function.
//
// Grounded at the exact interface Task 3 of the plan produces:
//   func RunBackgroundReindex(ctx context.Context, a *app.App, factsDir string, interval time.Duration)
// — the background mechanism a real `bbrain mcp` session runs. This is a
// black-box observation of the mechanism's own cost contract (does it
// actually rebuild the index file when nothing changed?), not a peek at an
// internal call counter.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/app"
	"github.com/JaraEsequiel/BBrain/internal/store"
)

// TestAcceptance_AC3_NoFullReindexCostWhenFactsDirUnchanged runs the
// background mechanism against a brain with no changes across several ticks,
// then makes one real change and runs it again — using the on-disk index
// file's mtime as the observable proxy for "a full rebuild actually ran"
// (RebuildAll/Reset rewrite the derived index file; a no-op tick must not
// touch it at all):
//
//   - TC-3.1 (positive): with nothing changed in facts dir since the last
//     reindex, N consecutive ticks never touch (rewrite) the index file.
//   - TC-3.2 (negative): a real, single-fact change does trigger a rebuild —
//     proving the gate isn't just permanently stuck off; a genuine change is
//     still picked up.
func TestAcceptance_AC3_NoFullReindexCostWhenFactsDirUnchanged(t *testing.T) {
	dir := t.TempDir()
	a := app.New(dir)
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{Type: "note", Title: "seed", Body: "seed body", Project: "p"}); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	indexPath := a.Brain.IndexPath()
	before, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index before: %v", err)
	}

	// TC-3.1: nothing changes in factsDir for several ticks.
	runLoop := func(factsDir string, d time.Duration) {
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			RunBackgroundReindex(ctx, a, factsDir, 20*time.Millisecond)
		}()
		time.Sleep(d)
		cancel()
		wg.Wait()
	}
	runLoop(a.Brain.FactsDir(), 150*time.Millisecond)

	afterNoChange, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index after no-op ticks: %v", err)
	}
	if afterNoChange.ModTime().After(before.ModTime()) || afterNoChange.Size() != before.Size() {
		t.Errorf("AC-3 TC-3.1: index file was rewritten though facts dir did not change (before mtime=%v size=%d, after mtime=%v size=%d)",
			before.ModTime(), before.Size(), afterNoChange.ModTime(), afterNoChange.Size())
	}

	// TC-3.2: a real change must still trigger a rebuild.
	if err := os.WriteFile(filepath.Join(a.Brain.FactsDir(), "extra.md"),
		[]byte("---\nkey: extra\ntype: note\nproject: p\n---\n\n# Extra\n\nextra body\n"), 0o644); err != nil {
		t.Fatalf("write extra fact: %v", err)
	}
	runLoop(a.Brain.FactsDir(), 150*time.Millisecond)

	afterChange, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index after real change: %v", err)
	}
	if !afterChange.ModTime().After(afterNoChange.ModTime()) {
		t.Errorf("AC-3 TC-3.2: index file was not rebuilt after a real facts dir change (before mtime=%v, after mtime=%v)",
			afterNoChange.ModTime(), afterChange.ModTime())
	}
}
