package accounting_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
)

// Delivery B: Invoices' sales journal posts itself (A12–A16, R1, R3, R4, R5,
// R7, R9, R14). Each test feeds the fake journal documents and facts, runs
// the catch-up worker as module.Workers builds it, and reads the vouchers
// back through the API, to the øre.

// day is a date for a test.
func day(t *testing.T, text string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, text)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// catchUpWorker is the catch-up as worker mode builds it (module.Workers),
// never through the harness's own deps alone: a contract that only Compose
// resolves would be nil here, as it was once in production.
func catchUpWorker(t *testing.T, h *harness) *accounting.CatchUpWorker {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if w.Name() == "accounting.catchup" {
			cw, ok := w.(*accounting.CatchUpWorker)
			if !ok {
				t.Fatalf("accounting.catchup is a %T", w)
			}
			return cw
		}
	}
	t.Fatal("module.Workers builds no accounting.catchup")
	return nil
}

// catchUp runs the worker once the sweep is due: five minutes and a second
// after the last run, so no wake-up is needed.
func catchUp(t *testing.T, h *harness) {
	t.Helper()
	h.Advance(5*time.Minute + time.Second)
	if err := catchUpWorker(t, h).RunOnce(t.Context()); err != nil {
		t.Fatalf("catch-up: %v", err)
	}
}

// line and summary build a document's lines and VAT summaries.
func jline(position int32, net, saft string) contracts.JournalLine {
	return contracts.JournalLine{ID: int64(position), Position: position, NetNOK: net, SAFT: saft, Kind: contracts.LineKindManual}
}

func jsum(saft, rate, base, vat string) contracts.JournalVATSummary {
	return contracts.JournalVATSummary{SAFT: saft, RatePercent: rate, BaseNOK: base, VATNOK: vat}
}

// invoice is an issued invoice for Acme.
func invoice(id, number int64, issued time.Time, lines []contracts.JournalLine, sums ...contracts.JournalVATSummary) contracts.JournalDocument {
	due := issued.AddDate(0, 0, 14)
	return contracts.JournalDocument{ID: id, Number: number, Kind: contracts.JournalInvoice, IssueDate: issued, DueDate: &due,
		CustomerID: customerAcme, Currency: "NOK", Lines: lines, VATSummaries: sums}
}

// voucherOf is the system voucher of a source, through the API.
func voucherOf(t *testing.T, b *books, kind string, id int64) (gen.AccountingVoucher, bool) {
	t.Helper()
	vid := b.h.Count(t, `SELECT coalesce(max(id), 0)::int FROM accounting.vouchers
		WHERE source_module = 'invoices' AND source_kind = $1 AND source_id = $2`, kind, id)
	if vid == 0 {
		return gen.AccountingVoucher{}, false
	}
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, vid), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("voucher %d = %d %s", vid, res.Status, res.Body)
	}
	var v gen.AccountingVoucher
	res.JSON(&v)
	return v, true
}

// postingsOf is a voucher's postings as "account amount[ code role spec]".
func postingsOf(v gen.AccountingVoucher) []string {
	var out []string
	for _, p := range v.Postings {
		s := p.AccountNumber + " " + p.Amount
		if p.TaxCode != nil {
			s += " " + *p.TaxCode + " " + string(*p.TaxRole)
		}
		if p.TaxSpecification != nil {
			s += " " + *p.TaxSpecification
		}
		out = append(out, s)
	}
	return out
}

// expectVoucher fails unless the source's voucher has exactly want, in order.
func expectVoucher(t *testing.T, b *books, kind string, id int64, want ...string) gen.AccountingVoucher {
	t.Helper()
	v, ok := voucherOf(t, b, kind, id)
	if !ok {
		t.Fatalf("no voucher for %s %d", kind, id)
	}
	if got := postingsOf(v); !slices.Equal(got, want) {
		t.Fatalf("%s %d posted\n  %s\nwant\n  %s", kind, id, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	return v
}

// position is a stream's cursor.
func position(t *testing.T, b *books, stream string) int {
	t.Helper()
	return b.h.Count(t, `SELECT coalesce(max(position), 0)::int FROM accounting.cursors WHERE stream = $1`, stream)
}

// A two-rate invoice (A13, R3): receivables for the summaries' gross on the
// invoice's own item, each line's net to its default rule's account with its
// code, each summary's VAT to its code's account, a zero line left out; and
// the rounding between the lines' nets and the summaries' bases on the
// largest line.
func TestSales_InvoiceWithMixedRatesAndRounding(t *testing.T) {
	b := newBooks(t)
	issued := day(t, "2026-03-02")
	b.h.journal.addDocument(invoice(9001, 101, issued,
		[]contracts.JournalLine{jline(1, "1000.00", "3"), jline(2, "200.00", "31"), jline(3, "0.00", "3")},
		jsum("3", "25.00", "1000.00", "250.00"), jsum("31", "15.00", "200.00", "30.00")))
	b.h.journal.addDocument(invoice(9002, 102, issued,
		[]contracts.JournalLine{jline(1, "0.33", "3"), jline(2, "0.33", "3"), jline(3, "0.34", "3"), jline(4, "0.33", "3")},
		jsum("3", "25.00", "1.34", "0.34")))
	catchUp(t, b.h)
	v := expectVoucher(t, b, "invoice", 9001,
		"1500 1480.00", "3000 -1000.00 3 base", "3010 -200.00 31 base", "2700 -250.00 3 tax", "2701 -30.00 31 tax")
	if v.Kind != "sales_invoice" || deref(v.Rule) != "sales.invoice" || deref(v.RuleVersion) != 1 || v.SourceDocumentId == nil ||
		*v.SourceDocumentId != 9001 || v.VoucherDate.String() != "2026-03-02" || !strings.Contains(v.Postings[1].Description, "v1") {
		t.Errorf("voucher = %+v", v)
	}
	// The lines' nets are 1.33, the summary's base 1.34: the øre goes on the
	// largest line, so the voucher follows the document's own totals.
	expectVoucher(t, b, "invoice", 9002,
		"1500 1.68", "3000 -0.33 3 base", "3000 -0.33 3 base", "3000 -0.35 3 base", "3000 -0.33 3 base", "2700 -0.34 3 tax")
	if got := position(t, b, "invoices.documents"); got != 102 {
		t.Errorf("documents cursor = %d, want 102", got)
	}
	// The customers report links each item to its invoice.
	res := b.c.Do(http.MethodGet, base+"/reports/customers?from=2026-01-01&to=2026-12-31", nil)
	var rep gen.AccountingReport
	res.JSON(&rep)
	linked := 0
	for _, r := range rep.Rows {
		if r.DocumentId != nil && (*r.DocumentId == 9001 || *r.DocumentId == 9002) {
			linked++
		}
	}
	if linked != 2 {
		t.Errorf("the customers report links %d items to their invoices, want 2: %s", linked, res.Body)
	}
	// An invoice links to its vouchers.
	res = b.c.Do(http.MethodGet, base+"/vouchers?sourceDocumentId=9001", nil)
	var list gen.AccountingVoucherList
	res.JSON(&list)
	if list.Total != 1 || list.Items[0].SourceDocumentId == nil {
		t.Errorf("the invoice's vouchers = %+v", list)
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// A document of zeros posts nothing and the cursor moves past it (R3); a
// document dated before the books start is skipped (A13).
func TestSales_ZeroAndEarlyDocumentsPostNothing(t *testing.T) {
	b := newBooks(t)
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "0.00", "3")},
		jsum("3", "25.00", "0.00", "0.00")))
	b.h.journal.addDocument(invoice(9002, 102, day(t, "2025-12-30"), []contracts.JournalLine{jline(1, "100.00", "3")},
		jsum("3", "25.00", "100.00", "25.00")))
	catchUp(t, b.h)
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vouchers"); n != 0 {
		t.Errorf("%d vouchers, want none", n)
	}
	if got := position(t, b, "invoices.documents"); got != 102 {
		t.Errorf("cursor = %d, want past both", got)
	}
}

