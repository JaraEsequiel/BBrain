package mcp

import (
	"context"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/app"
	"github.com/JaraEsequiel/BBrain/internal/watch"
)

// RunBackgroundReindex polls factsDir at interval and reindexes only when the fingerprint
// changed, reusing watch.FactsFingerprint verbatim (no full rebuild when nothing changed —
// AC-3). It seeds its baseline from the current on-disk state before entering the loop, so
// starting the loop never counts as a spurious first-tick rebuild. It returns as soon as ctx
// is cancelled — the caller owns the goroutine's lifetime.
//
// ponytail: fixed interval reused from `bbrain watch`'s default (2s); no new config knob until
// a real need to tune it shows up.
func RunBackgroundReindex(ctx context.Context, a *app.App, factsDir string, interval time.Duration) {
	last, _ := watch.FactsFingerprint(factsDir)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fp, err := watch.FactsFingerprint(factsDir)
			if err != nil || fp == last {
				continue
			}
			if _, err := a.Reindex(); err == nil {
				last = fp
			}
		}
	}
}
