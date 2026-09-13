package communications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the attachment-cleanup worker:
// SV/CommunicationsAttachmentCleanupWorker.cs:3-19 (the BackgroundService) plus
// SV/AttachmentCleanupService.cs:11-85 (the work), ported per communications
// inventory §12.2 and §12.3, and design doc §4.
//
// It is the third of this module's three workers, and it INVERTS the two that
// came before it in four places. Every one of them is a place where copying
// outbox.go or retention.go next door would be the defect, so each is pinned
// by a test in cleanup_test.go:
//
//  1. **There is no terminal state.** The outbox gives up at
//     outboxMaxAttempts = 8 and writes 'failed'; cleanup NEVER gives up. A key
//     that can never be deleted comes back every 1024 s forever (inventory
//     §12.2). There is no attempt ceiling here to find, and adding one to
//     match the outbox would silently strand objects in the store with no
//     record that anything is still owed.
//  2. **Nothing is configurable.** A hardcoded one-minute schedule and a
//     hardcoded batch of 100, unlike every other worker in this module — and
//     ONE batch per tick, with no drain loop, which is the other inversion:
//     retention drains (`while CleanupBatch() > 0`), this does not.
//  3. **It takes no advisory lease.** Retention is the one worker that does
//     (inventory §14); here, as in the outbox, the conditional UPDATE in
//     queries/cleanup.sql *is* the lock and every replica runs every cycle.
//  4. **The return value counts records CLAIMED, not deletes that succeeded**
//     (`:83`'s `records.Count`).
//
// The candidate predicate's second branch is the one worth reading twice: a
// 'staged' reservation whose window has expired is an object written to the
// store by a process that then died before committing the metadata which
// would have marked it 'owned'. Reclaiming it here is the entire reason the
// reservation protocol in objects.go exists (inventory §5.4 step 10, §12.3),
// and nothing else in the module ever does it.
const (
	// cleanupWorkerName is what the runner logs this worker as.
	cleanupWorkerName = "communications-attachment-cleanup"

	// cleanupPollInterval is .NET's hardcoded `TimeSpan.FromMinutes(1)`
	// (`SV/CommunicationsAttachmentCleanupWorker.cs:16`). It is a constant and
	// not a configuration key because .NET has no key for it either: this is
	// the only worker in the module whose cadence and batch size are both
	// literals (inventory §12.2, "not configurable, unlike every other
	// worker"). The batch size lives in the SQL for the same reason.
	cleanupPollInterval = time.Minute

	// cleanupLeaseDuration is `now.AddMinutes(5)` (`:58`): how long a claimed
	// record stays invisible to other replicas. Nothing renews it, so a delete
	// that outlives it is redone by whoever claims next — which is safe here
	// in a way it is not for the outbox, because deleting an object twice is
	// harmless (inventory §16.1) while sending a mail twice is not.
	cleanupLeaseDuration = 5 * time.Minute

	// cleanupFailureMessage is the CONSTANT written to
	// attachment_cleanup_records.last_error on every failure (`:76`). The real
	// error text is logged and never stored — the same redaction the outbox
	// makes with outboxFailureMessage, and for the same reason: this column is
	// read back by operators and must not accumulate storage paths or provider
	// messages. It is not "improved" into the real error.
	cleanupFailureMessage = "Object cleanup failed."
)

// CleanupWorker drains communications.attachment_cleanup_records: claim a
// record by conditional update, delete its object from the module's store, and
// record the outcome. It implements worker.Worker, so module.Workers hands it
// to cmd/vantigo's runner in worker mode and in api mode when
// WORKERS_IN_PROCESS=1 (design §2).
//
// Unlike retention, which never touches the object store, this worker is the
// only thing in the module that ever deletes an object: everything else queues
// a row here and lets this worker carry it out, because the database and the
// object store cannot enlist in one transaction (inventory §12.3).
type CleanupWorker struct {
	deps module.Deps
	// store is this module's scoped object store, the one whose keys the
	// ledger's storage_key column is written in.
	store storage.ObjectStore
	// storeErr is deferred rather than returned by the constructor, exactly as
	// the outbox worker defers its own: a worker whose object store cannot be
	// built must still be startable, and an unconfigured store must fail per
	// operation rather than at boot. A record whose delete fails that way goes
	// back to 'pending' and retries forever, which is precisely what inventory
	// §16.3 says an unconfigured store does to this worker.
	storeErr error
}

