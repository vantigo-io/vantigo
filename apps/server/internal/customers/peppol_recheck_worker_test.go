package customers_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// This file is the Peppol re-check worker (registry workers design D5, D6),
// driven through RunCycle against the DB harness with a fake
// Deps.PeppolLookup. No test here resolves a name or opens a socket.

// peppolCheckedAt is the stored lookup's checked_at, or nil when the customer
// has never been checked. The aggregate is what makes "never checked" an answer
// rather than a fixture error: modtest.One fatals on zero rows, and "there is no
// row yet" is precisely the state half of these tests start from.
func peppolCheckedAt(t *testing.T, h *modtest.Harness, customerID int32) *time.Time {
	t.Helper()
	return modtest.One[*time.Time](t, h,
		`SELECT max(checked_at) FROM customers.customer_peppol_lookups WHERE customer_id = $1`, customerID)
}

// peppolLookupRowCount is how many stored answers a customer has: one or none,
// so it is the helper for a test about a row being dropped rather than
// re-stamped, where peppolCheckedAt's own nil would also be the answer for a
// customer that was never checked at all.
func peppolLookupRowCount(t *testing.T, h *modtest.Harness, customerID int32) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM customers.customer_peppol_lookups WHERE customer_id = $1`, customerID)
}

// agePeppolLookup backdates a stored lookup, which is how a test makes a row
// "old" without advancing the harness clock past everything else in the
// installation.
func agePeppolLookup(t *testing.T, h *modtest.Harness, customerID int32, at time.Time) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customer_peppol_lookups SET checked_at = $2 WHERE customer_id = $1`, customerID, at)
}

// TestPeppolRecheckWorker_AsksAgainForAnAgedAnswerAndNotForAFreshOne pins
// design D6's first candidate set and its bound: an answer older than
// CUSTOMERS_PEPPOL_RECHECK_AGE is asked again, and one younger than it is left
// alone. The second half is the one that matters — a worker that re-asked
// everything every cycle would be a daily network call per customer for an
// answer that had not changed.
func TestPeppolRecheckWorker_AsksAgainForAnAgedAnswerAndNotForAFreshOne(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)

	aged := createNorwegianBusiness(t, c, "Aged AS", "923609016")
	fresh := createNorwegianBusiness(t, c, "Fresh AS", "974760673")
	if r := postPeppolLookup(t, c, aged.Id); r.Status != 200 {
		t.Fatalf("seed the aged lookup: %d %s", r.Status, r.Body)
	}
	if r := postPeppolLookup(t, c, fresh.Id); r.Status != 200 {
		t.Fatalf("seed the fresh lookup: %d %s", r.Status, r.Body)
	}
	agePeppolLookup(t, h, aged.Id, h.Now().Add(-800*time.Hour))
	before := len(calls.all())

	if ran, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
	asked := calls.all()[before:]
	if len(asked) != 1 || asked[0] != "0192:923609016" {
		t.Errorf("participants asked = %v, want exactly [0192:923609016]", asked)
	}
	if at := peppolCheckedAt(t, h, aged.Id); at == nil || !at.Equal(h.Now()) {
		t.Errorf("the aged lookup's checked_at = %v, want the cycle's clock %v", at, h.Now())
	}
}

// TestPeppolRecheckWorker_DeletesALookupWhoseParticipantChanged pins design
// D6's participant rule and what becomes of the row it refuses to refresh. A
// lookup made for a participant the customer no longer resolves to is stale BY
// IDENTITY: the billing profile already drops it from every response and every
// warning (resolvedPeppolLookup), so refreshing it would spend a network call to
// update a row nothing reads, and re-stamping its checked_at would make it look
// current while still naming the wrong participant.
//
// Leaving it in place is no better, which is why it goes: its checked_at never
// moves, so the row is in the aged set again next cycle and every cycle after
// that, holding one of the batch's hundred slots for the rest of the
// installation's life. Deleting it also makes the next lookup for this customer
// a first one, which is exactly what it is for the participant it resolves to
// now.
func TestPeppolRecheckWorker_DeletesALookupWhoseParticipantChanged(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: true}, nil)))
	c := authenticatedClient(t, h)

	created := createNorwegianBusiness(t, c, "Moved AS", "923609016")
	if r := postPeppolLookup(t, c, created.Id); r.Status != 200 {
		t.Fatalf("seed the lookup: %d %s", r.Status, r.Body)
	}
	agePeppolLookup(t, h, created.Id, h.Now().Add(-800*time.Hour))
	// The stored row now names a participant this customer does not resolve to.
	h.Exec(t, `UPDATE customers.customer_peppol_lookups SET participant_id = '0192:974760673' WHERE customer_id = $1`, created.Id)
	before := len(calls.all())

	if _, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if asked := calls.all()[before:]; len(asked) != 0 {
		t.Errorf("participants asked = %v, want none: the stored lookup is already stale by identity", asked)
	}
	if n := peppolLookupRowCount(t, h, created.Id); n != 0 {
		t.Errorf("stored lookups = %d, want 0: a row for a participant that moved on is dropped, not kept aged forever", n)
	}
}

