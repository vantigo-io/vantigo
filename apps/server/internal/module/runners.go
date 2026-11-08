package module

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
)

// Runner is background work that runs in the API process for as long as it
// serves, because it needs what only that process has: the operations
// dispatcher (Module.APIRunners). It is not a worker.Worker: it is not run in
// worker mode, and it is not interval-driven by the platform — Run polls or
// waits as it needs to.
type Runner interface {
	// Name labels the runner in the log.
	Name() string
	// Run does the work until ctx is cancelled and then returns; a runner that
	// returns while ctx is live has stopped for good and says why in its error.
	Run(ctx context.Context) error
}

// StartRunners runs each runner in its own goroutine on ctx and answers a
// function that waits until all have returned. Cancelling ctx is how they are
// stopped. An error or a panic is logged and does not touch the others.
func StartRunners(ctx context.Context, logger *slog.Logger, runners []Runner) (wait func()) {
	var wg sync.WaitGroup
	for _, r := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("runner panicked", "runner", r.Name(), "panic", rec, "stack", string(debug.Stack()))
				}
			}()
			logger.Info("runner started", "runner", r.Name())
			if err := r.Run(ctx); err != nil {
				logger.Error("runner stopped", "runner", r.Name(), "error", err)
				return
			}
			logger.Info("runner stopped", "runner", r.Name())
		}()
	}
	return wg.Wait
}
