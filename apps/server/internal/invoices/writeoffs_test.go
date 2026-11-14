package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
)

// The write-off (accounting design A16 as amended by R4): an issued
// invoice's whole open amount given up with a reason, the undo, money that
// arrives after it as a recovered payment, the facts a ledger reads, and every
// place the module judges what is open — none of which sees a written-off
// invoice as open.

func writeOffPath(id int64) string { return fmt.Sprintf("%s/%d/write-off", invoicesPath, id) }
func undoPath(id int64) string     { return fmt.Sprintf("%s/%d/write-off/undo", invoicesPath, id) }

// writeOffJSON is a write-off as the document answers it.
type writeOffJSON struct {
	ID              int64   `json:"id"`
	WrittenOffOn    string  `json:"writtenOffOn"`
	Amount          float64 `json:"amount"`
	Reason          string  `json:"reason"`
	VatCorrection   bool    `json:"vatCorrection"`
	RecoveredAmount float64 `json:"recoveredAmount"`
	UndoneAt        *string `json:"undoneAt"`
	UndoReason      *string `json:"undoReason"`
}

// writtenOffJSON is what a document says of its write-offs.
type writtenOffJSON struct {
	State            string         `json:"state"`
	OpenAmount       float64        `json:"openAmount"`
	PaidAmount       float64        `json:"paidAmount"`
	WrittenOffAmount float64        `json:"writtenOffAmount"`
	RecoveredAmount  float64        `json:"recoveredAmount"`
	WriteOffs        []writeOffJSON `json:"writeOffs"`
	Payments         []struct {
		ID         int64  `json:"id"`
		Kind       string `json:"kind"`
		WriteOffID *int64 `json:"writeOffId"`
	} `json:"payments"`
}

// doc reads the document as its write-off figures, failing unless status.
func writtenOff(t *testing.T, what string, res interface {
	JSON(any)
}, status, want int) writtenOffJSON {
	t.Helper()
	var out writtenOffJSON
	if status != want {
		t.Fatalf("%s = %d, want %d", what, status, want)
	}
	res.JSON(&out)
	return out
}

