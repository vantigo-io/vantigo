package invoices_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
)

// contracts.CustomerReceivables as this module provides it (customers
// receivables design D1): what one customer was invoiced and owes, judged on
// the Oslo day of the module's clock. Every test builds the provider the way
// Compose does, from the harness's Deps, and reads through it rather than
// through any endpoint: the contract is the thing under test.

// customerReceivablesOf is the provider's answer for customer, built as Compose
// builds it.
func customerReceivablesOf(t *testing.T, h *harness, customer int32) contracts.CustomerReceivableTotals {
	t.Helper()
	if invoices.Module().Receivables == nil {
		t.Fatal("invoices.Module().Receivables is nil: nothing would provide contracts.CustomerReceivables")
	}
	got, err := invoices.Module().Receivables(h.Deps()).ReceivablesForCustomer(context.Background(), customer)
	if err != nil {
		t.Fatalf("ReceivablesForCustomer(%d): %v", customer, err)
	}
	return got
}

// issuedDoc is an issued document planted straight into the schema: an
// invoice with its due date, or a credit note (credits set) which the schema
// allows no due date on. Nothing of the buyer snapshot matters here but the
// name.
type issuedDoc struct {
	customer int32
	kind     string // "invoice" or "credit_note"
	credits  int64  // the invoice a credit note credits
	number   int64
	issue    string
	due      string // "" for a credit note
	gross    string
	currency string // "" for NOK
}

func plantIssued(t *testing.T, h *harness, d issuedDoc) int64 {
	t.Helper()
	currency := d.currency
	if currency == "" {
		currency = "NOK"
	}
	return plantID(t, h, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, credits_invoice_id, issue_date, due_date, exchange_rate_date,
		    currency, seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ($1, 'issued', $2, $3, NULLIF($4, 0), $5::date, NULLIF($6, '')::date, $5::date,
		    $7, 'Selger AS', 'Kunde', $8::numeric, now(), gen_random_uuid(), now(), now())
		RETURNING id`, d.kind, d.number, d.customer, d.credits, d.issue, d.due, currency, d.gross)
}

// plantPaid is a live payment of amount paid on paidOn against invoice
// id, or — with removed — one registered and then removed, which is no
// payment at all.
func plantPaid(t *testing.T, h *harness, id int64, amount, paidOn string, removed bool) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at,
		    removed_at, removed_by_user_id, removal_reason)
		VALUES ($1, $2::date, $3::numeric, 'NOK', gen_random_uuid(), now(),
		    CASE WHEN $4 THEN now() END, CASE WHEN $4 THEN gen_random_uuid() END, CASE WHEN $4 THEN 'Feilført' END)`,
		id, paidOn, amount, removed)
}

func wantCurrencies(t *testing.T, got, want []contracts.CurrencyReceivables) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Currencies = %+v, want %+v", got, want)
	}
}

func wantDate(t *testing.T, what string, got *string, want string) {
	t.Helper()
	switch {
	case want == "" && got != nil:
		t.Errorf("%s = %q, want none", what, *got)
	case want != "" && got == nil:
		t.Errorf("%s = none, want %s", what, want)
	case want != "" && *got != want:
		t.Errorf("%s = %s, want %s", what, *got, want)
	}
}

// A customer with no issued document answers the zero value with today in
// it: no currencies, every count zero, no date — never an error.
func TestReceivables_NothingAnswersZerosAndToday(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got := customerReceivablesOf(t, h, customerAcme)
	if got.AsOf != "2026-09-12" {
		t.Errorf("AsOf = %s, want 2026-09-12, the harness clock's Oslo day", got.AsOf)
	}
	if len(got.Currencies) != 0 {
		t.Errorf("Currencies = %+v, want none", got.Currencies)
	}
	if got.OutstandingCount != 0 || got.OverdueCount != 0 || got.OnHoldCount != 0 || got.AtCollectionCount != 0 {
		t.Errorf("counts = %d/%d/%d/%d, want all zero", got.OutstandingCount, got.OverdueCount, got.OnHoldCount, got.AtCollectionCount)
	}
	wantDate(t, "OldestOverdueDueOn", got.OldestOverdueDueOn, "")
	wantDate(t, "LastIssuedOn", got.LastIssuedOn, "")
	wantDate(t, "LastPaymentOn", got.LastPaymentOn, "")
}