// TestPeppolRecheckWorker_TheEhfShareLeavesRoomForTheAgedSet pins the split
// between the two candidate sets. The EHF set comes first because it matters
// most, but it may not take the whole batch: sixty customers set to EHF with no
// stored answer are sixty good reasons to ask the network, and an aged answer
// that never fits behind them is one nobody re-checks for as long as the queue
// lasts. Half the batch is the EHF set's share, and the other half is the aged
// set's however long that queue is.
func TestPeppolRecheckWorker_TheEhfShareLeavesRoomForTheAgedSet(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)

	// One aged answer, and sixty EHF customers ahead of it. The sixty are
	// inserted with an explicit peppolId rather than a legal identity, because
	// that is what lookupParticipant reads first and there are not sixty valid
	// organisation numbers in this package's fixtures.
	aged := createNorwegianBusiness(t, c, "Aged AS", "923609016")
	if r := postPeppolLookup(t, c, aged.Id); r.Status != 200 {
		t.Fatalf("seed the aged lookup: %d %s", r.Status, r.Body)
	}
	agePeppolLookup(t, h, aged.Id, h.Now().Add(-800*time.Hour))
	for i := 0; i < 60; i++ {
		id := insertCustomer(t, h, fmt.Sprintf("Ehf %02d", i), "active")
		h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf', peppol_id = $2 WHERE id = $1`,
			id, fmt.Sprintf("0192:8000000%02d", i))
	}
	before := len(calls.all())

	if ran, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
	asked := calls.all()[before:]
	if len(asked) != 51 {
		t.Errorf("participants asked = %d, want 51: fifty of the sixty ehf customers plus the aged answer", len(asked))
	}
	if at := peppolCheckedAt(t, h, aged.Id); at == nil || !at.Equal(h.Now()) {
		t.Errorf("the aged lookup's checked_at = %v, want the cycle's clock %v: the ehf queue must not starve it", at, h.Now())
	}
}

// TestPeppolRecheckWorker_UnaskableEhfCustomersDoNotStarveTheSet pins the
// pre-filter on the EHF candidate query (final fix wave I1). A customer whose
// invoice_delivery is 'ehf' but which resolves to no participant at all — no
// peppolId, and a legal identity no organisation number can be derived from —
// is nothing this worker can ask the network about: the Go loop skips it
// without writing anything, so it comes back at the head of the batch every
// cycle for the rest of the installation's life. Fifty-one of them ahead of one
// genuine candidate would mean the genuine one is never asked at all, silently.
//
// The query therefore pre-filters on a superset of the Go rule, exactly as
// CustomersWithoutRegistryRecord does; lookupParticipant stays the authority.
func TestPeppolRecheckWorker_UnaskableEhfCustomersDoNotStarveTheSet(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)

	// Fifty-one at the LOW ids — one more than the EHF share, so a query that
	// selected them would fill the batch with nothing askable. A Swedish
	// organisation number is the realistic shape of this: a business identity
	// that is perfectly valid and that derivedPeppolID answers "" for.
	for i := 0; i < 51; i++ {
		id := insertCustomer(t, h, fmt.Sprintf("Unaskable %02d", i), "active")
		h.Exec(t, `UPDATE customers.customers
			SET invoice_delivery = 'ehf', type = 'business',
			    legal_country = 'se', legal_type = 'business', legal_source = 'manual',
			    legal_id = $2, legal_name = name
			WHERE id = $1`, id, fmt.Sprintf("55600000%02d", i))
	}
	genuine := createNorwegianBusiness(t, c, "Askable AS", "923609016")
	h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf' WHERE id = $1`, genuine.Id)
	before := len(calls.all())

	if ran, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
	asked := calls.all()[before:]
	if len(asked) != 1 || asked[0] != "0192:923609016" {
		t.Errorf("participants asked = %v, want exactly [0192:923609016]: the unaskable customers must not hold the batch's places", asked)
	}
	if at := peppolCheckedAt(t, h, genuine.Id); at == nil {
		t.Error("the one askable ehf customer still has no stored lookup after a cycle")
	}
}

