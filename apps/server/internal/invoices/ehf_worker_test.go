package invoices_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// ehfWorking is an installation ready to send as EHF whose stored
// credentials are the test Storecove's real key and legal entity, so the
// workers reach it through the module's own adapter.
func ehfWorking(t *testing.T, opts ...modtest.Option) (*harness, *fakeLookup) {
	t.Helper()
	h, fake := ehfReady(t, opts...)
	key := storecovetest.APIKey
	putAccessPoint(t, h, accessPointBody(&key))
	return h, fake
}

// queuedEhf issues an invoice to Acme and sends it as EHF, answering the
// document's id and its queued transmission's.
func queuedEhf(t *testing.T, h *harness) (invoiceID, transmissionID int64) {
	t.Helper()
	inv := issuedAcme(t, h)
	sentAsEhf(t, h, inv.ID)
	tid, _, _, _ := latestTransmission(t, h, inv.ID)
	return inv.ID, tid
}

// txRow is one transmission row as stored.
func txRow(t *testing.T, h *harness, id int64) store.InvoicesTransmission {
	t.Helper()
	row, err := store.New(h.Pool()).GetTransmission(context.Background(), id)
	if err != nil {
		t.Fatalf("read transmission %d: %v", id, err)
	}
	return row
}

// processOne runs one claim, failing the test on an error.
func processOne(t *testing.T, w *invoices.EhfWorker) bool {
	t.Helper()
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	return processed
}

// next is a row's next_attempt_at; infinite rows answer the zero time and
// true.
func next(t *testing.T, row store.InvoicesTransmission) (time.Time, bool) {
	t.Helper()
	if !row.NextAttemptAt.Valid {
		t.Fatalf("transmission %d has no next_attempt_at", row.ID)
	}
	return row.NextAttemptAt.Time, row.NextAttemptAt.InfinityModifier == pgtype.Infinity
}

// wantNext asserts a row is due at exactly want.
func wantNext(t *testing.T, row store.InvoicesTransmission, want time.Time) {
	t.Helper()
	got, infinite := next(t, row)
	if infinite || !got.Equal(want) {
		t.Errorf("transmission %d next_attempt_at = %v (infinite %v), want %v", row.ID, got, infinite, want)
	}
}

// wantParked asserts a row is never due again: 'infinity'.
func wantParked(t *testing.T, row store.InvoicesTransmission) {
	t.Helper()
	if _, infinite := next(t, row); !infinite {
		t.Errorf("transmission %d next_attempt_at = %v, want 'infinity'", row.ID, row.NextAttemptAt.Time)
	}
}

// sameTime is whether a nullable time is want (nil for none).
func sameTime(got, want *time.Time) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return got.Equal(*want)
}

// rejectedAt is the credentials row's rejected_at.
func rejectedAt(t *testing.T, h *harness) *time.Time {
	t.Helper()
	return storedCredentials(t, h).RejectedAt
}

