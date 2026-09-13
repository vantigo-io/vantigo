package communications_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is task 13: the attachment-cleanup worker
// (SV/CommunicationsAttachmentCleanupWorker.cs:3-19 + SV/AttachmentCleanupService.cs:11-85,
// communications inventory §12.2 and §12.3).
//
// The .NET tests these port are TS/Integration/ObjectLifecycleTests.cs:98-119
// (an 'owned' record is never selected), TS/Integration/AttachmentScanningTests.cs:50-55
// (an expired staged upload's queued object is deleted and its ledger row
// survives as a completed record) and TS/Integration/MailgunInboundIntegrationTests.cs:118-135
// (the crash-recovery path: a 'staged' reservation whose owner never committed
// is reclaimed once its window passes — ported here without the dropped
// inbound subsystem, since the mechanism is the reservation protocol's, not
// that subsystem's).
//
// Four properties of this worker INVERT the two workers that precede it, and
// each one has a test here because copying the neighbouring worker is the
// natural mistake:
//
//   - There is no terminal state (TestCleanup_NeverGivesUp). The outbox stops
//     at max_attempts = 8; this worker retries a permanently failing key
//     forever at the backoff's 1024 s ceiling.
//   - Nothing is configurable (TestCleanupWorker_NameAndIntervalAreFixed,
//     TestCleanup_BatchIsOneHundredWithNoDrainLoop): a hardcoded one-minute
//     schedule and a hardcoded batch of 100, one batch per tick, no drain.
//   - It takes no advisory lease (TestCleanup_RunsWhileTheRetentionLeaseIsHeld)
//     — the exact inverse of retention, which requires one.
//   - The return value counts records CLAIMED, not records succeeded
//     (TestCleanup_ReturnsRecordsClaimedNotSucceeded).

// ---- fixtures ----

// cleanupSeed describes one attachment_cleanup_records row. Every duration is
// relative to the harness clock, so a negative value is "already due" and a
// positive one is "not yet".
type cleanupSeed struct {
	// status is the ledger state: pending, staged, deleting, owned or
	// completed.
	status string
	// storageKey is the object the record stands for.
	storageKey string
	// nextAttemptIn offsets next_attempt_at, which only the 'pending' branch
	// of the candidate predicate reads.
	nextAttemptIn time.Duration
	// reservation, when set, offsets reservation_expires_at — the 'staged'
	// branch's own column.
	reservation *time.Duration
	// lease, when set, offsets lease_until — the 'deleting' branch's column.
	lease *time.Duration
	// attempts is the failure count already recorded on the row.
	attempts int32
	// createdAgo backdates created_at, which is the candidate ordering.
	createdAgo time.Duration
}

func offset(d time.Duration) *time.Duration { return &d }