// A credit note on a partly paid invoice (A13): every sign turned, on the
// invoice's item, whose balance is then what is left.
func TestSales_CreditNoteOnAPartlyPaidInvoice(t *testing.T) {
	b := newBooks(t)
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "1000.00", "3")},
		jsum("3", "25.00", "1000.00", "250.00")))
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPayment, SubjectID: 501, Date: day(t, "2026-03-10"),
		InvoiceID: 9001, InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "500.00", Source: contracts.PaymentSourceOCR,
		BankAccount: "15030012345"})
	b.h.journal.addDocument(contracts.JournalDocument{ID: 9002, Number: 102, Kind: contracts.JournalCreditNote,
		IssueDate: day(t, "2026-03-12"), CustomerID: customerAcme, Currency: "NOK", CreditsID: 9001, CreditsNumber: 101,
		Lines: []contracts.JournalLine{jline(1, "500.00", "3")}, VATSummaries: []contracts.JournalVATSummary{jsum("3", "25.00", "500.00", "125.00")}})
	catchUp(t, b.h)
	expectVoucher(t, b, "payment", 501, "1920 500.00", "1500 -500.00")
	v := expectVoucher(t, b, "credit_note", 9002, "1500 -625.00", "3000 500.00 3 base", "2700 125.00 3 tax")
	if v.Kind != "sales_credit_note" || *v.SourceDocumentId != 9001 {
		t.Errorf("credit note = %+v", v)
	}
	if bal := itemBalance(t, b, 9001); bal != "125.00" {
		t.Errorf("the invoice's item = %s, want 125.00", bal)
	}
	// A document's vouchers (L3): the invoice's page lists its own, its
	// payment's and its credit note's; the credit note's page its own, which
	// belongs to the invoice it credits.
	for doc, want := range map[int64][]string{9001: {"credit_note", "payment", "invoice"}, 9002: {"credit_note"}} {
		var list gen.AccountingVoucherList
		b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers?sourceDocumentId=%d", base, doc), nil).JSON(&list)
		var kinds []string
		for _, v := range list.Items {
			kinds = append(kinds, v.Source.Kind)
		}
		if !slices.Equal(kinds, want) || int(list.Total) != len(want) {
			t.Errorf("the vouchers of %d = %v (total %d), want %v", doc, kinds, list.Total, want)
		}
	}
}

// itemBalance is the balance of the item standing for an Invoices document.
func itemBalance(t *testing.T, b *books, document int64) string {
	t.Helper()
	var out string
	if err := b.h.Pool().QueryRow(t.Context(), `SELECT coalesce(sum(p.amount), 0)::text FROM accounting.postings p
		JOIN accounting.open_items o ON o.id = p.open_item_id WHERE o.source_document_id = $1`, document).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Payments and their removals (A13, R11): a Vipps payment through Vipps
// clearing, a manual one to the bank; a removal after its period locked is
// booked on the first open day, keeping its own date.
func TestSales_PaymentsRemovalsAndTheLock(t *testing.T) {
	b := newBooks(t)
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-01-05"), []contracts.JournalLine{jline(1, "1000.00", "3")},
		jsum("3", "25.00", "1000.00", "250.00")))
	pay := func(id int64, kind string, date, amount, source string) {
		b.h.journal.addFact(contracts.JournalFact{Kind: kind, SubjectID: id, Date: day(t, date), InvoiceID: 9001,
			InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: amount, Source: source})
	}
	pay(501, contracts.FactPayment, "2026-01-10", "250.00", contracts.PaymentSourceVipps)
	pay(502, contracts.FactPayment, "2026-01-12", "100.00", contracts.PaymentSourceManual)
	catchUp(t, b.h)
	expectVoucher(t, b, "payment", 501, "1580 250.00", "1500 -250.00")
	expectVoucher(t, b, "payment", 502, "1920 100.00", "1500 -100.00")

	if res := b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-01-31"}); res.Status != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Status, res.Body)
	}
	pay(502, contracts.FactPaymentRemoved, "2026-01-20", "100.00", contracts.PaymentSourceManual)
	catchUp(t, b.h)
	v := expectVoucher(t, b, "payment_removed", 502, "1920 -100.00", "1500 100.00")
	if v.VoucherDate.String() != "2026-02-01" || v.DocumentDate.String() != "2026-01-20" || deref(v.Rule) != "sales.payment_removed" {
		t.Errorf("removal = %s on %s, rule %v", v.DocumentDate, v.VoucherDate, v.Rule)
	}
	if bal := itemBalance(t, b, 9001); bal != "1000.00" {
		t.Errorf("item = %s", bal)
	}
}