func TestWriteOff_ClosesTheOpenAmountRecoversAndUndoes(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := payer(t, h)
	inv := thousand(t, h)
	registered(t, h, inv.ID, pay(400, "2026-09-12"))

	// The fields first.
	for name, body := range map[string]map[string]any{
		"before the issue":           {"writtenOffOn": "2026-09-11", "reason": "loss"},
		"after today":                {"writtenOffOn": "2026-09-13", "reason": "loss"},
		"a correction for rounding":  {"writtenOffOn": "2026-09-12", "reason": "rounding", "vatCorrection": true},
		"a correction for any other": {"writtenOffOn": "2026-09-12", "reason": "other", "vatCorrection": true},
	} {
		if res := c.Do(http.MethodPost, writeOffPath(inv.ID), body); res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Status, res.Body)
		}
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, writeOffPath(inv.ID),
		map[string]any{"writtenOffOn": "2026-09-12", "reason": "loss"}); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:payments = %d, want 403", res.Status)
	}

	res := c.Do(http.MethodPost, writeOffPath(inv.ID), map[string]any{"writtenOffOn": "2026-09-12", "reason": "loss", "vatCorrection": true})
	got := writtenOff(t, "write off", res, res.Status, http.StatusOK)
	if got.State != "written_off" || got.OpenAmount != 0 || got.PaidAmount != 400 || got.WrittenOffAmount != 600 ||
		len(got.WriteOffs) != 1 || got.WriteOffs[0].Amount != 600 || !got.WriteOffs[0].VatCorrection || got.WriteOffs[0].Reason != "loss" {
		t.Fatalf("written off = %+v", got)
	}
	refusedAs(t, "a second write-off", c.Do(http.MethodPost, writeOffPath(inv.ID), map[string]any{"writtenOffOn": "2026-09-12", "reason": "loss"}), "invoice_settled")
	refusedAs(t, "a payment of what is written off", c.Do(http.MethodPost, paymentsPath(inv.ID), pay(100, "2026-09-12")), "invoice_settled")
	if l := list(t, h, "?state=written_off"); !slices.Equal(ids(l), []int64{inv.ID}) {
		t.Errorf("?state=written_off = %v", ids(l))
	}
	if l := list(t, h, "?state=partially_paid"); len(l.Data) != 0 {
		t.Errorf("?state=partially_paid = %v, want none", ids(l))
	}

	// Money that arrives after it: recovered, up to what is written off.
	recovered := map[string]any{"amount": 200, "paidOn": "2026-09-12", "kind": "recovered"}
	res = c.Do(http.MethodPost, paymentsPath(inv.ID), recovered)
	got = writtenOff(t, "recover", res, res.Status, http.StatusOK)
	if got.State != "written_off" || got.OpenAmount != 0 || got.RecoveredAmount != 200 || got.PaidAmount != 400 ||
		got.WriteOffs[0].RecoveredAmount != 200 || got.Payments[1].Kind != "recovered" || got.Payments[1].WriteOffID == nil {
		t.Fatalf("recovered = %+v", got)
	}
	p := refusedAs(t, "a recovery over the write-off", c.Do(http.MethodPost, paymentsPath(inv.ID),
		map[string]any{"amount": 500, "paidOn": "2026-09-12", "kind": "recovered"}), "recovery_exceeds_written_off")
	if p.OpenAmount == nil || *p.OpenAmount != 400 {
		t.Errorf("the room left = %v, want 400", p.OpenAmount)
	}

	// The undo waits for the recoveries' removal, then opens it again.
	refusedAs(t, "an undo with a recovery standing", c.Do(http.MethodPost, undoPath(inv.ID), map[string]any{"reason": "Betalte likevel"}), "write_off_recovered")
	if res := c.Do(http.MethodPost, undoPath(inv.ID), map[string]any{"reason": " "}); res.Status != http.StatusBadRequest {
		t.Errorf("an undo without a reason = %d", res.Status)
	}
	if res := c.Do(http.MethodPost, removalPath(inv.ID, got.Payments[1].ID), map[string]any{"reason": "Feil"}); res.Status != http.StatusOK {
		t.Fatalf("remove the recovery = %d %s", res.Status, res.Body)
	}
	res = c.Do(http.MethodPost, undoPath(inv.ID), map[string]any{"reason": "Kunden betaler"})
	got = writtenOff(t, "undo", res, res.Status, http.StatusOK)
	if got.State != "partially_paid" || got.OpenAmount != 600 || got.WrittenOffAmount != 0 || got.WriteOffs[0].UndoneAt == nil {
		t.Fatalf("undone = %+v", got)
	}
	refusedAs(t, "an undo of nothing", c.Do(http.MethodPost, undoPath(inv.ID), map[string]any{"reason": "Igjen"}), "no_write_off")
	refusedAs(t, "a recovery with no write-off", c.Do(http.MethodPost, paymentsPath(inv.ID), recovered), "no_write_off")
	registered(t, h, inv.ID, pay(600, "2026-09-12"))
	if res := c.Do(http.MethodPost, undoPath(999999), map[string]any{"reason": "x"}); res.Status != http.StatusNotFound {
		t.Errorf("an unknown invoice's undo = %d", res.Status)
	}
	if res := c.Do(http.MethodPost, writeOffPath(999999), map[string]any{"writtenOffOn": "2026-09-12", "reason": "loss"}); res.Status != http.StatusNotFound {
		t.Errorf("an unknown invoice's write-off = %d", res.Status)
	}

	// A credit note is never written off; a rounding is a write-off too.
	credit := issued(t, h, creditDraft(t, h, thousand(t, h).ID).ID)
	refusedAs(t, "a credit note's write-off", c.Do(http.MethodPost, writeOffPath(credit.ID), map[string]any{"writtenOffOn": "2026-09-12", "reason": "loss"}), "credit_note_no_payments")
	small := thousand(t, h)
	registered(t, h, small.ID, pay(999.5, "2026-09-12"))
	res = c.Do(http.MethodPost, writeOffPath(small.ID), map[string]any{"writtenOffOn": "2026-09-12", "reason": "rounding"})
	got = writtenOff(t, "a rounding", res, res.Status, http.StatusOK)
	if got.State != "written_off" || got.WrittenOffAmount != 0.5 {
		t.Errorf("rounding = %+v", got)
	}
}

