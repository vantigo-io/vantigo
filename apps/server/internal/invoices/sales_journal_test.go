package invoices_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// contracts.SalesJournal (accounting design A12, R1, R6): the documents by
// number with their lines and VAT summaries, the fact log by seq, the open
// invoices at a day, the line snapshots and the two events that wake a reader.

// salesJournal is the module's provider, built as Compose builds it.
func salesJournal(h *harness) contracts.SalesJournal {
	return invoices.Module().SalesJournal(h.Deps())
}

func TestSalesJournal_DocumentsByNumberWithLinesAndSummaries(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ctx := context.Background()
	first := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 1200, vat25), line("Kurs", 1, 5000, vatExempt))).ID)
	second := thousand(t, h)
	credit := issued(t, h, creditDraft(t, h, second.ID).ID)
	j := salesJournal(h)

	page, err := j.Documents(ctx, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Number != *first.Number || page[1].Number != *second.Number {
		t.Fatalf("first page = %+v, want the two invoices by number", page)
	}
	d := page[0]
	if d.ID != first.ID || d.Kind != contracts.JournalInvoice || d.CustomerID != customerAcme || d.Currency != "NOK" ||
		d.IssueDate.Format(time.DateOnly) != "2026-09-12" || d.DueDate == nil || d.DueDate.Format(time.DateOnly) != "2026-10-12" ||
		d.Net != "17000.00" || d.VAT != "3000.00" || d.Gross != "20000.00" || d.VATNOK != "3000.00" || d.CreditsID != 0 {
		t.Errorf("document = %+v, want the first invoice's figures", d)
	}
	if len(d.Lines) != 2 {
		t.Fatalf("lines = %+v, want two", d.Lines)
	}
	if l := d.Lines[0]; l.Position != 1 || l.NetNOK != "12000.00" || l.Kind != contracts.LineKindManual || l.SAFT != "3" ||
		l.Category != "S" || l.RatePercent != "25.00" || l.TaxCategoryID != nil || l.ProjectID != nil {
		t.Errorf("line 1 = %+v, want a manual line of 12 000 at code 3, 25 %%", l)
	}
	if l := d.Lines[1]; l.NetNOK != "5000.00" || l.SAFT != "6" || l.Category != "E" || l.RatePercent != "0.00" {
		t.Errorf("line 2 = %+v, want 5 000 outside the VAT act", l)
	}
	if len(d.VATSummaries) != 2 || d.VATSummaries[0] != (contracts.JournalVATSummary{SAFT: "3", Category: "S", RatePercent: "25.00", BaseNOK: "12000.00", VATNOK: "3000.00"}) ||
		d.VATSummaries[1] != (contracts.JournalVATSummary{SAFT: "6", Category: "E", RatePercent: "0.00", BaseNOK: "5000.00", VATNOK: "0.00"}) {
		t.Errorf("summaries = %+v, want 25 %% then the exempt one", d.VATSummaries)
	}

	next, err := j.Documents(ctx, page[1].Number, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].ID != credit.ID || next[0].Kind != contracts.JournalCreditNote || next[0].CreditsID != second.ID ||
		next[0].CreditsNumber != *second.Number || next[0].DueDate != nil || next[0].Gross != "1000.00" {
		t.Fatalf("second page = %+v, want the credit note of the second invoice, positive", next)
	}
	if l := next[0].Lines; len(l) != 1 || l[0].Kind != contracts.LineKindManual || l[0].NetNOK != "800.00" {
		t.Errorf("credit note lines = %+v, want its original's kind and net", l)
	}
	if rest, err := j.Documents(ctx, next[0].Number, 10); err != nil || len(rest) != 0 {
		t.Errorf("after the last = %v, %v; want none", rest, err)
	}
	if one, err := j.Documents(ctx, 0, 0); err != nil || len(one) != 1 {
		t.Errorf("a limit of 0 = %d documents, %v; want one", len(one), err)
	}

	// invoices.document.issued, once for each document, on its issue.
	got := eventPayloads(t, h, "invoices.document.issued")
	if len(got) != 3 {
		t.Fatalf("%d issued events, want three", len(got))
	}
	var ev contracts.DocumentIssued
	if err := json.Unmarshal(got[2], &ev); err != nil {
		t.Fatal(err)
	}
	if ev != (contracts.DocumentIssued{DocumentID: credit.ID, Number: *credit.Number, Kind: "credit_note", CustomerID: customerAcme,
		IssueDate: "2026-09-12", GrossTotal: "1000.00", Currency: "NOK"}) {
		t.Errorf("event = %+v, want the credit note's", ev)
	}
	want := map[string]bool{"documentId": true, "number": true, "kind": true, "customerId": true, "issueDate": true, "grossTotal": true, "currency": true}
	for k := range keysOf(t, got[0]) {
		if !want[k] {
			t.Errorf("the event carries %q, which is not in the contract", k)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'invoices.document.issued' AND aggregate = 'invoices.document' AND aggregate_id = $1`, strconv.FormatInt(first.ID, 10)); n != 1 {
		t.Errorf("the first invoice's event is not under its aggregate")
	}
	// A refused issue publishes nothing.
	empty := createDraft(t, h, draftBody(customerAcme))
	refusedWith(t, h, empty.ID, "", "no_lines")
	if n := len(eventPayloads(t, h, "invoices.document.issued")); n != 3 {
		t.Errorf("%d issued events after a refused issue, want still three", n)
	}
}

func TestSalesJournal_FactsAndThePaymentRemovedEvent(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ctx := context.Background()
	// An invoice issued 1 September, number 9001, of 1000.
	id := plantOverdue(t, h, overdueSpec{number: 9001, customer: customerAcme, issue: "2026-09-01", due: "2026-09-30"})
	inv := getInvoice(t, h, id)
	registered(t, h, inv.ID, map[string]any{"amount": 300, "paidOn": "2026-09-10", "reference": "KID 1", "note": "Kari Nordmann"})
	registered(t, h, inv.ID, pay(200, "2026-09-12"))
	payments := getInvoice(t, h, inv.ID).Payments
	var firstID int64
	for _, p := range payments {
		if p.Amount == 300 {
			firstID = p.ID
		}
	}
	res := payer(t, h).Do(http.MethodPost, removalPath(inv.ID, firstID), map[string]any{"reason": "Feil kunde"})
	if res.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", res.Status, res.Body)
	}

	j := salesJournal(h)
	facts, err := j.Facts(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 3 {
		t.Fatalf("facts = %+v, want three", facts)
	}
	for i, f := range facts {
		if f.Seq != int64(i+1) || f.InvoiceID != inv.ID || f.InvoiceNumber != *inv.Number || f.CustomerID != customerAcme ||
			f.Source != contracts.PaymentSourceManual || f.BankAccount != "" || f.Charges != nil {
			t.Errorf("fact %d = %+v, want seq %d of the invoice, manual, no account", i, f, i+1)
		}
	}
	if f := facts[0]; f.Kind != contracts.FactPayment || f.SubjectID != firstID || f.AmountNOK != "300.00" || f.Date.Format(time.DateOnly) != "2026-09-10" {
		t.Errorf("fact 1 = %+v, want the 300 paid on 2026-09-10", f)
	}
	if f := facts[1]; f.Kind != contracts.FactPayment || f.AmountNOK != "200.00" || f.Date.Format(time.DateOnly) != "2026-09-12" {
		t.Errorf("fact 2 = %+v, want the 200", f)
	}
	if f := facts[2]; f.Kind != contracts.FactPaymentRemoved || f.SubjectID != firstID || f.AmountNOK != "300.00" || f.Date.Format(time.DateOnly) != "2026-09-12" {
		t.Errorf("fact 3 = %+v, want the 300's removal, dated the business day it was removed", f)
	}
	after, err := j.Facts(ctx, 2, 10)
	if err != nil || len(after) != 1 || after[0].Seq != 3 {
		t.Errorf("after seq 2 = %+v, %v; want the removal alone", after, err)
	}

	// invoices.payment.removed, once, by the removal; no reason, no note.
	got := eventPayloads(t, h, "invoices.payment.removed")
	if len(got) != 1 {
		t.Fatalf("%d removed events, want one", len(got))
	}
	var ev contracts.PaymentRemoved
	if err := json.Unmarshal(got[0], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.PaymentID != firstID || ev.InvoiceID != inv.ID || ev.Number != *inv.Number || ev.CustomerID != customerAcme ||
		ev.Amount != "300.00" || ev.Currency != "NOK" || ev.PaidOn != "2026-09-10" || ev.Source != "manual" || ev.Removed.IsZero() {
		t.Errorf("event = %+v, want the removed registration's", ev)
	}
	want := map[string]bool{"paymentId": true, "invoiceId": true, "number": true, "customerId": true, "amount": true, "currency": true, "paidOn": true, "source": true, "removedAt": true}
	for k := range keysOf(t, got[0]) {
		if !want[k] {
			t.Errorf("the event carries %q, which is not in the contract", k)
		}
	}
	for _, text := range []string{"Feil kunde", "Kari", "KID 1"} {
		if containsFold(string(got[0]), text) {
			t.Errorf("the event carries %q", text)
		}
	}
	if n := len(eventPayloads(t, h, "invoices.payment.received")); n != 2 {
		t.Errorf("%d received events, want the two registrations'", n)
	}
	// A second removal of the same payment is refused and publishes nothing.
	res = payer(t, h).Do(http.MethodPost, removalPath(inv.ID, firstID), map[string]any{"reason": "Igjen"})
	if res.Status != http.StatusConflict {
		t.Errorf("a second removal = %d, want 409", res.Status)
	}
	if n := len(eventPayloads(t, h, "invoices.payment.removed")); n != 1 {
		t.Errorf("%d removed events after a refused removal, want still one", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.journal_facts`); n != 3 {
		t.Errorf("%d facts after a refused removal, want still three", n)
	}
}

func TestSalesJournal_ChargePaymentsAreSplitByWhatTheyPaid(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ctx := context.Background()
	id := plantOverdue(t, h, overdueSpec{number: 9001, customer: customerAcme, issue: "2026-07-01", due: "2026-08-01", deliveredOnIssueOn: true})
	plantLetterSent(t, h, id, 1, "reminder", "2026-08-15", "35.00", "12.00")
	// 40 pays the fee (35) and 5 of the interest; then 10 pays the other 7 of
	// it and 3 beyond every charge.
	first := plantID(t, h, `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source, registered_by_user_id, registered_at)
		VALUES ($1, '2026-09-01', 40, 'NOK', 'manual', gen_random_uuid(), now()) RETURNING id`, id)
	plantID(t, h, `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source, registered_by_user_id, registered_at)
		VALUES ($1, '2026-09-02', 10, 'NOK', 'manual', gen_random_uuid(), now()) RETURNING id`, id)
	h.Exec(t, `UPDATE invoices.charge_payments SET removed_at = now(), removed_by_user_id = gen_random_uuid(), removal_reason = 'Feil' WHERE id = $1`, first)

	facts, err := salesJournal(h).Facts(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 3 || facts[0].Kind != contracts.FactChargePayment || facts[1].Kind != contracts.FactChargePayment ||
		facts[2].Kind != contracts.FactChargePaymentRemoved || facts[2].SubjectID != first {
		t.Fatalf("facts = %+v, want two charge payments and the first's removal", facts)
	}
	parts := func(f contracts.JournalFact) map[string]string {
		out := map[string]string{}
		for _, p := range f.Charges {
			out[p.Kind] = p.AmountNOK
		}
		return out
	}
	if got := parts(facts[0]); len(got) != 2 || got[contracts.ChargeReminderFee] != "35.00" || got[contracts.ChargeInterest] != "5.00" {
		t.Errorf("first = %v, want the fee and 5 of the interest", got)
	}
	if got := parts(facts[1]); len(got) != 2 || got[contracts.ChargeInterest] != "7.00" || got[contracts.ChargeRefundDue] != "3.00" {
		t.Errorf("second = %v, want the rest of the interest and 3 beyond", got)
	}
	if got := parts(facts[2]); len(got) != 2 || got[contracts.ChargeReminderFee] != "35.00" || got[contracts.ChargeInterest] != "5.00" {
		t.Errorf("removal = %v, want what its registration paid", got)
	}
}

func TestSalesJournal_OpenAt(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ctx := context.Background()
	partly := thousand(t, h)
	registered(t, h, partly.ID, pay(300, "2026-09-12"))
	removed := thousand(t, h)
	registered(t, h, removed.ID, pay(1000, "2026-09-12"))
	res := payer(t, h).Do(http.MethodPost, removalPath(removed.ID, getInvoice(t, h, removed.ID).Payments[0].ID), map[string]any{"reason": "Feil"})
	if res.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", res.Status, res.Body)
	}
	credited := thousand(t, h)
	issued(t, h, creditDraft(t, h, credited.ID).ID)
	later := thousand(t, h)
	registered(t, h, later.ID, pay(1000, "2026-09-12"))
	// Removed the next business day: on the 12th it was paid.
	h.Exec(t, `UPDATE invoices.payments SET removed_at = '2026-09-12T22:30:00Z', removed_by_user_id = gen_random_uuid(), removal_reason = 'Feil'
		WHERE invoice_id = $1`, later.ID)
	paid := thousand(t, h)
	registered(t, h, paid.ID, pay(1000, "2026-09-12"))

	j := salesJournal(h)
	if before, err := j.OpenAt(ctx, time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)); err != nil || len(before) != 0 {
		t.Errorf("the day before = %+v, %v; want none issued yet", before, err)
	}
	open, err := j.OpenAt(ctx, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]string{}
	for _, o := range open {
		got[o.ID] = o.OpenNOK
		if o.CustomerID != customerAcme || o.Currency != "NOK" || o.IssueDate.Format(time.DateOnly) != "2026-09-12" || o.DueDate.Format(time.DateOnly) != "2026-10-12" {
			t.Errorf("open document = %+v, want its customer and dates", o)
		}
	}
	want := map[int64]string{partly.ID: "700.00", removed.ID: "1000.00"}
	if len(got) != len(want) || got[partly.ID] != want[partly.ID] || got[removed.ID] != want[removed.ID] {
		t.Errorf("open at 2026-09-12 = %v, want %v: the partly paid one and the one whose payment was removed that day", got, want)
	}
	if len(open) == 2 && open[0].Number > open[1].Number {
		t.Errorf("open = %+v, want number order", open)
	}
}