var _ worker.Worker = (*CleanupWorker)(nil)

// NewCleanupWorker builds the worker over d. It never fails: an object store
// that cannot be constructed is remembered and reported when a record actually
// needs one (see CleanupWorker.storeErr).
func NewCleanupWorker(d module.Deps) *CleanupWorker {
	objects, err := moduleObjectStore(d)
	return &CleanupWorker{deps: d, store: objects, storeErr: err}
}

// Name identifies this worker in the runner's logs.
func (w *CleanupWorker) Name() string { return cleanupWorkerName }

// Interval is the poll cadence between batches: .NET's hardcoded one minute.
// It reads no configuration, deliberately — see cleanupPollInterval.
func (w *CleanupWorker) Interval() time.Duration { return cleanupPollInterval }

// Run is the worker loop (`SV/CommunicationsAttachmentCleanupWorker.cs:5-18`):
// ONE batch, then sleep a minute, then one batch again, until ctx is done.
// There is no inner drain loop — a queue longer than one batch is worked a
// hundred records per minute, which is .NET's shape and not an oversight. A
// failing batch is logged and the loop continues, so the loop never dies;
// the runner never restarts a worker, so that property is load-bearing.
func (w *CleanupWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(cleanupPollInterval)
	defer ticker.Stop()
	for {
		if _, err := w.RunBatch(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("communications object cleanup failed", "worker", cleanupWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunBatch is CleanupTenantBatchAsync (`:41-84`) minus tenancy: select up to a
// hundred due records oldest-first, claim each with a conditional update,
// delete the objects of those it won, and flush every outcome in one
// transaction.
//
// It returns how many records were CLAIMED — .NET's `records.Count` (`:83`),
// not a success count. A batch of ten claims where nine deletes failed still
// returns ten; the failures are visible in the ledger's attempts and
// last_error, which is where an operator reads them.
func (w *CleanupWorker) RunBatch(ctx context.Context) (int, error) {
	// now is frozen for the whole batch, as .NET freezes the DateTimeOffset
	// its worker passes in: the candidate predicate, every claim's re-assertion
	// of that predicate, the lease window and the backoff must all be measured
	// from one instant, or a slow batch would quietly extend the leases it is
	// taking.
	now := w.now()
	q := store.New(w.deps.Pool)

	candidates, err := q.SelectClaimableCleanupRecordIDs(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("communications: select cleanup candidates: %w", err)
	}

	claimed := make([]store.CommunicationsAttachmentCleanupRecord, 0, len(candidates))
	for _, id := range candidates {
		// Guid.NewGuid().ToString("N"): 32 lowercase hex characters, no
		// dashes, a fresh one per record exactly as .NET mints one inside the
		// loop (`:52`).
		leaseID := hexN(uuid.New())
		leaseUntil := now.Add(cleanupLeaseDuration)
		rows, err := q.ClaimCleanupRecord(ctx, store.ClaimCleanupRecordParams{
			LeaseID: &leaseID, LeaseUntil: &leaseUntil, ID: id, Now: now,
		})
		if err != nil {
			return len(claimed), fmt.Errorf("communications: claim cleanup record: %w", err)
		}
		if rows == 0 {
			// Another replica won it between the select and the update. This
			// is the whole of this worker's mutual exclusion.
			continue
		}

		record, err := q.GetCleanupRecord(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			// Unreachable, here and in .NET (which uses SingleAsync and would
			// throw): nothing in this module ever deletes a row from
			// attachment_cleanup_records — completed records are kept
			// deliberately so queueObjectForDeletion can recognise an object
			// that has already gone (objects.go). Handled rather than left to
			// panic, and left untested for the same reason the outbox's
			// equivalent branch is: no fixture can construct the state.
			continue
		}
		if err != nil {
			return len(claimed), fmt.Errorf("communications: re-read claimed cleanup record: %w", err)
		}
		claimed = append(claimed, record)
	}
	if len(claimed) == 0 {
		return 0, nil
	}

	outcomes := make([]cleanupOutcome, 0, len(claimed))
	for _, record := range claimed {
		err := w.deleteObject(ctx, record.StorageKey)
		if err == nil {
			outcomes = append(outcomes, cleanupOutcome{id: record.ID})
			continue
		}
		// .NET catches only when the token is NOT cancelled (`:72`); a
		// cancellation propagates rather than burning an attempt on a delete
		// that was interrupted rather than refused.
		if ctx.Err() != nil {
			return len(claimed), err
		}
		w.logger().Warn("communications object cleanup failed",
			"worker", cleanupWorkerName, "record", record.ID, "error", err)
		attempts := record.Attempts + 1
		outcomes = append(outcomes, cleanupOutcome{
			id: record.ID, failed: true, attempts: attempts,
			// The module's shared backoff expression, the dead 3600 branch
			// included (backoff.go): min(3600, 2^min(attempts, 10)) seconds off
			// the INCREMENTED count, so the first failure waits 2 s and the
			// ceiling is 1024 s. The outbox reaches that ceiling only with a
			// raised max_attempts; this worker, having no ceiling at all,
			// parks there permanently.
			nextAttemptAt: now.Add(retryBackoff(attempts)),
		})
	}

	// .NET's single `SaveChangesAsync` (`:82`): every outcome in the batch
	// lands together or not at all. A failure here leaves the claims in place
	// with their leases, so the records are simply reclaimed once those expire
	// — no object is lost and no record is stranded.
	if err := db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		for _, outcome := range outcomes {
			if !outcome.failed {
				if err := txq.CompleteCleanupRecord(ctx, outcome.id); err != nil {
					return fmt.Errorf("communications: complete cleanup record: %w", err)
				}
				continue
			}
			failure := cleanupFailureMessage
			if err := txq.FailCleanupRecord(ctx, store.FailCleanupRecordParams{
				Attempts: outcome.attempts, LastError: &failure,
				NextAttemptAt: outcome.nextAttemptAt, ID: outcome.id,
			}); err != nil {
				return fmt.Errorf("communications: fail cleanup record: %w", err)
			}
		}
		return nil
	}); err != nil {
		return len(claimed), err
	}
	return len(claimed), nil
}

// cleanupOutcome is one claimed record's result, staged in memory until the
// batch's single flush. It exists because .NET mutates its tracked entities in
// the delete loop and writes them all at the end; keeping the same shape means
// a delete that fails midway through a batch cannot leave half the outcomes
// committed and half not.
type cleanupOutcome struct {
	id     uuid.UUID
	failed bool
	// attempts and nextAttemptAt are the failure path's writes, unused when
	// failed is false.
	attempts      int32
	nextAttemptAt time.Time
}

// deleteObject is `objectStore.DeleteAsync(record.StorageKey)` (`:67`).
//
// Deleting a key that is not there is SUCCESS by contract (inventory §16.1,
// `STA/IObjectStore.cs:24-25`, and internal/storage's fs driver honours it —
// TestFS_DeleteMissingKeySucceeds), so a double delete completes the record
// instead of retrying it forever. That contract is what makes this worker's
// missing terminal state safe: the only permanent retries are storage outages,
// not objects that are already gone.
func (w *CleanupWorker) deleteObject(ctx context.Context, key string) error {
	if w.storeErr != nil {
		return fmt.Errorf("communications: object storage is required for cleanup: %w", w.storeErr)
	}
	if err := w.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("communications: delete a cleanup object: %w", err)
	}
	return nil
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers and the other two workers.
func (w *CleanupWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *CleanupWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
