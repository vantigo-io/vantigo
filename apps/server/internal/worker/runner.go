package worker

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
)

// Runner starts a batch of Workers as goroutines and can wait for them all
// to return. It is not reusable: create one Runner per Start call (cmd/vantigo
// creates exactly one, for the process's lifetime).
type Runner struct {
	logger *slog.Logger
	wg     sync.WaitGroup
}

// NewRunner returns a Runner that logs through logger.
func NewRunner(logger *slog.Logger) *Runner {
	return &Runner{logger: logger}
}

// Start launches every worker in workers as its own goroutine running
// Run(ctx), logging its start and, once Run returns, its stop. ctx is
// passed to every worker unchanged, so cancelling it is how the caller asks
// every worker to stop — the runner has no separate Stop method. A worker
// whose Run returns a non-nil error is logged and does not affect its
// siblings: the runner does not restart it and does not stop the others. A
// panic inside Run is recovered and logged the same way an error is,
// rather than crashing the process: background work must not take the
// whole server down over one job's bug, and nothing above the runner would
// usefully restart the process if it did. Start returns immediately; call
// Wait to block until every worker it started has returned.
func (r *Runner) Start(ctx context.Context, workers []Worker) {
	for _, w := range workers {
		r.wg.Add(1)
		go r.run(ctx, w)
	}
}

// run is one worker's goroutine body: log its start, run it to completion
// (recovering a panic as a logged failure rather than letting it propagate),
// log its stop, and mark it done in wg.
func (r *Runner) run(ctx context.Context, w Worker) {
	defer r.wg.Done()
	defer func() {
		if rec := recover(); rec != nil {
			// debug.Stack() here, not the recover site's caller (this defer's
			// own frame): it captures the goroutine's stack as of the panic,
			// including w.Run's frames, which is what makes a recovered panic
			// actionable instead of just a one-line "something panicked".
			r.logger.Error("worker panicked", "worker", w.Name(), "panic", rec, "stack", string(debug.Stack()))
		}
	}()

	r.logger.Info("worker started", "worker", w.Name(), "interval", w.Interval())
	err := w.Run(ctx)
	if err != nil {
		r.logger.Error("worker stopped", "worker", w.Name(), "error", err)
		return
	}
	r.logger.Info("worker stopped", "worker", w.Name())
}

// Wait blocks until every worker a prior Start launched has returned, or
// until ctx is done, whichever comes first — the caller bounds this with
// its own shutdown timeout rather than Wait inventing one. It returns
// ctx.Err() if ctx ends first and nil once every worker has returned. A nil
// Runner (no Start was ever called) returns immediately: a mode that runs
// no workers has nothing to wait for.
func (r *Runner) Wait(ctx context.Context) error {
	if r == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