// documentNumber is an issued document's number.
func documentNumber(t *testing.T, h *harness, id int64) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT number FROM invoices.invoices WHERE id = $1`, id)
}

// The submit claim: one row, one worker, one POST. Two workers racing for the
// same row submit it once; a row another worker holds a lease on is not
// claimed while the lease runs, and is once it has run out. The submission
// carries the stored UBL under the row's idempotency key, the receiver mapped
// onto Storecove's scheme, and the row records the reference, the crash
// marker and the first probe five minutes out. A lookup younger than a day is
// not asked again.
func TestEhfWorker_SubmitsOnceUnderALease(t *testing.T) {
	t.Parallel()
	h, fake := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	now := h.Now()

	workers := []*invoices.EhfWorker{invoices.NewEhfWorker(h.Deps()), invoices.NewEhfWorker(h.Deps())}
	var wg sync.WaitGroup
	claimed := make([]bool, len(workers))
	errs := make([]error, len(workers))
	for i, w := range workers {
		wg.Go(func() { claimed[i], errs[i] = w.ProcessOne(context.Background()) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if claimed[0] == claimed[1] {
		t.Fatalf("claimed = %v, want exactly one of the two workers to claim the row", claimed)
	}
	for _, w := range workers {
		if processOne(t, w) {
			t.Errorf("a second claim found a row due; want none until the first probe")
		}
	}
	subs := h.storecove.Submissions()
	if len(subs) != 1 {
		t.Fatalf("submissions = %d, want 1", len(subs))
	}
	row := txRow(t, h, tid)
	if row.Status != "submitted" || row.ProviderRef == nil || *row.ProviderRef != subs[0].GUID {
		t.Errorf("row = %s ref %v, want submitted with %s", row.Status, row.ProviderRef, subs[0].GUID)
	}
	if !sameTime(row.SubmittedAt, &now) || !sameTime(row.SubmitAttemptedAt, &now) || row.LeaseID != nil || row.PollAttempts != 0 {
		t.Errorf("row submitted_at %v marker %v lease %v polls %d, want %v, %v, none, 0",
			row.SubmittedAt, row.SubmitAttemptedAt, row.LeaseID, row.PollAttempts, now, now)
	}
	wantNext(t, row, now.Add(5*time.Minute))
	if subs[0].IdempotencyGUID != row.IdempotencyKey.String() || subs[0].Scheme != "NO:ORG" || subs[0].ID != "923609016" {
		t.Errorf("submission key %s scheme %s id %s, want %s NO:ORG 923609016", subs[0].IdempotencyGUID, subs[0].Scheme, subs[0].ID, row.IdempotencyKey)
	}
	if string(subs[0].UBL) != string(h.objects.object(row.UblObjectKey)) {
		t.Errorf("the submitted UBL is not the stored one")
	}
	if asked := fake.asked(); len(asked) != 1 {
		t.Errorf("lookups = %v, want only the send's: the stored one is younger than a day", asked)
	}

	// A lease another worker holds keeps the row; an expired one does not.
	_, held := queuedEhf(t, h)
	h.Exec(t, `UPDATE invoices.transmissions SET lease_id = 'another', lease_until = $2 WHERE id = $1`, held, now.Add(30*time.Second))
	if processOne(t, workers[0]) {
		t.Fatalf("a row leased by another worker was claimed")
	}
	h.Advance(31 * time.Second)
	if !processOne(t, workers[0]) {
		t.Fatalf("a row whose lease ran out was not claimed")
	}
	if row := txRow(t, h, held); row.Status != "submitted" || len(h.storecove.Submissions()) != 2 {
		t.Errorf("after the lease ran out: %s with %d submissions, want submitted with 2", row.Status, len(h.storecove.Submissions()))
	}
}

// The submit claim asks the Peppol network again when the send's answer is a
// day old, records the fresh answer, and ends a row whose receiver no longer
// takes the document as failed (receiver_not_receivable) without stamping
// the crash marker or calling the provider. A lookup that fails waits on the
// backoff.
func TestEhfWorker_RefreshesAnAgedLookup(t *testing.T) {
	t.Parallel()
	h, fake := ehfWorking(t)
	_, fresh := queuedEhf(t, h)
	_, gone := queuedEhf(t, h)
	_, unasked := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	h.Advance(25 * time.Hour)
	now := h.Now()

	processOne(t, w)
	if row := txRow(t, h, fresh); row.Status != "submitted" || !row.LookupAt.Equal(now) || !row.LookupCanReceive {
		t.Errorf("fresh = %s, lookup_at %v can %v; want submitted, looked up at %v", row.Status, row.LookupAt, row.LookupCanReceive, now)
	}
	if asked := fake.asked(); len(asked) != 4 || asked[3] != "0192:923609016" {
		t.Errorf("lookups = %v, want the three sends' and one refresh of 0192:923609016", asked)
	}

	fake.answer(peppol.Result{Registered: true, CanReceiveInvoice: false, CanReceiveCreditNote: true}, nil)
	processOne(t, w)
	row := txRow(t, h, gone)
	if row.Status != "failed" || row.LastError == nil || *row.LastError != "receiver_not_receivable" {
		t.Errorf("gone = %s (%v), want failed with receiver_not_receivable", row.Status, row.LastError)
	}
	if row.SubmitAttemptedAt != nil || row.LookupCanReceive || !row.LookupAt.Equal(now) {
		t.Errorf("gone marker %v can %v lookup_at %v, want no marker and the fresh refusal", row.SubmitAttemptedAt, row.LookupCanReceive, row.LookupAt)
	}

	fake.answer(peppol.Result{}, errors.New("dns timeout"))
	processOne(t, w)
	row = txRow(t, h, unasked)
	if row.Status != "queued" || row.SubmitAttempts != 1 || row.SubmitAttemptedAt != nil {
		t.Errorf("unasked = %s attempts %d marker %v, want queued, one attempt, no marker", row.Status, row.SubmitAttempts, row.SubmitAttemptedAt)
	}
	wantNext(t, row, now.Add(2*time.Second))
	if subs := h.storecove.Submissions(); len(subs) != 1 {
		t.Errorf("submissions = %d, want only fresh's", len(subs))
	}
}

// An erase cancels a never-attempted queued row even while a worker holds
// it (D12): the claim has read the row and is reading its stored UBL when the
// anonymisation commits, and its marker — stamped only on a row still queued
// — matches nothing, so the claim stops there. No POST is made, the row stays
// cancelled without a marker, and nothing is due again.
func TestEhfWorker_ACancelledClaimMakesNoPost(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	var erased []contracts.ErasedData
	var eraseErr error
	h.objects.beforeGets(func(key string) {
		if !strings.HasSuffix(key, ".xml") || erased != nil {
			return
		}
		inTx(t, h, true, func(tx pgx.Tx) {
			erased, eraseErr = data.EraseCustomerData(context.Background(), tx, customerAcme)
		})
	})
	w := invoices.NewEhfWorker(h.Deps())

	if !processOne(t, w) {
		t.Fatal("the queued row was not claimed")
	}
	if eraseErr != nil || !slices.Contains(erased, contracts.ErasedData{Kind: "invoices.transmissions", Count: 1}) {
		t.Fatalf("the erase under the claim = %+v, %v; want the leased row cancelled", erased, eraseErr)
	}
	if subs := h.storecove.Submissions(); len(subs) != 0 {
		t.Errorf("submissions = %d, want none: the row was cancelled before its marker", len(subs))
	}
	row := txRow(t, h, tid)
	if row.Status != "cancelled" || row.SubmitAttemptedAt != nil || row.SubmitAttempts != 0 {
		t.Errorf("the row = %s marker %v attempts %d, want cancelled, no marker, nothing counted", row.Status, row.SubmitAttemptedAt, row.SubmitAttempts)
	}
	h.objects.beforeGets(nil)
	if processOne(t, w) {
		t.Error("a cancelled row is due again")
	}
}

// The crash marker is stamped before the POST, so a submission the provider
// never answered leaves it set — the outcome is unknown. An outcome that
// proves the provider did not take the document (429, 401) puts it back to
// what it was before the claim: the earlier stamp, not the claim's own, and
// NULL when there was none.
func TestEhfWorker_StampsTheMarkerBeforeThePost(t *testing.T) {
	defer invoices.SetEhfCallTimeout(300 * time.Millisecond)()
	h, _ := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	first := h.Now()

	h.storecove.Fail(storecovetest.Timeout)
	processOne(t, w)
	row := txRow(t, h, tid)
	if row.Status != "queued" || !sameTime(row.SubmitAttemptedAt, &first) || row.SubmitAttempts != 1 || row.LastError == nil {
		t.Fatalf("after a timeout: %s marker %v attempts %d error %v; want queued, the marker %v, one attempt, a reason",
			row.Status, row.SubmitAttemptedAt, row.SubmitAttempts, row.LastError, first)
	}
	wantNext(t, row, first.Add(2*time.Second))

	h.Advance(2 * time.Second)
	h.storecove.Fail(storecovetest.Throttled)
	processOne(t, w)
	if row = txRow(t, h, tid); !sameTime(row.SubmitAttemptedAt, &first) || row.SubmitAttempts != 1 {
		t.Errorf("after a 429: marker %v attempts %d, want the earlier %v and no attempt counted", row.SubmitAttemptedAt, row.SubmitAttempts, first)
	}

	h.Advance(storecovetest.RetryAfterSeconds * time.Second)
	h.storecove.Fail(storecovetest.Unauthorized)
	processOne(t, w)
	if row = txRow(t, h, tid); !sameTime(row.SubmitAttemptedAt, &first) || row.SubmitAttempts != 1 {
		t.Errorf("after a 401: marker %v attempts %d, want the earlier %v and no attempt counted", row.SubmitAttemptedAt, row.SubmitAttempts, first)
	}

	_, never := queuedEhf(t, h)
	h.Exec(t, `UPDATE invoices.transmissions SET next_attempt_at = 'infinity' WHERE id = $1`, tid)
	h.storecove.Fail(storecovetest.Throttled)
	processOne(t, w)
	if row = txRow(t, h, never); row.SubmitAttemptedAt != nil {
		t.Errorf("a first attempt throttled left the marker %v, want NULL", row.SubmitAttemptedAt)
	}
	if subs := h.storecove.Submissions(); len(subs) != 0 {
		t.Errorf("submissions = %d, want none", len(subs))
	}
}

// A receiver whose scheme the adapter cannot map — a GLN, on the EAS list but
// not in Storecove's table — is refused before any call:
// the row fails with the reason, and the marker is what it was before the
// claim.
func TestEhfWorker_UnmappedSchemeFailsAndRestoresTheMarker(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.PeppolID = "0088:7080000000001" })
	_, tid := queuedEhf(t, h)
	earlier := h.Now().Add(-time.Hour)
	h.Exec(t, `UPDATE invoices.transmissions SET submit_attempted_at = $2 WHERE id = $1`, tid, earlier)
	before := h.storecove.Requests()

	processOne(t, invoices.NewEhfWorker(h.Deps()))
	row := txRow(t, h, tid)
	if row.Status != "failed" || row.LastError == nil || !strings.Contains(*row.LastError, "scheme") {
		t.Errorf("row = %s (%v), want failed naming the scheme", row.Status, row.LastError)
	}
	if !sameTime(row.SubmitAttemptedAt, &earlier) {
		t.Errorf("marker = %v, want the pre-claim %v", row.SubmitAttemptedAt, earlier)
	}
	if got := h.storecove.Requests(); got != before {
		t.Errorf("requests = %d, want %d: the refusal is made before any call", got, before)
	}
}

// The 422 rule (D9). On the first attempt — the marker was NULL before the
// claim — it is a validation refusal: failed, with the provider's messages.
// On a retry it may be the duplicate-key refusal of a submission that went
// through: here the provider took the document, the completion found the
// lease changed and recorded nothing, and the next claim's 422 makes the row
// submitted without a reference, for the event drain to resolve by key.
func TestEhfWorker_TheUnprocessableRule(t *testing.T) {
	h, _ := ehfWorking(t)
	_, refused := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())

	h.storecove.Fail(storecovetest.Unprocessable)
	processOne(t, w)
	row := txRow(t, h, refused)
	rejection := storecovetest.Rejection.Source + ": " + storecovetest.Rejection.Details
	if row.Status != "failed" || row.LastError == nil || *row.LastError != rejection {
		t.Errorf("first attempt = %s (%v), want failed with %q", row.Status, deref(row.LastError), rejection)
	}
	h.storecove.Fail(storecovetest.Normal)

	_, retried := queuedEhf(t, h)
	moved := false
	defer invoices.SetEhfAfterCall(func(ctx context.Context, id int64) {
		if id == retried && !moved {
			moved = true
			h.Exec(t, `UPDATE invoices.transmissions SET lease_id = 'another', lease_until = $2 WHERE id = $1`, id, h.Now().Add(time.Minute))
		}
	})()
	processOne(t, w)
	row = txRow(t, h, retried)
	if row.Status != "queued" || row.SubmitAttemptedAt == nil || row.ProviderRef != nil || len(h.storecove.Submissions()) != 1 {
		t.Fatalf("after the lease changed: %s marker %v ref %v, %d submissions; want queued, marked, no reference, one submission",
			row.Status, row.SubmitAttemptedAt, row.ProviderRef, len(h.storecove.Submissions()))
	}
	if !strings.Contains(h.Logs(), "moved under the claim") {
		t.Errorf("the no-op completion was not logged")
	}

	h.Advance(61 * time.Second)
	now := h.Now()
	processOne(t, w)
	row = txRow(t, h, retried)
	if row.Status != "submitted" || row.ProviderRef != nil || !sameTime(row.SubmittedAt, &now) || row.LastError == nil {
		t.Errorf("the retry's 422 = %s ref %v submitted_at %v error %v; want submitted at %v without a reference, with the reason",
			row.Status, row.ProviderRef, row.SubmittedAt, row.LastError, now)
	}
	wantNext(t, row, now.Add(time.Hour))
	if n := len(h.storecove.Submissions()); n != 1 {
		t.Errorf("submissions = %d, want 1", n)
	}
}

// A transport failure or a 5xx counts an attempt and waits 2^n seconds,
// never more than an hour; attempts are never capped, and the marker stays
// set — the outcome is unknown.
func TestEhfWorker_BacksOffOnServerErrors(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	h.storecove.Fail(storecovetest.ServerError)

	for attempt, wait := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		now := h.Now()
		processOne(t, w)
		row := txRow(t, h, tid)
		if row.Status != "queued" || row.SubmitAttempts != int32(attempt+1) || !sameTime(row.SubmitAttemptedAt, &now) {
			t.Fatalf("attempt %d: %s attempts %d marker %v; want queued, %d, %v", attempt+1, row.Status, row.SubmitAttempts, row.SubmitAttemptedAt, attempt+1, now)
		}
		wantNext(t, row, now.Add(wait))
		h.Advance(wait)
	}
	h.Exec(t, `UPDATE invoices.transmissions SET submit_attempts = 11 WHERE id = $1`, tid)
	now := h.Now()
	processOne(t, w)
	row := txRow(t, h, tid)
	if row.SubmitAttempts != 12 || row.Status != "queued" {
		t.Errorf("attempts = %d (%s), want 12 and still queued: attempts are not capped", row.SubmitAttempts, row.Status)
	}
	wantNext(t, row, now.Add(time.Hour))
}

// A 429 waits what its Retry-After asked — seconds or an HTTP date — or a
// minute when it named nothing, and counts no attempt.
func TestEhfWorker_HonoursRetryAfter(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())

	h.storecove.Fail(storecovetest.Throttled)
	now := h.Now()
	processOne(t, w)
	row := txRow(t, h, tid)
	if row.Status != "queued" || row.SubmitAttempts != 0 || row.SubmitAttemptedAt != nil || row.LastError == nil {
		t.Errorf("throttled: %s attempts %d marker %v error %v; want queued, none, none, a reason", row.Status, row.SubmitAttempts, row.SubmitAttemptedAt, row.LastError)
	}
	wantNext(t, row, now.Add(storecovetest.RetryAfterSeconds*time.Second))

	h.Advance(storecovetest.RetryAfterSeconds * time.Second)
	h.storecove.Fail(storecovetest.ThrottledWithoutRetryAfter)
	now = h.Now()
	processOne(t, w)
	wantNext(t, txRow(t, h, tid), now.Add(time.Minute))

	// The date form, judged by the module clock: the harness clock is put
	// three hours before RetryAfterDate.
	dated, _ := ehfWorking(t)
	dated.Advance(storecovetest.RetryAfterDate.Add(-3 * time.Hour).Sub(dated.Now()))
	_, dtid := queuedEhf(t, dated)
	dated.storecove.Fail(storecovetest.ThrottledUntilDate)
	processOne(t, invoices.NewEhfWorker(dated.Deps()))
	if row := txRow(t, dated, dtid); row.SubmitAttempts != 0 {
		t.Errorf("attempts = %d, want none counted", row.SubmitAttempts)
	} else {
		wantNext(t, row, storecovetest.RetryAfterDate)
	}
}

// A refused key leaves the row queued an hour out, counts no attempt, flags
// the credentials as rejected (meta reports it) and logs at error; the next
// successful call clears the flag.
func TestEhfWorker_UnauthorizedFlagsTheKey(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())

	h.storecove.Fail(storecovetest.Unauthorized)
	now := h.Now()
	processOne(t, w)
	row := txRow(t, h, tid)
	if row.Status != "queued" || row.SubmitAttempts != 0 || row.SubmitAttemptedAt != nil {
		t.Errorf("after a 401: %s attempts %d marker %v; want queued, none, none", row.Status, row.SubmitAttempts, row.SubmitAttemptedAt)
	}
	wantNext(t, row, now.Add(time.Hour))
	if rejectedAt(t, h) == nil {
		t.Errorf("rejected_at is not set")
	}
	if !strings.Contains(h.Logs(), `"level":"ERROR","msg":"invoices: the access point refused the key`) {
		t.Errorf("no error log for the refused key")
	}

	h.storecove.Fail(storecovetest.Normal)
	h.Advance(time.Hour)
	processOne(t, w)
	if row := txRow(t, h, tid); row.Status != "submitted" || rejectedAt(t, h) != nil {
		t.Errorf("after the key was accepted: %s rejected_at %v; want submitted and the flag cleared", row.Status, rejectedAt(t, h))
	}
}

