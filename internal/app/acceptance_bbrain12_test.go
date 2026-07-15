package app

// Acceptance suite for BBRAIN-12 (MCP auto-reindex of hand-edited facts),
// AC-2 half. See .dev-tools/plans/BBRAIN-11/BBRAIN-12-mcp-auto-reindex.md for
// the full AC → Task Coverage table. Per this suite's own dispatch: one test
// per AC (not one per TC-n.m) — TC-2.1/TC-2.2 are covered as sub-checks inside
// this single test, not as separate test functions.
//
// This exercises the App façade directly (Save/Reindex), the same two public
// entry points a real bbrain mcp session drives: Reindex stands in for the
// background reindex loop (Task 3/4), Save stands in for a concurrent
// mem_save tool call. That is the acceptance angle for AC-2 — the concurrency
// contract is a property of App, not of one internal helper (withIndex is an
// implementation detail the plan hasn't produced yet).

import (
	"fmt"
	"sync"
	"testing"

	"github.com/JaraEsequiel/BBrain/internal/store"
)

// TestAcceptance_AC2_ConcurrentReindexAndSaveDoNotCorruptIndex fires a
// background-reindex-shaped Reindex() call concurrently with several
// mem_save-shaped Save() calls, repeated across multiple rounds to raise the
// odds of a real overlap. Run with `go test -race`:
//
//   - TC-2.1 (positive): every round finishes with no error from either side,
//     and the index stays consistent — every fact saved across every round is
//     still searchable afterward (no dropped/corrupted rows).
//   - TC-2.2 (negative, regression proof): this test is expected to fail
//     today — either a data race flagged by -race, or a SQLite-level error
//     surfacing from Save/Reindex ("database is locked" / SQLITE_BUSY) — since
//     nothing in App serializes index access yet (no mutex/withIndex exists).
//     A pass here, once Task 1 lands, is the proof the guard is load-bearing,
//     not cosmetic.
func TestAcceptance_AC2_ConcurrentReindexAndSaveDoNotCorruptIndex(t *testing.T) {
	a := New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{Type: "note", Title: "seed", Body: "seed body", Project: "p"}); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	const rounds = 10
	var wantTitles []string
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		wg.Add(2)
		errs := make(chan error, 2)
		title := fmt.Sprintf("concurrent-%d", round)
		wantTitles = append(wantTitles, title)

		go func() {
			defer wg.Done()
			if _, err := a.Reindex(); err != nil {
				errs <- fmt.Errorf("round %d reindex: %w", round, err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := a.Save(store.SaveInput{Type: "note", Title: title, Body: title + " body", Project: "p"}); err != nil {
				errs <- fmt.Errorf("round %d save: %w", round, err)
			}
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("AC-2 TC-2.1/TC-2.2: %v", err)
		}
	}

	// A final full reindex must see every fact saved across every round —
	// nothing silently dropped by a racing DROP TABLE/insert interleave.
	if _, err := a.Reindex(); err != nil {
		t.Fatalf("final reindex: %v", err)
	}
	for _, title := range wantTitles {
		res, stale, err := a.Search(title, 10, "", "")
		if err != nil || stale || len(res) == 0 {
			t.Errorf("AC-2 TC-2.1: %q not searchable after concurrent rounds: res=%v stale=%v err=%v", title, res, stale, err)
		}
	}
}
