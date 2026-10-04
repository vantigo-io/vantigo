package invoices_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
)

// eventsReady is an installation with the test Storecove's real credentials
// and its events worker.
func eventsReady(t *testing.T) (*harness, *invoices.EhfEventsWorker) {
	t.Helper()
	h, _ := ehfWorking(t)
	return h, invoices.NewEhfEventsWorker(h.Deps())
}

// plantWithRef plants a transmission of a fresh document at status, with
// provider reference ref ("" for none), and answers its id.
func plantWithRef(t *testing.T, h *harness, status, ref string) int64 {
	t.Helper()
	id := insertTransmission(t, h, issuedAcme(t, h).ID)
	if ref != "" {
		// Before the status: a delivered or failed row's reference is frozen.
		h.Exec(t, `UPDATE invoices.transmissions SET provider_ref = $2 WHERE id = $1`, id, ref)
	}
	if status != "queued" {
		h.Exec(t, `UPDATE invoices.transmissions SET status = $2::varchar, submit_attempted_at = $3,
			submitted_at = $3,
			delivered_at = CASE WHEN $2::varchar = 'delivered' THEN $3::timestamptz END,
			failed_at = CASE WHEN $2::varchar = 'failed' THEN $3::timestamptz END
			WHERE id = $1`, id, status, h.Now())
	}
	return id
}

// runEvents runs one events cycle, failing unless it held the lease and
// succeeded.
func runEvents(t *testing.T, w *invoices.EhfEventsWorker) {
	t.Helper()
	ran, err := w.RunCycle(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want it run", ran, err)
	}
}

// An event is matched to its row by the provider reference, or by the
// idempotency key when the row never learned the reference — which it then
// takes. succeeded makes it delivered at the module clock's time (Storecove's
// events carry none); failed makes it failed with the provider's reason,
// redacted; a row that was unconfirmed carries the machine's note and no
// user. Every event is acknowledged.
func TestEhfEventsWorker_MatchesByReferenceAndByKey(t *testing.T) {
	t.Parallel()
	h, w := eventsReady(t)
	byRef := plantWithRef(t, h, "submitted", "guid-ref")
	byKey := plantWithRef(t, h, "submitted", "")
	open := plantWithRef(t, h, "unconfirmed", "guid-open")
	key := txRow(t, h, byKey).IdempotencyKey
	events := []string{
		h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-ref"}),
		h.storecove.Enqueue(storecovetest.Event{Event: "failed", SubmissionGUID: "guid-key", IdempotencyGUID: key.String(),
			Details: "Refused by ap@example.com for 0192:923609016"}),
		h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-open"}),
	}
	h.Advance(time.Minute)
	now := h.Now()
	runEvents(t, w)

	if row := txRow(t, h, byRef); row.Status != "delivered" || !sameTime(row.DeliveredAt, &now) || row.ResolutionNote != nil {
		t.Errorf("by reference = %s at %v note %v, want delivered at %v without a note", row.Status, row.DeliveredAt, row.ResolutionNote, now)
	} else {
		wantNext(t, row, now)
	}
	row := txRow(t, h, byKey)
	if row.Status != "failed" || !sameTime(row.FailedAt, &now) || row.ProviderRef == nil || *row.ProviderRef != "guid-key" {
		t.Errorf("by key = %s at %v ref %v, want failed at %v taking guid-key", row.Status, row.FailedAt, row.ProviderRef, now)
	}
	if row.LastError == nil || *row.LastError != "Refused by <e-mail> for <participant>" {
		t.Errorf("by key reason = %v, want the provider's words redacted", row.LastError)
	}
	row = txRow(t, h, open)
	if row.Status != "delivered" || row.ResolvedByUserID != nil || row.ResolutionNote == nil || *row.ResolutionNote != "Resolved by the provider's event." {
		t.Errorf("unconfirmed = %s by %v note %v, want delivered by the machine with its note", row.Status, row.ResolvedByUserID, row.ResolutionNote)
	}
	if acked := h.storecove.Acked(); len(acked) != 3 || acked[0] != events[0] || acked[2] != events[2] {
		t.Errorf("acknowledged = %v, want %v", acked, events)
	}
}