// The cap is by age (reading 11): a queued row 48 hours old becomes
// unconfirmed when the marker says the provider may have it — never probed,
// it waits for a person — and failed when it was never attempted. Neither
// asks the network or the provider.
func TestEhfWorker_CapsAQueuedRowByAge(t *testing.T) {
	t.Parallel()
	h, fake := ehfWorking(t)
	_, never := queuedEhf(t, h)
	_, attempted := queuedEhf(t, h)
	h.Exec(t, `UPDATE invoices.transmissions SET submit_attempted_at = $2, submit_attempts = 40 WHERE id = $1`, attempted, h.Now())
	w := invoices.NewEhfWorker(h.Deps())
	h.Advance(48*time.Hour + time.Minute)
	lookups, requests := len(fake.asked()), h.storecove.Requests()

	processOne(t, w)
	processOne(t, w)
	if row := txRow(t, h, never); row.Status != "failed" || row.LastError == nil || row.FailedAt == nil {
		t.Errorf("never attempted = %s (%v), want failed with a reason", row.Status, row.LastError)
	}
	row := txRow(t, h, attempted)
	if row.Status != "unconfirmed" || row.LastError == nil {
		t.Errorf("attempted = %s (%v), want unconfirmed with a reason", row.Status, row.LastError)
	}
	wantParked(t, row)
	if len(fake.asked()) != lookups || h.storecove.Requests() != requests {
		t.Errorf("the cap asked the network or the provider")
	}
	if processOne(t, w) {
		t.Errorf("a capped row is due again")
	}
}