// seedCleanupRecord writes one ledger row directly. Every cleanup fixture is
// raw SQL rather than an API call, and deliberately so: the states this worker
// acts on are a crashed process's leftovers (an expired staged reservation, a
// dead lease) and a retention batch's queue, none of which any HTTP request
// can produce on demand.
func seedCleanupRecord(t *testing.T, h *modtest.Harness, seed cleanupSeed) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var reservationExpiresAt, leaseUntil, leaseID any
	if seed.reservation != nil {
		reservationExpiresAt = h.Now().Add(*seed.reservation)
	}
	if seed.lease != nil {
		leaseUntil = h.Now().Add(*seed.lease)
		leaseID = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	h.Exec(t, `INSERT INTO communications.attachment_cleanup_records
		(id, message_id, storage_key, status, attempts, next_attempt_at, lease_id, lease_until, reservation_expires_at, created_at)
		VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, seed.storageKey, seed.status, seed.attempts, h.Now().Add(seed.nextAttemptIn),
		leaseID, leaseUntil, reservationExpiresAt, h.Now().Add(-seed.createdAgo))
	return id
}

// cleanupLedgerRow is one attachment_cleanup_records row, every column the
// worker writes included — readCleanupRecords in retention_test.go carries
// only the subset the reservation protocol's own tests need.
type cleanupLedgerRow struct {
	status        string
	attempts      int32
	nextAttemptAt time.Time
	leaseID       *string
	leaseUntil    *time.Time
	lastError     *string
}

func readCleanupRow(t *testing.T, h *modtest.Harness, id uuid.UUID) cleanupLedgerRow {
	t.Helper()
	var row cleanupLedgerRow
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.Pool().QueryRow(ctx, `SELECT status, attempts, next_attempt_at, lease_id, lease_until, last_error
		FROM communications.attachment_cleanup_records WHERE id = $1`, id).
		Scan(&row.status, &row.attempts, &row.nextAttemptAt, &row.leaseID, &row.leaseUntil, &row.lastError)
	if err != nil {
		t.Fatalf("read cleanup record %s: %v", id, err)
	}
	return row
}

// cleanupStore is the object store the cleanup tests drive: it records what it
// holds and every key it was asked to delete, can fail a chosen key (or all of
// them) with a chosen error, and can run a hook *inside* Delete — the only
// place a test can observe the database as the worker sees it mid-claim, which
// is what the five-minute lease assertion needs.
type cleanupStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	deleted []string
	failing map[string]error
	failAll error
	hook    func(key string)
}

var _ storage.ObjectStore = (*cleanupStore)(nil)

func newCleanupStore() *cleanupStore {
	return &cleanupStore{objects: map[string][]byte{}, failing: map[string]error{}}
}

// put writes an object the way a staging upload or an inbound write would
// have, so a test's ledger row stands for an object that really exists.
func (s *cleanupStore) put(t *testing.T, key string) {
	t.Helper()
	if err := s.Put(context.Background(), key, bytes.NewReader([]byte("object")), "application/octet-stream"); err != nil {
		t.Fatalf("seed object %q: %v", key, err)
	}
}

func (s *cleanupStore) failOn(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing[key] = err
}

func (s *cleanupStore) failEverything(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAll = err
}

func (s *cleanupStore) onDelete(fn func(key string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = fn
}

func (s *cleanupStore) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

func (s *cleanupStore) deleteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleted)
}

func (s *cleanupStore) deletedKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.deleted))
	copy(out, s.deleted)
	return out
}

func (s *cleanupStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	return nil
}

func (s *cleanupStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *cleanupStore) Exists(_ context.Context, key string) (bool, error) {
	return s.has(key), nil
}

func (s *cleanupStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	hook, failAll, failKey := s.hook, s.failAll, s.failing[key]
	s.mu.Unlock()
	if hook != nil {
		hook(key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, key)
	if failAll != nil {
		return failAll
	}
	if failKey != nil {
		return failKey
	}
	// Deleting a key that is not there is a success by contract
	// (inventory §16.1, docs/storage.md): a double delete is harmless.
	delete(s.objects, key)
	return nil
}

// newCleanupHarness is a harness whose object store is store, since every
// cleanup test cares about exactly which keys reached the store.
func newCleanupHarness(t *testing.T, store *cleanupStore) *modtest.Harness {
	t.Helper()
	return newHarness(t, modtest.WithObjectStore(store))
}

// ---- the candidate predicate ----

// TestCleanup_ClaimsEveryDueBranchAndLeavesTheRest pins all three disjuncts of
// the candidate predicate (`SV/AttachmentCleanupService.cs:44-48`, inventory
// §12.2) together with everything that must NOT match.
//
// The middle branch is the one to read twice: a 'staged' record whose
// reservation window has passed is the crash-recovery path — the object was
// written but its owner never committed the metadata that would have marked
// the record 'owned' — and it is the entire reason the reservation protocol
// exists (inventory §5.4 step 10, §12.3). A port that implemented only the
// 'pending' branch would leak every abandoned object forever, and every
// sequential test of the ordinary path would still pass.
func TestCleanup_ClaimsEveryDueBranchAndLeavesTheRest(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)

	duePending := "cleanup/due-pending/" + uuid.NewString()
	expiredStaged := "cleanup/expired-staged/" + uuid.NewString()
	deadLease := "cleanup/dead-lease/" + uuid.NewString()
	futurePending := "cleanup/future-pending/" + uuid.NewString()
	liveStaged := "cleanup/live-staged/" + uuid.NewString()
	liveLease := "cleanup/live-lease/" + uuid.NewString()
	owned := "cleanup/owned/" + uuid.NewString()
	completed := "cleanup/completed/" + uuid.NewString()

	claimable := map[string]uuid.UUID{
		duePending:    seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: duePending, nextAttemptIn: -time.Minute}),
		expiredStaged: seedCleanupRecord(t, h, cleanupSeed{status: "staged", storageKey: expiredStaged, reservation: offset(-time.Minute)}),
		deadLease:     seedCleanupRecord(t, h, cleanupSeed{status: "deleting", storageKey: deadLease, lease: offset(-time.Minute)}),
	}
	untouched := map[string]uuid.UUID{
		futurePending: seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: futurePending, nextAttemptIn: time.Minute}),
		liveStaged:    seedCleanupRecord(t, h, cleanupSeed{status: "staged", storageKey: liveStaged, reservation: offset(time.Minute)}),
		liveLease:     seedCleanupRecord(t, h, cleanupSeed{status: "deleting", storageKey: liveLease, lease: offset(time.Minute)}),
		owned:         seedCleanupRecord(t, h, cleanupSeed{status: "owned", storageKey: owned}),
		completed:     seedCleanupRecord(t, h, cleanupSeed{status: "completed", storageKey: completed}),
	}
	for key := range claimable {
		store.put(t, key)
	}
	for key := range untouched {
		store.put(t, key)
	}

	claimed, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background())
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if claimed != len(claimable) {
		t.Errorf("claimed = %d, want %d (one per branch of the candidate predicate)", claimed, len(claimable))
	}

	for key, id := range claimable {
		if store.has(key) {
			t.Errorf("object %q survived, want deleted", key)
		}
		row := readCleanupRow(t, h, id)
		if row.status != "completed" {
			t.Errorf("record for %q: status = %q, want completed", key, row.status)
		}
		if row.leaseID != nil || row.leaseUntil != nil {
			t.Errorf("record for %q: lease not cleared on completion (%v/%v)", key, row.leaseID, row.leaseUntil)
		}
	}
	for key, id := range untouched {
		if !store.has(key) {
			t.Errorf("object %q was deleted, want kept: its record matches no branch of the candidate predicate", key)
		}
		seededStatus := readCleanupRow(t, h, id).status
		switch key {
		case futurePending:
			if seededStatus != "pending" {
				t.Errorf("a pending record not yet due became %q", seededStatus)
			}
		case liveStaged:
			if seededStatus != "staged" {
				t.Errorf("an unexpired staged reservation became %q", seededStatus)
			}
		case liveLease:
			if seededStatus != "deleting" {
				t.Errorf("a record under a live cleanup lease became %q", seededStatus)
			}
		case owned:
			if seededStatus != "owned" {
				t.Errorf("an owned record became %q: a live attachment's object must never be swept", seededStatus)
			}
		case completed:
			if seededStatus != "completed" {
				t.Errorf("a completed record became %q", seededStatus)
			}
		}
	}
	if got := store.deleteCount(); got != len(claimable) {
		t.Errorf("object deletes = %d, want %d: %v", got, len(claimable), store.deletedKeys())
	}
}

// TestCleanup_StagedReservationExpiryReclaimsAnAbandonedObject is the
// crash-recovery path on its own, and it also pins what happens to the ledger
// row afterwards: the record is NOT deleted, it becomes 'completed' and stays
// (TS/Integration/AttachmentScanningTests.cs:54-55 asserts exactly that count
// of 1). That surviving row is load-bearing — queueObjectForDeletion reads it
// back to know the object is already gone and refuses to queue a second
// deletion for the key (objects.go, objects.sql's CleanupRecordCompletedExists),
// and without it a worker with no terminal failure state would retry a
// long-deleted key forever.
func TestCleanup_StagedReservationExpiryReclaimsAnAbandonedObject(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	key := "staged-attachments/" + uuid.NewString()

	// Exactly the state a crash between the object-store write and the
	// metadata commit leaves behind: a reservation still 'staged', its
	// 10-minute window elapsed, and an object nobody owns.
	id := seedCleanupRecord(t, h, cleanupSeed{status: "staged", storageKey: key, reservation: offset(-time.Second)})
	store.put(t, key)

	claimed, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background())
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if claimed != 1 {
		t.Fatalf("claimed = %d, want 1: an expired staged reservation is a candidate", claimed)
	}
	if store.has(key) {
		t.Error("the abandoned object survived: nothing else in the module ever reclaims it")
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE storage_key = $1`, key); n != 1 {
		t.Errorf("cleanup records for the key = %d, want 1: the row is kept, not deleted", n)
	}
	if got := readCleanupRow(t, h, id).status; got != "completed" {
		t.Errorf("status = %q, want completed", got)
	}
}

