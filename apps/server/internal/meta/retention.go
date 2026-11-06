package meta

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/meta/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// The module's retention (Messenger design N11, N12a): personal data lives here
// at most thirty days, and retention is the only bound on it.
const (
	retentionInterval = time.Hour
	// rawRetention is how long a stored webhook event is kept.
	rawRetention = 7 * 24 * time.Hour
	// ackedRetention is how long an acknowledged row and its files are kept.
	ackedRetention = 7 * 24 * time.Hour
	// unackedRetention is the longest a row is kept, acknowledged or not.
	unackedRetention = 30 * 24 * time.Hour
	// profileRetention is how long a cached name is kept.
	profileRetention = 30 * 24 * time.Hour
	// retentionBatch is how many expired rows one delete takes.
	retentionBatch = 200
)

// RetentionWorker is meta-retention: it deletes what the module keeps past its bounds.
type RetentionWorker struct {
	core

	filesOnce sync.Once
	files     storage.ObjectStore
	filesErr  error
}

var _ worker.Worker = (*RetentionWorker)(nil)

// NewRetentionWorker builds the worker over d.
func NewRetentionWorker(d module.Deps) *RetentionWorker { return &RetentionWorker{core: newCore(d)} }

// Name identifies the worker in the runner's logs.
func (w *RetentionWorker) Name() string { return "meta-retention" }

// Interval is the worker's cycle.
func (w *RetentionWorker) Interval() time.Duration { return retentionInterval }

// Run is the worker loop: a pass, then the interval. A failure is logged and
// the loop goes on.
func (w *RetentionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "meta: the retention pass failed", "worker", w.Name(), "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce is one pass: raw events, inbound rows with their files, then cached
// profiles. A step that fails does not stop the ones after it; the errors are
// joined.
func (w *RetentionWorker) RunOnce(ctx context.Context) error {
	now := w.now()
	var errs []error
	if _, err := w.q.DeleteOldWebhookEvents(ctx, now.Add(-rawRetention)); err != nil {
		errs = append(errs, fmt.Errorf("meta: delete old webhook events: %w", err))
	}
	if err := w.deleteExpiredInbound(ctx, now); err != nil {
		errs = append(errs, err)
	}
	if _, err := w.q.DeleteOldProfiles(ctx, now.Add(-profileRetention)); err != nil {
		errs = append(errs, fmt.Errorf("meta: delete old profiles: %w", err))
	}
	return errors.Join(errs...)
}

func (w *RetentionWorker) objects() (storage.ObjectStore, error) {
	w.filesOnce.Do(func() { w.files, w.filesErr = inboundStore(w.deps) })
	return w.files, w.filesErr
}

// deleteExpiredInbound deletes the expired rows in batches, their files first:
// a row is deleted only once every one of its files is gone (one never written
// counts as gone), so a failure leaves the row for the next pass to find
// again. A batch that deletes nothing ends the pass.
func (w *RetentionWorker) deleteExpiredInbound(ctx context.Context, now time.Time) error {
	ackedBefore := now.Add(-ackedRetention)
	for {
		rows, err := w.q.ListExpiredInbound(ctx, store.ListExpiredInboundParams{
			AckedBefore: &ackedBefore, CreatedBefore: now.Add(-unackedRetention), MaxRows: retentionBatch,
		})
		if err != nil {
			return fmt.Errorf("meta: list expired inbound rows: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(rows))
		var failed error
		for _, r := range rows {
			if err := w.deleteFiles(ctx, r.StorageKeys); err != nil {
				failed = err
				continue
			}
			ids = append(ids, r.ID)
		}
		if len(ids) > 0 {
			if _, err := w.q.DeleteInboundByIDs(ctx, ids); err != nil {
				return fmt.Errorf("meta: delete expired inbound rows: %w", err)
			}
		}
		if failed != nil {
			w.logger.WarnContext(ctx, "meta: an expired row's files could not be deleted; it stays for the next pass", "err", failed)
		}
		if len(ids) == 0 || len(rows) < retentionBatch {
			return failed
		}
	}
}

// deleteFiles deletes the keys from the object store; a key that is not there
// is gone already.
func (w *RetentionWorker) deleteFiles(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	objects, err := w.objects()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := objects.Delete(ctx, k); err != nil && !errors.Is(err, storage.ErrNotExist) {
			return fmt.Errorf("meta: delete an inbound file: %w", err)
		}
	}
	return nil
}