// Events apply idempotently and without a row lease: a duplicate, an event
// for a row the probe already delivered, one for a failed row and one for no
// row of ours change nothing and are acknowledged; one for a row a worker
// holds a lease on applies; an event short of an outcome, or not about a
// submission at all, is acknowledged and nothing more.
func TestEhfEventsWorker_AppliesEachEventOnce(t *testing.T) {
	t.Parallel()
	h, w := eventsReady(t)
	dup := plantWithRef(t, h, "submitted", "guid-dup")
	probed := plantWithRef(t, h, "delivered", "guid-probed")
	failed := plantWithRef(t, h, "failed", "")
	leased := plantWithRef(t, h, "submitted", "guid-leased")
	h.Exec(t, `UPDATE invoices.transmissions SET lease_id = 'another', lease_until = $2 WHERE id = $1`, leased, h.Now().Add(time.Minute))

	h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-dup"})
	first := h.Now()
	runEvents(t, w)
	h.Advance(time.Hour)

	h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-dup"})
	h.storecove.Enqueue(storecovetest.Event{Event: "failed", SubmissionGUID: "guid-probed", Details: "late"})
	h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", IdempotencyGUID: txRow(t, h, failed).IdempotencyKey.String()})
	h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: uuid.NewString(), IdempotencyGUID: uuid.NewString()})
	h.storecove.Enqueue(storecovetest.Event{Event: "accepted", SubmissionGUID: "guid-leased"})
	h.storecove.Enqueue(storecovetest.Event{RawBody: `{"event_type":"invoice_response","event":"succeeded","guid":"guid-leased"}`})
	h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-leased"})
	now := h.Now()
	runEvents(t, w)

	if row := txRow(t, h, dup); row.Status != "delivered" || !sameTime(row.DeliveredAt, &first) {
		t.Errorf("duplicate = %s at %v, want delivered at the first event's %v", row.Status, row.DeliveredAt, first)
	}
	if row := txRow(t, h, probed); row.Status != "delivered" || row.LastError != nil {
		t.Errorf("already delivered = %s (%v), want delivered untouched", row.Status, row.LastError)
	}
	if row := txRow(t, h, failed); row.Status != "failed" || row.DeliveredAt != nil {
		t.Errorf("failed = %s delivered_at %v, want failed untouched", row.Status, row.DeliveredAt)
	}
	if row := txRow(t, h, leased); row.Status != "delivered" || !sameTime(row.DeliveredAt, &now) {
		t.Errorf("leased = %s at %v, want delivered at %v", row.Status, row.DeliveredAt, now)
	}
	if acked, queued := len(h.storecove.Acked()), h.storecove.Queued(); acked != 8 || queued != 0 {
		t.Errorf("acknowledged %d with %d left, want all 8 and none", acked, queued)
	}
}

// The drain reads until the queue is empty — one more read, answered 204 —
// and acknowledges every event; a read that fails ends the cycle with
// nothing acknowledged, and the next cycle reads the same event again.
func TestEhfEventsWorker_DrainsUntilTheQueueIsEmpty(t *testing.T) {
	t.Parallel()
	h, w := eventsReady(t)
	plantWithRef(t, h, "submitted", "guid-a")
	for range 3 {
		h.storecove.Enqueue(storecovetest.Event{Event: "accepted", SubmissionGUID: "guid-a"})
	}
	requests := h.storecove.Requests()
	runEvents(t, w)
	if got := h.storecove.Requests() - requests; got != 7 {
		t.Errorf("requests = %d, want three reads, three acknowledgements and the empty read", got)
	}
	if h.storecove.Queued() != 0 {
		t.Errorf("%d events left", h.storecove.Queued())
	}

	guid := h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-a"})
	h.storecove.Fail(storecovetest.ServerError)
	if _, err := w.RunCycle(context.Background()); err == nil {
		t.Errorf("a failed read ended the cycle without an error")
	}
	if len(h.storecove.Acked()) != 3 || h.storecove.Queued() != 1 {
		t.Errorf("the failed read acknowledged something")
	}
	h.storecove.Fail(storecovetest.Normal)
	runEvents(t, w)
	if acked := h.storecove.Acked(); acked[len(acked)-1] != guid {
		t.Errorf("the event was not read again")
	}
}

