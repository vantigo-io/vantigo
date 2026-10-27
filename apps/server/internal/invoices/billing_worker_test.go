package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The invoices-billing worker (energy consumption billing design D10;
// billing runs plan readings 4–7), driven by ProcessOne on the fixed clock
// (12 September 2026): an item settles its customer through the run's date,
// drafts through the wizard's own core — split by metering point when the
// lines would not fit on one document — and, when the run issues, issues
// through the issue's own core, all under the run's creator; a wizard refusal
// skips it naming the point, an issue refusal fails it, an error puts it back
// twice and fails it the third time; the run is done once no item is queued.
// Every harness here also fails its test when a call out of the module is
// made under a lock (newInvoicesHarness), and noLockedReads checks Energy's.

// billingFixture is an installation billing Energy's settlements, a complete
// seller, every kind's holder, and a caller who makes runs and issues.
type billingFixture struct {
	h       *harness
	energy  *fakeConsumption
	holders *fakeHolders
	maker   *modtest.Client
	makerID uuid.UUID
}

func newBillingFixture(t *testing.T, withStore bool) billingFixture {
	t.Helper()
	fx := billingFixture{energy: newFakeConsumption(), holders: newFakeHolders()}
	opts := append(fx.holders.options(), fx.energy.options()...)
	if withStore {
		fx.h = newHarness(t, opts...)
	} else {
		fx.h = newHarnessWithoutStore(t, opts...)
	}
	saveSeller(t, fx.h, completeSeller(1))
	fx.maker, fx.makerID = runMaker(t, fx.h)
	return fx
}

// run makes a run for customers, issuing or not.
func (fx billingFixture) run(t *testing.T, issue bool, customers ...int32) billingRunJSON {
	t.Helper()
	return startRun(t, fx.maker, map[string]any{"issue": issue, "customerIds": customers})
}

// billingWorker is the invoices-billing worker as worker mode builds it
// (module.Workers), its single-provider slots — Users among them, whom the
// issue reads the issuer's name from — resolved.
func billingWorker(t *testing.T, h *harness) *invoices.BillingWorker {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if b, ok := w.(*invoices.BillingWorker); ok {
			return b
		}
	}
	t.Fatal("no invoices-billing worker is registered")
	return nil
}

// billOne claims and carries one item, answering whether one was due.
func billOne(t *testing.T, h *harness) bool {
	t.Helper()
	ok, err := billingWorker(t, h).ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	return ok
}

// carryAll carries every due item, answering how many.
func carryAll(t *testing.T, h *harness) int {
	t.Helper()
	n := 0
	for billOne(t, h) {
		if n++; n > 50 {
			t.Fatal("the worker never ran out of items")
		}
	}
	return n
}

// onlyItem is the run's one item as the detail reads it.
func onlyItem(t *testing.T, h *harness, runID int64) billingRunItemJSON {
	t.Helper()
	d := runDetail(t, h, runID, "")
	if len(d.Items) != 1 {
		t.Fatalf("items = %v, want one", itemStatusesOf(d.Items))
	}
	return d.Items[0]
}