// Charges post as cash received on one account, code 7 (R9, R14); a removal
// reverses it.
func TestSales_ChargePayments(t *testing.T) {
	b := newBooks(t)
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactChargePayment, SubjectID: 801, Date: day(t, "2026-04-02"),
		InvoiceID: 9001, InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "105.00", Source: contracts.PaymentSourceOCR})
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactChargePaymentRemoved, SubjectID: 801, Date: day(t, "2026-04-03"),
		InvoiceID: 9001, InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "105.00", Source: contracts.PaymentSourceOCR})
	catchUp(t, b.h)
	v := expectVoucher(t, b, "charge_payment", 801, "1920 105.00", "3900 -105.00 7 base")
	if v.Kind != "charge" {
		t.Errorf("kind = %s", v.Kind)
	}
	expectVoucher(t, b, "charge_payment_removed", 801, "1920 -105.00", "3900 105.00 7 base")
}

// The books' start and the opening (R5, R14): an invoice from before the
// books paid after them posts on its opening item; a backdated payment
// registered after the opening's snapshot is booked on the books' start; one
// the snapshot reflects is not posted again.
func TestSales_TheOpeningAndBackdatedPayments(t *testing.T) {
	b := newBooks(t)
	b.h.journal.open = []contracts.OpenDocument{
		{ID: 77, Number: 1042, CustomerID: customerAcme, IssueDate: day(t, "2025-12-01"), DueDate: day(t, "2025-12-15"), OpenNOK: "1250.00"},
		{ID: 78, Number: 1043, CustomerID: customerAcme, IssueDate: day(t, "2025-12-02"), DueDate: day(t, "2025-12-16"), OpenNOK: "400.00"},
	}
	b.h.journal.seqs = []int64{5}
	res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1500"), "amount": "1250", "customerId": customerAcme, "documentNumber": "1042", "sourceDocumentId": 77},
		{"accountId": b.acc("1500"), "amount": "400", "customerId": customerAcme, "documentNumber": "1043", "sourceDocumentId": 78},
	}})
	if res.Status != http.StatusCreated {
		t.Fatalf("opening = %d %s", res.Status, res.Body)
	}
	fact := func(seq, id, invoice int64, date, amount string) {
		b.h.journal.addFact(contracts.JournalFact{Seq: seq, Kind: contracts.FactPayment, SubjectID: id, Date: day(t, date),
			InvoiceID: invoice, InvoiceNumber: invoice + 965, CustomerID: customerAcme, AmountNOK: amount, Source: contracts.PaymentSourceManual})
	}
	fact(4, 601, 77, "2025-12-10", "250.00") // in the opening amount already
	fact(6, 602, 77, "2026-02-03", "1250.00")
	fact(7, 603, 78, "2025-12-20", "400.00") // backdated, registered after the snapshot
	catchUp(t, b.h)
	if _, ok := voucherOf(t, b, "payment", 601); ok {
		t.Error("a payment the opening reflects was posted again")
	}
	expectVoucher(t, b, "payment", 602, "1920 1250.00", "1500 -1250.00")
	v := expectVoucher(t, b, "payment", 603, "1920 400.00", "1500 -400.00")
	if v.VoucherDate.String() != "2026-01-01" || v.DocumentDate.String() != "2025-12-20" {
		t.Errorf("backdated payment on %s (%s), want the books' start", v.VoucherDate, v.DocumentDate)
	}
	for _, doc := range []int64{77, 78} {
		if bal := itemBalance(t, b, doc); bal != "0.00" {
			t.Errorf("item of %d = %s, want settled", doc, bal)
		}
	}
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.open_items WHERE source_document_id IN (77, 78)"); n != 2 {
		t.Errorf("%d items, want the opening's two", n)
	}
}

// Write-offs (A16, R4) on a two-rate invoice paid in part: without the VAT
// correction the whole open amount to bad debt; with it, shared over the
// summaries by gross, each share's VAT back on its code's account marked
// tapPåKrav; a recovery credits bad debt and gives the VAT back in
// proportion; the undo reverses; a rounding goes to the rounding account.
func TestSales_WriteOffs(t *testing.T) {
	b := newBooks(t)
	twoRates := func(id, number int64) {
		b.h.journal.addDocument(invoice(id, number, day(t, "2026-02-02"),
			[]contracts.JournalLine{jline(1, "1000.00", "3"), jline(2, "1000.00", "31")},
			jsum("3", "25.00", "1000.00", "250.00"), jsum("31", "15.00", "1000.00", "150.00")))
	}
	twoRates(9001, 101)
	twoRates(9002, 102)
	b.h.journal.addDocument(invoice(9003, 103, day(t, "2026-02-02"), []contracts.JournalLine{jline(1, "0.80", "3")},
		jsum("3", "25.00", "0.80", "0.20")))
	fact := func(kind string, id, invoice int64, date, amount string, w *contracts.JournalWriteOff) {
		b.h.journal.addFact(contracts.JournalFact{Kind: kind, SubjectID: id, Date: day(t, date), InvoiceID: invoice,
			InvoiceNumber: invoice - 8900, CustomerID: customerAcme, AmountNOK: amount, Source: contracts.PaymentSourceManual, WriteOff: w})
	}
	fact(contracts.FactPayment, 501, 9001, "2026-03-01", "400.00", nil)
	fact(contracts.FactPayment, 502, 9002, "2026-03-01", "400.00", nil)
	plain := &contracts.JournalWriteOff{ID: 31, Reason: contracts.WriteOffLoss, AmountNOK: "2000.00"}
	corrected := &contracts.JournalWriteOff{ID: 32, Reason: contracts.WriteOffLoss, VATCorrection: true, AmountNOK: "2000.00"}
	rounding := &contracts.JournalWriteOff{ID: 33, Reason: contracts.WriteOffRounding, AmountNOK: "1.00"}
	fact(contracts.FactWriteOff, 31, 9001, "2026-06-30", "2000.00", plain)
	fact(contracts.FactWriteOff, 32, 9002, "2026-06-30", "2000.00", corrected)
	fact(contracts.FactWriteOff, 33, 9003, "2026-06-30", "1.00", rounding)
	fact(contracts.FactRecoveredPayment, 503, 9002, "2026-08-01", "500.00", corrected)
	fact(contracts.FactRecoveredPayment, 504, 9001, "2026-08-02", "300.00", plain)
	fact(contracts.FactRecoveredPaymentRemoved, 504, 9001, "2026-08-03", "300.00", plain)
	fact(contracts.FactWriteOffUndone, 33, 9003, "2026-08-04", "1.00", rounding)
	catchUp(t, b.h)

	v := expectVoucher(t, b, "write_off", 31, "1500 -2000.00", "7830 2000.00")
	if v.Kind != "write_off" || deref(v.Rule) != "sales.write_off" {
		t.Errorf("plain write-off = %s %v", v.Kind, v.Rule)
	}
	// 2000 over gross 1250 and 1150 of 2400: 1041.67 and 958.33; their VAT
	// 208.33 and 125.00.
	v = expectVoucher(t, b, "write_off", 32, "1500 -2000.00",
		"7830 833.34 3 base tapPåKrav", "2700 208.33 3 tax tapPåKrav",
		"7830 833.33 31 base tapPåKrav", "2701 125.00 31 tax tapPåKrav")
	if deref(v.Rule) != "sales.write_off_vat" {
		t.Errorf("corrected write-off rule = %v", v.Rule)
	}
	expectVoucher(t, b, "write_off", 33, "1500 -1.00", "7790 1.00")
	// 500 of 2000 recovered: a quarter of each line, the last taking the
	// rounding.
	expectVoucher(t, b, "recovered_payment", 503, "1920 500.00",
		"7830 -208.34 3 base tapPåKrav", "2700 -52.08 3 tax tapPåKrav",
		"7830 -208.33 31 base tapPåKrav", "2701 -31.25 31 tax tapPåKrav")
	expectVoucher(t, b, "recovered_payment", 504, "1920 300.00", "7830 -300.00")
	expectVoucher(t, b, "recovered_payment_removed", 504, "1920 -300.00", "7830 300.00")
	expectVoucher(t, b, "write_off_undone", 33, "1500 1.00", "7790 -1.00")
	for doc, want := range map[int64]string{9001: "0.00", 9002: "0.00", 9003: "1.00"} {
		if bal := itemBalance(t, b, doc); bal != want {
			t.Errorf("item of %d = %s, want %s", doc, bal, want)
		}
	}
}

