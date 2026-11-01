package assistant

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

// RetentionCycle runs one cycle of the retention worker: the conversations it
// expired, the people the erasure sweep found erased.
func RetentionCycle(ctx context.Context, d module.Deps) (expired, erased int, err error) {
	return (&retentionWorker{deps: d}).RunCycle(ctx)
}

// StaleRunsCycle runs one cycle of the stale-runs worker.
func StaleRunsCycle(ctx context.Context, d module.Deps) (int, error) {
	return (&staleRunsWorker{deps: d}).RunCycle(ctx)
}