// itemState is an item's row as "status aN", read by SQL.
func itemState(t *testing.T, h *harness, runID int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT format('%s a%s', status, attempts) FROM invoices.billing_run_items WHERE run_id = $1`, runID)
}

// manyLines is st with n settlement lines of 1.00 each, its kWh and amount
// their sums.
func manyLines(st contracts.Settlement, n int) contracts.Settlement {
	st.Lines = nil
	for range n {
		st.Lines = append(st.Lines, contracts.SettlementLine{Kind: "spot_energy", Text: "Spot NO4", Quantity: "1.000",
			Unit: contracts.SettlementUnitKWh, UnitPrice: "1.000000", Amount: "1.00", VATHint: contracts.VATHintStandard})
	}
	st.KWh, st.Amount = fmt.Sprintf("%d.000", n), fmt.Sprintf("%d.00", n)
	return st
}

// An item per customer, carried one at a time: Acme's and Kari's
// settlements drafted, one document each, by the run's creator — whose
// principal every call the worker makes carries — and a customer named with
// nothing due skipped no_consumption_due. The run is running after its first
// item and done after its last.
func TestBillingWorker_DraftsAndTheRunIsDone(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	h := fx.h
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	fx.energy.put(august(9102, customerPerson, 3, gsrnBodo, contracts.VATHintStandard))
	run := fx.run(t, false, customerAcme, customerPerson, customerNoTerms)

	if !billOne(t, h) {
		t.Fatal("no item was claimed")
	}
	d := runDetail(t, h, run.ID, "")
	if d.Run.Status != "running" || d.Run.StartedAt == nil || d.Run.FinishedAt != nil {
		t.Errorf("after one item the run = %s started %v finished %v, want running", d.Run.Status, d.Run.StartedAt, d.Run.FinishedAt)
	}
	if got := itemStatusesOf(d.Items); !slices.Equal(got, []string{"2001:drafted", "2002:queued", "2004:queued"}) {
		t.Errorf("after one item = %v", got)
	}
	if n := carryAll(t, h); n != 2 {
		t.Errorf("carried %d more, want 2", n)
	}
	d = runDetail(t, h, run.ID, "")
	if got := itemStatusesOf(d.Items); !slices.Equal(got, []string{"2001:drafted", "2002:drafted", "2004:skipped"}) {
		t.Fatalf("items = %v", got)
	}
	if d.Run.Status != "done" || d.Run.FinishedAt == nil || d.Run.Counts["drafted"] != 2 || d.Run.Counts["skipped"] != 1 {
		t.Errorf("run = %+v, want done with 2 drafted and 1 skipped", d.Run)
	}
	if code := deref(d.Items[2].Code); code != "no_consumption_due" || d.Items[2].Detail == nil {
		t.Errorf("the customer with nothing due = %s %v, want no_consumption_due with a detail", code, d.Items[2].Detail)
	}
	for _, item := range d.Items[:2] {
		if len(item.Invoices) != 1 || item.Invoices[0].Status != "draft" || item.Code != nil || item.FinishedAt == nil {
			t.Fatalf("item = %+v, want one draft and no code", item)
		}
		id := item.Invoices[0].InvoiceID
		if by := modtest.One[uuid.UUID](t, h.Harness, `SELECT created_by_user_id FROM invoices.invoices WHERE id = $1`, id); by != fx.makerID {
			t.Errorf("draft %d made by %s, want the run's creator %s", id, by, fx.makerID)
		}
		if n := h.Count(t, `SELECT count(*) FROM invoices.lines WHERE invoice_id = $1`, id); n != 3 {
			t.Errorf("draft %d has %d lines, want the settlement's 3", id, n)
		}
	}
	var settles int
	for _, c := range contractCalls.by(fx.makerID) {
		if c.method == "Consumption.Settle" {
			settles++
		}
	}
	if settles != 3 {
		t.Errorf("Settle was called %d times under the creator, want once per item", settles)
	}
	if billOne(t, h) {
		t.Error("an item was claimed after the run was done")
	}
	noLockedReads(t, fx.energy)
}

// A run that issues issues each draft as its creator (readings on the record
// 10): the document's issuer and the holder's IssuedBy are the creator's.
func TestBillingWorker_IssuesAsTheCreator(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	h := fx.h
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	run := fx.run(t, true, customerAcme)
	carryAll(t, h)

	item := onlyItem(t, h, run.ID)
	if item.Status != "issued" || len(item.Invoices) != 1 || item.Invoices[0].Status != "issued" || item.Invoices[0].Number == nil {
		t.Fatalf("item = %+v, want issued with one numbered invoice", item)
	}
	id := item.Invoices[0].InvoiceID
	if by := modtest.One[uuid.UUID](t, h.Harness, `SELECT issued_by_user_id FROM invoices.invoices WHERE id = $1`, id); by != fx.makerID {
		t.Errorf("issued by %s, want the run's creator %s", by, fx.makerID)
	}
	calls := fx.holders.callsOf(kindSettlement)
	if len(calls) != 1 || calls[0].op != "mark" || calls[0].ref.IssuedBy != fx.makerID || calls[0].ref.ID != id {
		t.Errorf("energy's holder calls = %+v, want one mark of %d by %s", calls, id, fx.makerID)
	}
	if run := runDetail(t, h, run.ID, "").Run; run.Status != "done" || run.Counts["issued"] != 1 {
		t.Errorf("run = %+v, want done with 1 issued", run)
	}
	noLockedReads(t, fx.energy)
}

// A wizard refusal skips the item with its code, the detail naming the
// metering point of the settlement it is about; nothing is drafted.
func TestBillingWorker_SkipsABlockedSettlementNamingThePoint(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	h := fx.h
	blocked := august(9102, customerAcme, 2, gsrnBodo, contracts.VATHintStandard)
	blocked.Blocked, blocked.BlockedDetail, blocked.Lines = contracts.SettlementPricePreliminary, "prices for 2026-08-14 are preliminary", nil
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	fx.energy.put(blocked)
	run := fx.run(t, true, customerAcme)
	carryAll(t, h)

	item := onlyItem(t, h, run.ID)
	if item.Status != "skipped" || deref(item.Code) != "settlement_blocked" || len(item.Invoices) != 0 {
		t.Fatalf("item = %+v, want skipped settlement_blocked with no document", item)
	}
	if want := "Metering point " + gsrnBodo + ": "; !strings.HasPrefix(deref(item.Detail), want) || !strings.Contains(deref(item.Detail), "9102") {
		t.Errorf("detail = %q, want it to start %q and name the settlement", deref(item.Detail), want)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices`); n != 0 {
		t.Errorf("%d documents, want none", n)
	}
	if billOne(t, h) {
		t.Error("a refused item was claimed again")
	}
}