// A submitted row is probed on the cadence — five minutes, fifteen, then
// hourly — one Evidence call per claim; the answer that arrives makes it
// delivered, committed first, and the next claim fetches the evidence again
// and stores the receipt and the delivered copy beside the document's PDF.
func TestEhfWorker_ProbesOnTheCadenceThenStoresTheEvidence(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	id, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	processOne(t, w)

	for polls, wait := range []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour} {
		h.Advance(wait)
		requests := h.storecove.Requests()
		processOne(t, w)
		if got := h.storecove.Requests() - requests; got != 1 {
			t.Errorf("probe %d made %d calls, want 1", polls+1, got)
		}
		row := txRow(t, h, tid)
		if row.Status != "submitted" || row.PollAttempts != int32(polls+1) {
			t.Fatalf("probe %d: %s polls %d, want submitted, %d", polls+1, row.Status, row.PollAttempts, polls+1)
		}
		wantNext(t, row, h.Now().Add([]time.Duration{15 * time.Minute, time.Hour, time.Hour}[polls]))
	}

	guid := h.storecove.Submissions()[0].GUID
	delivered := []byte("<Invoice>as delivered</Invoice>")
	receipt := h.storecove.Evidence(guid, storecovetest.Document{Body: delivered})
	h.Advance(time.Hour)
	now := h.Now()
	requests := h.storecove.Requests()
	processOne(t, w)
	row := txRow(t, h, tid)
	if row.Status != "delivered" || !sameTime(row.DeliveredAt, &now) || row.PollAttempts != 0 || row.EvidenceObjectKey != nil {
		t.Fatalf("the probe that found it: %s delivered_at %v polls %d evidence %v; want delivered at %v, polls reset, no evidence yet",
			row.Status, row.DeliveredAt, row.PollAttempts, row.EvidenceObjectKey, now)
	}
	if row.ResolutionNote != nil {
		t.Errorf("a submitted row delivered carries the note %q", *row.ResolutionNote)
	}
	if got := h.storecove.Requests() - requests; got != 1 {
		t.Errorf("the probe made %d calls, want 1", got)
	}

	requests = h.storecove.Requests()
	processOne(t, w)
	if got := h.storecove.Requests() - requests; got != 1 {
		t.Errorf("the evidence claim made %d calls, want 1", got)
	}
	base := "documents/" + itoa(id) + "/" + itoa(documentNumber(t, h, id)) + "-" + itoa(tid)
	row = txRow(t, h, tid)
	if row.EvidenceObjectKey == nil || *row.EvidenceObjectKey != base+"-receipt.json" || row.EvidenceSha256 == nil || *row.EvidenceSha256 != sha(receipt) {
		t.Errorf("evidence = %v %v, want %s with the receipt's hash", row.EvidenceObjectKey, row.EvidenceSha256, base+"-receipt.json")
	}
	if string(h.objects.object(base+"-receipt.json")) != string(receipt) || string(h.objects.object(base+"-delivered.xml")) != string(delivered) {
		t.Errorf("the stored evidence is not the provider's receipt and delivered copy")
	}
	if processOne(t, w) {
		t.Errorf("a delivered row with its evidence is due again")
	}
}

