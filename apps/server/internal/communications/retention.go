package communications

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the retention worker: SV/CommunicationsRetentionWorker.cs:9-44
// (the BackgroundService) plus SV/RetentionCleanupService.cs:15-116 (the work),
// ported per communications inventory §12.1, §12.3 and §14, and design doc §4
// and D3.
//
// The three things about it that a reasonable implementer would get wrong, all
// pinned by tests:
//
//  1. **message_events is deleted BEFORE message_deliveries.** The instinct is
//     to delete the parent first; here that is a permanent outage.
//     message_events.delivery_id is the schema's only ON DELETE RESTRICT FK
//     between two tables one batch deletes together (inventory §10 item 8), so
//     the reverse order raises 23001, rolls the whole single-transaction batch
//     back, and retention never drains again on its own.
//  2. **This worker takes the advisory lease, and it is the only one that
//     does** — the exact inverse of the outbox worker next door, where adding
//     a lock would be the defect (inventory §11 D2, §14; design §4). A second
//     replica logs and skips its cycle.
//  3. **It never calls the object store.** Object deletion is made durable by
//     queueing rows in attachment_cleanup_records through the reservation
//     protocol (objects.go), which the attachment-cleanup worker drains later.

const (
	// retentionWorkerName is what the runner logs this worker as.
	retentionWorkerName = "communications-retention"

	// retentionLeaseKey is CommunicationsAdvisoryLease.RetentionKey
	// (`SV/CommunicationsAdvisoryLease.cs:13`): the ASCII string "COMMRET1"
	// read as a big-endian 64-bit value. Postgres advisory locks are
	// per-database, not per-schema or per-table, so this key shares one space
	// with every other advisory-lock user in the installation — which is why
	// it is a recognisable constant rather than a small number, and why the
	// one-argument pg_try_advisory_lock(bigint) overload is used rather than
	// the two-argument (int, int) one that identity and energy take for their
	// own row-scoped locks (inventory §14).
	retentionLeaseKey int64 = 0x434F4D4D52455431

	// retentionUploadExpiryBatch is the staged-upload sweep's own hardcoded
	// Take(100) (`SV/AttachmentScanning.cs:258`). It is deliberately NOT the
	// configurable retention batch size: the sweep arrived here from the
	// deleted scanner with its own fixed bound (design doc D3), and giving it
	// retention's knob would silently change a limit no configuration ever
	// governed.
	retentionUploadExpiryBatch = 100

	// The defaults a Deps with no Config falls back on — the same values
	// config.go defaults COMMUNICATIONS_RETENTION_DAYS, _BATCH_SIZE and _POLL
	// to, repeated here so a worker built from a bare module.Deps (as
	// module_internal_test.go builds one) is still well-defined rather than
	// deleting everything ever written.
	defaultRetentionDays      = 365
	defaultRetentionBatchSize = 100
	defaultRetentionPoll      = time.Hour
)

// RetentionWorker deletes terminal communication history — and only terminal
// history: queued and retryable work is excluded so retention can never
// interrupt delivery. It also owns the staged-upload expiry re-homed from the
// removed scanner (design doc D3). It implements worker.Worker, so
// module.Workers hands it to cmd/vantigo's runner in worker mode and in api
// mode when WORKERS_IN_PROCESS=1.
type RetentionWorker struct {
	deps module.Deps
}

var _ worker.Worker = (*RetentionWorker)(nil)

// NewRetentionWorker builds the worker over d. Unlike the outbox worker it
// needs no object store: everything it disposes of is queued through the
// cleanup ledger instead (see this file's header, point 3).
func NewRetentionWorker(d module.Deps) *RetentionWorker { return &RetentionWorker{deps: d} }

// Name identifies this worker in the runner's logs.
func (w *RetentionWorker) Name() string { return retentionWorkerName }

// Interval is the poll cadence between cycles: .NET's
// `TimeSpan.FromMinutes(max(1, Communications:Retention:PollMinutes))`,
// default 60 minutes (inventory §12.1), here COMMUNICATIONS_RETENTION_POLL.
// The minute floor .NET applies is a consequence of its minute-granularity
// unit; config.go's own rule — a positive duration — is the same bound
// expressed in the unit this port actually configures.
func (w *RetentionWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CommunicationsRetentionPoll > 0 {
		return w.deps.Config.CommunicationsRetentionPoll
	}
	return defaultRetentionPoll
}