// A draft is not a sales document: a customer with only drafts has been
// invoiced nothing.
func TestReceivables_DraftsNeverCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25)))

	got := customerReceivablesOf(t, h, customerAcme)
	if len(got.Currencies) != 0 || got.LastIssuedOn != nil {
		t.Errorf("a draft counted: %+v", got)
	}
}

// The figures over a customer's documents, with today 2026-09-12: an
// overdue invoice partly credited, an open one with a removed payment, a paid
// one, last year's invoice partly paid and overdue, and another customer's
// invoice that must not mix in. Issued and credited are split by the Oslo
// calendar year of the issue date — 31 December is last year, 1 January
// this — and the open amounts are gross less credited less paid.
func TestReceivables_TheFiguresOverACustomersDocuments(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	overdue := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 1, issue: "2026-07-01", due: "2026-07-15", gross: "1000"})
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "credit_note", credits: overdue, number: 2, issue: "2026-09-05", gross: "200"})
	open := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 3, issue: "2026-09-01", due: "2026-10-01", gross: "500"})
	plantPaid(t, h, open, "100", "2026-09-11", true) // removed: no payment, and not the last payment date
	paid := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 4, issue: "2026-08-01", due: "2026-08-15", gross: "300"})
	plantPaid(t, h, paid, "300", "2026-08-20", false)
	lastYear := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 5, issue: "2025-12-31", due: "2026-01-14", gross: "2000"})
	plantPaid(t, h, lastYear, "500", "2026-01-10", false)
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 6, issue: "2026-01-01", due: "2026-01-15", gross: "50"})
	plantIssued(t, h, issuedDoc{customer: customerPerson, kind: "invoice", number: 7, issue: "2026-09-01", due: "2026-09-15", gross: "9999"})

	got := customerReceivablesOf(t, h, customerAcme)
	wantCurrencies(t, got.Currencies, []contracts.CurrencyReceivables{{
		Currency: "NOK", IssuedThisYear: "1850.00", CreditedThisYear: "200.00", IssuedLastYear: "2000.00", CreditedLastYear: "0.00",
		Outstanding: "2850.00", Overdue: "2350.00",
	}})
	if got.OutstandingCount != 4 || got.OverdueCount != 3 {
		t.Errorf("outstanding %d overdue %d, want 4 and 3", got.OutstandingCount, got.OverdueCount)
	}
	wantDate(t, "OldestOverdueDueOn", got.OldestOverdueDueOn, "2026-01-14")
	wantDate(t, "LastIssuedOn", got.LastIssuedOn, "2026-09-05")
	wantDate(t, "LastPaymentOn", got.LastPaymentOn, "2026-08-20")
	if got.AsOf != "2026-09-12" {
		t.Errorf("AsOf = %s, want 2026-09-12", got.AsOf)
	}
}

// A credit note counts in the calendar year it was issued, whatever year the
// invoice it credits was: one in December against a December invoice is last
// year's, one in January against the same December invoice is this year's.
// And a currency whose every figure is zero today is still an entry, because
// the customer was invoiced in it.
func TestReceivables_ACreditNoteCountsInItsOwnYear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	december := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 1, issue: "2025-12-10", due: "2025-12-24", gross: "1000"})
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "credit_note", credits: december, number: 2, issue: "2025-12-20", gross: "300"})
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "credit_note", credits: december, number: 3, issue: "2026-01-05", gross: "700"})
	old := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 4, issue: "2023-05-01", due: "2023-05-15", gross: "40", currency: "EUR"})
	plantPaid(t, h, old, "40", "2023-05-10", false)

	got := customerReceivablesOf(t, h, customerAcme)
	wantCurrencies(t, got.Currencies, []contracts.CurrencyReceivables{
		{Currency: "EUR", IssuedThisYear: "0.00", CreditedThisYear: "0.00", IssuedLastYear: "0.00", CreditedLastYear: "0.00", Outstanding: "0.00", Overdue: "0.00"},
		{Currency: "NOK", IssuedThisYear: "0.00", CreditedThisYear: "700.00", IssuedLastYear: "1000.00", CreditedLastYear: "300.00", Outstanding: "0.00", Overdue: "0.00"},
	})
	if got.OutstandingCount != 0 {
		t.Errorf("OutstandingCount = %d, want 0: the invoice is fully credited", got.OutstandingCount)
	}
	wantDate(t, "LastIssuedOn", got.LastIssuedOn, "2026-01-05")
}