// TestCleanup_OwnedObjectIsNeverClaimed is
// ObjectLifecycleTests.Owned_object_is_not_selected_by_cleanup_worker
// (`:98-119`): the batch claims nothing and the object survives. An 'owned'
// record is a live attachment's object; sweeping it would delete a file a
// message still points at.
//
// It is named for what it proves. RunBatch reports records *claimed*, so a
// regression that made the candidate SELECT return an 'owned' row would be
// invisible here: the claim's own WHERE re-asserts the same predicate and
// refuses the row, keeping the object safe either way. That duplication
// between the select and the claim is deliberate and is .NET's own shape
// (queries/cleanup.sql's ClaimCleanupRecord comment) — it is the reason the
// invariant survives, not redundancy to remove. The select's negative
// predicate has no independent coverage, which this name no longer claims.
func TestCleanup_OwnedObjectIsNeverClaimed(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	key := "owned/" + uuid.NewString()
	seedCleanupRecord(t, h, cleanupSeed{status: "owned", storageKey: key})
	store.put(t, key)

	claimed, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background())
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if claimed != 0 {
		t.Errorf("claimed = %d, want 0", claimed)
	}
	if !store.has(key) {
		t.Error("an owned object was deleted")
	}
	if got := store.deleteCount(); got != 0 {
		t.Errorf("object deletes = %d, want 0", got)
	}
}

