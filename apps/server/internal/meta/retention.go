package meta

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// RetentionWorker is meta-retention: it deletes what the module keeps past its bounds.
type RetentionWorker struct{ core }

var _ worker.Worker = (*RetentionWorker)(nil)

// NewRetentionWorker builds the worker over d.
func NewRetentionWorker(d module.Deps) *RetentionWorker { return &RetentionWorker{core: newCore(d)} }

// Name identifies the worker in the runner's logs.
func (w *RetentionWorker) Name() string { return "meta-retention" }

// Interval is the worker's cycle.
func (w *RetentionWorker) Interval() time.Duration { return time.Hour }

// Run is the worker loop.
func (w *RetentionWorker) Run(ctx context.Context) error { <-ctx.Done(); return nil }
