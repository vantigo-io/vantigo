package communications_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is task 12: the retention worker
// (SV/CommunicationsRetentionWorker.cs:9-44 + SV/RetentionCleanupService.cs:15-116,
// communications inventory §12.1, §12.3, §14; design doc §4, D3).
//
// The .NET tests these port are TS/Integration/RetentionCleanupTests.cs
// (terminal history deleted, retrying work kept, `:11-17`; the object-deletion
// queue persisted before the owner rows are deleted, `:20-54`) and
// TS/Integration/RetentionLeaseTests.cs (a second holder skips, the lease is
// reusable, `:16-57`). The three test methods of RetentionCleanupTests that
// exercise inbound_email_jobs / inbound_receipts are dropped with those two
// tables (inventory §9), and with them candidate sets 2 and 3.
//
// Every fixture here is written with raw SQL rather than through the API, and
// that is deliberate rather than lazy: retention only ever looks at rows older
// than the cutoff (365 days by default), and no API call can produce one. The
// staged-upload expiry test is the exception — it stages through the real
// endpoint and moves the harness clock, because that path IS reachable.

// ---- fixtures ----

// retentionSeed describes one old message and the rows hanging off it.
type retentionSeed struct {
	// age is how far before the harness clock the message (and every row
	// created with it) is stamped.
	age time.Duration
	// outboxStatus is the status of the message's outbox job; "" seeds no job
	// at all, which is the other half of "terminal" (inventory §12.1 set 1:
	// no job, or every job completed/cancelled/failed).
	outboxStatus string
	// attachmentKey, when set, seeds a message_attachments row carrying it.
	attachmentKey string
	// rawPayloadKey, when set, is written to
	// conversation_messages.raw_payload_storage_key.
	rawPayloadKey string
	// idempotency seeds an idempotency_records row pointing at the message —
	// the row nothing cascades (there is no FK on message_id), so retention
	// has to delete it explicitly.
	idempotency bool
	// ai seeds an ai_interactions row referencing the message, whose
	// message_id FK is SetNull: the audit row must survive the deletion.
	ai bool
}

// retentionFixture is what seedRetentionMessage wrote, for the assertions.
type retentionFixture struct {
	channelID      uuid.UUID
	conversationID uuid.UUID
	participantID  uuid.UUID
	messageID      uuid.UUID
	deliveryID     uuid.UUID
	eventID        uuid.UUID
	attachmentID   uuid.UUID
	aiID           uuid.UUID
}