// ---- the claim ----

// TestCleanup_ClaimTakesAFiveMinuteLease observes the row from inside the
// delete, the only moment the claim's own writes are visible: status
// 'deleting', a fresh 32-hex lease id and lease_until exactly five minutes out
// (`:57-58`). Everything the worker writes afterwards clears those fields, so
// without this hook the claim's lease would be untested — and a lease that was
// never taken, or taken for the wrong window, would silently let a second
// replica delete the same object.
func TestCleanup_ClaimTakesAFiveMinuteLease(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	key := "cleanup/" + uuid.NewString()
	id := seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: key, nextAttemptIn: -time.Minute})
	store.put(t, key)

	var midFlight cleanupLedgerRow
	store.onDelete(func(string) { midFlight = readCleanupRow(t, h, id) })

	if _, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background()); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}

	if midFlight.status != "deleting" {
		t.Errorf("status during the delete = %q, want deleting", midFlight.status)
	}
	if midFlight.leaseID == nil {
		t.Fatal("lease_id during the delete = nil, want a fresh lease")
	}
	if len(*midFlight.leaseID) != 32 || strings.ContainsAny(*midFlight.leaseID, "-ABCDEF") {
		t.Errorf("lease_id = %q, want 32 lowercase hex characters with no dashes (Guid.NewGuid().ToString(\"N\"))", *midFlight.leaseID)
	}
	if midFlight.leaseUntil == nil {
		t.Fatal("lease_until during the delete = nil, want now + 5 minutes")
	}
	if want := h.Now().Add(5 * time.Minute); !midFlight.leaseUntil.Equal(want) {
		t.Errorf("lease_until = %s, want %s (now + 5 minutes)", midFlight.leaseUntil, want)
	}
	if midFlight.attempts != 0 {
		t.Errorf("attempts during the delete = %d, want 0: unlike the outbox, this claim does not increment attempts", midFlight.attempts)
	}
}

