package meta

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// SplitWorker is meta-split: it splits each stored webhook event into rows of meta.inbound.
type SplitWorker struct{ core }

var _ worker.Worker = (*SplitWorker)(nil)

// NewSplitWorker builds the worker over d.
func NewSplitWorker(d module.Deps) *SplitWorker { return &SplitWorker{core: newCore(d)} }

// Name identifies the worker in the runner's logs.
func (w *SplitWorker) Name() string { return "meta-split" }

// Interval is the worker's cycle.
func (w *SplitWorker) Interval() time.Duration { return time.Second }

// Run is the worker loop.
func (w *SplitWorker) Run(ctx context.Context) error { <-ctx.Done(); return nil }