// seedRetentionMessage writes a channel, a conversation, a participant and one
// message with a delivery and an event that references that delivery — the
// shape that makes the delete order load-bearing, since
// message_events.delivery_id is the schema's only ON DELETE RESTRICT FK
// (inventory §10 item 8).
func seedRetentionMessage(t *testing.T, h *modtest.Harness, seed retentionSeed) retentionFixture {
	t.Helper()
	at := h.Now().Add(-seed.age)
	fx := retentionFixture{
		channelID:      uuid.New(),
		conversationID: uuid.New(),
		participantID:  uuid.New(),
		messageID:      uuid.New(),
		deliveryID:     uuid.New(),
		eventID:        uuid.New(),
	}

	h.Exec(t, `INSERT INTO communications.channels (id, type, address, created_at) VALUES ($1, 'email', $2, $3)`,
		fx.channelID, "retention-"+uuid.NewString()+"@example.test", at)
	h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, subject, last_activity_at, created_at)
		VALUES ($1, $2, 'Retention fixture', $3, $3)`, fx.conversationID, fx.channelID, at)
	h.Exec(t, `INSERT INTO communications.participants (id, channel_id, address, created_at) VALUES ($1, $2, $3, $4)`,
		fx.participantID, fx.channelID, "party-"+uuid.NewString()+"@example.test", at)
	h.Exec(t, `INSERT INTO communications.conversation_participants (conversation_id, participant_id) VALUES ($1, $2)`,
		fx.conversationID, fx.participantID)
	h.Exec(t, `INSERT INTO communications.conversation_messages
		(id, conversation_id, direction, participant_id, subject, raw_payload_storage_key, occurred_at, created_at)
		VALUES ($1, $2, 'outbound', $3, 'Retention fixture', $4, $5, $5)`,
		fx.messageID, fx.conversationID, fx.participantID, nullableKey(seed.rawPayloadKey), at)
	h.Exec(t, `INSERT INTO communications.message_deliveries
		(id, message_id, recipient_address, recipient_type, status, attempts, created_at)
		VALUES ($1, $2, $3, 'to', 'relay_accepted', 1, $4)`,
		fx.deliveryID, fx.messageID, "to-"+uuid.NewString()+"@example.test", at)
	h.Exec(t, `INSERT INTO communications.message_events (id, message_id, delivery_id, event_type, occurred_at)
		VALUES ($1, $2, $3, 'relay_accepted', $4)`, fx.eventID, fx.messageID, fx.deliveryID, at)

	if seed.outboxStatus != "" {
		h.Exec(t, `INSERT INTO communications.outbox_jobs (id, message_id, status, attempts, next_attempt_at, created_at)
			VALUES ($1, $2, $3, 1, $4, $4)`, uuid.New(), fx.messageID, seed.outboxStatus, at)
	}
	if seed.attachmentKey != "" {
		fx.attachmentID = uuid.New()
		h.Exec(t, `INSERT INTO communications.message_attachments
			(id, message_id, file_name, content_type, size_bytes, content_hash, storage_key, is_inline, created_at)
			VALUES ($1, $2, 'a.txt', 'text/plain', 2, 'hash', $3, false, $4)`,
			fx.attachmentID, fx.messageID, seed.attachmentKey, at)
	}
	if seed.idempotency {
		h.Exec(t, `INSERT INTO communications.idempotency_records
			(id, key, payload_fingerprint, conversation_id, message_id, created_at)
			VALUES ($1, $2, 'fingerprint', $3, $4, $5)`,
			uuid.New(), "key-"+uuid.NewString(), fx.conversationID, fx.messageID, at)
	}
	if seed.ai {
		fx.aiID = uuid.New()
		h.Exec(t, `INSERT INTO communications.ai_interactions
			(id, conversation_id, message_id, operation, provider, model, context_digest, context_version, created_at)
			VALUES ($1, $2, $3, 'draft', 'openai', 'gpt-4o-mini', 'digest', 'v1', $4)`,
			fx.aiID, fx.conversationID, fx.messageID, at)
	}
	return fx
}

func nullableKey(key string) any {
	if key == "" {
		return nil
	}
	return key
}

// messageExists is the assertion every candidate-set test makes.
func messageExists(t *testing.T, h *modtest.Harness, id uuid.UUID) bool {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE id = $1`, id) == 1
}

// memStore is a storage.ObjectStore that keeps objects in memory and counts
// every Delete. Retention must never call it: object deletion is queued
// through attachment_cleanup_records and carried out by the cleanup worker
// (inventory §12.1, "retention never calls the object store"), so a non-zero
// delete count here is a real defect, not a style issue.
type memStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	deletes int
}

func newMemStore() *memStore { return &memStore{objects: map[string][]byte{}} }

func (s *memStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	return nil
}

func (s *memStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	delete(s.objects, key)
	return nil
}

func (s *memStore) deleteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deletes
}

// cleanupRecord is one attachment_cleanup_records row, for the reservation
// assertions.
type cleanupRecord struct {
	id        uuid.UUID
	messageID *uuid.UUID
	status    string
	attempts  int32
	leaseID   *string
}

func readCleanupRecords(t *testing.T, h *modtest.Harness, key string) []cleanupRecord {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(),
		`SELECT id, message_id, status, attempts, lease_id FROM communications.attachment_cleanup_records
		 WHERE storage_key = $1 ORDER BY created_at, id`, key)
	if err != nil {
		t.Fatalf("read cleanup records: %v", err)
	}
	defer rows.Close()
	var out []cleanupRecord
	for rows.Next() {
		var r cleanupRecord
		if err := rows.Scan(&r.id, &r.messageID, &r.status, &r.attempts, &r.leaseID); err != nil {
			t.Fatalf("scan cleanup record: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read cleanup records: %v", err)
	}
	return out
}

// ---- candidate selection ----