// ---- failure, retry and the absence of terminality ----

// TestCleanup_StorageFailureLeavesTheRecordForRetry is the brief's own step-1
// requirement: a storage failure must leave the record for another pass rather
// than losing the object. It also pins the two details of that path that are
// easy to get wrong — last_error is a CONSTANT, not the exception's text
// (`:76`), and the backoff is the module's shared retryBackoff on the
// incremented attempt count, so the first failure waits 2 s and not 1 s.
func TestCleanup_StorageFailureLeavesTheRecordForRetry(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	key := "cleanup/" + uuid.NewString()
	id := seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: key, nextAttemptIn: -time.Minute})
	store.put(t, key)
	store.failOn(key, errors.New("object store is on fire at /var/lib/secret-root"))

	claimed, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background())
	if err != nil {
		t.Fatalf("RunBatch: %v (a failed delete is an outcome, not a batch failure)", err)
	}
	if claimed != 1 {
		t.Errorf("claimed = %d, want 1: the return value counts claims, and a failed delete was still claimed", claimed)
	}

	row := readCleanupRow(t, h, id)
	if row.status != "pending" {
		t.Errorf("status = %q, want pending: a failed delete goes back on the queue", row.status)
	}
	if row.attempts != 1 {
		t.Errorf("attempts = %d, want 1", row.attempts)
	}
	if row.lastError == nil {
		t.Fatal("last_error = nil, want the constant failure message")
	}
	if *row.lastError != "Object cleanup failed." {
		t.Errorf("last_error = %q, want the constant %q", *row.lastError, "Object cleanup failed.")
	}
	if strings.Contains(*row.lastError, "secret-root") || strings.Contains(*row.lastError, "on fire") {
		t.Errorf("last_error = %q leaks the exception text, which is deliberately never stored", *row.lastError)
	}
	if want := h.Now().Add(2 * time.Second); !row.nextAttemptAt.Equal(want) {
		t.Errorf("next_attempt_at = %s, want %s (retryBackoff of the incremented attempt count)", row.nextAttemptAt, want)
	}
	if row.leaseID != nil || row.leaseUntil != nil {
		t.Errorf("lease not cleared on failure (%v/%v)", row.leaseID, row.leaseUntil)
	}
	if !store.has(key) {
		t.Error("the object is gone even though its delete failed")
	}
}

// TestCleanup_NeverGivesUp is the single most likely mistranslation in this
// worker, pinned: the outbox next door stops at max_attempts = 8 and writes a
// 'failed' status, and cleanup does NEITHER (inventory §12.2, "There is no
// terminal state for cleanup — a permanently failing key retries forever at
// 1024 s intervals"). A record that has failed far past any plausible ceiling
// comes straight back as 'pending', still due, still a candidate.
func TestCleanup_NeverGivesUp(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	store.failEverything(errors.New("the object store is permanently unavailable"))

	// 8 is the outbox's max_attempts, 10 the exponent clamp, and 99 is simply
	// far past anything a ceiling would allow.
	for _, attempts := range []int32{0, 7, 8, 9, 10, 99} {
		key := "cleanup/" + uuid.NewString()
		id := seedCleanupRecord(t, h, cleanupSeed{
			status: "pending", storageKey: key, nextAttemptIn: -time.Minute, attempts: attempts,
		})
		store.put(t, key)

		claimed, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background())
		if err != nil {
			t.Fatalf("attempts=%d: RunBatch: %v", attempts, err)
		}
		if claimed == 0 {
			t.Fatalf("attempts=%d: the record was not claimed", attempts)
		}

		row := readCleanupRow(t, h, id)
		if row.status != "pending" {
			t.Errorf("attempts=%d: status = %q, want pending — there is no terminal state for cleanup", attempts, row.status)
		}
		if row.attempts != attempts+1 {
			t.Errorf("attempts=%d: attempts = %d, want %d", attempts, row.attempts, attempts+1)
		}
		// min(3600, 2^min(attempts, 10)) seconds, the same expression and the
		// same effectively-1024 s ceiling as the outbox (backoff.go).
		want := h.Now().Add(retryBackoffForTest(attempts + 1))
		if !row.nextAttemptAt.Equal(want) {
			t.Errorf("attempts=%d: next_attempt_at = %s, want %s", attempts, row.nextAttemptAt, want)
		}
	}
}

