package communications

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
)

// This file is ObjectOwnershipLifecycle (`SV/ObjectOwnershipLifecycle.cs:14-189`,
// communications inventory §12.3): the small state machine that protects
// communications objects while the database and the object store cannot
// participate in one transaction.
//
// It lives in its own file, not in attachments.go or retention.go, because it
// belongs to three callers rather than one: attachment staging reserves and
// marks owned (task 6, §5.4 steps 8 and 11), retention queues deletions
// (task 12, §12.1 step 4), and the attachment-cleanup worker drains the same
// ledger (task 13, §12.2). The queries are in queries/objects.sql for the same
// reason.
//
// The states a record moves through (`:16-20`):
//
//	staged    a reservation written before an object-store write, with a
//	          10-minute expiry so a crash between the write and the metadata
//	          commit cannot strand the object
//	owned     the metadata committed; the object belongs to a live row
//	pending   the object is due for deletion, now
//	deleting  the cleanup worker has claimed it under a lease
//	completed the object is gone
//
// Only the state this file's own logic branches on is a constant here; the
// others are written as literals in queries/objects.sql and
// queries/attachments.sql, where they are the values actually stored.
const cleanupStatusDeleting = "deleting"

// errUnsafeObjectKey is EnsureSafeKey's throw (`:185-189`). Like every other
// error in this module that could carry a storage key, it deliberately does
// not name the key: these errors reach worker and request logs, and a storage
// key is not something a log line should manufacture (the same reasoning
// reserveStorageKey's own comment records).
var errUnsafeObjectKey = errors.New("communications: object keys must be safe relative keys")

// deterministicGUID is ObjectOwnershipLifecycle.DeterministicGuid (`:32-36`):
// SHA-256 over "{seed:N}:{discriminator}", truncated to the first 16 bytes and
// handed to .NET's Guid(ReadOnlySpan<byte>) constructor.
//
// THE BYTE ORDER IS THE WHOLE POINT. That constructor does not copy the bytes
// straight through: it reads the first four as a little-endian int32 and the
// next two pairs as little-endian int16s, leaving the final eight in order. A
// Go port that does the obvious `uuid.FromBytes(sum[:16])` produces a
// DIFFERENT id from the same input, with no error and no symptom other than a
// crash-retry that no longer recognises its own earlier reservation. The three
// swaps below are that layout, and objects_internal_test.go pins them against
// values computed outside this package.
func deterministicGUID(seed uuid.UUID, discriminator string) uuid.UUID {
	sum := sha256.Sum256([]byte(hexN(seed) + ":" + discriminator))
	var id uuid.UUID
	// int32, little-endian.
	id[0], id[1], id[2], id[3] = sum[3], sum[2], sum[1], sum[0]
	// two int16s, little-endian.
	id[4], id[5] = sum[5], sum[4]
	id[6], id[7] = sum[7], sum[6]
	// the remaining eight bytes, in order.
	copy(id[8:], sum[8:16])
	return id
}

// cleanupRecordID is ReserveAsync's id for a new reservation (`:54`):
// DeterministicGuid(Guid.Empty, "cleanup:{key}"). Deterministic so a
// crash-retry for the same object key reuses the same row rather than
// accumulating reservations; the caller falls back to a random id when that id
// is already taken, which is the legitimate case of a completed historical
// record for a reused key (`:61-64`).
func cleanupRecordID(storageKey string) uuid.UUID {
	return deterministicGUID(uuid.Nil, "cleanup:"+storageKey)
}

// isSafeRelativeKey is ObjectOwnershipLifecycle.IsSafeRelativeKey (`:24-30`),
// run before every reserve, mark, release and queue. It is deliberately NOT
// internal/storage's own key validation, which is stricter and different
// (inventory §16.1: "both run, on different call paths") — folding the two
// together would change which keys each path accepts.
func isSafeRelativeKey(key string) bool {
	if strings.TrimSpace(key) == "" || strings.HasPrefix(key, "/") || strings.Contains(key, `\`) {
		return false
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// queueObjectForDeletion is QueueForDeletionAsync (`:139-183`, inventory
// §12.3): it makes an object's deletion durable by writing a row to
// attachment_cleanup_records, and it is the ONLY way retention disposes of an
// object — retention never calls the object store itself (inventory §12.1).
// The caller runs it inside the same transaction as the deletes that remove
// the object's owner, so the reservation is committed with them or not at all.
//
// Two early returns carry behaviour that is easy to lose in a port:
//
//   - **No live record but a completed one for this key: return, writing
//     nothing** (`:151-160`). A completed reservation means the object has
//     already been deleted, and the cleanup worker has no terminal failure
//     state — so a port that always inserted would resurrect a deletion record
//     on every pass and retry a key that no longer exists, forever.
//   - **An existing 'deleting' record: leave it exactly as it is** (`:175`).
//     The cleanup worker holds a lease on it; reopening it to 'pending' would
//     hand the same object to a second deleter.
//
// messageID is the owner recorded on the record, and is nil for an object with
// no message — an expired staged upload, say.
func queueObjectForDeletion(ctx context.Context, q *store.Queries, storageKey string, messageID *uuid.UUID, now time.Time) error {
	if !isSafeRelativeKey(storageKey) {
		return errUnsafeObjectKey
	}

	record, err := q.FindLatestCleanupRecordByStorageKey(ctx, storageKey)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		completed, existsErr := q.CleanupRecordCompletedExists(ctx, storageKey)
		if existsErr != nil {
			return fmt.Errorf("communications: look for a completed cleanup record: %w", existsErr)
		}
		if completed {
			// The object is already gone; a second record would be a
			// permanent retry against a key that no longer exists.
			return nil
		}
		if insertErr := q.InsertPendingCleanupRecord(ctx, store.InsertPendingCleanupRecordParams{
			ID: uuid.New(), MessageID: messageID, StorageKey: storageKey, NextAttemptAt: now, CreatedAt: now,
		}); insertErr != nil {
			return fmt.Errorf("communications: queue an object for deletion: %w", insertErr)
		}
		return nil
	case err != nil:
		return fmt.Errorf("communications: find the live cleanup record: %w", err)
	case record.Status == cleanupStatusDeleting:
		// Claimed by the cleanup worker under a live lease: not ours to touch.
		return nil
	default:
		if updateErr := q.MarkCleanupRecordPending(ctx, store.MarkCleanupRecordPendingParams{
			MessageID: messageID, NextAttemptAt: now, ID: record.ID,
		}); updateErr != nil {
			return fmt.Errorf("communications: mark a cleanup record pending: %w", updateErr)
		}
		return nil
	}
}