// TestRetention_DeletesTerminalHistoryAndKeepsRetryableWork is
// RetentionCleanupTests.Deletes_terminal_history_but_keeps_retrying_work
// (`:11-17`) widened to every branch of the predicate: old-and-terminal goes
// (whether the job is completed, cancelled, failed, or absent), old-and-live
// stays, and fresh stays regardless. The class comment this pins is
// SV/RetentionCleanupService.cs:11-14, "Queued and retryable work is
// intentionally excluded so retention cannot interrupt delivery" — retention
// must never race delivery for a message the outbox still owns.
func TestRetention_DeletesTerminalHistoryAndKeepsRetryableWork(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	old := 400 * 24 * time.Hour

	completed := seedRetentionMessage(t, h, retentionSeed{age: old, outboxStatus: "completed"})
	cancelled := seedRetentionMessage(t, h, retentionSeed{age: old, outboxStatus: "cancelled"})
	failed := seedRetentionMessage(t, h, retentionSeed{age: old, outboxStatus: "failed"})
	noJob := seedRetentionMessage(t, h, retentionSeed{age: old})
	retrying := seedRetentionMessage(t, h, retentionSeed{age: old, outboxStatus: "retry"})
	pending := seedRetentionMessage(t, h, retentionSeed{age: old, outboxStatus: "pending"})
	processing := seedRetentionMessage(t, h, retentionSeed{age: old, outboxStatus: "processing"})
	fresh := seedRetentionMessage(t, h, retentionSeed{age: time.Hour, outboxStatus: "completed"})

	w := communications.NewRetentionWorker(h.Deps())
	deleted, err := w.CleanupBatch(context.Background())
	if err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}
	if deleted != 4 {
		t.Errorf("deleted = %d, want 4 (completed, cancelled, failed and no-job)", deleted)
	}

	for _, gone := range []struct {
		name string
		fx   retentionFixture
	}{
		{"completed", completed}, {"cancelled", cancelled}, {"failed", failed}, {"no job", noJob},
	} {
		if messageExists(t, h, gone.fx.messageID) {
			t.Errorf("%s message still exists, want deleted", gone.name)
		}
	}
	for _, kept := range []struct {
		name string
		fx   retentionFixture
	}{
		{"retry", retrying}, {"pending", pending}, {"processing", processing}, {"fresh", fresh},
	} {
		if !messageExists(t, h, kept.fx.messageID) {
			t.Errorf("%s message was deleted, want kept: retention must not interrupt delivery", kept.name)
		}
	}
}

// TestRetention_CutoffIsTheConfiguredRetentionWindow pins the cutoff
// (`now - max(1, Retention:Days)`, default 365, inventory §12.1) against the
// configured window rather than the default, so a port that hardcoded 365
// days or inverted the comparison fails here.
func TestRetention_CutoffIsTheConfiguredRetentionWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("COMMUNICATIONS_RETENTION_DAYS", "30"))

	justOutside := seedRetentionMessage(t, h, retentionSeed{age: 30*24*time.Hour + time.Minute})
	justInside := seedRetentionMessage(t, h, retentionSeed{age: 30*24*time.Hour - time.Minute})

	w := communications.NewRetentionWorker(h.Deps())
	if _, err := w.CleanupBatch(context.Background()); err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}
	if messageExists(t, h, justOutside.messageID) {
		t.Error("a message older than the 30-day window survived")
	}
	if !messageExists(t, h, justInside.messageID) {
		t.Error("a message inside the 30-day window was deleted")
	}
}

// TestRetention_BatchSizeBoundsOneBatchAndTheCycleDrains pins both halves of
// the batching: one CleanupBatch call touches at most batchSize messages
// (`Math.Clamp(BatchSize, 1, 1000)`, applied to the candidate select), and one
// cycle keeps calling it until a batch deletes nothing
// (`while (await cleanup.CleanupBatchAsync(...) > 0) { }`, inventory §12.1).
func TestRetention_BatchSizeBoundsOneBatchAndTheCycleDrains(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("COMMUNICATIONS_RETENTION_BATCH_SIZE", "1"))
	old := 400 * 24 * time.Hour
	for i := 0; i < 3; i++ {
		seedRetentionMessage(t, h, retentionSeed{age: old + time.Duration(i)*time.Hour})
	}

	w := communications.NewRetentionWorker(h.Deps())
	deleted, err := w.CleanupBatch(context.Background())
	if err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("one batch deleted %d messages, want 1 (the configured batch size)", deleted)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_messages`); n != 2 {
		t.Fatalf("messages left after one batch = %d, want 2", n)
	}

	ran, err := w.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if !ran {
		t.Fatal("RunCycle reported the lease was held; nothing else holds it")
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_messages`); n != 0 {
		t.Errorf("messages left after a full cycle = %d, want 0: the cycle drains until a batch deletes nothing", n)
	}
}

