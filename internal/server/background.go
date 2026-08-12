package server

import (
	"context"
	"errors"
	"sync"
	"time"
)

// startPeriodicTask gives each external integration its own execution lane.
// A slow upstream must not delay unrelated IRC or metadata work.
func startPeriodicTask(ctx context.Context, workers *sync.WaitGroup, interval time.Duration, run func(context.Context) error, report func(error)) {
	if run == nil {
		return
	}
	if interval <= 0 {
		interval = time.Second
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) && report != nil {
				report(err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