// One bad fact does not stop the books (A15): an unknown tax code and a kind
// this version does not know wait under Needs attention while the cursor
// moves on and later facts post; a rule's inactive account too, and Retry
// posts it once the account is active again. There is no dismissal.
func TestSales_NeedsAttentionAndRetry(t *testing.T) {
	b := newBooks(t)
	res := b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Hours", "scope": "sales", "match": "line_kind",
		"lineKind": "hours", "accountId": b.acc("3400")})
	if res.Status != http.StatusCreated {
		t.Fatalf("rule = %d %s", res.Status, res.Body)
	}
	acct := b.chart["3400"]
	setActive := func(active bool) {
		t.Helper()
		res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/accounts/%d", base, acct.Id), nil)
		res.JSON(&acct)
		res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/accounts/%d", base, acct.Id), map[string]any{"number": acct.Number,
			"name": acct.Name, "kind": acct.Kind, "active": active, "defaultTaxCodeId": acct.DefaultTaxCodeId, "revision": acct.Revision})
		if res.Status != http.StatusOK {
			t.Fatalf("account active %v = %d %s", active, res.Status, res.Body)
		}
	}
	setActive(false)
	hours := jline(1, "800.00", "3")
	hours.Kind = contracts.LineKindHours
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "100.00", "99")},
		jsum("99", "25.00", "100.00", "25.00")))
	b.h.journal.addDocument(invoice(9002, 102, day(t, "2026-03-02"), []contracts.JournalLine{hours},
		jsum("3", "25.00", "800.00", "200.00")))
	b.h.journal.addDocument(invoice(9003, 103, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "100.00", "3")},
		jsum("3", "25.00", "100.00", "25.00")))
	// The unknown kind is another invoice's: a later fact of the same invoice
	// would wait behind it (R15, TestSales_WhatWaitsWaitsInOrder).
	b.h.journal.addFact(contracts.JournalFact{Kind: "refund", SubjectID: 1, Date: day(t, "2026-03-03"), InvoiceID: 9004,
		InvoiceNumber: 104, CustomerID: customerAcme, AmountNOK: "5.00"})
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPayment, SubjectID: 501, Date: day(t, "2026-03-04"), InvoiceID: 9003,
		InvoiceNumber: 103, CustomerID: customerAcme, AmountNOK: "125.00", Source: contracts.PaymentSourceManual})
	catchUp(t, b.h)
	expectVoucher(t, b, "invoice", 9003, "1500 125.00", "3000 -100.00 3 base", "2700 -25.00 3 tax")
	expectVoucher(t, b, "payment", 501, "1920 125.00", "1500 -125.00")
	if got := position(t, b, "invoices.facts"); got != 2 {
		t.Errorf("facts cursor = %d, want past the unknown kind", got)
	}
	res = b.c.Do(http.MethodGet, base+"/unposted", nil)
	var list gen.AccountingUnpostedList
	res.JSON(&list)
	reasons := map[string]string{}
	var hoursID int64
	for _, u := range list.Items {
		reasons[u.Source.Kind+fmt.Sprint(u.Source.Id)] = u.Reason
		if u.Source.Id == 9002 {
			hoursID = u.Id
		}
	}
	if list.Total != 3 || reasons["invoice9001"] != "tax_code_unknown" || reasons["invoice9002"] != "account_inactive" ||
		reasons["refund1"] != "unknown_kind" {
		t.Fatalf("needs attention = %s", res.Body)
	}
	var meta gen.AccountingMeta
	b.c.Do(http.MethodGet, base+"/meta", nil).JSON(&meta)
	if meta.Unposted != 3 {
		t.Errorf("meta counts %d, want 3", meta.Unposted)
	}

	// A retry before the cause is fixed fails again; after, it posts.
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/retry", base, hoursID), nil)
	var u gen.AccountingUnposted
	res.JSON(&u)
	if res.Status != http.StatusOK || u.PostedVoucher != nil || u.Attempts != 2 {
		t.Fatalf("retry too early = %d %s", res.Status, res.Body)
	}
	setActive(true)
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/retry", base, hoursID), nil)
	res.JSON(&u)
	if res.Status != http.StatusOK || u.PostedVoucher == nil {
		t.Fatalf("retry = %d %s", res.Status, res.Body)
	}
	v := expectVoucher(t, b, "invoice", 9002, "1500 1000.00", "3400 -800.00 3 base", "2700 -200.00 3 tax")
	if !strings.Contains(v.Postings[1].Description, "Hours v1") {
		t.Errorf("the revenue line names %q, want the rule and its version", v.Postings[1].Description)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/retry", base, hoursID), nil),
		http.StatusConflict, "unposted_already_posted")
	res = b.c.Do(http.MethodGet, base+"/unposted?open=false", nil)
	res.JSON(&list)
	if list.Total != 3 {
		t.Errorf("all = %d", list.Total)
	}
	if got := b.c.Do(http.MethodPost, base+"/unposted/999999/retry", nil).Status; got != http.StatusNotFound {
		t.Errorf("retry of nothing = %d", got)
	}
}