// TestPeppolRecheckWorker_LeavesArchivedCustomersOutOfBothSets pins the one
// filter both candidate queries share. An archived customer is invoiced by
// nobody, so asking a public network about it every month is a request made for
// no reason — and on an installation with years of archived customers it is the
// batch spent on them instead of on the live ones.
func TestPeppolRecheckWorker_LeavesArchivedCustomersOutOfBothSets(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)

	// One of each set: an aged answer whose participant still matches, and an
	// ehf customer that has never been checked. Both archived after the fact,
	// which is the order a real installation gets there in.
	agedRow := createNorwegianBusiness(t, c, "Aged Archived AS", "923609016")
	if r := postPeppolLookup(t, c, agedRow.Id); r.Status != 200 {
		t.Fatalf("seed the aged lookup: %d %s", r.Status, r.Body)
	}
	agePeppolLookup(t, h, agedRow.Id, h.Now().Add(-800*time.Hour))
	ehf := createNorwegianBusiness(t, c, "Ehf Archived AS", "974760673")
	h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf' WHERE id = $1`, ehf.Id)
	h.Exec(t, `UPDATE customers.customers SET status = 'archived' WHERE id = ANY($1)`, []int32{agedRow.Id, ehf.Id})
	stored := peppolCheckedAt(t, h, agedRow.Id)
	before := len(calls.all())

	if ran, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
	if asked := calls.all()[before:]; len(asked) != 0 {
		t.Errorf("participants asked = %v, want none: both candidates are archived", asked)
	}
	if at := peppolCheckedAt(t, h, agedRow.Id); at == nil || stored == nil || !at.Equal(*stored) {
		t.Errorf("the archived customer's checked_at = %v, want it untouched at %v", at, stored)
	}
	if n := peppolLookupRowCount(t, h, ehf.Id); n != 0 {
		t.Errorf("stored lookups for the archived ehf customer = %d, want 0: it was never a candidate", n)
	}
}

// TestPeppolRecheckWorker_ChecksAnEhfCustomerThatHasNeverBeenChecked pins
// design D6's second candidate set: the customer whose invoices are already
// going to Peppol is the one whose registration must not be assumed.
func TestPeppolRecheckWorker_ChecksAnEhfCustomerThatHasNeverBeenChecked(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)

	created := createNorwegianBusiness(t, c, "Already Sending AS", "923609016")
	h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf' WHERE id = $1`, created.Id)
	if at := peppolCheckedAt(t, h, created.Id); at != nil {
		t.Fatalf("checked_at = %v before the cycle, want no stored lookup at all", at)
	}

	if _, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if asked := calls.all(); len(asked) != 1 || asked[0] != "0192:923609016" {
		t.Errorf("participants asked = %v, want exactly [0192:923609016]", asked)
	}
	if at := peppolCheckedAt(t, h, created.Id); at == nil {
		t.Error("the ehf customer still has no stored lookup after a cycle")
	}
}

// TestPeppolRecheckWorker_LeavesANonEhfCustomerWithNoLookupAlone is the
// boundary of that second set: "could receive EHF" is a question a person asks
// with a click (design D6's own scope), and a worker that asked it for every
// customer would turn an offer into a crawl of the Peppol network.
func TestPeppolRecheckWorker_LeavesANonEhfCustomerWithNoLookupAlone(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: true}, nil)))
	c := authenticatedClient(t, h)
	createNorwegianBusiness(t, c, "Emailed AS", "923609016")

	if _, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if asked := calls.all(); len(asked) != 0 {
		t.Errorf("participants asked = %v, want none", asked)
	}
}

// TestPeppolRecheckWorker_RecordsAnEventOnlyWhenTheAnswerChanged pins design
// D6's event rule and its actor: a re-check that finds the same answer is
// silent — otherwise every customer would collect a timeline entry a month
// saying nothing happened — and one that finds a different answer is recorded
// with the system actor, not attributed to whoever clicked last.
func TestPeppolRecheckWorker_RecordsAnEventOnlyWhenTheAnswerChanged(t *testing.T) {
	t.Parallel()
	registered := true
	h := newHarness(t, modtest.WithPeppolLookup(func(context.Context, string) (peppol.Result, error) {
		if registered {
			return peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil
		}
		return peppol.Result{Registered: false}, nil
	}))
	c := authenticatedClient(t, h)

	created := createNorwegianBusiness(t, c, "Lapsing AS", "923609016")
	if r := postPeppolLookup(t, c, created.Id); r.Status != 200 {
		t.Fatalf("seed the lookup: %d %s", r.Status, r.Body)
	}
	if n := len(fetchPeppolLookupEvents(t, c, created.Id)); n != 1 {
		t.Fatalf("events after the click = %d, want 1", n)
	}

	w := customers.NewPeppolRecheckWorker(h.Deps())
	agePeppolLookup(t, h, created.Id, h.Now().Add(-800*time.Hour))
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("unchanged RunCycle: %v", err)
	}
	if n := len(fetchPeppolLookupEvents(t, c, created.Id)); n != 1 {
		t.Errorf("events after an unchanged re-check = %d, want still 1", n)
	}

	registered = false
	agePeppolLookup(t, h, created.Id, h.Now().Add(-800*time.Hour))
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("changed RunCycle: %v", err)
	}
	events := fetchPeppolLookupEvents(t, c, created.Id)
	if len(events) != 2 {
		t.Fatalf("events after a changed re-check = %d, want 2", len(events))
	}
	last := events[len(events)-1]
	if last.ActorKind != "system" || str(last.ActorDisplay) != "System" {
		t.Errorf("event actor = %q/%q, want system/System (design D6)", last.ActorKind, str(last.ActorDisplay))
	}
}