// The year, too, is Oslo's: at 23:30 UTC on 31 December it is already
// 1 January in Oslo, so what was issued in 2026 is last year's and AsOf
// says 2027.
func TestReceivables_TheYearIsJudgedOnTheOsloDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 1, issue: "2026-09-01", due: "2026-10-01", gross: "100"})

	before := customerReceivablesOf(t, h, customerAcme)
	if before.Currencies[0].IssuedThisYear != "100.00" || before.Currencies[0].IssuedLastYear != "0.00" {
		t.Errorf("in 2026: this year %s, last year %s, want 100.00 and 0.00", before.Currencies[0].IssuedThisYear, before.Currencies[0].IssuedLastYear)
	}
	moveClockTo(t, h, time.Date(2026, time.December, 31, 23, 30, 0, 0, time.UTC))
	after := customerReceivablesOf(t, h, customerAcme)
	if after.AsOf != "2027-01-01" {
		t.Errorf("AsOf = %s, want 2027-01-01: 23:30 UTC on New Year's Eve is past midnight in Oslo", after.AsOf)
	}
	if after.Currencies[0].IssuedThisYear != "0.00" || after.Currencies[0].IssuedLastYear != "100.00" {
		t.Errorf("in 2027: this year %s, last year %s, want 0.00 and 100.00", after.Currencies[0].IssuedThisYear, after.Currencies[0].IssuedLastYear)
	}
}

// A full payment and a full credit note each take an invoice out of
// outstanding; a partial payment keeps it in at what is left.
func TestReceivables_PaidAndCreditedAreNotOutstanding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	paid := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 1, issue: "2026-09-01", due: "2026-10-01", gross: "100"})
	plantPaid(t, h, paid, "100", "2026-09-10", false)
	credited := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 2, issue: "2026-09-01", due: "2026-10-01", gross: "100"})
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "credit_note", credits: credited, number: 3, issue: "2026-09-02", gross: "100"})
	partly := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 4, issue: "2026-09-01", due: "2026-10-01", gross: "100"})
	plantPaid(t, h, partly, "40", "2026-09-10", false)

	got := customerReceivablesOf(t, h, customerAcme)
	wantCurrencies(t, got.Currencies, []contracts.CurrencyReceivables{{
		Currency: "NOK", IssuedThisYear: "300.00", CreditedThisYear: "100.00", IssuedLastYear: "0.00", CreditedLastYear: "0.00",
		Outstanding: "60.00", Overdue: "0.00",
	}})
	if got.OutstandingCount != 1 || got.OverdueCount != 0 {
		t.Errorf("outstanding %d overdue %d, want 1 and 0", got.OutstandingCount, got.OverdueCount)
	}
}

// Overdue is judged on the Oslo day, not the UTC one: at 22:30 UTC on the
// 12th it is already the 13th in Oslo, so an invoice due on the 12th is
// overdue — and AsOf says the 13th.
func TestReceivables_OverdueIsJudgedOnTheOsloDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 1, issue: "2026-09-01", due: "2026-09-12", gross: "100"})

	before := customerReceivablesOf(t, h, customerAcme)
	if before.OverdueCount != 0 || before.Currencies[0].Overdue != "0.00" {
		t.Errorf("at noon on the due date: overdue %d %s, want nothing overdue", before.OverdueCount, before.Currencies[0].Overdue)
	}
	moveClockTo(t, h, time.Date(2026, time.September, 12, 22, 30, 0, 0, time.UTC))
	after := customerReceivablesOf(t, h, customerAcme)
	if after.AsOf != "2026-09-13" {
		t.Errorf("AsOf = %s, want 2026-09-13: 22:30 UTC is past midnight in Oslo", after.AsOf)
	}
	if after.OverdueCount != 1 || after.Currencies[0].Overdue != "100.00" {
		t.Errorf("past midnight in Oslo: overdue %d %s, want 1 and 100.00", after.OverdueCount, after.Currencies[0].Overdue)
	}
	wantDate(t, "OldestOverdueDueOn", after.OldestOverdueDueOn, "2026-09-12")
}

