package knowledge

// The knowledge.retention worker (knowledge design K9–K11, K17): once a day
// it deletes trash groups trashed more than 30 days ago, prunes versions
// older than 365 days beyond each article's newest 20, removes favourites of
// articles their person can no longer read, and sweeps erased users. Delivery
// K-A's server lane fills its cycle; until then it only waits.

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	retentionWorkerName = "knowledge.retention"
	retentionInterval   = 24 * time.Hour
)

// workers is the module's Workers.
func workers(module.Deps) []worker.Worker {
	return []worker.Worker{&retentionWorker{}}
}

// retentionWorker runs the retention cycle every interval.
type retentionWorker struct{}

func (w *retentionWorker) Name() string            { return retentionWorkerName }
func (w *retentionWorker) Interval() time.Duration { return retentionInterval }

// Run runs a cycle every interval until ctx is done.
func (w *retentionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	for {
		w.cycle(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// cycle is one pass. It does nothing yet.
func (w *retentionWorker) cycle(context.Context) {}
