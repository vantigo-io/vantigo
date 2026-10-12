package customers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the deletion ledger (customer attachments design D6):
// customers.object_deletions, the objects whose rows a delete or an
// anonymisation removed and that the object store has not yet confirmed
// gone. The two cannot be one transaction, so the row's transaction queues
// the object's key and the ledger finishes it afterwards — retried, without
// a ceiling, until the store confirms, so an erase is never best-effort.
//
// It is Staff's ledger (internal/staff/object_deletions.go) for this module:
// rows claimed by a conditional update with a five-minute window, a hundred
// a pass, retryBackoff between attempts, a constant reason on the row and the
// store's own words in the log. Every statement takes a pooled connection for
// itself and gives it back, so none is held while the store is asked — a
// store that hangs never holds a connection hostage.

const (
	// ledgerBatch bounds one pass at a hundred rows.
	ledgerBatch = 100

	// ledgerClaimWindow is how long a pass's claim keeps another pass off a
	// row: long past one attempt's objectCleanupTimeout, so a claim lapses
	// only when the pass that took it is gone.
	ledgerClaimWindow = 5 * time.Minute

	// maxRetryBackoff caps the wait between two attempts at one object.
	maxRetryBackoff = time.Hour
)

// The reasons a failed attempt records on its row. The store's own error
// stays in the log, since it may name the key or the bucket.
const (
	objectDeleteRefused    = "the object store refused the delete"
	objectStoreUnavailable = "storage_unavailable: no object store is configured or it could not be built"
)

// retryBackoff is how long the ledger waits before trying an object again
// after attempts failed attempts at it: two seconds after the first,
// doubling, at most an hour — so a store that is down for a while is asked
// again every hour, not hammered, and a passing failure is retried within
// seconds (Staff's backoff.go).
func retryBackoff(attempts int32) time.Duration {
	if attempts < 1 {
		return time.Second
	}
	if attempts >= 12 {
		return maxRetryBackoff
	}
	return min(time.Duration(1<<attempts)*time.Second, maxRetryBackoff)
}

// logger is the module's logger, or the default one for a Deps without.
func (s *server) logger() *slog.Logger {
	if s.deps.Logger != nil {
		return s.deps.Logger
	}
	return slog.Default()
}

// runLedger is one pass: every due row nobody else has claimed — a hundred
// at most, the longest waiting first — claimed by a conditional update and
// given one attempt. It answers the rows attempted and the attempts the
// store confirmed. A row another pass won between the selection and the
// claim is left to it. A pass whose ctx ends stops where it is and answers
// what it attempted.
func (s *server) runLedger(ctx context.Context) (attempted, succeeded int, err error) {
	q := store.New(s.deps.Pool)
	keys, err := q.DueObjectDeletions(ctx, store.DueObjectDeletionsParams{Now: s.deps.Clock(), RowLimit: ledgerBatch})
	if err != nil {
		return 0, 0, fmt.Errorf("customers: select the ledger's due rows: %w", err)
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
			return attempted, succeeded, fmt.Errorf("customers: claim a ledger row: %w", err)
		}
		attempted++
		if s.finishObjectDeletion(ctx, key, failed, "worker", anonymisationWorkerName) {
			succeeded++
		}
	}
	return attempted, succeeded, nil
}

// finishObjectDeletion is one attempt at a ledger row's object — the hand
// delete's one attempt after its commit, and each of the ledger's — on a
// context detached from the caller's and bounded by objectCleanupTimeout,
// holding no connection while the store is asked. When the store confirms
// (a key it no longer has is confirmed too: Delete is idempotent by
// storage's contract), the row is cleared; when it refuses, the attempt is
// recorded on the row — counted, its reason, the next attempt after
// retryBackoff of the attempts failed so far — and the claim given up. An
// object store that is not configured, or could not be built, refuses every
// attempt as storage_unavailable, and the rows wait for it. failedBefore is
// the attempts the row had failed already; logAttrs say whose object it is
// in the log line a failure writes. It answers whether the store confirmed.
func (s *server) finishObjectDeletion(ctx context.Context, key string, failedBefore int32, logAttrs ...any) bool {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	defer cancel()
	q := store.New(s.deps.Pool)
	err := s.objectDelete(cleanup, key)
	if err == nil {
		if err := q.DeleteObjectDeletion(cleanup, key); err != nil {
			s.logger().ErrorContext(cleanup, "customers: a removed object's ledger row could not be cleared; the next pass clears it",
				append(logAttrs, "error", err.Error())...)
		}
		return true
	}
	reason := objectDeleteRefused
	if s.objectsErr != nil || errors.Is(err, storage.ErrNotConfigured) {
		reason = objectStoreUnavailable
	}
	s.logger().ErrorContext(cleanup, "customers: an object could not be removed from the store; the ledger retries it",
		append(logAttrs, "attempts", failedBefore+1, "error", err.Error())...)
	if err := q.RecordObjectDeletionFailure(cleanup, store.RecordObjectDeletionFailureParams{
		ObjectKey: key, LastError: &reason, NextAttemptAt: s.deps.Clock().Add(retryBackoff(failedBefore + 1)),
	}); err != nil {
		s.logger().ErrorContext(cleanup, "customers: a failed object delete could not be recorded on its ledger row",
			append(logAttrs, "error", err.Error())...)
	}
	return false
}