// ---- the delete order ----

// TestRetention_DeletesEventsBeforeDeliveries is the delete-order pin.
// message_events.delivery_id -> message_deliveries.id is ON DELETE RESTRICT —
// the only Restrict FK between two tables retention batch-deletes together
// (inventory §10 item 8, §12.1, §19.1 item 6). Deleting the deliveries first
// raises SQLSTATE 23001 and, because every delete is in one transaction, the
// whole batch rolls back: retention would never drain and never recover.
//
// The fixture deliberately gives the event a NON-NULL delivery_id, which is
// what makes the constraint bite at all; swapping the two statements in
// retention.go turns this test red with a restrict violation rather than a
// missing row.
func TestRetention_DeletesEventsBeforeDeliveries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fx := seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour, outboxStatus: "completed"})

	// The fixture must actually exercise the constraint.
	if n := h.Count(t, `SELECT count(*) FROM communications.message_events WHERE id = $1 AND delivery_id = $2`,
		fx.eventID, fx.deliveryID); n != 1 {
		t.Fatalf("fixture event does not reference its delivery (rows = %d): the Restrict FK would not be exercised", n)
	}

	w := communications.NewRetentionWorker(h.Deps())
	deleted, err := w.CleanupBatch(context.Background())
	if err != nil {
		t.Fatalf("CleanupBatch: %v (a restrict violation here means message_deliveries was deleted before message_events)", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.message_events WHERE id = $1`, fx.eventID); n != 0 {
		t.Errorf("message_events rows = %d, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.message_deliveries WHERE id = $1`, fx.deliveryID); n != 0 {
		t.Errorf("message_deliveries rows = %d, want 0", n)
	}
	if messageExists(t, h, fx.messageID) {
		t.Error("the message survived its own batch")
	}
}

// TestRetention_DeletesIdempotencyRecordsNothingCascades pins step 6's
// explicit idempotency_records delete (inventory §12.1): the column has no FK
// at all — only an index — so nothing cascades it and a port that relied on
// the database would leak a row per deleted message forever.
func TestRetention_DeletesIdempotencyRecordsNothingCascades(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fx := seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour, idempotency: true})

	w := communications.NewRetentionWorker(h.Deps())
	if _, err := w.CleanupBatch(context.Background()); err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.idempotency_records WHERE message_id = $1`, fx.messageID); n != 0 {
		t.Errorf("idempotency_records rows = %d, want 0: nothing cascades this table", n)
	}
}

// TestRetention_LeavesTheTablesItMustNotTouch pins the never-touched list
// (inventory §12.1): conversations survive their messages, channels and
// participants-with-links survive, and ai_interactions survives with its
// message_id nulled rather than deleted, because that FK is SetNull and not
// Cascade.
func TestRetention_LeavesTheTablesItMustNotTouch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fx := seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour, ai: true})
	h.Exec(t, `INSERT INTO communications.tags (id, name) VALUES ($1, $2)`, uuid.New(), "tag-"+uuid.NewString())
	h.Exec(t, `INSERT INTO communications.suppressions (id, normalized_email_address, created_at) VALUES ($1, $2, $3)`,
		uuid.New(), "SUPPRESSED-"+uuid.NewString()+"@EXAMPLE.TEST", h.Now())

	w := communications.NewRetentionWorker(h.Deps())
	if _, err := w.CleanupBatch(context.Background()); err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}

	if n := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1`, fx.conversationID); n != 1 {
		t.Errorf("conversations rows = %d, want 1: only their messages go", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channels WHERE id = $1`, fx.channelID); n != 1 {
		t.Errorf("channels rows = %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.tags`); n != 1 {
		t.Errorf("tags rows = %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.suppressions`); n != 1 {
		t.Errorf("suppressions rows = %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.ai_interactions WHERE id = $1 AND message_id IS NULL`, fx.aiID); n != 1 {
		t.Errorf("ai_interactions rows with a nulled message_id = %d, want 1: the FK is SetNull, not Cascade", n)
	}
}

// TestRetention_DeletesOrphanParticipantLinksAndParticipants pins step 7: a
// conversation left with no messages loses its conversation_participants rows,
// and a participant left with no link is deleted — while a participant still
// linked to a live conversation is not.
func TestRetention_DeletesOrphanParticipantLinksAndParticipants(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	gone := seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour})
	kept := seedRetentionMessage(t, h, retentionSeed{age: time.Hour})

	w := communications.NewRetentionWorker(h.Deps())
	if _, err := w.CleanupBatch(context.Background()); err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}

	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_participants WHERE conversation_id = $1`,
		gone.conversationID); n != 0 {
		t.Errorf("orphan conversation_participants rows = %d, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.participants WHERE id = $1`, gone.participantID); n != 0 {
		t.Errorf("orphan participants rows = %d, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.participants WHERE id = $1`, kept.participantID); n != 1 {
		t.Errorf("a still-linked participant was deleted (rows = %d, want 1)", n)
	}
}