// Run is the worker loop (`SV/CommunicationsRetentionWorker.cs:32-44`): run a
// cycle, sleep the poll interval, repeat until ctx is done. The interval is
// computed ONCE before the loop, as .NET computes it (`:19-20`). A failing
// cycle is logged and the loop continues — the loop never dies, which is the
// property the runner depends on since it never restarts a worker.
func (w *RetentionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("retention cycle failed", "worker", retentionWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the advisory lease, reporting whether it ran:
// the staged-upload expiry sweep, then batches until one deletes nothing
// (.NET's `while (await cleanup.CleanupBatchAsync(UtcNow, ct) > 0) { }`,
// `:34`). false means another replica holds the lease and this one skipped,
// which is a normal, logged outcome and not an error (`:37-38`).
//
// The expiry sweep runs first because it only ever ADDS cleanup-ledger rows
// and never touches a message: ordering it ahead of the drain means a cycle
// that fails partway still expired what it could.
func (w *RetentionWorker) RunCycle(ctx context.Context) (bool, error) {
	return w.underLease(ctx, func(ctx context.Context) error {
		if _, err := w.ExpireStagedUploads(ctx); err != nil {
			return err
		}
		for {
			deleted, err := w.CleanupBatch(ctx)
			if err != nil {
				return err
			}
			if deleted == 0 {
				return nil
			}
		}
	})
}

// underLease is CommunicationsAdvisoryLease.TryRunAsync
// (`SV/CommunicationsAdvisoryLease.cs:19-55`, inventory §14): a non-blocking,
// installation-wide lease that elects one replica per cycle.
//
// pg_try_advisory_lock is SESSION-scoped, not transaction-scoped, so the lease
// needs one connection held for the whole action — hence the explicit Acquire
// rather than the pool's usual per-statement checkout. The unlock runs on a
// context stripped of cancellation (.NET passes CancellationToken.None for the
// same reason): a cancelled cycle must still release, or this process's own
// next cycle would skip forever against a lock nothing will ever drop.
func (w *RetentionWorker) underLease(ctx context.Context, action func(context.Context) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("communications: acquire a connection for the retention lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, retentionLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("communications: take the retention lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the communications retention lease is held by another replica; skipping this cycle",
			"worker", retentionWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, retentionLeaseKey); err != nil {
			// A session that still holds the lease must never go back into
			// the pool: every later cycle in this process would draw it, find
			// the lock already held by its own session, and skip silently
			// forever. Closing the connection makes Release destroy it.
			w.logger().Error("releasing the retention lease failed; discarding the connection",
				"worker", retentionWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx)
}

// CleanupBatch is RetentionCleanupService.CleanupTenantBatchAsync
// (`:47-115`) minus tenancy and minus the two inbound candidate sets the scope
// cut removed: one batch, ONE transaction, returning how many
// conversation_messages it deleted (the drain loop's condition).
//
// Everything below happens in .NET's order, and the first two statements'
// order is the load-bearing one this file's header opens with.
func (w *RetentionWorker) CleanupBatch(ctx context.Context) (int, error) {
	now := w.now()
	cutoff := now.AddDate(0, 0, -w.retentionDays())
	batchSize := w.batchSize()

	var deleted int64
	err := db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)

		messageIDs, err := q.SelectRetentionMessageIDs(ctx, store.SelectRetentionMessageIDsParams{
			Cutoff: cutoff, BatchSize: batchSize,
		})
		if err != nil {
			return fmt.Errorf("communications: select retention candidates: %w", err)
		}

		// Step 1, then step 2. NOT the other way round: see the header.
		if err := q.DeleteMessageEventsByMessageIDs(ctx, messageIDs); err != nil {
			return fmt.Errorf("communications: delete message events: %w", err)
		}
		if err := q.DeleteMessageDeliveriesByMessageIDs(ctx, messageIDs); err != nil {
			return fmt.Errorf("communications: delete message deliveries: %w", err)
		}

		// Step 3: read every object key the batch is about to orphan, while
		// the rows that identify their owners still exist.
		attachments, err := q.ListRetentionAttachments(ctx, messageIDs)
		if err != nil {
			return fmt.Errorf("communications: list retention attachments: %w", err)
		}
		rawPayloads, err := q.ListRetentionRawPayloadKeys(ctx, messageIDs)
		if err != nil {
			return fmt.Errorf("communications: list retention raw payload keys: %w", err)
		}

		// Step 4: queue them. Step 5 in .NET is an explicit SaveChanges
		// ("Persist every reservation before deleting the rows that identify
		// its owner", `:101-102`) which forces EF's buffered inserts out
		// ahead of the ExecuteDeletes below. Here every statement is sent
		// when it is written, so that ordering is inherent rather than
		// something to arrange — and the enclosing transaction gives the same
		// all-or-nothing guarantee either way.
		for _, attachment := range attachments {
			messageID := attachment.MessageID
			if err := queueObjectForDeletion(ctx, q, attachment.StorageKey, &messageID, now); err != nil {
				return err
			}
		}
		// .NET dedupes the raw keys through an Ordinal HashSet (`:94`); two
		// messages sharing a raw-payload key would otherwise be queued twice,
		// and the second call would merely re-stamp the first's record.
		seen := make(map[string]bool, len(rawPayloads))
		for _, raw := range rawPayloads {
			if raw.RawPayloadStorageKey == nil || seen[*raw.RawPayloadStorageKey] {
				continue
			}
			seen[*raw.RawPayloadStorageKey] = true
			messageID := raw.ID
			if err := queueObjectForDeletion(ctx, q, *raw.RawPayloadStorageKey, &messageID, now); err != nil {
				return err
			}
		}

		// Step 6.
		if err := q.DeleteMessageAttachmentsByMessageIDs(ctx, messageIDs); err != nil {
			return fmt.Errorf("communications: delete message attachments: %w", err)
		}
		if err := q.DeleteIdempotencyRecordsByMessageIDs(ctx, messageIDs); err != nil {
			return fmt.Errorf("communications: delete idempotency records: %w", err)
		}
		if err := q.DeleteOutboxJobsByMessageIDs(ctx, messageIDs); err != nil {
			return fmt.Errorf("communications: delete outbox jobs: %w", err)
		}
		deleted, err = q.DeleteConversationMessagesByIDs(ctx, messageIDs)
		if err != nil {
			return fmt.Errorf("communications: delete conversation messages: %w", err)
		}

		// Step 7, run on every batch exactly as .NET runs it — including a
		// batch that deleted no message at all.
		if err := q.DeleteOrphanConversationParticipants(ctx); err != nil {
			return fmt.Errorf("communications: delete orphan conversation participants: %w", err)
		}
		if err := q.DeleteOrphanParticipants(ctx); err != nil {
			return fmt.Errorf("communications: delete orphan participants: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
}

// ExpireStagedUploads is ExpireUploadsAsync (`SV/AttachmentScanning.cs:251-280`),
// re-homed here from the scanner the port deletes (design doc D3, inventory
// §5.8 and §19.1 item 2). It marks every staged upload past its expires_at
// 'expired' and queues its object for deletion, returning how many it claimed.
//
// Without this, nothing in the port would ever expire a staged upload: the
// 24-hour window staging stamps would be decorative and every abandoned upload
// would keep its object forever.
func (w *RetentionWorker) ExpireStagedUploads(ctx context.Context) (int, error) {
	now := w.now()
	q := store.New(w.deps.Pool)
	candidates, err := q.SelectExpiredAttachmentUploads(ctx, store.SelectExpiredAttachmentUploadsParams{
		Now: now, BatchSize: retentionUploadExpiryBatch,
	})
	if err != nil {
		return 0, fmt.Errorf("communications: select expired staged uploads: %w", err)
	}
	if len(candidates) == 0 {
		// .NET returns before opening a transaction too (`:260`).
		return 0, nil
	}

	expired := 0
	err = db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		for _, candidate := range candidates {
			// The conditional transition IS the expiry claim: a concurrent
			// reply may have taken this upload clean -> claimed since the
			// select, and this worker must not then queue its object for
			// deletion (.NET's own comment, `:264-266`).
			rows, err := txq.ExpireAttachmentUpload(ctx, store.ExpireAttachmentUploadParams{
				ID: candidate.ID, Now: now,
			})
			if err != nil {
				return fmt.Errorf("communications: expire staged upload: %w", err)
			}
			if rows != 1 {
				continue
			}
			expired++
			if err := queueObjectForDeletion(ctx, txq, candidate.StorageKey, nil, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}

// retentionDays is `max(1, Communications:Retention:Days)`, default 365
// (inventory §12.1). config.go rejects a value outside [1, 36500] rather than
// clamping it silently, the same treatment COMMUNICATIONS_ATTACHMENT_MAX_BYTES
// already gets, so the max() here only covers a Deps built without a Config.
func (w *RetentionWorker) retentionDays() int {
	if w.deps.Config != nil && w.deps.Config.CommunicationsRetentionDays > 0 {
		return w.deps.Config.CommunicationsRetentionDays
	}
	return defaultRetentionDays
}

// batchSize is `Math.Clamp(Communications:Retention:BatchSize, 1, 1000)`,
// default 100, applied to the candidate select (inventory §12.1).
func (w *RetentionWorker) batchSize() int32 {
	if w.deps.Config != nil && w.deps.Config.CommunicationsRetentionBatchSize > 0 {
		return int32(w.deps.Config.CommunicationsRetentionBatchSize)
	}
	return defaultRetentionBatchSize
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers and the outbox worker.
func (w *RetentionWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *RetentionWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
