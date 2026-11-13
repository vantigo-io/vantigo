package manufacturing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// Erasure (manufacturing design §15, rule 19 of the module boundaries): a time
// log is a record, like a production and an order's event, and keeps its
// operator's user id and display name after identity erases the person. Its
// free-text note is the person's own words and goes: no module is told of an
// erasure, so the module asks contracts.UserDirectory, which leaves out a user
// that no longer exists, about the operators whose logs carry a note, a chunk
// at a time, and blanks the notes of those it no longer knows. A disabled user
// is still known and keeps their notes. A note can outlive the erasure by one
// cycle, a day.

const (
	erasurePoll  = 24 * time.Hour
	erasureChunk = 200
)

// workers are the module's background workers: the erasure sweep, the
// make-to-order worker (mto.go), the photos' deletion ledger
// (quality_photos.go) and the replenishment job (replenishment.go).
func workers(d module.Deps) []worker.Worker {
	s := newServer(d)
	return []worker.Worker{&erasureWorker{deps: d}, &mtoWorker{s: s}, &objectDeletionsWorker{s: s}, &replenishmentWorker{s: s}}
}

// erasureWorker blanks the time-log notes of the operators identity erased.
type erasureWorker struct{ deps module.Deps }

func (*erasureWorker) Name() string            { return "manufacturing-erasure" }
func (*erasureWorker) Interval() time.Duration { return erasurePoll }

func (w *erasureWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(erasurePoll)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.Error("manufacturing erasure cycle failed", "worker", w.Name(), "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle sweeps the operators with a noted time log and blanks the notes of
// those identity no longer knows. It answers how many notes it blanked. Each
// statement runs on the pool by itself, and the directory is asked between
// them, so the worker never holds a connection while asking for another. It is
// idempotent: two replicas running it at once blank the same notes.
func (w *erasureWorker) RunCycle(ctx context.Context) (int64, error) {
	if w.deps.Users == nil {
		return 0, nil
	}
	q := store.New(w.deps.Pool)
	after := uuid.Nil
	var blanked int64
	for {
		ids, err := q.ListNotedUserIDs(ctx, store.ListNotedUserIDsParams{After: after, BatchSize: erasureChunk})
		if err != nil {
			return blanked, fmt.Errorf("manufacturing: list the operators with notes: %w", err)
		}
		if len(ids) == 0 {
			return blanked, nil
		}
		known, err := w.deps.Users.Users(ctx, ids)
		if err != nil {
			return blanked, fmt.Errorf("manufacturing: ask which operators still exist: %w", err)
		}
		exists := make(map[uuid.UUID]bool, len(known))
		for _, u := range known {
			exists[u.ID] = true
		}
		var gone []uuid.UUID
		for _, id := range ids {
			if !exists[id] {
				gone = append(gone, id)
			}
		}
		if len(gone) > 0 {
			n, err := q.BlankNotesOfUsers(ctx, gone)
			if err != nil {
				return blanked, fmt.Errorf("manufacturing: blank an erased operator's notes: %w", err)
			}
			blanked += n
			if n, err = q.BlankUnbuildNotesOfUsers(ctx, gone); err != nil {
				return blanked, fmt.Errorf("manufacturing: blank an erased poster's unbuild notes: %w", err)
			}
			blanked += n
		}
		after = ids[len(ids)-1]
		if len(ids) < erasureChunk || ctx.Err() != nil {
			return blanked, nil
		}
	}
}