// The posting rules (A14, R15): precedence line kind, tax category, tax
// code, then the defaults by tax code; a sales rule names an income account;
// a change of name or account is a new version and changes future vouchers
// only, each posting naming the rule and version that placed it; a rule is
// archived, never deleted, a default one not even that; books set up before
// the rules existed are seeded by the worker, never by a read.
func TestPostingRules(t *testing.T) {
	b := newBooks(t)
	// Books set up before the rules existed: a read writes nothing (L1), the
	// catch-up worker seeds them.
	b.h.Exec(t, `ALTER TABLE accounting.posting_rules DISABLE TRIGGER tr_posting_rules_no_delete`)
	b.h.Exec(t, `DELETE FROM accounting.posting_rule_versions`)
	b.h.Exec(t, `DELETE FROM accounting.posting_rules`)
	b.h.Exec(t, `ALTER TABLE accounting.posting_rules ENABLE TRIGGER tr_posting_rules_no_delete`)
	var rules gen.AccountingPostingRuleList
	b.c.Do(http.MethodGet, base+"/posting-rules", nil).JSON(&rules)
	if len(rules.Items) != 0 {
		t.Fatalf("a read seeded %d rules", len(rules.Items))
	}
	catchUp(t, b.h)
	res := b.c.Do(http.MethodGet, base+"/posting-rules", nil)
	res.JSON(&rules)
	if len(rules.Items) != 10 {
		t.Fatalf("the worker seeded %d rules, want the ten defaults", len(rules.Items))
	}
	defaults := map[string]string{}
	for _, r := range rules.Items {
		if r.Match == gen.Default {
			defaults[deref(r.TaxCode)] = r.AccountNumber
		}
	}
	if defaults["3"] != "3000" || defaults["5"] != "3100" || defaults["52"] != "3150" || defaults["6"] != "3200" || defaults["33"] != "3020" {
		t.Fatalf("defaults = %v", defaults)
	}
	create := func(body map[string]any) gen.AccountingPostingRule {
		t.Helper()
		res := b.c.Do(http.MethodPost, base+"/posting-rules", body)
		if res.Status != http.StatusCreated {
			t.Fatalf("create %v = %d %s", body, res.Status, res.Body)
		}
		var r gen.AccountingPostingRule
		res.JSON(&r)
		return r
	}
	byCode := create(map[string]any{"name": "Code 3 to 3600", "scope": "sales", "match": "tax_code", "taxCode": "3", "accountId": b.acc("3600")})
	create(map[string]any{"name": "Category 7", "scope": "sales", "match": "tax_category", "taxCategoryId": 7, "accountId": b.acc("3700")})
	create(map[string]any{"name": "Milestones", "scope": "sales", "match": "line_kind", "lineKind": "milestone", "accountId": b.acc("3400")})
	expectProblem(t, b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Code 3 to 3600", "scope": "sales",
		"match": "tax_code", "taxCode": "31", "accountId": b.acc("3600")}), http.StatusConflict, "posting_rule_name_taken")
	expectProblem(t, b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Again", "scope": "sales",
		"match": "tax_code", "taxCode": "3", "accountId": b.acc("3600")}), http.StatusConflict, "posting_rule_match_taken")
	expectProblem(t, b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Bad", "scope": "sales",
		"match": "tax_code", "taxCode": "99", "accountId": b.acc("3600")}), http.StatusBadRequest, "tax_code_unknown")
	expectProblem(t, b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Exp", "scope": "expense",
		"match": "tax_code", "taxCode": "1", "accountId": b.acc("6300")}), http.StatusBadRequest, "")
	// A sales rule credits income (R15): an expense or a balance account is
	// refused, on a new rule and on a change.
	for _, number := range []string{"6300", "1920"} {
		res := b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "To " + number, "scope": "sales",
			"match": "tax_code", "taxCode": "31", "accountId": b.acc(number)})
		if res.Status != http.StatusBadRequest || !strings.Contains(string(res.Body), "account_kind_mismatch") {
			t.Errorf("a sales rule to %s = %d %s, want 400 account_kind_mismatch", number, res.Status, res.Body)
		}
	}
	if res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/posting-rules/%d", base, byCode.Id), map[string]any{"name": byCode.Name,
		"accountId": b.acc("6300"), "revision": byCode.Revision}); res.Status != http.StatusBadRequest ||
		!strings.Contains(string(res.Body), "account_kind_mismatch") {
		t.Errorf("a change to 6300 = %d %s, want 400 account_kind_mismatch", res.Status, res.Body)
	}
	for _, r := range rules.Items {
		if r.Match == gen.Default {
			expectProblem(t, b.c.Do(http.MethodDelete, fmt.Sprintf("%s/posting-rules/%d", base, r.Id), nil),
				http.StatusConflict, "posting_rule_default")
			break
		}
	}

	mixed := func(id, number int64) {
		milestone := jline(1, "100.00", "3")
		milestone.Kind = contracts.LineKindMilestone
		milestone.TaxCategoryID = ptrTo(int32(7))
		category := jline(2, "100.00", "3")
		category.TaxCategoryID = ptrTo(int32(7))
		b.h.journal.addDocument(invoice(id, number, day(t, "2026-03-02"),
			[]contracts.JournalLine{milestone, category, jline(3, "100.00", "3"), jline(4, "100.00", "33")},
			jsum("3", "25.00", "300.00", "75.00"), jsum("33", "12.00", "100.00", "12.00")))
	}
	mixed(9001, 101)
	catchUp(t, b.h)
	expectVoucher(t, b, "invoice", 9001, "1500 487.00", "3400 -100.00 3 base", "3700 -100.00 3 base",
		"3600 -100.00 3 base", "3020 -100.00 33 base", "2700 -75.00 3 tax", "2702 -12.00 33 tax")

	// A new account is a new version, for what comes after.
	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/posting-rules/%d", base, byCode.Id), map[string]any{"name": byCode.Name,
		"accountId": b.acc("3000"), "revision": byCode.Revision})
	var changed gen.AccountingPostingRule
	res.JSON(&changed)
	if res.Status != http.StatusOK || changed.Version != 2 || changed.AccountNumber != "3000" {
		t.Fatalf("change = %d %s", res.Status, res.Body)
	}
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/posting-rules/%d", base, byCode.Id), map[string]any{"name": "x",
		"accountId": b.acc("3000"), "revision": byCode.Revision}), http.StatusConflict, "revision_conflict")
	mixed(9002, 102)
	catchUp(t, b.h)
	v := expectVoucher(t, b, "invoice", 9002, "1500 487.00", "3400 -100.00 3 base", "3700 -100.00 3 base",
		"3000 -100.00 3 base", "3020 -100.00 33 base", "2700 -75.00 3 tax", "2702 -12.00 33 tax")
	if !strings.Contains(v.Postings[3].Description, "v2") {
		t.Errorf("the line names %q, want version 2", v.Postings[3].Description)
	}
	old := expectVoucher(t, b, "invoice", 9001, "1500 487.00", "3400 -100.00 3 base", "3700 -100.00 3 base",
		"3600 -100.00 3 base", "3020 -100.00 33 base", "2700 -75.00 3 tax", "2702 -12.00 33 tax")

	// A new name is a new version too (R15), and the posting stores the rule
	// and the version that placed it, not only its text.
	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/posting-rules/%d", base, byCode.Id), map[string]any{"name": "Standard sales",
		"accountId": b.acc("3000"), "revision": changed.Revision})
	res.JSON(&changed)
	if res.Status != http.StatusOK || changed.Version != 3 || changed.Name != "Standard sales" {
		t.Fatalf("rename = %d %s", res.Status, res.Body)
	}
	placed := func(p gen.AccountingPosting) string {
		return fmt.Sprintf("%d v%d %s", deref(p.PostingRuleId), deref(p.PostingRuleVersion), deref(p.PostingRuleName))
	}
	if got, want := placed(old.Postings[3]), fmt.Sprintf("%d v1 Code 3 to 3600", byCode.Id); got != want {
		t.Errorf("invoice 101's line was placed by %q, want %q", got, want)
	}
	if got, want := placed(v.Postings[3]), fmt.Sprintf("%d v2 Code 3 to 3600", byCode.Id); got != want {
		t.Errorf("invoice 102's line was placed by %q, want %q", got, want)
	}
	if old.Postings[0].PostingRuleId != nil || old.Postings[5].PostingRuleId != nil {
		t.Errorf("a receivables or a VAT line names a rule: %v %v", old.Postings[0].PostingRuleId, old.Postings[5].PostingRuleId)
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.posting_rule_versions WHERE rule_id = $1`, byCode.Id); n != 3 {
		t.Errorf("the rule keeps %d versions, want 3", n)
	}

	// Archived, never deleted: it matches nothing from now on, is no longer
	// listed, frees its match, and what it placed still names it.
	if res := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/posting-rules/%d", base, byCode.Id), nil); res.Status != http.StatusNoContent {
		t.Errorf("archive = %d %s", res.Status, res.Body)
	}
	if got := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/posting-rules/%d", base, byCode.Id), nil).Status; got != http.StatusNotFound {
		t.Errorf("archive again = %d", got)
	}
	b.c.Do(http.MethodGet, base+"/posting-rules", nil).JSON(&rules)
	for _, r := range rules.Items {
		if r.Id == byCode.Id {
			t.Errorf("the archived rule is listed: %+v", r)
		}
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.posting_rules WHERE id = $1`, byCode.Id); n != 1 {
		t.Errorf("the archived rule's row: %d, want kept", n)
	}
	create(map[string]any{"name": "Code 3 again", "scope": "sales", "match": "tax_code", "taxCode": "3", "accountId": b.acc("3600")})
	if got, want := placed(voucherAgain(t, b, old.Id).Postings[3]), fmt.Sprintf("%d v1 Code 3 to 3600", byCode.Id); got != want {
		t.Errorf("after the archive invoice 101's line names %q, want %q", got, want)
	}
}