// TestPeppolRecheckWorker_AFailureLeavesCheckedAtAlone pins design D6's
// failure rule: a customer whose re-check could not complete stays first in
// line next cycle, which it only does while its checked_at still says how long
// it has been since anyone actually got an answer.
func TestPeppolRecheckWorker_AFailureLeavesCheckedAtAlone(t *testing.T) {
	t.Parallel()
	fail := false
	h := newHarness(t, modtest.WithPeppolLookup(func(context.Context, string) (peppol.Result, error) {
		if fail {
			return peppol.Result{}, errors.New("peppol: simulated network failure")
		}
		return peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil
	}))
	c := authenticatedClient(t, h)

	created := createNorwegianBusiness(t, c, "Unreachable AS", "923609016")
	if r := postPeppolLookup(t, c, created.Id); r.Status != 200 {
		t.Fatalf("seed the lookup: %d %s", r.Status, r.Body)
	}
	aged := h.Now().Add(-800 * time.Hour)
	agePeppolLookup(t, h, created.Id, aged)

	fail = true
	if ran, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil — one unreachable customer is not a failed cycle", ran, err)
	}
	if at := peppolCheckedAt(t, h, created.Id); at == nil || !at.UTC().Equal(aged.UTC()) {
		t.Errorf("checked_at = %v, want it untouched at %v", at, aged)
	}
}

// TestPeppolRecheckWorker_SkipsTheCycleWhenTheLeaseIsHeld is design D5 through
// this worker: two replicas must not both spend the batch's worth of network
// lookups on the same hundred customers.
func TestPeppolRecheckWorker_SkipsTheCycleWhenTheLeaseIsHeld(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: true}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Contended AS", "923609016")
	h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf' WHERE id = $1`, created.Id)

	ctx := context.Background()
	holder, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the lease holder's connection: %v", err)
	}
	defer holder.Release()
	var locked bool
	if err := holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, customers.PeppolRecheckLeaseKeyForTest).Scan(&locked); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	if !locked {
		t.Fatal("the lease was already held; this test's database is its own")
	}

	w := customers.NewPeppolRecheckWorker(h.Deps())
	ran, err := w.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if ran {
		t.Error("RunCycle ran while another holder had the lease")
	}
	if asked := calls.all(); len(asked) != 0 {
		t.Errorf("participants asked = %v while the lease was held, want none", asked)
	}

	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, customers.PeppolRecheckLeaseKeyForTest); err != nil {
		t.Fatalf("release the lease: %v", err)
	}
	ran, err = w.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle after release: %v", err)
	}
	if !ran {
		t.Fatal("RunCycle still reported the lease held after it was released")
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 0 {
		// None: this test released its holder above, and the cycle that then ran
		// released its own in a defer. The lock is session-scoped, so a cycle
		// that forgot would wedge this replica's pooled connection and every
		// later cycle drawing it would skip in silence
		// (TestRegistryFeedWorker_ReleasesTheLeaseAfterEveryCycle asserts the
		// same thing the same way). The database is this test's own — testdb
		// gives each test its own — so nothing else can hold one.
		t.Errorf("advisory locks held = %d, want none left after the cycle", n)
	}
}

// TestPeppolRecheckWorker_RunStopsWithItsContext pins the loop contract the
// runner depends on.
func TestPeppolRecheckWorker_RunStopsWithItsContext(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Ticking AS", "923609016")
	h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf' WHERE id = $1`, created.Id)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- customers.NewPeppolRecheckWorker(h.Deps()).Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for peppolCheckedAt(t, h, created.Id) == nil {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("Run never checked the ehf customer")
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
