package mcp

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/app"
)

func TestRunBackgroundReindexTicksOnlyOnChange(t *testing.T) {
	dir := t.TempDir()
	a := app.New(dir)
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	factsDir := a.Brain.FactsDir()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		RunBackgroundReindex(ctx, a, factsDir, 20*time.Millisecond)
	}()

	time.Sleep(80 * time.Millisecond) // AC-3: no changes yet, just confirm it's alive

	factPath := filepath.Join(factsDir, "f1.md")
	if err := os.WriteFile(factPath, []byte("---\nkey: f1\ntype: note\n---\n\n# hi\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)

	if res, _, err := a.Search("hi", 10, "", ""); err != nil || len(res) == 0 {
		t.Errorf("AC-1: TC-1.1 expected hand-edited fact to be searchable, got res=%v err=%v", res, err)
	}

	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("AC-1: background loop leaked past ctx cancellation")
	}
}
