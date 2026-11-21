// Package worker is the application background-work port: a Worker
// interface every module's long-running job implements, and a Runner
// (runner.go) that starts every enabled module's workers as goroutines,
// logs each start and stop, propagates cancellation, and waits for them to
// finish during shutdown.
//
// Each worker owns its loop, so it opens each cycle itself with StartCycle
// (cycle.go): a span and the cycle metrics per cycle, a panic recorded on
// the cycle it unwound through (Kubernetes readiness KR21). The runner
// carries no locking of its own — a lease is each worker's own concern.
package worker

import (
	"context"
	"time"
)

// Worker is one long-running background job a module contributes. Run
// blocks until ctx is done or the worker decides to stop on its own; a
// worker that polls loops internally, waking on its own Interval, and must
// return once ctx is done rather than run forever. The runner starts Run
// once per worker and never restarts it: a worker that wants to keep
// running after a transient failure catches it internally and keeps
// looping, and returns from Run only when it is really done.
type Worker interface {
	// Name identifies this worker in logs. It should be stable and unique
	// among every module's workers, so a log line names exactly which one
	// started, stopped, or returned an error.
	Name() string
	// Interval is how often this worker polls for work. The runner does not
	// enforce it — Run owns its own loop — but logs it alongside Name when
	// the worker starts, so an operator can see each worker's cadence
	// without reading its source.
	Interval() time.Duration
	// Run performs this worker's job until ctx is done. A returned error is
	// logged by the runner and does not stop this worker's siblings or the
	// process; a nil return means Run stopped cleanly, typically because ctx
	// was done.
	Run(ctx context.Context) error
}