// voucherAgain is a voucher read again.
func voucherAgain(t *testing.T, b *books, id int64) gen.AccountingVoucher {
	t.Helper()
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("voucher %d = %d %s", id, res.Status, res.Body)
	}
	var v gen.AccountingVoucher
	res.JSON(&v)
	return v
}

func ptrTo[T any](v T) *T { return &v }

// The catch-up's guarantees (A12, R7, R12): nothing before the books are set
// up; a fact delivered twice, and two workers at once, post one voucher; the
// sweep alone catches up with no event; a handler only asks; a crashed
// claim's lease runs out; the status says where each stream stands.
func TestCatchUp_Guarantees(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	h.journal.addDocument(invoice(9001, 101, time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), []contracts.JournalLine{jline(1, "100.00", "3")},
		jsum("3", "25.00", "100.00", "25.00")))
	if err := catchUpWorker(t, h).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, "SELECT count(*) FROM accounting.cursors"); n != 0 {
		t.Fatalf("the worker ran before the books were set up: %d cursors", n)
	}
	setUp(t, c, "en")
	b := &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}

	// A handler only asks; the claim takes the wake-up and runs at once.
	handlers := accounting.Module().EventHandlers(h.Deps())
	if len(handlers) != 4 {
		t.Fatalf("%d handlers, want Invoices' three and Expenses' one", len(handlers))
	}
	if err := db.WithTx(t.Context(), h.Pool(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		return handlers[0].Handle(t.Context(), tx, events.Delivered{ID: 1, Name: "invoices.document.issued", Version: 1})
	}); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, "SELECT count(*) FROM accounting.catchup_requests WHERE stream = 'invoices.documents'"); n != 1 {
		t.Fatalf("the handler wrote %d requests", n)
	}
	if err := catchUpWorker(t, h).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := voucherOf(t, b, "invoice", 9001); !ok {
		t.Fatal("the first run posted nothing")
	}
	if n := h.Count(t, "SELECT count(*) FROM accounting.catchup_requests"); n != 0 {
		t.Errorf("%d requests left", n)
	}

	// Not due: no wake-up and the sweep not yet: a new document waits.
	h.journal.addDocument(invoice(9002, 102, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), []contracts.JournalLine{jline(1, "100.00", "3")},
		jsum("3", "25.00", "100.00", "25.00")))
	if err := catchUpWorker(t, h).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := voucherOf(t, b, "invoice", 9002); ok {
		t.Error("a stream that was not due ran")
	}
	var status gen.AccountingCatchUp
	c.Do(http.MethodGet, base+"/catch-up", nil).JSON(&status)
	if len(status.Streams) != 3 || status.Streams[2].Stream != "expenses.facts" || status.Streams[0].Position != 101 || deref(status.Streams[0].Behind) != 1 || status.Streams[0].LastRunAt == nil {
		t.Errorf("status = %+v", status)
	}
	// The sweep alone — no event, as after a late handler or a pruned one.
	catchUp(t, h)
	if _, ok := voucherOf(t, b, "invoice", 9002); !ok {
		t.Fatal("the sweep did not catch up")
	}

	// The same document delivered again (the cursor put back) and two
	// workers at once: one voucher each.
	h.Exec(t, "UPDATE accounting.cursors SET position = 0")
	h.Advance(6 * time.Minute)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = catchUpWorker(t, h).RunOnce(context.Background())
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, "SELECT count(*) FROM accounting.vouchers"); n != 2 {
		t.Errorf("%d vouchers after a repeat and two workers, want 2", n)
	}

	// A claim that crashed holds the stream until its lease runs out.
	h.journal.addDocument(invoice(9003, 103, time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC), []contracts.JournalLine{jline(1, "100.00", "3")},
		jsum("3", "25.00", "100.00", "25.00")))
	h.Exec(t, "UPDATE accounting.cursors SET lease_id = gen_random_uuid(), lease_until = $1 WHERE stream = 'invoices.documents'",
		h.Now().Add(10*time.Minute))
	catchUp(t, h)
	if _, ok := voucherOf(t, b, "invoice", 9003); ok {
		t.Error("a held stream ran")
	}
	h.Advance(5 * time.Minute)
	catchUp(t, h)
	if _, ok := voucherOf(t, b, "invoice", 9003); !ok {
		t.Error("the crashed claim's lease never ran out")
	}

	// A journal that cannot be read stops the run and says why; nothing
	// moves.
	h.journal.mu.Lock()
	h.journal.fail = errors.New("journal down")
	h.journal.mu.Unlock()
	h.Advance(6 * time.Minute)
	if err := catchUpWorker(t, h).RunOnce(t.Context()); err == nil {
		t.Error("a failed read was not reported")
	}
	c.Do(http.MethodGet, base+"/catch-up", nil).JSON(&status)
	if status.Streams[0].LastError == nil || status.Streams[0].Behind != nil {
		t.Errorf("status after a failed read = %+v", status.Streams[0])
	}
}