// What a line was made from (R6), as SnapshotLine — the statement the issue
// runs on every line, while the document is a draft — derives it from the
// line's sources: one draft with a line of each kind.
func TestSalesJournal_LineSnapshotsForEachSource(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ctx := context.Background()
	draft := plantID(t, h, `INSERT INTO invoices.invoices (kind, customer_id, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', $1, gen_random_uuid(), now(), now()) RETURNING id`, customerAcme)
	lineAt := func(position int, quantity string, deducts *int64) int64 {
		return plantID(t, h, `INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, vat_code_id,
			line_gross, line_allowance, line_net, deducts_invoice_id)
			VALUES ($1, $2, 'Linje', $3::numeric, 100, 1, 100, 0, 100, $4) RETURNING id`, draft, position, quantity, deducts)
	}
	source := func(line int64, kind string, id int64, subkind *string, project *int32, count int, taxCategory *int32, point bool) {
		gsrn, address, area, contract, from, estimated := (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*time.Time)(nil), (*bool)(nil)
		if point {
			g, a, p, c, f, e := "707057500000000001", "Havnegata 1", "NO4", "Spot", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), false
			gsrn, address, area, contract, from, estimated = &g, &a, &p, &c, &f, &e
		}
		h.Exec(t, `INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
			project_id, quantity, amount, currency, source_date, line_count, point_gsrn, point_address, price_area, contract_name,
			period_from, estimated, tax_category_id)
			VALUES ($1, $2, $3, $4, 1, $5, $6, 1, 100, 'NOK', '2026-08-31', $7, $8, $9, $10, $11, $12, $13, $14)`,
			line, draft, kind, id, subkind, project, count, gsrn, address, area, contract, from, estimated, taxCategory)
	}
	str := func(s string) *string { return &s }
	i32 := func(v int32) *int32 { return &v }
	acont := int64(424242)

	hours := lineAt(1, "1", nil)
	source(hours, "time.entry", 1, nil, i32(41), 1, nil, false)
	source(hours, "time.entry", 2, nil, i32(41), 1, nil, false)
	expense := lineAt(2, "1", nil)
	source(expense, "expenses.entry", 3, str("expense"), i32(41), 1, nil, false)
	milestone := lineAt(3, "1", nil)
	source(milestone, "projects.milestone", 4, nil, i32(42), 1, nil, false)
	energyFirst, energySecond := lineAt(4, "1", nil), lineAt(5, "1", nil)
	source(energyFirst, "energy.settlement", 5, str("regular"), nil, 2, nil, true)
	orderLine := lineAt(6, "1", nil)
	source(orderLine, "orders.allocation", 6, str("order_line"), i32(43), 1, i32(77), false)
	period := lineAt(7, "1", nil)
	source(period, "orders.allocation", 7, str("subscription_period"), nil, 1, i32(78), false)
	mixed := lineAt(8, "1", nil)
	source(mixed, "time.entry", 8, nil, i32(41), 1, nil, false)
	source(mixed, "projects.milestone", 9, nil, i32(44), 1, nil, false)
	deduction := lineAt(9, "-1", &acont)
	manual := lineAt(10, "1", nil)

	q := store.New(h.Pool())
	for _, id := range []int64{hours, expense, milestone, energyFirst, energySecond, orderLine, period, mixed, deduction, manual} {
		var rate pgtype.Numeric
		if err := rate.Scan("25"); err != nil {
			t.Fatal(err)
		}
		if err := q.SnapshotLine(ctx, store.SnapshotLineParams{ID: id, VatRatePercent: rate, VatCategory: str("S"), SafTCode: str("3")}); err != nil {
			t.Fatalf("snapshot line %d: %v", id, err)
		}
	}
	type snapshot struct {
		kind        string
		taxCategory *int32
		project     *int32
	}
	read := func(id int64) snapshot {
		var s snapshot
		if err := h.Pool().QueryRow(ctx, `SELECT line_kind, tax_category_id, project_id FROM invoices.lines WHERE id = $1`, id).Scan(&s.kind, &s.taxCategory, &s.project); err != nil {
			t.Fatalf("read line %d: %v", id, err)
		}
		return s
	}
	eq := func(a, b *int32) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
	for _, c := range []struct {
		name string
		line int64
		want snapshot
	}{
		{"two hours of one project", hours, snapshot{"hours", nil, i32(41)}},
		{"an expense", expense, snapshot{"expense", nil, i32(41)}},
		{"a milestone", milestone, snapshot{"milestone", nil, i32(42)}},
		{"a settlement's first line", energyFirst, snapshot{"settlement", nil, nil}},
		{"a settlement's second line", energySecond, snapshot{"settlement", nil, nil}},
		{"an order line's part", orderLine, snapshot{"order_line", i32(77), i32(43)}},
		{"a subscription period", period, snapshot{"subscription_period", i32(78), nil}},
		{"hours and a milestone of two projects", mixed, snapshot{"mixed", nil, nil}},
		{"an a-konto deducted", deduction, snapshot{"deduction", nil, nil}},
		{"a line of free text", manual, snapshot{"manual", nil, nil}},
	} {
		if got := read(c.line); got.kind != c.want.kind || !eq(got.taxCategory, c.want.taxCategory) || !eq(got.project, c.want.project) {
			t.Errorf("%s = %s/%v/%v, want %s/%v/%v", c.name, got.kind, deref32(got.taxCategory), deref32(got.project),
				c.want.kind, deref32(c.want.taxCategory), deref32(c.want.project))
		}
	}

	// A credit note's line takes its original's: issue the draft by hand,
	// then snapshot a credit-note line naming the order line.
	h.Exec(t, `UPDATE invoices.invoices SET status = 'issued', number = 77, issue_date = '2026-09-12', due_date = '2026-10-12',
		exchange_rate_date = '2026-09-12', seller_legal_name = 'Selger AS', buyer_name = 'Kunde AS', issued_at = now() WHERE id = $1`, draft)
	note := plantID(t, h, `INSERT INTO invoices.invoices (kind, customer_id, credits_invoice_id, created_by_user_id, created_at, updated_at)
		VALUES ('credit_note', $1, $2, gen_random_uuid(), now(), now()) RETURNING id`, customerAcme, draft)
	credit := plantID(t, h, `INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, vat_code_id,
		line_gross, line_allowance, line_net, credits_line_id) VALUES ($1, 1, 'Kreditert', 1, 100, 1, 100, 0, 100, $2) RETURNING id`, note, orderLine)
	var rate pgtype.Numeric
	if err := rate.Scan("25"); err != nil {
		t.Fatal(err)
	}
	if err := q.SnapshotLine(ctx, store.SnapshotLineParams{ID: credit, VatRatePercent: rate, VatCategory: str("S"), SafTCode: str("3")}); err != nil {
		t.Fatal(err)
	}
	if got := read(credit); got.kind != "order_line" || !eq(got.taxCategory, i32(77)) || !eq(got.project, i32(43)) {
		t.Errorf("the credit note's line = %+v, want its original's", got)
	}
	// And an issued line is never written again.
	err := q.SnapshotLine(ctx, store.SnapshotLineParams{ID: manual, VatRatePercent: rate, VatCategory: str("S"), SafTCode: str("3")})
	if err == nil {
		t.Error("a snapshot of an issued line went through")
	}
}