// A delivered row's evidence is retried on the cadence when the fetch fails;
// a claim whose lease changed while it stored the evidence records nothing,
// and the next holder finds the objects there (Exists before Put) and records
// them without writing again.
func TestEhfWorker_StoresTheEvidenceOnce(t *testing.T) {
	h, _ := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	processOne(t, w)
	guid := h.storecove.Submissions()[0].GUID
	good := storecovetest.Document{Body: []byte("<Invoice/>")}
	h.storecove.Evidence(guid, good)
	h.Advance(5 * time.Minute)
	processOne(t, w)

	broken := storecovetest.Document{URL: strings.TrimSuffix(h.storecove.URL(), "/api/v2") + "/documents/gone"}
	h.storecove.Evidence(guid, broken)
	now := h.Now()
	processOne(t, w)
	row := txRow(t, h, tid)
	if row.Status != "delivered" || row.EvidenceObjectKey != nil || row.PollAttempts != 1 || row.LastError == nil {
		t.Fatalf("after a failed fetch: %s evidence %v polls %d error %v; want delivered, none, 1, a reason",
			row.Status, row.EvidenceObjectKey, row.PollAttempts, row.LastError)
	}
	wantNext(t, row, now.Add(15*time.Minute))

	h.storecove.Evidence(guid, good)
	h.Advance(15 * time.Minute)
	moved := false
	restore := invoices.SetEhfAfterCall(func(ctx context.Context, id int64) {
		if id == tid && !moved {
			moved = true
			h.Exec(t, `UPDATE invoices.transmissions SET lease_id = 'another', lease_until = $2 WHERE id = $1`, id, h.Now().Add(time.Minute))
		}
	})
	_, puts, _ := h.objects.stored()
	processOne(t, w)
	restore()
	if row := txRow(t, h, tid); row.EvidenceObjectKey != nil {
		t.Fatalf("a claim whose lease changed recorded the evidence %v", *row.EvidenceObjectKey)
	}
	_, afterFirst, _ := h.objects.stored()
	if afterFirst-puts != 2 {
		t.Fatalf("the first claim stored %d objects, want the receipt and the delivered copy", afterFirst-puts)
	}

	// The provider's evidence differs on every fetch — its document URLs are
	// presigned and expire — so the next claim's receipt is not the stored
	// one, and the hash recorded must be the stored receipt's.
	signed := storecovetest.Document{URL: strings.TrimSuffix(h.storecove.URL(), "/api/v2") + "/documents/" + guid + "/0?sig=2"}
	refetched := h.storecove.Evidence(guid, signed)
	h.Advance(61 * time.Second)
	processOne(t, w)
	_, afterSecond, _ := h.objects.stored()
	if afterSecond != afterFirst {
		t.Errorf("the second claim wrote %d objects again, want none: they exist", afterSecond-afterFirst)
	}
	row = txRow(t, h, tid)
	if row.EvidenceObjectKey == nil || row.LastError != nil {
		t.Fatalf("after the second claim: evidence %v error %v, want recorded and cleared", row.EvidenceObjectKey, row.LastError)
	}
	stored := h.objects.object(*row.EvidenceObjectKey)
	if string(stored) == string(refetched) {
		t.Fatalf("the re-planted evidence is the stored one; the test cannot tell them apart")
	}
	if row.EvidenceSha256 == nil || *row.EvidenceSha256 != sha(stored) {
		t.Errorf("evidence_sha256 = %v, want the stored receipt's %s (not this fetch's %s)", deref(row.EvidenceSha256), sha(stored), sha(refetched))
	}
}