// One replica drains at a time: while another holds the advisory lease, a
// cycle reads nothing.
func TestEhfEventsWorker_OneDrainAtATime(t *testing.T) {
	t.Parallel()
	h, w := eventsReady(t)
	plantWithRef(t, h, "submitted", "guid-a")
	h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-a"})
	ctx := context.Background()
	conn, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(0x494E5645484631)); err != nil {
		t.Fatal(err)
	}
	ran, err := w.RunCycle(ctx)
	if err != nil || ran {
		t.Errorf("RunCycle under another's lease = %v, %v; want skipped", ran, err)
	}
	if h.storecove.Requests() != 0 || h.storecove.Queued() != 1 {
		t.Errorf("the queue was read under another replica's lease")
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, int64(0x494E5645484631)); err != nil {
		t.Fatal(err)
	}
	runEvents(t, w)
	if h.storecove.Queued() != 0 {
		t.Errorf("the drain after the lease was released left the event")
	}
}

// The provider is asked nothing unless a row awaits an event: a submitted or
// unconfirmed row, or a queued one whose marker is set.
func TestEhfEventsWorker_AsksNothingWhenNothingAwaits(t *testing.T) {
	t.Parallel()
	h, w := eventsReady(t)
	queued := plantWithRef(t, h, "queued", "")
	plantWithRef(t, h, "delivered", "guid-d")
	h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-x"})
	runEvents(t, w)
	if h.storecove.Requests() != 0 {
		t.Errorf("requests = %d, want none", h.storecove.Requests())
	}

	h.Exec(t, `UPDATE invoices.transmissions SET submit_attempted_at = $2 WHERE id = $1`, queued, h.Now())
	runEvents(t, w)
	if h.storecove.Queued() != 0 {
		t.Errorf("a queued row with its marker set did not start a drain")
	}
}

// An event the database refuses — here a reference longer than the column,
// SQLSTATE 22001, on a row it matches by key — would be refused every cycle
// and, the queue being first in, first out, hold every event behind it. It
// is dead-lettered: logged at error and acknowledged, and the event behind
// it is applied.
func TestEhfEventsWorker_DeadLettersAPoisonEvent(t *testing.T) {
	t.Parallel()
	h, w := eventsReady(t)
	poisoned := plantWithRef(t, h, "submitted", "")
	next := plantWithRef(t, h, "submitted", "guid-next")
	poison := h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: strings.Repeat("g", 201),
		IdempotencyGUID: txRow(t, h, poisoned).IdempotencyKey.String()})
	behind := h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-next"})
	runEvents(t, w)

	if acked := h.storecove.Acked(); !slices.Equal(acked, []string{poison, behind}) {
		t.Errorf("acknowledged = %v, want the poison event and the one behind it", acked)
	}
	if row := txRow(t, h, poisoned); row.Status != "submitted" || row.ProviderRef != nil {
		t.Errorf("poisoned = %s ref %v, want untouched", row.Status, row.ProviderRef)
	}
	if row := txRow(t, h, next); row.Status != "delivered" {
		t.Errorf("the event behind = %s, want delivered", row.Status)
	}
	logs := h.Logs()
	if !strings.Contains(logs, `"level":"ERROR","msg":"invoices: an access point event the database refuses is dead-lettered and acknowledged"`) ||
		!strings.Contains(logs, `"event":"`+poison+`"`) || !strings.Contains(logs, `"sqlstate":"22001"`) {
		t.Errorf("no dead-letter line naming the event and its SQLSTATE")
	}
}