// A write-off and its undoing, and a recovery and its removal, are facts in
// the sales journal with what a ledger needs; OpenAt leaves a written-off
// invoice out; Document reads one document by its id.
func TestWriteOff_TheSalesJournal(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := payer(t, h)
	inv := thousand(t, h)
	registered(t, h, inv.ID, pay(400, "2026-09-12"))
	if res := c.Do(http.MethodPost, writeOffPath(inv.ID), map[string]any{"writtenOffOn": "2026-09-12", "reason": "loss", "vatCorrection": true}); res.Status != http.StatusOK {
		t.Fatalf("write off = %d %s", res.Status, res.Body)
	}
	j := salesJournal(h)
	ctx := context.Background()
	page, err := j.OpenAt(ctx, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), 0, 10)
	if err != nil || len(page.Documents) != 0 {
		t.Errorf("open at the day = %+v, %v; want the written-off invoice left out", page, err)
	}
	res := c.Do(http.MethodPost, paymentsPath(inv.ID), map[string]any{"amount": 150, "paidOn": "2026-09-12", "kind": "recovered"})
	got := writtenOff(t, "recover", res, res.Status, http.StatusOK)
	if res := c.Do(http.MethodPost, removalPath(inv.ID, got.Payments[1].ID), map[string]any{"reason": "Feil"}); res.Status != http.StatusOK {
		t.Fatalf("remove = %d", res.Status)
	}
	if res := c.Do(http.MethodPost, undoPath(inv.ID), map[string]any{"reason": "Angret"}); res.Status != http.StatusOK {
		t.Fatalf("undo = %d %s", res.Status, res.Body)
	}
	facts, err := j.Facts(ctx, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, f := range facts {
		kinds = append(kinds, f.Kind)
		switch f.Kind {
		case contracts.FactWriteOff, contracts.FactWriteOffUndone:
			if f.WriteOff == nil || f.WriteOff.AmountNOK != "600.00" || !f.WriteOff.VATCorrection || f.WriteOff.Reason != "loss" ||
				f.AmountNOK != "600.00" || f.Date.Format(time.DateOnly) != "2026-09-12" || f.InvoiceID != inv.ID {
				t.Errorf("%s fact = %+v %+v", f.Kind, f, f.WriteOff)
			}
		case contracts.FactRecoveredPayment, contracts.FactRecoveredPaymentRemoved:
			if f.WriteOff == nil || f.WriteOff.ID != got.WriteOffs[0].ID || f.AmountNOK != "150.00" {
				t.Errorf("%s fact = %+v %+v", f.Kind, f, f.WriteOff)
			}
		case contracts.FactPayment:
			if f.WriteOff != nil {
				t.Errorf("a payment carries a write-off: %+v", f)
			}
		}
	}
	want := []string{"payment", "write_off", "recovered_payment", "recovered_payment_removed", "write_off_undone"}
	if !slices.Equal(kinds, want) {
		t.Errorf("facts = %v, want %v", kinds, want)
	}
	doc, err := j.Document(ctx, inv.ID)
	if err != nil || doc == nil || inv.Number == nil || doc.Number != *inv.Number || len(doc.VATSummaries) != 1 || doc.VATSummaries[0].VATNOK != "200.00" {
		t.Errorf("Document = %+v, %v", doc, err)
	}
	if none, err := j.Document(ctx, 999999); err != nil || none != nil {
		t.Errorf("an unknown document = %+v, %v; want nil", none, err)
	}
}

// writeOffPlanted writes off invoice id's open amount straight in the schema.
func writeOffPlanted(t *testing.T, h *harness, id int64, amount string) {
	t.Helper()
	h.Exec(t, `INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, currency, reason, registered_by_user_id, registered_at)
		VALUES ($1, DATE '2026-09-10', $2::numeric, 'NOK', 'loss', gen_random_uuid(), now())`, id, amount)
}