// retryBackoffForTest is the .NET expression written out independently of
// backoff.go, so this file's expectations cannot drift into simply echoing the
// implementation's own arithmetic.
func retryBackoffForTest(attempts int32) time.Duration {
	exponent := attempts
	if exponent > 10 {
		exponent = 10
	}
	seconds := 1
	for i := int32(0); i < exponent; i++ {
		seconds *= 2
	}
	if seconds > 3600 {
		seconds = 3600
	}
	return time.Duration(seconds) * time.Second
}

// TestCleanup_RetriesForeverAcrossBatches is the same property over time
// rather than over one batch: a permanently failing key is still being
// retried after many cycles, with its backoff parked at the 1024 s ceiling and
// its status still 'pending'. A terminality check bolted on to match the
// outbox would show up here as a record that stops coming back.
func TestCleanup_RetriesForeverAcrossBatches(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	store.failEverything(errors.New("the object store is permanently unavailable"))
	key := "cleanup/" + uuid.NewString()
	id := seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: key, nextAttemptIn: -time.Minute})
	store.put(t, key)

	w := communications.NewCleanupWorker(h.Deps())
	const cycles = 12
	for i := 0; i < cycles; i++ {
		claimed, err := w.RunBatch(context.Background())
		if err != nil {
			t.Fatalf("cycle %d: RunBatch: %v", i, err)
		}
		if claimed != 1 {
			t.Fatalf("cycle %d: claimed = %d, want 1 — the record stopped being a candidate, so something gave up on it", i, claimed)
		}
		row := readCleanupRow(t, h, id)
		if row.status != "pending" {
			t.Fatalf("cycle %d: status = %q, want pending", i, row.status)
		}
		// Advance past the backoff the failure just scheduled, exactly as the
		// next due cycle would.
		h.Advance(row.nextAttemptAt.Sub(h.Now()) + time.Second)
	}

	row := readCleanupRow(t, h, id)
	if row.attempts != cycles {
		t.Errorf("attempts = %d, want %d", row.attempts, cycles)
	}
	if row.status != "pending" {
		t.Errorf("status after %d failures = %q, want pending", cycles, row.status)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'failed'`); n != 0 {
		t.Errorf("records in a 'failed' status = %d, want 0: this table has no such state", n)
	}
}

// TestCleanup_ReturnsRecordsClaimedNotSucceeded pins the return value against
// the reading that looks more useful and is wrong: it is `records.Count`
// (`:83`) — how many records this batch CLAIMED — not how many deletes
// succeeded.
func TestCleanup_ReturnsRecordsClaimedNotSucceeded(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	good := "cleanup/good/" + uuid.NewString()
	bad := "cleanup/bad/" + uuid.NewString()
	seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: good, nextAttemptIn: -time.Minute})
	seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: bad, nextAttemptIn: -time.Minute})
	store.put(t, good)
	store.put(t, bad)
	store.failOn(bad, errors.New("nope"))

	claimed, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background())
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if claimed != 2 {
		t.Errorf("claimed = %d, want 2: one delete failed, but both records were claimed", claimed)
	}
	if store.has(good) {
		t.Error("the deletable object survived")
	}
	if !store.has(bad) {
		t.Error("the failing object was deleted after all")
	}
}

// TestCleanup_DeletingAMissingKeySucceeds pins the contract half of the double
// delete (inventory §12.2's closing line, §16.1, docs/storage.md): deleting a
// key that is not there is a success, so a record whose object has already
// gone — a repeated retention queue, a partially completed earlier batch —
// completes rather than retrying forever.
//
// This test runs on the harness's REAL fs-backed store rather than a fake,
// because the contract it depends on is the fs driver's
// (internal/storage/fs.go's Delete, pinned by
// TestFS_DeleteMissingKeySucceeds): a driver that errored on a missing key
// would turn every already-deleted object into a permanent retry, and a fake
// would happily hide that.
func TestCleanup_DeletingAMissingKeySucceeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := "cleanup/never-written/" + uuid.NewString()
	id := seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: key, nextAttemptIn: -time.Minute})

	claimed, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background())
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if claimed != 1 {
		t.Fatalf("claimed = %d, want 1", claimed)
	}
	row := readCleanupRow(t, h, id)
	if row.status != "completed" {
		t.Errorf("status = %q, want completed: deleting a missing key is a success by contract", row.status)
	}
	if row.lastError != nil {
		t.Errorf("last_error = %q, want nil", *row.lastError)
	}
}

// ---- batching and the schedule ----

// TestCleanup_BatchIsOneHundredWithNoDrainLoop pins both halves of this
// worker's deliberate lack of configuration (inventory §12.2): one batch is
// .NET's hardcoded Take(100), and one call processes exactly one batch. The
// retention worker next door drains — `while (CleanupBatchAsync(...) > 0)` —
// and this one must not: at one batch per minute, the 101st record waits for
// the next tick.
func TestCleanup_BatchIsOneHundredWithNoDrainLoop(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	const total = 101
	for i := 0; i < total; i++ {
		key := "cleanup/bulk/" + uuid.NewString()
		seedCleanupRecord(t, h, cleanupSeed{
			status: "pending", storageKey: key, nextAttemptIn: -time.Minute,
			createdAgo: time.Duration(total-i) * time.Minute,
		})
		store.put(t, key)
	}

	w := communications.NewCleanupWorker(h.Deps())
	claimed, err := w.RunBatch(context.Background())
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if claimed != 100 {
		t.Errorf("one batch claimed %d records, want 100 (the hardcoded Take(100))", claimed)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'pending'`); n != 1 {
		t.Errorf("records still pending after one batch = %d, want 1: one call is one batch, never a drain", n)
	}

	second, err := w.RunBatch(context.Background())
	if err != nil {
		t.Fatalf("second RunBatch: %v", err)
	}
	if second != 1 {
		t.Errorf("the second batch claimed %d, want the 1 record the first left", second)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'completed'`); n != total {
		t.Errorf("completed records = %d, want %d", n, total)
	}
}

// TestCleanup_ClaimsTheOldestRecordsFirst pins the candidate ordering
// (`OrderBy(item => item.CreatedAt)`, `:48`): with more due records than one
// batch holds, it is the oldest that go first, so nothing starves behind a
// steady stream of newer work.
func TestCleanup_ClaimsTheOldestRecordsFirst(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	oldest := "cleanup/oldest/" + uuid.NewString()
	newest := "cleanup/newest/" + uuid.NewString()
	// 101 filler records, all newer than `oldest` and older than `newest`, so
	// a batch of 100 can only contain `oldest` if the ordering is by
	// created_at ascending.
	seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: oldest, nextAttemptIn: -time.Minute, createdAgo: 48 * time.Hour})
	store.put(t, oldest)
	for i := 0; i < 100; i++ {
		key := "cleanup/filler/" + uuid.NewString()
		seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: key, nextAttemptIn: -time.Minute, createdAgo: 24 * time.Hour})
		store.put(t, key)
	}
	seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: newest, nextAttemptIn: -time.Minute})
	store.put(t, newest)

	if _, err := communications.NewCleanupWorker(h.Deps()).RunBatch(context.Background()); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if store.has(oldest) {
		t.Error("the oldest due record was left out of the batch: candidates are ordered by created_at")
	}
	if !store.has(newest) {
		t.Error("the newest record was processed ahead of older ones")
	}
}

// TestCleanupWorker_NameAndIntervalAreFixed pins the schedule as a constant.
// Every other worker in this module reads its cadence from configuration; this
// one is a hardcoded TimeSpan.FromMinutes(1) (`SV/CommunicationsAttachmentCleanupWorker.cs:16`,
// inventory §12.2's "not configurable, unlike every other worker"), so the
// test also sets the neighbouring workers' knobs and shows they move nothing.
func TestCleanupWorker_NameAndIntervalAreFixed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := communications.NewCleanupWorker(h.Deps())
	if got := w.Name(); got != "communications-attachment-cleanup" {
		t.Errorf("Name = %q, want communications-attachment-cleanup", got)
	}
	if got := w.Interval(); got != time.Minute {
		t.Errorf("Interval = %v, want 1m (hardcoded)", got)
	}

	tuned := newHarness(t,
		modtest.WithEnv("COMMUNICATIONS_RETENTION_POLL", "90s"),
		modtest.WithEnv("COMMUNICATIONS_RETENTION_BATCH_SIZE", "7"))
	if got := communications.NewCleanupWorker(tuned.Deps()).Interval(); got != time.Minute {
		t.Errorf("Interval = %v with retention's keys set, want an unmoved 1m: cleanup has no configuration of its own", got)
	}
}

// TestCleanup_RunsWhileTheRetentionLeaseIsHeld is the inverse of
// retention_concurrency_test.go's lease test, and it is deliberately an
// inverse rather than an absence: cleanup takes no advisory lease at all
// (inventory §12.2, §14 — "every replica runs it"), so a batch must run to
// completion while another connection holds the retention lease.
func TestCleanup_RunsWhileTheRetentionLeaseIsHeld(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	key := "cleanup/" + uuid.NewString()
	seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: key, nextAttemptIn: -time.Minute})
	store.put(t, key)

	ctx := context.Background()
	holder, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the lease connection: %v", err)
	}
	defer holder.Release()
	var taken bool
	if err := holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, communications.RetentionLeaseKeyForTest).Scan(&taken); err != nil {
		t.Fatalf("take the retention lease: %v", err)
	}
	if !taken {
		t.Fatal("could not take the retention lease; nothing else should hold it")
	}
	defer func() {
		_, _ = holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, communications.RetentionLeaseKeyForTest)
	}()

	claimed, err := communications.NewCleanupWorker(h.Deps()).RunBatch(ctx)
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if claimed != 1 {
		t.Errorf("claimed = %d, want 1: this worker is not guarded by the retention lease", claimed)
	}
	if store.has(key) {
		t.Error("the object survived: the batch did not run")
	}
}

// ---- the worker port ----

// TestCleanupWorker_RunStopsWithItsContext pins the loop contract the runner
// depends on (internal/worker's Worker doc): Run works a batch and returns nil
// once ctx is done, rather than running forever or returning early.
func TestCleanupWorker_RunStopsWithItsContext(t *testing.T) {
	t.Parallel()
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	key := "cleanup/" + uuid.NewString()
	seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: key, nextAttemptIn: -time.Minute})
	store.put(t, key)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- communications.NewCleanupWorker(h.Deps()).Run(ctx) }()

	// The first batch runs immediately, before the first tick — which matters,
	// since the tick is a whole minute away.
	deadline := time.Now().Add(10 * time.Second)
	for store.has(key) {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("Run never worked the queued batch")
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