// A transient database failure — here a lock timeout while another
// transaction holds the row — is not the event's fault: the event is not
// acknowledged, the cycle ends with the error, and the next cycle applies it.
// Acknowledging before applying would lose it here.
func TestEhfEventsWorker_LeavesAnEventOnATransientFailure(t *testing.T) {
	t.Parallel()
	h, _ := eventsReady(t)
	ctx := context.Background()
	cfg := h.Pool().Config().Copy()
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200ms"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	d := h.Deps()
	d.Pool = pool
	w := invoices.NewEhfEventsWorker(d)

	held := plantWithRef(t, h, "submitted", "guid-held")
	guid := h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-held"})
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices.transmissions WHERE id = $1 FOR UPDATE`, held); err != nil {
		t.Fatal(err)
	}
	ran, err := w.RunCycle(ctx)
	if !ran || err == nil {
		t.Errorf("RunCycle under a held row lock = %v, %v; want it run and failed", ran, err)
	}
	if len(h.storecove.Acked()) != 0 || h.storecove.Queued() != 1 {
		t.Errorf("acknowledged %v with %d queued, want none and the event kept", h.storecove.Acked(), h.storecove.Queued())
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	runEvents(t, w)
	if acked := h.storecove.Acked(); !slices.Equal(acked, []string{guid}) {
		t.Errorf("acknowledged = %v, want the event once applied", acked)
	}
	if row := txRow(t, h, held); row.Status != "delivered" {
		t.Errorf("held = %s, want delivered", row.Status)
	}
}

// A cycle needs exactly one pooled connection, the one its advisory lease
// holds (the branch's smoke failure, PR #124's shape): on the api's pool of
// four, four lease workers each held one and waited for a second, and
// /health/ready timed out. On a pool of one, a cycle that reached for a second
// connection would wait for itself; the deadline turns that hang into this
// test's failure. Every path runs: nothing awaiting; an awaiting row whose
// event is read, applied and acknowledged; and a key that cannot be opened,
// flagged — the credentials read, the flags and the apply all on the lease's
// connection.
func TestEhfEventsWorker_RunsTheCycleOnTheOneConnectionItHolds(t *testing.T) {
	t.Parallel()
	h, _ := eventsReady(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	d := h.Deps()
	d.Pool = one
	w := invoices.NewEhfEventsWorker(d)

	if ran, err := w.RunCycle(ctx); err != nil || !ran {
		t.Fatalf("RunCycle with nothing awaiting on a pool of one = %v, %v; want true, nil", ran, err)
	}

	tid := plantWithRef(t, h, "submitted", "guid-one")
	guid := h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-one"})
	if ran, err := w.RunCycle(ctx); err != nil || !ran {
		t.Fatalf("RunCycle with an event on a pool of one = %v, %v; want true, nil", ran, err)
	}
	if row := txRow(t, h, tid); row.Status != "delivered" {
		t.Errorf("row = %s, want delivered", row.Status)
	}
	if acked := h.storecove.Acked(); !slices.Equal(acked, []string{guid}) {
		t.Errorf("acknowledged = %v, want %v", acked, []string{guid})
	}

	// A key the box cannot open is flagged on the same connection.
	plantWithRef(t, h, "submitted", "guid-two")
	plantAccessPointCredentials(t, h)
	if ran, err := w.RunCycle(ctx); err != nil || !ran {
		t.Fatalf("RunCycle with an unreadable key on a pool of one = %v, %v; want true, nil", ran, err)
	}
	if rejectedAt(t, h) == nil {
		t.Errorf("rejected_at is not set for the unreadable key")
	}
}