// A live hold and a live hand-off are counted on an outstanding invoice; a
// lifted hold, a withdrawn hand-off and a hold on a paid invoice are not.
func TestReceivables_HoldsAndHandoffsCountOnlyWhileLiveAndOutstanding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	held := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 1, issue: "2026-07-01", due: "2026-07-15", gross: "100"})
	h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id, lifted_at, lifted_by_user_id, lift_note, charges_allowed)
		VALUES ($1, 'disputed', 'Bestridt', now(), gen_random_uuid(), now(), gen_random_uuid(), 'Avklart', true),
		       ($1, 'disputed', 'Bestridt igjen', now(), gen_random_uuid(), NULL, NULL, NULL, NULL)`, held)
	handed := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 2, issue: "2026-07-01", due: "2026-07-15", gross: "100"})
	h.Exec(t, `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id, withdrawn_on, withdrawn_by_user_id, withdrawal_reason)
		VALUES ($1, DATE '2026-08-01', 'Inkasso AS', now(), gen_random_uuid(), DATE '2026-08-02', gen_random_uuid(), 'Betalt'),
		       ($1, DATE '2026-08-03', 'Inkasso AS', now(), gen_random_uuid(), NULL, NULL, NULL)`, handed)
	paid := plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 3, issue: "2026-07-01", due: "2026-07-15", gross: "100"})
	plantPaid(t, h, paid, "100", "2026-07-10", false)
	h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
		VALUES ($1, 'disputed', 'Bestridt etter betaling', now(), gen_random_uuid())`, paid)

	got := customerReceivablesOf(t, h, customerAcme)
	if got.OnHoldCount != 1 || got.AtCollectionCount != 1 {
		t.Errorf("on hold %d, at collection %d, want 1 and 1", got.OnHoldCount, got.AtCollectionCount)
	}
	if got.OutstandingCount != 2 || got.OverdueCount != 2 {
		t.Errorf("outstanding %d overdue %d, want 2 and 2: a hold changes no state", got.OutstandingCount, got.OverdueCount)
	}
}

// One entry per currency, by ISO code, and nothing ever added across them.
func TestReceivables_PerCurrencyNeverAcross(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 1, issue: "2026-09-01", due: "2026-10-01", gross: "100"})
	plantIssued(t, h, issuedDoc{customer: customerAcme, kind: "invoice", number: 2, issue: "2026-09-01", due: "2026-10-01", gross: "40", currency: "EUR"})

	got := customerReceivablesOf(t, h, customerAcme)
	wantCurrencies(t, got.Currencies, []contracts.CurrencyReceivables{
		{Currency: "EUR", IssuedThisYear: "40.00", CreditedThisYear: "0.00", IssuedLastYear: "0.00", CreditedLastYear: "0.00", Outstanding: "40.00", Overdue: "0.00"},
		{Currency: "NOK", IssuedThisYear: "100.00", CreditedThisYear: "0.00", IssuedLastYear: "0.00", CreditedLastYear: "0.00", Outstanding: "100.00", Overdue: "0.00"},
	})
	if got.OutstandingCount != 2 {
		t.Errorf("OutstandingCount = %d, want 2 across both currencies", got.OutstandingCount)
	}
}

// A merge re-points a customer's documents to the survivor: the merged-away
// customer then answers zeros and the survivor everything. An anonymised
// person's issued documents stay — bookkeeping keeps them — and still answer,
// while the erase took their drafts.
func TestReceivables_FollowAMergeAndSurviveAnErase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantIssued(t, h, issuedDoc{customer: customerMerged, kind: "invoice", number: 1, issue: "2026-09-01", due: "2026-10-01", gross: "100"})
	inTx(t, h, true, func(tx pgx.Tx) {
		if _, err := invoices.Module().CustomerReferences(h.Deps()).RepointCustomer(context.Background(), tx, customerMerged, customerAcme); err != nil {
			t.Fatalf("RepointCustomer: %v", err)
		}
	})
	if got := customerReceivablesOf(t, h, customerMerged); len(got.Currencies) != 0 {
		t.Errorf("the merged-away customer still answers %+v", got.Currencies)
	}
	if got := customerReceivablesOf(t, h, customerAcme); len(got.Currencies) != 1 || got.Currencies[0].Outstanding != "100.00" {
		t.Errorf("the survivor answers %+v, want the re-pointed invoice outstanding", got.Currencies)
	}

	plantIssued(t, h, issuedDoc{customer: customerPerson, kind: "invoice", number: 2, issue: "2026-09-01", due: "2026-10-01", gross: "250"})
	createDraft(t, h, draftBody(customerPerson, line("Konsulenttime", 1, 80, vat25)))
	eraseOf(t, h, h.Deps(), customerPerson)
	got := customerReceivablesOf(t, h, customerPerson)
	if len(got.Currencies) != 1 || got.Currencies[0].Outstanding != "250.00" || got.OutstandingCount != 1 {
		t.Errorf("after the erase: %+v, want the issued invoice still outstanding", got)
	}
}