// A probe's completion names the status its claim saw: an event that failed
// the row while the probe's call ran leaves the probe's delivery a 0-row
// no-op, never the trigger's refusal of a change to a final row.
func TestEhfWorker_AProbeAfterAnEventIsANoOp(t *testing.T) {
	h, _ := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	processOne(t, w)
	key := txRow(t, h, tid).IdempotencyKey
	h.storecove.Evidence(h.storecove.Submissions()[0].GUID, storecovetest.Document{Body: []byte("<Invoice/>")})
	h.Advance(5 * time.Minute)
	defer invoices.SetEhfAfterCall(func(ctx context.Context, id int64) {
		if id != tid {
			return
		}
		if n, err := store.New(h.Pool()).ApplyEventFailed(ctx, store.ApplyEventFailedParams{
			Now: h.Now(), LastError: ptr("refused by the receiver"), IdempotencyKey: key,
		}); err != nil || n != 1 {
			t.Errorf("the event under the probe = %d rows, %v; want 1", n, err)
		}
	})()
	if _, err := w.ProcessOne(context.Background()); err != nil {
		t.Fatalf("the probe after the event = %v, want a 0-row no-op", err)
	}
	if row := txRow(t, h, tid); row.Status != "failed" || row.DeliveredAt != nil {
		t.Errorf("row = %s delivered_at %v, want failed as the event left it", row.Status, row.DeliveredAt)
	}
}

// A claim whose lookup and store read left less of its lease than the call
// may take does not submit: the row is due again at once, its marker
// untouched and nothing counted.
func TestEhfWorker_DoesNotSubmitOnAnExpiringLease(t *testing.T) {
	t.Parallel()
	h, fake := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	h.Advance(25 * time.Hour)
	fake.whenCalled(func(string) { h.Advance(31 * time.Second) })
	processOne(t, w)
	row := txRow(t, h, tid)
	if row.Status != "queued" || row.SubmitAttemptedAt != nil || row.SubmitAttempts != 0 || row.LeaseID != nil || len(h.storecove.Submissions()) != 0 {
		t.Fatalf("after a slow lookup: %s marker %v attempts %d lease %v, %d submissions; want queued, unmarked, uncounted, released, none",
			row.Status, row.SubmitAttemptedAt, row.SubmitAttempts, row.LeaseID, len(h.storecove.Submissions()))
	}
	wantNext(t, row, h.Now())
	fake.whenCalled(nil)
	processOne(t, w)
	if row := txRow(t, h, tid); row.Status != "submitted" {
		t.Errorf("the next claim = %s, want submitted", row.Status)
	}
}