// ---- the reservation protocol ----

// TestRetention_QueuesObjectsForDeletionWithoutTouchingTheStore is the heart
// of §12.1 step 4: every attachment key and every non-null raw-payload key in
// the batch is queued through the reservation protocol, and the object store is
// NEVER called — durability comes from attachment_cleanup_records, which the
// cleanup worker drains later (inventory §12.1's "retention never calls the
// object store", §12.3's QueueForDeletionAsync).
func TestRetention_QueuesObjectsForDeletionWithoutTouchingTheStore(t *testing.T) {
	t.Parallel()
	objects := newMemStore()
	h := newHarness(t, modtest.WithObjectStore(objects))
	attachmentKey := "staged-attachments/" + uuid.NewString() + "/a.bin"
	rawKey := "raw/" + uuid.NewString() + ".eml"
	ctx := context.Background()
	if err := objects.Put(ctx, attachmentKey, bytes.NewReader([]byte("body")), "application/octet-stream"); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	if err := objects.Put(ctx, rawKey, bytes.NewReader([]byte("raw")), "message/rfc822"); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	fx := seedRetentionMessage(t, h, retentionSeed{
		age: 400 * 24 * time.Hour, attachmentKey: attachmentKey, rawPayloadKey: rawKey,
	})

	w := communications.NewRetentionWorker(h.Deps())
	if _, err := w.CleanupBatch(ctx); err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}

	for _, key := range []string{attachmentKey, rawKey} {
		records := readCleanupRecords(t, h, key)
		if len(records) != 1 {
			t.Fatalf("cleanup records for %q = %d, want exactly 1", key, len(records))
		}
		if records[0].status != "pending" {
			t.Errorf("cleanup record status for %q = %q, want pending", key, records[0].status)
		}
		if records[0].messageID == nil || *records[0].messageID != fx.messageID {
			t.Errorf("cleanup record message_id for %q = %v, want %v", key, records[0].messageID, fx.messageID)
		}
	}
	if got := objects.deleteCount(); got != 0 {
		t.Errorf("object-store deletes = %d, want 0: retention queues deletions, it never performs them", got)
	}
	if ok, _ := objects.Exists(ctx, attachmentKey); !ok {
		t.Error("the attachment object is gone: retention must leave it to the cleanup worker")
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.message_attachments WHERE id = $1`, fx.attachmentID); n != 0 {
		t.Errorf("message_attachments rows = %d, want 0", n)
	}
}

// TestRetention_CompletedReservationIsNotResurrected pins
// QueueForDeletionAsync's early return (`SV/ObjectOwnershipLifecycle.cs:151-160`,
// inventory §12.3): with no live record for the key but a completed one, it
// returns WITHOUT creating a second record, because a completed reservation
// means the object has already been deleted. A port that always inserts would
// resurrect deletion records for objects that no longer exist, and the cleanup
// worker would retry them forever.
func TestRetention_CompletedReservationIsNotResurrected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := "staged-attachments/" + uuid.NewString() + "/gone.bin"
	fx := seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour, attachmentKey: key})
	completedID := uuid.New()
	h.Exec(t, `INSERT INTO communications.attachment_cleanup_records
		(id, storage_key, status, attempts, next_attempt_at, created_at)
		VALUES ($1, $2, 'completed', 1, $3, $3)`, completedID, key, h.Now().Add(-time.Hour))

	w := communications.NewRetentionWorker(h.Deps())
	if _, err := w.CleanupBatch(context.Background()); err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}

	records := readCleanupRecords(t, h, key)
	if len(records) != 1 {
		t.Fatalf("cleanup records for a completed reservation = %d, want exactly 1 (no second record)", len(records))
	}
	if records[0].id != completedID || records[0].status != "completed" {
		t.Errorf("record = %+v, want the original completed one untouched", records[0])
	}
	if messageExists(t, h, fx.messageID) {
		t.Error("the message survived; the early return must not abort the batch")
	}
}

// TestRetention_DeletingReservationIsLeftAlone pins the other early return
// (`:175`): a record the cleanup worker has already claimed ('deleting', with a
// live lease) is left exactly as it is — reopening it to 'pending' would hand
// the same object to a second deleter while the first still holds its lease.
func TestRetention_DeletingReservationIsLeftAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := "staged-attachments/" + uuid.NewString() + "/claimed.bin"
	seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour, attachmentKey: key})
	h.Exec(t, `INSERT INTO communications.attachment_cleanup_records
		(id, storage_key, status, attempts, next_attempt_at, lease_id, lease_until, created_at)
		VALUES ($1, $2, 'deleting', 2, $3, 'held-by-the-cleanup-worker', $4, $3)`,
		uuid.New(), key, h.Now().Add(-time.Hour), h.Now().Add(5*time.Minute))

	w := communications.NewRetentionWorker(h.Deps())
	if _, err := w.CleanupBatch(context.Background()); err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}

	records := readCleanupRecords(t, h, key)
	if len(records) != 1 {
		t.Fatalf("cleanup records = %d, want exactly 1", len(records))
	}
	if records[0].status != "deleting" {
		t.Errorf("status = %q, want deleting: a claimed record is left alone", records[0].status)
	}
	if records[0].leaseID == nil || *records[0].leaseID != "held-by-the-cleanup-worker" {
		t.Errorf("lease_id = %v, want the cleanup worker's own lease untouched", records[0].leaseID)
	}
}

// TestRetention_ReusesTheLiveReservationRatherThanAddingASecond pins the
// update branch: an 'owned' record for the key (the state every promoted
// attachment is in) is transitioned to 'pending' in place, with its message id
// recorded and its reservation window cleared — one row per key, not two
// (there is deliberately no unique index on storage_key, so a second row is
// possible and would be a leak: inventory §10 item 11).
func TestRetention_ReusesTheLiveReservationRatherThanAddingASecond(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := "staged-attachments/" + uuid.NewString() + "/owned.bin"
	fx := seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour, attachmentKey: key})
	ownedID := uuid.New()
	h.Exec(t, `INSERT INTO communications.attachment_cleanup_records
		(id, storage_key, status, attempts, next_attempt_at, created_at)
		VALUES ($1, $2, 'owned', 0, $3, $3)`, ownedID, key, h.Now().Add(-time.Hour))

	w := communications.NewRetentionWorker(h.Deps())
	if _, err := w.CleanupBatch(context.Background()); err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}

	records := readCleanupRecords(t, h, key)
	if len(records) != 1 {
		t.Fatalf("cleanup records = %d, want exactly 1 (the live reservation, reused)", len(records))
	}
	if records[0].id != ownedID {
		t.Errorf("record id = %v, want the existing reservation %v reused", records[0].id, ownedID)
	}
	if records[0].status != "pending" {
		t.Errorf("status = %q, want pending", records[0].status)
	}
	if records[0].messageID == nil || *records[0].messageID != fx.messageID {
		t.Errorf("message_id = %v, want the owning message %v", records[0].messageID, fx.messageID)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records
		WHERE storage_key = $1 AND reservation_expires_at IS NULL`, key); n != 1 {
		t.Errorf("records with a cleared reservation window = %d, want 1", n)
	}
}

