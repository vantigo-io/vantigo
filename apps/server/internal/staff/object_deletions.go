package staff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the deletion ledger (design S15, M7; plan readings 21 and
// 22): staff.object_deletions, the objects whose rows a delete or an erase
// removed and that the object store has not yet confirmed gone. The two
// cannot be one transaction, so the row's transaction queues the object's
// key and the ledger finishes it afterwards — retried, without a ceiling,
// until the store confirms, so a legal erase is never best-effort.
//
// It is communications' cleanup ledger (internal/communications/cleanup.go)
// for this module: rows claimed by a conditional update with a five-minute
// window, a hundred a pass, retryBackoff between attempts, a constant reason
// on the row and the store's own words in the log. The ledger is the plan's
// second connection discipline: every statement takes a pooled connection
// for itself and gives it back, so none is held while the store is asked —
// a store that hangs never holds a connection hostage.

const (
	// ledgerBatch bounds one pass at a hundred rows (communications').
	ledgerBatch = 100

	// ledgerClaimWindow is how long a pass's claim keeps another pass off a
	// row: long past one attempt's objectCleanupTimeout, so a claim lapses
	// only when the pass that took it is gone.
	ledgerClaimWindow = 5 * time.Minute
)

// The reasons a failed attempt records on its row. The store's own error
// stays in the log, since it may name the key or the bucket.
const (
	objectDeleteRefused    = "the object store refused the delete"
	objectStoreUnavailable = "storage_unavailable: no object store is configured or it could not be built"
)

// runLedger is one pass: every due row nobody else has claimed — a hundred at
// most, the longest waiting first — claimed by a conditional update and
// given one attempt. It answers the rows attempted and the attempts the store
// confirmed. A row another pass won between the selection and the claim is
// left to it. Each claim is timed when it is taken, not when the pass began,
// so a slow store does not leave a late row's window already run out. A pass
// whose ctx ends — a run by hand's budget — stops where it is, the attempt
// under way ended with it, and answers what it attempted.
func (s *server) runLedger(ctx context.Context) (attempted, succeeded int, err error) {
	now := s.deps.Clock()
	q := store.New(s.deps.Pool)
	keys, err := q.DueObjectDeletions(ctx, store.DueObjectDeletionsParams{Now: now, RowLimit: ledgerBatch})
	if err != nil {
		return 0, 0, fmt.Errorf("staff: select the ledger's due rows: %w", err)
	}
	for _, key := range keys {
		if ctx.Err() != nil {
			break
		}
		claimedAt := s.deps.Clock()
		failed, err := q.ClaimObjectDeletion(ctx, store.ClaimObjectDeletionParams{
			ClaimedUntil: claimedAt.Add(ledgerClaimWindow), ObjectKey: key, Now: claimedAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil && ctx.Err() != nil {
			break
		}
		if err != nil {
			return attempted, succeeded, fmt.Errorf("staff: claim a ledger row: %w", err)
		}
		attempted++
		if s.finishObjectDeletion(ctx, key, failed, "worker", retentionWorkerName) {
			succeeded++
		}
	}
	return attempted, succeeded, nil
}

// finishObjectDeletion is one attempt at a ledger row's object — the hand
// delete's one attempt after its commit, and each of the ledger's — on a
// context detached from the caller's and bounded by objectCleanupTimeout,
// holding no connection while the store is asked. The store's call keeps the
// caller's deadline, when it has one — a run by hand's ledger budget — though
// not its cancellation; the outcome is recorded on the detached context. When the store confirms
// (a key it no longer has is confirmed too: Delete is idempotent by
// storage's contract), the row is cleared; when it refuses, the attempt is
// recorded on the row — counted, its reason, the next attempt after
// retryBackoff of the attempts failed so far — and the claim given up. An
// object store that is not configured, or could not be built, refuses every
// attempt as storage_unavailable, and the rows wait for it. failedBefore is
// the attempts the row had failed already; logAttrs say whose object it is in
// the log line a failure writes. It answers whether the store confirmed.
func (s *server) finishObjectDeletion(ctx context.Context, key string, failedBefore int32, logAttrs ...any) bool {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	defer cancel()
	q := store.New(s.deps.Pool)
	attempt := cleanup
	if deadline, ok := ctx.Deadline(); ok {
		var stop context.CancelFunc
		attempt, stop = context.WithDeadline(cleanup, deadline)
		defer stop()
	}
	err := s.objectDelete(attempt, key)
	if err == nil {
		if err := q.DeleteObjectDeletion(cleanup, key); err != nil {
			s.logger().ErrorContext(cleanup, "staff: a removed object's ledger row could not be cleared; the next pass clears it",
				append(logAttrs, "error", err.Error())...)
		}
		return true
	}
	reason := objectDeleteRefused
	if s.objectsErr != nil || errors.Is(err, storage.ErrNotConfigured) {
		reason = objectStoreUnavailable
	}
	s.logger().ErrorContext(cleanup, "staff: an object could not be removed from the store; the ledger retries it",
		append(logAttrs, "attempts", failedBefore+1, "error", err.Error())...)
	if err := q.RecordObjectDeletionFailure(cleanup, store.RecordObjectDeletionFailureParams{
		ObjectKey: key, LastError: &reason, NextAttemptAt: s.deps.Clock().Add(retryBackoff(failedBefore + 1)),
	}); err != nil {
		s.logger().ErrorContext(cleanup, "staff: a failed object delete could not be recorded on its ledger row",
			append(logAttrs, "error", err.Error())...)
	}
	return false
}