// The new operations' permissions: reads with accounting:read, a retry with
// accounting:post, the rules with accounting:close.
func TestPermissions_Sales(t *testing.T) {
	h := newHarness(t)
	setUp(t, h.SignIn(t, accountant...), "en")
	none := h.SignIn(t)
	read := h.SignIn(t, reader...)
	post := h.SignIn(t, poster...)
	for _, p := range []string{"/posting-rules", "/unposted", "/catch-up"} {
		if got := none.Do(http.MethodGet, base+p, nil).Status; got != http.StatusForbidden {
			t.Errorf("GET %s without permission = %d", p, got)
		}
	}
	if got := read.Do(http.MethodPost, base+"/unposted/1/retry", nil).Status; got != http.StatusForbidden {
		t.Errorf("a reader's retry = %d", got)
	}
	for _, c := range []struct{ method, path string }{{"POST", "/posting-rules"}, {"PUT", "/posting-rules/1001"}, {"DELETE", "/posting-rules/1001"}} {
		var body any = map[string]any{}
		if c.method == http.MethodDelete {
			body = nil
		}
		if got := post.Do(c.method, base+c.path, body).Status; got != http.StatusForbidden {
			t.Errorf("a poster's %s %s = %d", c.method, c.path, got)
		}
	}
}

// A payment the bank feed brought is a bank payment, as a bank file's is
// (bank feed design F6): it posts to the bank, and its charge payment too.
func TestSales_BankFeedPaymentsPostToTheBank(t *testing.T) {
	b := newBooks(t)
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-01-05"), []contracts.JournalLine{jline(1, "1000.00", "3")},
		jsum("3", "25.00", "1000.00", "250.00")))
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPayment, SubjectID: 601, Date: day(t, "2026-01-15"),
		InvoiceID: 9001, InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "400.00",
		Source: contracts.PaymentSourceBankFeed, BankAccount: "15030012345"})
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactChargePayment, SubjectID: 802, Date: day(t, "2026-01-15"),
		InvoiceID: 9001, InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "35.00",
		Source: contracts.PaymentSourceBankFeed, BankAccount: "15030012345"})
	catchUp(t, b.h)
	expectVoucher(t, b, "payment", 601, "1920 400.00", "1500 -400.00")
	expectVoucher(t, b, "charge_payment", 802, "1920 35.00", "3900 -35.00 7 base")
	if bal := itemBalance(t, b, 9001); bal != "850.00" {
		t.Errorf("the invoice's item = %s, want 850.00", bal)
	}
}