// ---- the re-homed staged-upload expiry (design D3) ----

// TestRetention_ExpiresStagedUploadsAndQueuesTheirObjects is the sweep
// re-homed from the deleted scanner (ExpireUploadsAsync,
// SV/AttachmentScanning.cs:251-280; design doc D3, inventory §5.8, §19.1 item
// 2). Without it nothing ever expires a staged upload or queues its object,
// and staged objects leak despite expires_at being set. The upload is staged
// through the real endpoint, so this exercises the production key and row.
func TestRetention_ExpiresStagedUploadsAndQueuesTheirObjects(t *testing.T) {
	t.Parallel()
	objects := newMemStore()
	h := newHarness(t, modtest.WithObjectStore(objects))
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)
	upload := mustStageAttachment(t, c, convID, []byte("staged bytes"))
	key := modtest.One[string](t, h, `SELECT storage_key FROM communications.attachment_uploads WHERE id = $1`, upload.Id)

	// Past the 24-hour expiry the staging endpoint stamped.
	h.Advance(25 * time.Hour)
	w := communications.NewRetentionWorker(h.Deps())
	expired, err := w.ExpireStagedUploads(context.Background())
	if err != nil {
		t.Fatalf("ExpireStagedUploads: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}

	status := modtest.One[string](t, h, `SELECT scan_status FROM communications.attachment_uploads WHERE id = $1`, upload.Id)
	if status != "expired" {
		t.Errorf("scan_status = %q, want expired", status)
	}
	records := readCleanupRecords(t, h, key)
	if len(records) != 1 {
		t.Fatalf("cleanup records for the expired upload = %d, want 1", len(records))
	}
	if records[0].status != "pending" {
		t.Errorf("cleanup record status = %q, want pending (handed to the cleanup worker)", records[0].status)
	}
	if records[0].messageID != nil {
		t.Errorf("cleanup record message_id = %v, want NULL: an expired staged upload has no message", records[0].messageID)
	}
	if got := objects.deleteCount(); got != 0 {
		t.Errorf("object-store deletes = %d, want 0: expiry queues the object, it does not delete it", got)
	}
}

// TestRetention_ExpirySkipsUnexpiredClaimedAndAlreadyExpiredUploads pins the
// rest of the sweep's predicate (`SV/AttachmentScanning.cs:253-256`): an
// unexpired upload is untouched, and a 'claimed' one — a reply is mid-flight
// with it, the conditional transition the sweep would otherwise steal — is
// left alone so its object is never queued for deletion under a live message.
func TestRetention_ExpirySkipsUnexpiredClaimedAndAlreadyExpiredUploads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)
	fresh := mustStageAttachment(t, c, convID, []byte("fresh"))
	claimed := mustStageAttachment(t, c, convID, []byte("claimed"))
	already := mustStageAttachment(t, c, convID, []byte("already"))
	h.Exec(t, `UPDATE communications.attachment_uploads SET scan_status = 'claimed' WHERE id = $1`, claimed.Id)
	h.Exec(t, `UPDATE communications.attachment_uploads SET scan_status = 'expired' WHERE id = $1`, already.Id)
	claimedKey := modtest.One[string](t, h, `SELECT storage_key FROM communications.attachment_uploads WHERE id = $1`, claimed.Id)

	// The claimed and already-expired uploads are past their expiry; the fresh
	// one is not.
	h.Advance(25 * time.Hour)
	h.Exec(t, `UPDATE communications.attachment_uploads SET expires_at = $1 WHERE id = $2`, h.Now().Add(time.Hour), fresh.Id)

	w := communications.NewRetentionWorker(h.Deps())
	expired, err := w.ExpireStagedUploads(context.Background())
	if err != nil {
		t.Fatalf("ExpireStagedUploads: %v", err)
	}
	if expired != 0 {
		t.Errorf("expired = %d, want 0", expired)
	}
	if got := modtest.One[string](t, h, `SELECT scan_status FROM communications.attachment_uploads WHERE id = $1`, fresh.Id); got != "clean" {
		t.Errorf("unexpired upload scan_status = %q, want clean", got)
	}
	if got := modtest.One[string](t, h, `SELECT scan_status FROM communications.attachment_uploads WHERE id = $1`, claimed.Id); got != "claimed" {
		t.Errorf("claimed upload scan_status = %q, want claimed: a reply owns it", got)
	}
	if records := readCleanupRecords(t, h, claimedKey); len(records) != 1 || records[0].status != "owned" {
		t.Errorf("claimed upload's cleanup records = %+v, want the single 'owned' reservation staging left", records)
	}
}