// Seven days after submitted_at without an outcome the row is handed to a
// person. With a reference it is still probed once a day, and the provider's
// evidence resolves it to delivered by the machine — a note, no user; thirty
// days on it is never probed again. Without a reference it is never probed:
// it waits an hour at a time, and at seven days it waits for a person at once.
func TestEhfWorker_HandsAnOpenOutcomeToAPerson(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	_, resolved := queuedEhf(t, h)
	_, abandoned := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	processOne(t, w)
	processOne(t, w)

	h.Advance(7 * 24 * time.Hour)
	now := h.Now()
	processOne(t, w)
	processOne(t, w)
	for _, id := range []int64{resolved, abandoned} {
		row := txRow(t, h, id)
		if row.Status != "unconfirmed" || row.LastError == nil {
			t.Fatalf("after seven days: %s (%v), want unconfirmed with a reason", row.Status, row.LastError)
		}
		wantNext(t, row, now.Add(24*time.Hour))
	}

	h.Advance(24 * time.Hour)
	requests := h.storecove.Requests()
	processOne(t, w)
	processOne(t, w)
	if got := h.storecove.Requests() - requests; got != 2 {
		t.Errorf("the daily probes made %d calls, want 2", got)
	}
	if row := txRow(t, h, resolved); row.Status != "unconfirmed" {
		t.Fatalf("a probe without evidence moved the row to %s", row.Status)
	}

	h.storecove.Evidence(*txRow(t, h, resolved).ProviderRef, storecovetest.Document{Body: []byte("<Invoice/>")})
	h.Advance(24 * time.Hour)
	processOne(t, w)
	row := txRow(t, h, resolved)
	if row.Status != "delivered" || row.ResolvedByUserID != nil || row.ResolutionNote == nil || *row.ResolutionNote != "Resolved by the provider's evidence." {
		t.Errorf("resolved = %s by %v note %v, want delivered by the machine with its note", row.Status, row.ResolvedByUserID, row.ResolutionNote)
	}
	processOne(t, w) // its evidence
	processOne(t, w) // abandoned's daily probe

	h.Advance(30 * 24 * time.Hour)
	requests = h.storecove.Requests()
	processOne(t, w)
	if h.storecove.Requests() != requests {
		t.Errorf("an unconfirmed row thirty days on was probed")
	}
	wantParked(t, txRow(t, h, abandoned))

	// Without a reference.
	id := issuedAcme(t, h).ID
	silent := plantTransmissionOn(t, h, id, "submitted")
	start := h.Now()
	requests = h.storecove.Requests()
	processOne(t, w)
	if row := txRow(t, h, silent); row.Status != "submitted" {
		t.Fatalf("without a reference: %s, want still submitted", row.Status)
	} else {
		wantNext(t, row, start.Add(time.Hour))
	}
	h.Advance(7 * 24 * time.Hour)
	processOne(t, w)
	row = txRow(t, h, silent)
	if row.Status != "unconfirmed" {
		t.Errorf("without a reference after seven days: %s, want unconfirmed", row.Status)
	}
	wantParked(t, row)
	if h.storecove.Requests() != requests {
		t.Errorf("a row without a reference was probed")
	}
}

// The adapter is built per claim from the credentials row. A key the box
// cannot open, settings that do not decode, or no credentials at all leave
// the row queued an hour out, count nothing, call nobody, flag the key and
// log at error.
func TestEhfWorker_AFailedAccessPointLeavesTheRow(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t) // a key the box cannot open
	_, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	now := h.Now()
	processOne(t, w)
	row := txRow(t, h, tid)
	if row.Status != "queued" || row.SubmitAttempts != 0 || row.SubmitAttemptedAt != nil {
		t.Errorf("row = %s attempts %d marker %v, want queued, none, none", row.Status, row.SubmitAttempts, row.SubmitAttemptedAt)
	}
	wantNext(t, row, now.Add(time.Hour))
	if rejectedAt(t, h) == nil {
		t.Errorf("rejected_at is not set")
	}
	if !strings.Contains(h.Logs(), `"level":"ERROR","msg":"invoices: the access point cannot be opened`) {
		t.Errorf("no error log for the access point")
	}

	// Settings that do not decode: the worker flags it itself.
	key := storecovetest.APIKey
	putAccessPoint(t, h, accessPointBody(&key))
	h.Exec(t, `UPDATE invoices.access_point_credentials SET settings_json = '{'`)
	h.Advance(time.Hour)
	now = h.Now()
	processOne(t, w)
	wantNext(t, txRow(t, h, tid), now.Add(time.Hour))
	if rejectedAt(t, h) == nil {
		t.Errorf("rejected_at is not set for settings that do not decode")
	}

	h.Exec(t, `DELETE FROM invoices.access_point_credentials`)
	h.Advance(time.Hour)
	now = h.Now()
	processOne(t, w)
	wantNext(t, txRow(t, h, tid), now.Add(time.Hour))
	if h.storecove.Requests() != 0 {
		t.Errorf("the provider was called")
	}
}