// A written-off invoice is not outstanding, not overdue, and not on the
// overdue list a reminder run reads.
func TestWriteOff_NeverOpenOverdueOrReminded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	remindersOn(t, h, "")
	over := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	gone := deliveredOn(t, h, 2, customerAcme, "2026-08-03")
	plantPayment(t, h, gone, "300", "2026-08-20")
	writeOffPlanted(t, h, gone, "700")

	if got := overdueIDs(overdueOf(t, reader(t, h), "")); !slices.Equal(got, []int64{over}) {
		t.Errorf("the overdue list = %v, want only the invoice not written off", got)
	}
	r := customerReceivablesOf(t, h, customerAcme)
	if r.OutstandingCount != 1 || r.OverdueCount != 1 || r.Currencies[0].Outstanding != "1000.00" {
		t.Errorf("receivables = %+v, want the one invoice not written off", r)
	}
	inv := getInvoice(t, h, gone)
	if inv.State != "written_off" || inv.OpenAmount == nil || *inv.OpenAmount != 0 {
		t.Errorf("the invoice = %s open %v", inv.State, inv.OpenAmount)
	}
	if l := list(t, h, "?state=overdue"); !slices.Equal(ids(l), []int64{over}) {
		t.Errorf("?state=overdue = %v", ids(l))
	}
}

// A written-off invoice has nothing open (accounting design A16, R4): a line
// paying it is queued invoice_settled and registers nothing, whether a bank
// file brought it or the bank feed read it (bank feed design F6) — the feed's
// lines are judged by the same rule as a file's. Money that arrives after a
// write-off is a person's to register, as a recovered payment, which is
// always manual (ck_payments_recovered).
func TestWriteOff_ALineForAWrittenOffInvoiceIsQueued(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	byFile, byFeed := kidInvoice(t, h), kidInvoice(t, h)
	for _, inv := range []invoiceJSON{byFile, byFeed} {
		h.Exec(t, `INSERT INTO invoices.write_offs (invoice_id, written_off_on, amount, currency, reason, registered_by_user_id, registered_at)
			VALUES ($1, DATE '2026-09-12', 1000, 'NOK', 'loss', gen_random_uuid(), now())`, inv.ID)
	}
	toMatchDay(h)
	r := imported(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1000, *byFile.Kid, "1")))
	fileLine := lineID(t, h, r.File.ID, "1")

	batch := plantID(t, h, `INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts,
		    transactions, ignored, ignored_kinds, uploaded_by_user_id, uploaded_at, provider)
		VALUES ('feed', repeat('b', 64), 'folio-1', 'bank-files/folio-1', 2, ARRAY[$1::text], 1, 0, '{}', NULL, now(), 'folio')
		RETURNING id`, sellerAccount)
	feedLine := plantID(t, h, `INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on,
		    amount, currency, kid, fingerprint, ordinal)
		VALUES ($1, 'feed-1', 'feed', $2, 'credit', DATE '2026-10-06', 1000, 'NOK', $3, repeat('e', 64), 1) RETURNING id`,
		batch, sellerAccount, *byFeed.Kid)
	if err := invoices.MatchFile(context.Background(), h.Deps(), batch, uuid.New(), h.Now()); err != nil {
		t.Fatalf("matching the feed batch: %v", err)
	}

	for name, id := range map[string]int64{"the file's line": fileLine, "the feed's line": feedLine} {
		if got := stateOf(t, h, id); got != "exception invoice_settled" {
			t.Errorf("%s = %s, want exception invoice_settled", name, got)
		}
		if got := paymentsFrom(t, h, id); len(got) != 0 {
			t.Errorf("%s's payments = %v, want none", name, got)
		}
		if got := chargePaymentsFrom(t, h, id); len(got) != 0 {
			t.Errorf("%s's charge payments = %v, want none", name, got)
		}
	}
	for _, inv := range []invoiceJSON{byFile, byFeed} {
		if got := getInvoice(t, h, inv.ID); got.State != "written_off" {
			t.Errorf("invoice %d = %s, want still written_off", inv.ID, got.State)
		}
	}
}