// An issue refusal fails the item with its code — here the installation
// keeps no object store, storage_unavailable — and the draft stays, listed.
func TestBillingWorker_FailsOnAnIssueRefusal(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, false)
	h := fx.h
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	run := fx.run(t, true, customerAcme)
	carryAll(t, h)

	item := onlyItem(t, h, run.ID)
	if item.Status != "failed" || deref(item.Code) != "storage_unavailable" || item.Detail == nil || item.Attempts != 0 {
		t.Fatalf("item = %+v, want failed storage_unavailable, no attempt counted", item)
	}
	if len(item.Invoices) != 1 || item.Invoices[0].Status != "draft" {
		t.Errorf("documents = %+v, want the draft listed", item.Invoices)
	}
	if run := runDetail(t, h, run.ID, "").Run; run.Status != "done" || run.Counts["failed"] != 1 {
		t.Errorf("run = %+v, want done with 1 failed", run)
	}
}

// Settlements whose lines do not fit on one document are split by metering
// point (plan reading 7.3): two points of 300 lines each make two documents,
// each point's settlements together on one.
func TestBillingWorker_SplitsByMeteringPoint(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	h := fx.h
	id := int64(9200)
	for _, point := range []struct {
		gsrn   string
		supply int32
	}{{gsrnBodo, 2}, {gsrnTromso, 1}} {
		for _, m := range []time.Month{time.June, time.July, time.August} {
			id++
			fx.energy.put(manyLines(monthSettlement(id, customerAcme, point.supply, point.gsrn, 2026, m, contracts.VATHintStandard), 100))
		}
	}
	run := fx.run(t, false, customerAcme)
	carryAll(t, h)

	item := onlyItem(t, h, run.ID)
	if item.Status != "drafted" || len(item.Invoices) != 2 {
		t.Fatalf("item = %+v, want drafted with two documents", item)
	}
	for i, gsrn := range []string{gsrnTromso, gsrnBodo} {
		doc := item.Invoices[i].InvoiceID
		if n := h.Count(t, `SELECT count(*) FROM invoices.lines WHERE invoice_id = $1`, doc); n != 300 {
			t.Errorf("document %d has %d lines, want 300", doc, n)
		}
		if got := modtest.One[[]string](t, h.Harness, `SELECT array_agg(DISTINCT point_gsrn) FROM invoices.line_sources WHERE invoice_id = $1`, doc); !slices.Equal(got, []string{gsrn}) {
			t.Errorf("document %d holds points %v, want %s alone", doc, got, gsrn)
		}
	}
	noLockedReads(t, fx.energy)
}