// What waits, waits in order (R15): an invoice that cannot be posted — no
// rule matches its line — holds back its payment, which waits under Needs
// attention behind it while another invoice's payment posts; a retry of the
// payment alone keeps it waiting; once a rule exists, retrying the invoice
// posts it and then its payment, the invoice first.
func TestSales_WhatWaitsWaitsInOrder(t *testing.T) {
	b := newBooks(t)
	// Code 1 is a purchase code: no sales rule matches it.
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "100.00", "1")},
		jsum("1", "0.00", "100.00", "0.00")))
	b.h.journal.addDocument(invoice(9002, 102, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "100.00", "3")},
		jsum("3", "25.00", "100.00", "25.00")))
	pay := func(id, invoice int64, amount string) {
		b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPayment, SubjectID: id, Date: day(t, "2026-03-05"),
			InvoiceID: invoice, InvoiceNumber: invoice - 8900, CustomerID: customerAcme, AmountNOK: amount,
			Source: contracts.PaymentSourceManual})
	}
	pay(501, 9001, "100.00")
	pay(502, 9002, "125.00")
	catchUp(t, b.h)

	expectVoucher(t, b, "payment", 502, "1920 125.00", "1500 -125.00")
	if _, ok := voucherOf(t, b, "payment", 501); ok {
		t.Fatal("the payment of the invoice under Needs attention posted before it")
	}
	if got := position(t, b, "invoices.facts"); got != 2 {
		t.Errorf("facts cursor = %d, want past the waiting payment", got)
	}
	rows := func() map[string]gen.AccountingUnposted {
		t.Helper()
		var list gen.AccountingUnpostedList
		b.c.Do(http.MethodGet, base+"/unposted", nil).JSON(&list)
		out := map[string]gen.AccountingUnposted{}
		for _, u := range list.Items {
			out[fmt.Sprintf("%s %d", u.Source.Kind, u.Source.Id)] = u
		}
		return out
	}
	open := rows()
	inv, payment := open["invoice 9001"], open["payment 501"]
	if len(open) != 2 || inv.Reason != "posting_rule_missing" || payment.Reason != "waiting_on" ||
		deref(payment.WaitingOn) != inv.Id || deref(payment.InvoiceId) != 9001 {
		t.Fatalf("needs attention = %+v", open)
	}

	// The payment alone cannot jump the queue.
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/retry", base, payment.Id), nil)
	var u gen.AccountingUnposted
	res.JSON(&u)
	if res.Status != http.StatusOK || u.PostedVoucher != nil || u.Reason != "waiting_on" {
		t.Fatalf("retry of the payment first = %d %s", res.Status, res.Body)
	}

	if res := b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Code 1 sales", "scope": "sales",
		"match": "tax_code", "taxCode": "1", "accountId": b.acc("3000")}); res.Status != http.StatusCreated {
		t.Fatalf("rule = %d %s", res.Status, res.Body)
	}
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/retry", base, inv.Id), nil)
	res.JSON(&u)
	if res.Status != http.StatusOK || u.PostedVoucher == nil {
		t.Fatalf("retry of the invoice = %d %s", res.Status, res.Body)
	}
	invoiceVoucher := expectVoucher(t, b, "invoice", 9001, "1500 100.00", "3000 -100.00 1 base")
	paymentVoucher := expectVoucher(t, b, "payment", 501, "1920 100.00", "1500 -100.00")
	if paymentVoucher.Number <= invoiceVoucher.Number {
		t.Errorf("the payment is voucher %s, the invoice %s: want the invoice first", paymentVoucher.Display, invoiceVoucher.Display)
	}
	if left := rows(); len(left) != 0 {
		t.Errorf("still under Needs attention: %+v", left)
	}
	if bal := itemBalance(t, b, 9001); bal != "0.00" {
		t.Errorf("invoice 101's item = %s, want settled", bal)
	}
}

// A backlog asks for itself again (R15): a run that stops at its lease's
// margin with items left leaves a wake-up behind, so the next poll — not the
// five-minute sweep — takes the rest up.
func TestCatchUp_ABacklogAsksForItselfAgain(t *testing.T) {
	b := newBooks(t)
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "100.00", "3")},
		jsum("3", "25.00", "100.00", "25.00")))
	worker := catchUpWorker(t, b.h)
	b.h.Advance(5*time.Minute + time.Second)
	// A lease shorter than the margin: the run stops before its first item.
	restore := accounting.SetCatchUpLease(10 * time.Second)
	if err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	restore()
	if got := position(t, b, "invoices.documents"); got != 0 {
		t.Fatalf("documents cursor = %d, want the run stopped at its margin", got)
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.catchup_requests WHERE stream = 'invoices.documents'`); n != 1 {
		t.Fatalf("%d wake-ups for the documents, want the backlog's own", n)
	}
	// Seconds later, well before the sweep: the wake-up is due.
	b.h.Advance(15 * time.Second)
	if err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	expectVoucher(t, b, "invoice", 9001, "1500 125.00", "3000 -100.00 3 base", "2700 -25.00 3 tax")
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.catchup_requests`); n != 0 {
		t.Errorf("%d wake-ups left after a run that read to the end", n)
	}
}

// An undo turns back the stored voucher (R15), line for line, whatever the
// accounts say now: a write-off whose tax code's VAT account moved since is
// undone on the account it was posted to.
func TestSales_AnUndoReversesTheStoredVoucher(t *testing.T) {
	b := newBooks(t)
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-02-02"), []contracts.JournalLine{jline(1, "1000.00", "3")},
		jsum("3", "25.00", "1000.00", "250.00")))
	w := &contracts.JournalWriteOff{ID: 31, Reason: contracts.WriteOffLoss, VATCorrection: true, AmountNOK: "1250.00"}
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactWriteOff, SubjectID: 31, Date: day(t, "2026-06-30"),
		InvoiceID: 9001, InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "1250.00", Source: contracts.PaymentSourceManual, WriteOff: w})
	catchUp(t, b.h)
	written := expectVoucher(t, b, "write_off", 31, "1500 -1250.00", "7830 1000.00 3 base tapPåKrav", "2700 250.00 3 tax tapPåKrav")

	b.h.Exec(t, `UPDATE accounting.tax_codes SET vat_account_id = $1 WHERE code = '3'`, b.acc("2701"))
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactWriteOffUndone, SubjectID: 31, Date: day(t, "2026-07-02"),
		InvoiceID: 9001, InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "1250.00", Source: contracts.PaymentSourceManual, WriteOff: w})
	catchUp(t, b.h)
	undone := expectVoucher(t, b, "write_off_undone", 31, "1500 1250.00", "7830 -1000.00 3 base tapPåKrav", "2700 -250.00 3 tax tapPåKrav")
	if undone.Reverses == nil || undone.Reverses.Id != written.Id {
		t.Errorf("the undo reverses %+v, want voucher %s", undone.Reverses, written.Display)
	}
	if bal := itemBalance(t, b, 9001); bal != "1250.00" {
		t.Errorf("the invoice's item = %s, want open again", bal)
	}
}
