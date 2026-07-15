package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/app"
	"github.com/JaraEsequiel/BBrain/internal/watch"
)

// lastReindexedFingerprint remembers, per facts dir, the fingerprint that was last
// actually reindexed. It outlives any single RunBackgroundReindex call so that a
// fresh loop start (e.g. a server restart) doesn't pay for a rebuild the facts dir
// hasn't earned — AC-3 must hold across loop starts, not just within one.
//
// ponytail: package-level map keyed by factsDir; a process only ever loops one
// brain's factsDir, so a mutex-guarded map is enough — move this onto app.App
// only if multiple concurrent brains per process becomes a real need.
var (
	lastReindexedMu sync.Mutex
	lastReindexed   = map[string]string{}
)

// RunBackgroundReindex polls factsDir at interval and reindexes only when the fingerprint
// changed, reusing watch.FactsFingerprint verbatim (no full rebuild when nothing changed —
// AC-3). It returns as soon as ctx is cancelled — the caller owns the goroutine's lifetime.
//
// ponytail: fixed interval reused from `bbrain watch`'s default (2s); no new config knob until
// a real need to tune it shows up.
func RunBackgroundReindex(ctx context.Context, a *app.App, factsDir string, interval time.Duration) {
	lastReindexedMu.Lock()
	if _, seeded := lastReindexed[factsDir]; !seeded {
		fp0, _ := watch.FactsFingerprint(factsDir)
		lastReindexed[factsDir] = fp0
	}
	lastReindexedMu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fp, err := watch.FactsFingerprint(factsDir)
			if err != nil {
				continue
			}
			lastReindexedMu.Lock()
			unchanged := fp == lastReindexed[factsDir]
			lastReindexedMu.Unlock()
			if unchanged {
				continue
			}
			if _, err := a.Reindex(); err == nil {
				lastReindexedMu.Lock()
				lastReindexed[factsDir] = fp
				lastReindexedMu.Unlock()
			}
		}
	}
}