// An error that is not a refusal counts an attempt and puts the item back,
// due after a minute, then two; the third fails it billing_error with a
// fixed detail, the error itself only in the log. The run is then done.
func TestBillingWorker_RetriesAndTheThirdErrorFails(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	h := fx.h
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	fx.energy.failSettle(errors.New("energy is down: secret-detail"))
	run := fx.run(t, false, customerAcme)
	next := func() time.Time {
		return modtest.One[time.Time](t, h.Harness, `SELECT next_attempt_at FROM invoices.billing_run_items WHERE run_id = $1`, run.ID)
	}

	billOne(t, h)
	if got := itemState(t, h, run.ID); got != "queued a1" || !next().Equal(h.Now().Add(time.Minute)) {
		t.Errorf("after one error = %s due %v, want queued a1 due a minute on", got, next())
	}
	if billOne(t, h) {
		t.Error("an item was claimed before it was due")
	}
	h.Advance(time.Minute)
	billOne(t, h)
	if got := itemState(t, h, run.ID); got != "queued a2" || !next().Equal(h.Now().Add(2*time.Minute)) {
		t.Errorf("after two errors = %s due %v, want queued a2 due two minutes on", got, next())
	}
	h.Advance(2 * time.Minute)
	billOne(t, h)
	item := onlyItem(t, h, run.ID)
	if item.Status != "failed" || deref(item.Code) != "billing_error" || item.Attempts != 3 || strings.Contains(deref(item.Detail), "secret") {
		t.Errorf("after three errors = %+v, want failed billing_error after 3 attempts, the error not in the detail", item)
	}
	if !strings.Contains(h.Logs(), "secret-detail") {
		t.Error("the error was not logged")
	}
	if run := runDetail(t, h, run.ID, "").Run; run.Status != "done" {
		t.Errorf("run = %s, want done", run.Status)
	}
}

// An attempt after an error carries the item as the first would have.
func TestBillingWorker_ARetryAfterAnErrorDrafts(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	h := fx.h
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	fx.energy.failSettle(errors.New("energy is down"))
	run := fx.run(t, false, customerAcme)
	billOne(t, h)
	fx.energy.failSettle(nil)
	h.Advance(time.Minute)
	billOne(t, h)
	if item := onlyItem(t, h, run.ID); item.Status != "drafted" || item.Attempts != 1 || len(item.Invoices) != 1 {
		t.Errorf("item = %+v, want drafted on its second attempt", item)
	}
}

// An item under another claim's live lease is not claimed until the lease
// runs out.
func TestBillingWorker_LeaveALiveLeaseAlone(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	h := fx.h
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	run := fx.run(t, false, customerAcme)
	h.Exec(t, `UPDATE invoices.billing_run_items SET lease_id = 'another', lease_until = $2 WHERE run_id = $1`, run.ID, h.Now().Add(5*time.Minute))
	if billOne(t, h) {
		t.Fatal("an item under a live lease was claimed")
	}
	h.Advance(5 * time.Minute)
	if !billOne(t, h) {
		t.Fatal("an item whose lease ran out was not claimed")
	}
	if got := itemState(t, h, run.ID); got != "drafted a0" {
		t.Errorf("item = %s, want drafted", got)
	}
}

// A run cancelled while one of its items is being carried: the item records
// its outcome, and the run stays cancelled — but an item whose attempt ends
// on an error is not put back under the cancelled run, where no claim would
// ever take it again: it ends cancelled.
func TestBillingWorker_CancelledWhileCarried(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		fail  bool
		state string
	}{
		{"the item finishes", false, "drafted a0"},
		{"an error ends it cancelled", true, "cancelled a1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx := newBillingFixture(t, true)
			h := fx.h
			fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
			run := fx.run(t, false, customerAcme)
			if c.fail {
				fx.energy.failSettle(errors.New("energy is down"))
			}
			fx.energy.duringSettle(func() {
				if res := fx.maker.Do(http.MethodPost, billingRunPath(run.ID)+"/cancel", nil); res.Status != http.StatusOK {
					t.Errorf("cancel = %d %s", res.Status, res.Body)
				}
			})
			billOne(t, h)
			if got := itemState(t, h, run.ID); got != c.state {
				t.Errorf("item = %s, want %s", got, c.state)
			}
			if n := h.Count(t, `SELECT count(*) FROM invoices.billing_run_items WHERE run_id = $1 AND finished_at IS NOT NULL AND lease_id IS NULL`, run.ID); n != 1 {
				t.Error("the item is not finished with its lease cleared")
			}
			if d := runDetail(t, h, run.ID, ""); d.Run.Status != "cancelled" || d.Run.Counts["queued"] != 0 {
				t.Errorf("run = %+v, want cancelled with nothing queued", d.Run)
			}
		})
	}
}