// Worker mode's Deps carry no directory, no access layer and no catalog: the
// workers need none.
func TestEhfWorker_RunsOnWorkerModeDeps(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	_, tid := queuedEhf(t, h)
	d := h.Deps()
	d.Directory, d.Access, d.Catalog, d.Doc, d.Limiter = nil, nil, nil, nil, nil
	if err := invoices.NewEhfWorker(d).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if row := txRow(t, h, tid); row.Status != "submitted" {
		t.Errorf("row = %s, want submitted", row.Status)
	}
	if _, err := invoices.NewEhfEventsWorker(d).RunCycle(context.Background()); err != nil {
		t.Errorf("events RunCycle: %v", err)
	}
}

// The two workers are handed to the runner exactly when INVOICES_EHF_ENABLED
// is on, PEPPOL_LOOKUP_ENABLED or not; the reminder worker always is.
func TestEhfWorker_RegisteredOnlyWhenEnabled(t *testing.T) {
	t.Parallel()
	names := func(h *harness) []string {
		var out []string
		for _, w := range module.Workers(h.Deps(), invoices.Module()) {
			out = append(out, w.Name()+"@"+w.Interval().String())
		}
		slices.Sort(out)
		return out
	}
	want := []string{"invoices-ehf-events@30s", "invoices-ehf@5s", "invoices-reminders@5s"}
	if got := names(newHarness(t)); !slices.Equal(got, want) {
		t.Errorf("enabled: %v, want %v", got, want)
	}
	if got := names(newHarness(t, modtest.WithEnv("PEPPOL_LOOKUP_ENABLED", "0"))); !slices.Equal(got, want) {
		t.Errorf("lookup disabled: %v, want %v", got, want)
	}
	if got := names(newHarness(t, modtest.WithEnv("INVOICES_EHF_ENABLED", "0"))); !slices.Equal(got, []string{"invoices-reminders@5s"}) {
		t.Errorf("switched off: %v, want the reminder worker alone", got)
	}
}

// An installation whose Peppol lookup is disabled still probes, resolves and
// stores evidence, but leaves a queued row alone — unleased, untouched — and
// says so when the number it leaves changes, or an hour on; the 48-hour age
// cap, which makes no call, still reaches the row.
func TestEhfWorker_LookupDisabledLeavesQueuedRowsAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("PEPPOL_LOOKUP_ENABLED", "0"))
	saveSeller(t, h, completeSeller(1))
	key := storecovetest.APIKey
	putAccessPoint(t, h, accessPointBody(&key))
	queued := insertTransmission(t, h, issuedAcme(t, h).ID)
	probed := plantTransmissionOn(t, h, issuedAcme(t, h).ID, "submitted")
	h.Exec(t, `UPDATE invoices.transmissions SET provider_ref = 'guid-probed' WHERE id = $1`, probed)
	h.storecove.Evidence("guid-probed", storecovetest.Document{Body: []byte("<Invoice/>")})
	before := txRow(t, h, queued)

	if err := invoices.NewEhfWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if row := txRow(t, h, probed); row.Status != "delivered" || row.EvidenceObjectKey == nil {
		t.Errorf("probed = %s evidence %v, want delivered with its evidence", row.Status, row.EvidenceObjectKey)
	}
	after := txRow(t, h, queued)
	if after.Status != "queued" || after.LeaseID != nil || after.SubmitAttemptedAt != nil || after.NextAttemptAt != before.NextAttemptAt {
		t.Errorf("queued = %s lease %v marker %v next %v, want untouched", after.Status, after.LeaseID, after.SubmitAttemptedAt, after.NextAttemptAt)
	}
	if n := strings.Count(h.Logs(), "left alone: the Peppol lookup is disabled"); n != 1 {
		t.Errorf("the queued rows were mentioned %d times, want once", n)
	}
	if len(h.storecove.Submissions()) != 0 {
		t.Errorf("a submission was made")
	}

	// Said again when the number changes or an hour on, not every cycle.
	w := invoices.NewEhfWorker(h.Deps())
	said := func() int { return strings.Count(h.Logs(), "left alone: the Peppol lookup is disabled") }
	runCycle := func() {
		t.Helper()
		if err := w.RunCycle(context.Background()); err != nil {
			t.Fatalf("RunCycle: %v", err)
		}
	}
	runCycle()
	h.Advance(5 * time.Second)
	runCycle()
	if n := said(); n != 2 {
		t.Errorf("after two cycles of a second worker: said %d times, want 2 (once by each worker)", n)
	}
	insertTransmission(t, h, issuedAcme(t, h).ID)
	runCycle()
	if n := said(); n != 3 {
		t.Errorf("after the number changed: said %d times, want 3", n)
	}
	h.Advance(time.Hour)
	runCycle()
	if n := said(); n != 4 {
		t.Errorf("an hour on: said %d times, want 4", n)
	}

	// The age cap still reaches a queued row: it makes no call. Its marker set,
	// it waits for a person rather than blocking the document until the
	// lookup returns.
	h.Exec(t, `UPDATE invoices.transmissions SET submit_attempted_at = queued_at WHERE id = $1`, queued)
	h.Advance(48 * time.Hour)
	runCycle()
	if row := txRow(t, h, queued); row.Status != "unconfirmed" {
		t.Errorf("a queued row 48 hours old with the lookup disabled = %s, want unconfirmed", row.Status)
	}
	if len(h.storecove.Submissions()) != 0 {
		t.Errorf("a submission was made")
	}
}

// itoa is n in decimal.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// deref is a nullable string's value, "<nil>" for none.
func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