func deref32(p *int32) any {
	if p == nil {
		return nil
	}
	return *p
}

// fakeCatalog answers each variant's tax category, as Products would.
type fakeCatalog struct {
	contracts.ProductCatalog
	categories map[int32]int32
}

func (f fakeCatalog) Variants(_ context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	var out []contracts.VariantEntry
	for _, id := range ids {
		if c, ok := f.categories[id]; ok {
			out = append(out, contracts.VariantEntry{ID: id, ProductID: id, TaxCategoryID: c})
		}
	}
	return out, nil
}

// From an order line to an issued line: the From orders wizard keeps the
// tax category of the line's product on the held row, read through the
// catalog before the draft is written; the issue snapshots it, with the kind.
func TestSalesJournal_AnOrderLineRemembersItsProductsTaxCategory(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t, modtest.WithProducts(fakeCatalog{categories: map[int32]int32{501: 77}}))
	f.orders.put(acmeOrder(order2210, 2210, "PO-1", "2026-09-01"),
		orderLine{id: 1, description: "Hylle", unit: "stk", quantity: "2", price: "1000.00000000", discount: "0.00", vatCode: vat25},
		orderLine{id: 2, description: "Frakt", unit: "stk", quantity: "1", price: "100.00000000", discount: "0.00", vatCode: vat25})
	f.orders.mu.Lock()
	f.orders.variants[1] = 501
	f.orders.mu.Unlock()
	drafts := fromOrders(t, f.h, false, pick(order2210, 1, 2), pick(order2210, 2, 1))
	if got := f.h.Count(t, `SELECT count(*) FROM invoices.line_sources WHERE invoice_id = $1 AND tax_category_id = 77`, drafts[0].InvoiceID); got != 1 {
		t.Errorf("%d held rows with the category, want the product's line's one", got)
	}
	inv := issued(t, f.h, drafts[0].InvoiceID)
	docs, err := salesJournal(f.h).Documents(context.Background(), *inv.Number-1, 1)
	if err != nil || len(docs) != 1 {
		t.Fatalf("documents = %+v, %v", docs, err)
	}
	var withProduct, freeText int
	for _, l := range docs[0].Lines {
		switch {
		case l.Kind == contracts.LineKindOrderLine && l.TaxCategoryID != nil && *l.TaxCategoryID == 77:
			withProduct++
		case l.Kind == contracts.LineKindOrderLine && l.TaxCategoryID == nil:
			freeText++
		}
	}
	if withProduct != 1 || freeText != 1 {
		t.Errorf("lines = %+v, want one order line with the product's category and one without", docs[0].Lines)
	}
}