// ---- the lease ----

// TestRetentionWorker_SkipsTheCycleWhenTheLeaseIsHeld is
// RetentionLeaseTests' first half (`:16-57`) and the inverse of task 11's
// outbox worker, which deliberately has no lock at all: retention is the only
// advisory-lease holder in the system (inventory §11 D2, §14; design §4). A
// second holder logs and skips until the next interval — it must not fall
// through and process the batch anyway.
func TestRetentionWorker_SkipsTheCycleWhenTheLeaseIsHeld(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fx := seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour})

	ctx := context.Background()
	holder, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the lease holder's connection: %v", err)
	}
	defer holder.Release()
	var locked bool
	if err := holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`,
		communications.RetentionLeaseKeyForTest).Scan(&locked); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	if !locked {
		t.Fatal("the lease was already held; this test's database is its own")
	}

	w := communications.NewRetentionWorker(h.Deps())
	ran, err := w.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if ran {
		t.Error("RunCycle ran while another holder had the lease")
	}
	if !messageExists(t, h, fx.messageID) {
		t.Error("the batch was processed despite the lease being held elsewhere")
	}

	// Releasing makes the lease available again, exactly as
	// RetentionLeaseTests' closing assertion requires (`:53-57`).
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, communications.RetentionLeaseKeyForTest); err != nil {
		t.Fatalf("release the lease: %v", err)
	}
	ran, err = w.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle after release: %v", err)
	}
	if !ran {
		t.Fatal("RunCycle still reported the lease held after it was released")
	}
	if messageExists(t, h, fx.messageID) {
		t.Error("the released lease let the cycle run, but nothing was deleted")
	}
}

// TestRetentionWorker_ReleasesTheLeaseAfterEveryCycle pins the finally-block
// release (`SV/CommunicationsAdvisoryLease.cs:44-51`): the lock is
// session-scoped, so a cycle that forgot to unlock would wedge retention on
// that replica's pooled connection forever — and the next cycle, on this same
// pool, would silently skip.
func TestRetentionWorker_ReleasesTheLeaseAfterEveryCycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := communications.NewRetentionWorker(h.Deps())
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		ran, err := w.RunCycle(ctx)
		if err != nil {
			t.Fatalf("RunCycle %d: %v", i, err)
		}
		if !ran {
			t.Fatalf("RunCycle %d reported the lease held: the previous cycle did not release it", i)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 0 {
		t.Errorf("advisory locks still held on this database = %d, want 0", n)
	}
}

// TestRetentionWorker_NameAndIntervalComeFromConfiguration pins the worker's
// runner-facing surface: a stable name and the configured poll cadence
// (`TimeSpan.FromMinutes(max(1, Retention:PollMinutes))`, default 60 minutes,
// inventory §12.1).
func TestRetentionWorker_NameAndIntervalComeFromConfiguration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if got := communications.NewRetentionWorker(h.Deps()).Name(); got != "communications-retention" {
		t.Errorf("Name = %q, want communications-retention", got)
	}
	if got := communications.NewRetentionWorker(h.Deps()).Interval(); got != time.Hour {
		t.Errorf("Interval = %v, want 1h (the default poll)", got)
	}

	tuned := newHarness(t, modtest.WithEnv("COMMUNICATIONS_RETENTION_POLL", "90s"))
	if got := communications.NewRetentionWorker(tuned.Deps()).Interval(); got != 90*time.Second {
		t.Errorf("Interval = %v, want the configured 90s", got)
	}
}

// TestRetentionWorker_RunStopsWithItsContext pins the loop contract the
// runner depends on: Run returns nil once ctx is done rather than running
// forever (internal/worker's Worker doc).
func TestRetentionWorker_RunStopsWithItsContext(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fx := seedRetentionMessage(t, h, retentionSeed{age: 400 * 24 * time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- communications.NewRetentionWorker(h.Deps()).Run(ctx) }()

	// The first cycle runs immediately, before the first tick.
	deadline := time.Now().Add(10 * time.Second)
	for messageExists(t, h, fx.messageID) {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("Run never processed the queued batch")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on a cancelled context", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
