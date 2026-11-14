package accounting_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The bank's harder cases: a part payment in a foreign currency, Vipps
// payouts of some days' sales, matches taken back by what turns their
// vouchers back, a movement read from two origins, rules kept off customer
// payments, a VAT term paid once, pages of lines.

// suggestionOf is a line's suggestion of a kind, which it must have.
func suggestionOf(t *testing.T, l gen.AccountingBankLine, kind string) gen.AccountingBankSuggestion {
	t.Helper()
	for _, sg := range l.Suggestions {
		if sg.Kind == kind {
			return sg
		}
	}
	t.Fatalf("line %s %s: no %s suggestion in %+v", l.BookedOn, l.Amount, kind, l.Suggestions)
	return gen.AccountingBankSuggestion{}
}

// hasSuggestion is whether a line has a suggestion of a kind.
func hasSuggestion(l gen.AccountingBankLine, kind string) bool {
	for _, sg := range l.Suggestions {
		if sg.Kind == kind {
			return true
		}
	}
	return false
}

// acceptWith accepts a suggestion with a body's extras.
func acceptWith(c *modtest.Client, line int64, body map[string]any) *modtest.Response {
	return c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/accept", base, line), body)
}

// csvLines imports CSV lines in latinMapping's shape on the account.
func csvLines(t *testing.T, b *books, account int64, name string, rows ...string) gen.AccountingBankImportResult {
	t.Helper()
	if res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/csv-mapping", base, account), latinMapping); res.Status != http.StatusOK {
		t.Fatalf("mapping = %d %s", res.Status, res.Body)
	}
	return imported(t, b, name, []byte("x\nDato;Tekst;Ut;Inn;Konto\n"+strings.Join(rows, "\n")+"\n"), account)
}

// TestReconcile_ForeignSupplierInstalments: 100 EUR at 11.5 paid in two
// instalments. 470 NOK implies 4.70 for all that is left — far from 11.5 —
// so the person gives the 40 EUR it settles and the invoice stays owed 60;
// 680 NOK for those 60 is within 5 % of the rate and settles them, the
// difference a gain.
func TestReconcile_ForeignSupplierInstalments(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	eur := supplier(t, b, map[string]any{"name": "Cloud GmbH", "country": "DE", "currency": "EUR", "bankAccount": "70580001112"})
	inv := draft(t, b, map[string]any{"supplierId": eur.Id, "kind": "invoice", "invoiceNumber": "INV-77", "invoiceDate": "2026-08-20",
		"exchangeRate": "11.5", "total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}})
	postInvoice(t, b, inv.Id)
	csvLines(t, b, op.Id, "eur.csv", "15.09.2026;Cloud GmbH INV-77;470,00;;70580001112", "20.09.2026;Cloud GmbH INV-77;680,00;;70580001112")

	part := lineOf(t, reconcile(t, b, op.Id), "2026-09-15", "-470.00")
	sg := suggestionOf(t, part, "supplier_invoice")
	if !sg.NeedsAmountCurrency || deref(sg.Currency) != "EUR" || deref(sg.AmountCurrency) != "40.87" || sg.Certainty == "exact" {
		t.Fatalf("part payment suggestion = %+v", sg)
	}
	expectProblem(t, acceptOn(b.c, part.Id, sg.Key), http.StatusBadRequest, "bank_amount_currency_required")
	res := acceptWith(b.c, part.Id, map[string]any{"key": sg.Key, "amountCurrency": "40.00"})
	if res.Status != http.StatusOK {
		t.Fatalf("accept with the amount = %d %s", res.Status, res.Body)
	}
	var line gen.AccountingBankLine
	res.JSON(&line)
	expectPostings(t, "first instalment", matchedVoucher(t, b, line), "2400 460.00", "1920 -470.00", "8160 10.00")
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil)
	var after gen.AccountingSupplierInvoice
	res.JSON(&after)
	if after.State != "posted" || deref(after.Balance) != "-690.00" {
		t.Fatalf("after the first instalment = %+v", after)
	}

	rest := lineOf(t, reconcile(t, b, op.Id), "2026-09-20", "-680.00")
	sg = suggestionOf(t, rest, "supplier_invoice")
	if sg.NeedsAmountCurrency || deref(sg.AmountCurrency) != "60.00" {
		t.Fatalf("last instalment suggestion = %+v", sg)
	}
	line = accepted(t, b, rest.Id, sg.Key)
	expectPostings(t, "last instalment", matchedVoucher(t, b, line), "2400 690.00", "1920 -680.00", "8060 -10.00")
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil)
	res.JSON(&after)
	if after.State != "paid" {
		t.Fatalf("after the last instalment = %+v", after)
	}
}

// TestReconcile_VippsSettlementDays: three days of Vipps sales outstanding
// and a payout of the first day's less its fee: the fee is the first day's
// sales over the payout, and only they leave the clearing account; the next
// payout takes the next day. A payout no run of days explains moves only
// itself, its fee given by the person and held to 5 % of it.
func TestReconcile_VippsSettlementDays(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	for _, s := range [][2]string{{"2026-09-04", "1000.00"}, {"2026-09-05", "1500.00"}, {"2026-09-06", "2000.00"}} {
		mustPost(t, b.c, s[0], "Vipps-salg", line{"accountId": b.acc("1580"), "amount": s[1]},
			line{"accountId": b.acc("3000"), "amount": "-" + s[1]})
	}
	csvLines(t, b, op.Id, "vipps.csv", "07.09.2026;Utbetaling Vipps;;980,00;", "08.09.2026;Utbetaling Vipps;;1 470,00;",
		"09.09.2026;Utbetaling Vipps;;1 234,00;")

	r := reconcile(t, b, op.Id)
	one := lineOf(t, r, "2026-09-07", "980.00")
	sg := suggestionOf(t, one, "vipps_payout")
	if sg.Key != "vipps:2026-09-04..2026-09-04" || deref(sg.Fee) != "20.00" || deref(sg.FeeMax) != "49.00" {
		t.Fatalf("first payout = %+v", sg)
	}
	expectPostings(t, "first payout", matchedVoucher(t, b, accepted(t, b, one.Id, sg.Key)), "1920 980.00", "7770 20.00", "1580 -1000.00")
	if bal := b.balances(t, "2026-09-30")["1580"]; bal != "3500.00" {
		t.Fatalf("the clearing after the first payout = %s", bal)
	}

	two := lineOf(t, reconcile(t, b, op.Id), "2026-09-08", "1470.00")
	sg = suggestionOf(t, two, "vipps_payout")
	if sg.Key != "vipps:2026-09-05..2026-09-05" || deref(sg.Fee) != "30.00" {
		t.Fatalf("second payout = %+v", sg)
	}
	expectPostings(t, "second payout", matchedVoucher(t, b, accepted(t, b, two.Id, sg.Key)), "1920 1470.00", "7770 30.00", "1580 -1500.00")

	three := lineOf(t, reconcile(t, b, op.Id), "2026-09-09", "1234.00")
	sg = suggestionOf(t, three, "vipps_payout")
	if sg.Key != "vipps:nofee" || deref(sg.Fee) != "0.00" || sg.Certainty != "possible" || !strings.Contains(sg.Reason, "settlement report") &&
		!strings.Contains(sg.Reason, "oppgjørsrapporten") {
		t.Fatalf("a payout no day explains = %+v", sg)
	}
	expectProblem(t, acceptWith(b.c, three.Id, map[string]any{"key": sg.Key, "fee": "61.71"}), http.StatusBadRequest, "bank_fee_too_large")
	res := acceptWith(b.c, three.Id, map[string]any{"key": sg.Key, "fee": "16.00"})
	if res.Status != http.StatusOK {
		t.Fatalf("accept with a fee = %d %s", res.Status, res.Body)
	}
	var l gen.AccountingBankLine
	res.JSON(&l)
	expectPostings(t, "third payout", matchedVoucher(t, b, l), "1920 1234.00", "7770 16.00", "1580 -1250.00")
	if bal := b.balances(t, "2026-09-30")["1580"]; bal != "750.00" {
		t.Fatalf("the clearing after three payouts = %s", bal)
	}
}

// TestReconcile_TakenBackWithTheirVouchers: a supplier payment removed on the
// invoice's page, a customer payment removed in Invoices and a voucher
// reversed by hand each reopen the line matched to them, with a note; undoing
// a transfer whose other side was matched to its posting opens both; the
// other side then gets no second transfer.
func TestReconcile_TakenBackWithTheirVouchers(t *testing.T) {
	b, op, _ := paymentBooks(t)
	s := supplier(t, b, map[string]any{"name": "Kontorrekvisita AS", "bankAccount": "86011117947"})
	inv := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "4711", "invoiceDate": "2026-08-25",
		"kid": "4711000058", "total": "6250.00",
		"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("1"), "amount": "6250.00", "entry": "gross"}}})
	postInvoice(t, b, inv.Id)
	fee := mustPost(t, b.c, "2026-09-04", "Gebyr", line{"accountId": b.acc("1920"), "amount": "-95.00"},
		line{"accountId": b.acc("7770"), "amount": "95.00"})
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	r := reconcile(t, b, op.Id)

	// The supplier payment, removed on its invoice's page.
	pay := lineOf(t, r, "2026-09-03", "-6250.00")
	accepted(t, b, pay.Id, first(t, pay, "supplier_invoice").Key)
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil)
	var si gen.AccountingSupplierInvoice
	res.JSON(&si)
	if res := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/supplier-invoices/%d/payments/%d", base, inv.Id, si.Payments[0].Id), nil); res.Status != http.StatusOK {
		t.Fatalf("remove = %d %s", res.Status, res.Body)
	}
	if l := lineOf(t, reconcile(t, b, op.Id), "2026-09-03", "-6250.00"); l.State != "open" || l.Match != nil || l.Note == "" {
		t.Fatalf("the payment's line after the removal = %+v", l)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/undo", base, pay.Id), nil), http.StatusConflict, "bank_line_not_matched")

	// The batch, one of whose payments is removed in Invoices.
	batch := lineOf(t, r, "2026-09-02", "3750.00")
	accepted(t, b, batch.Id, first(t, batch, "batch").Key)
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPaymentRemoved, SubjectID: 701, Date: day(t, "2026-09-05"),
		InvoiceID: 9101, InvoiceNumber: 201, CustomerID: customerAcme, AmountNOK: "1250.00", Source: contracts.PaymentSourceOCR,
		BankAccount: operating, BankReference: "OCR-0"})
	catchUp(t, b.h)
	again := lineOf(t, reconcile(t, b, op.Id), "2026-09-02", "3750.00")
	if again.State != "open" || again.Match != nil || again.Note == "" || hasSuggestion(again, "batch") {
		t.Fatalf("the batch after a payment's removal = %+v", again)
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.statement_match_postings WHERE standing`); n != 0 {
		t.Fatalf("standing links after the removal = %d", n)
	}

	// A posting matched, its voucher reversed by hand.
	feeLine := lineOf(t, r, "2026-09-04", "-95.00")
	accepted(t, b, feeLine.Id, fmt.Sprintf("posting:%d", postingOn(t, b, fee, "1920")))
	if res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, fee.Id), map[string]any{"date": "2026-09-04",
		"reason": "Feil"}); res.Status != http.StatusCreated && res.Status != http.StatusOK {
		t.Fatalf("reverse = %d %s", res.Status, res.Body)
	}
	if l := lineOf(t, reconcile(t, b, op.Id), "2026-09-04", "-95.00"); l.State != "open" || l.Note == "" {
		t.Fatalf("the fee after the reversal = %+v", l)
	}
}

// postingOn is a voucher's posting on an account.
func postingOn(t *testing.T, b *books, v gen.AccountingVoucher, account string) int64 {
	t.Helper()
	return int64(b.h.Count(t, `SELECT id::int FROM accounting.postings WHERE voucher_id = $1 AND account_id = $2`, v.Id, b.acc(account)))
}

// TestReconcile_TransferMatchedOnEachSide: a transfer posted from the
// operating account before the savings statement came, its other side
// matched later to its posting — and offered no second transfer; undone on
// the operating side, both lines open.
func TestReconcile_TransferMatchedOnEachSide(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	sp := bankAccount(t, b, "Sparing", savings, "1950")
	tr := lineOf(t, reconcile(t, b, op.Id), "2026-09-06", "-15000.00")
	sg := first(t, tr, "transfer")
	if sg.Key != fmt.Sprintf("transfer:account:%d", sp.Id) {
		t.Fatalf("transfer = %+v", sg)
	}
	accepted(t, b, tr.Id, sg.Key)
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	other := lineOf(t, reconcile(t, b, sp.Id), "2026-09-06", "15000.00")
	if hasSuggestion(other, "transfer") {
		t.Fatalf("the other side is offered a second transfer: %+v", other.Suggestions)
	}
	accepted(t, b, other.Id, first(t, other, "posting").Key)
	undone(t, b, tr.Id)
	if l := lineOf(t, reconcile(t, b, sp.Id), "2026-09-06", "15000.00"); l.State != "open" || l.Match != nil {
		t.Fatalf("the other side after the undo = %+v", l)
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.statement_matches WHERE undone_at IS NULL`); n != 0 {
		t.Fatalf("standing matches = %d", n)
	}
}

// TestReconcile_AcceptAgainstAReversal: an accept of a posting and a
// reversal of its voucher — one after the other, or at once — never leave a
// line matched to a reversed voucher.
func TestReconcile_AcceptAgainstAReversal(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	for round := range 6 {
		fee := mustPost(t, b.c, "2026-09-04", fmt.Sprintf("Gebyr %d", round), line{"accountId": b.acc("1920"), "amount": "-95.00"},
			line{"accountId": b.acc("7770"), "amount": "95.00"})
		l := lineOf(t, reconcile(t, b, op.Id), "2026-09-04", "-95.00")
		key := fmt.Sprintf("posting:%d", postingOn(t, b, fee, "1920"))
		var wg sync.WaitGroup
		wg.Add(2)
		acceptDone := make(chan struct{})
		go func() {
			defer wg.Done()
			acceptOn(b.c, l.Id, key)
			close(acceptDone)
		}()
		if round%2 == 1 {
			<-acceptDone
		}
		go func() {
			defer wg.Done()
			b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, fee.Id), map[string]any{"date": "2026-09-04", "reason": "Feil"})
		}()
		wg.Wait()
		if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.statement_match_postings mp
			JOIN accounting.postings p ON p.id = mp.posting_id
			WHERE mp.standing AND EXISTS (SELECT 1 FROM accounting.vouchers rv WHERE rv.reverses_voucher_id = p.voucher_id)`); n != 0 {
			t.Fatalf("round %d: %d links stand on a reversed voucher", round, n)
		}
		if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.statement_lines WHERE state = 'matched'`); n != 0 {
			t.Fatalf("round %d: %d lines matched", round, n)
		}
	}
}

// TestBankImport_AcrossOrigins: the feed starts after what files brought and
// never earlier; a file imported after the switch brings only the days
// before the feed's; a CSV line that repeats a camt.053 one is flagged, gets
// no suggestion and no bulk accept, and is set aside or kept by a person.
func TestBankImport_AcrossOrigins(t *testing.T) {
	feed := &fakeFeed{status: contracts.BankFeedStatus{Connected: true, Provider: "folio", Interval: 30 * time.Minute},
		accounts: []bankfeed.Account{{Number: "NO4615030012345", Name: "Drift", Kind: bankfeed.AccountOperational}}}
	h := newHarness(t, modtest.WithBankFeed(feed))
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	b := &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
	op := bankAccount(t, b, "Drift", operating, "1920")
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)

	// A CSV export of the same fee: flagged, not matched.
	out := csvLines(t, b, op.Id, "fee.csv", "04.09.2026;Gebyr nettbank september;95,00;;", "05.09.2026;Husleie;8 000,00;;70580001112")
	if s := out.Statements[0]; s.Imported != 2 || s.PossibleDuplicates != 1 {
		t.Fatalf("csv = %+v", s)
	}
	mustPost(t, b.c, "2026-09-04", "Gebyr", line{"accountId": b.acc("1920"), "amount": "-95.00"}, line{"accountId": b.acc("7770"), "amount": "95.00"})
	var dup, camt gen.AccountingBankLine
	for _, l := range reconcile(t, b, op.Id).Lines {
		if l.BookedOn.String() == "2026-09-04" && l.Amount == "-95.00" {
			if l.Source == "csv" {
				dup = l
			} else {
				camt = l
			}
		}
	}
	if deref(dup.PossibleDuplicateOf) != camt.Id || len(dup.Suggestions) != 0 || len(camt.Suggestions) == 0 {
		t.Fatalf("the duplicate = %+v, the original = %+v", dup, camt)
	}
	res := b.c.Do(http.MethodPost, base+"/bank/lines/accept-exact", map[string]any{"lineIds": []int64{dup.Id}})
	var bulk gen.AccountingBankBulkAcceptResult
	res.JSON(&bulk)
	if len(bulk.Accepted) != 0 {
		t.Fatalf("bulk took a possible duplicate: %+v", bulk)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/code", base, dup.Id), map[string]any{"key": "x",
		"lines": []map[string]any{{"accountId": b.acc("7770"), "amount": "95.00"}}}), http.StatusConflict, "bank_line_possible_duplicate")
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/duplicate", base, dup.Id), map[string]any{"duplicate": true})
	var set gen.AccountingBankLine
	res.JSON(&set)
	if res.Status != http.StatusOK || set.State != "duplicate" {
		t.Fatalf("set aside = %d %+v", res.Status, set)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/duplicate", base, dup.Id), map[string]any{"duplicate": false}),
		http.StatusConflict, "bank_line_not_duplicate")
	// The same fee again in another export is its own movement, kept.
	out = csvLines(t, b, op.Id, "fee2.csv", "04.09.2026;Gebyr nettbank september;95,00;;", "04.09.2026;Gebyr nettbank september;95,00;;")
	kept := int64(0)
	for _, l := range reconcile(t, b, op.Id).Lines {
		if l.Source == "csv" && l.State == "open" && l.PossibleDuplicateOf != nil {
			kept = l.Id
		}
	}
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/duplicate", base, kept), map[string]any{"duplicate": false})
	res.JSON(&set)
	if res.Status != http.StatusOK || set.State != "open" || set.PossibleDuplicateOf != nil {
		t.Fatalf("kept = %d %+v", res.Status, set)
	}

	// The feed starts after the files: by default the day after the last.
	expectProblem(t, c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", base, op.Id), map[string]any{"source": "feed",
		"startDay": "2026-09-05"}), http.StatusBadRequest, "bank_feed_start_too_early")
	res = c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", base, op.Id), map[string]any{"source": "feed"})
	var switched gen.AccountingBankAccount
	res.JSON(&switched)
	if res.Status != http.StatusOK || switched.FeedStartDay == nil || switched.FeedStartDay.String() != "2026-09-08" {
		t.Fatalf("switch = %d %+v", res.Status, switched)
	}
	// A file afterwards brings only the days before the feed's.
	late := csvLines(t, b, op.Id, "late.csv", "07.09.2026;Kaffe;45,00;;", "08.09.2026;Kaffe;45,00;;", "09.09.2026;Kaffe;45,00;;")
	if s := late.Statements[0]; s.Imported != 1 || s.CoveredByFeed != 2 {
		t.Fatalf("a file after the switch = %+v", s)
	}
}

// TestBankRules_NotForCustomerPayments: a rule for money in may not code to
// income or receivables; money in with a KID, or of a customer's open item,
// gets no rule's suggestion; a coded line to receivables names its item.
func TestBankRules_NotForCustomerPayments(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	for _, body := range []map[string]any{
		{"name": "Salg", "textContains": "vipps", "direction": "in", "accountId": b.acc("3000")},
		{"name": "Kunder", "textContains": "vipps", "accountId": b.acc("1500")},
	} {
		expectProblem(t, b.c.Do(http.MethodPost, base+"/bank/rules", body), http.StatusBadRequest, "bank_rule_customer_payment")
	}
	for _, body := range []map[string]any{
		{"name": "Renter", "textContains": "vipps", "direction": "in", "accountId": b.acc("8050")},
		{"name": "Innbetalt", "counterpartyContains": "acme", "direction": "in", "accountId": b.acc("8050")},
	} {
		if res := b.c.Do(http.MethodPost, base+"/bank/rules", body); res.Status != http.StatusCreated {
			t.Fatalf("rule = %d %s", res.Status, res.Body)
		}
	}
	// An invoice of 4 850 is owed by a customer: the Vipps line of that
	// amount is no rule's.
	b.h.journal.addDocument(invoice(9401, 401, day(t, "2026-08-20"), []contracts.JournalLine{jline(1, "3880.00", "3")},
		jsum("3", "25.00", "3880.00", "970.00")))
	catchUp(t, b.h)
	imported(t, b, "sb1.xml", fixture(t, "sparebank1-camt053-001-08.xml"), 0)
	r := reconcile(t, b, op.Id)
	if l := lineOf(t, r, "2026-09-08", "4850.00"); hasSuggestion(l, "rule") {
		t.Fatalf("a rule for money of a customer's open item: %+v", l.Suggestions)
	}
	if l := lineOf(t, r, "2026-09-12", "2000.00"); hasSuggestion(l, "rule") {
		t.Fatalf("a rule for money in with a KID: %+v", l.Suggestions)
	}
	card := lineOf(t, r, "2026-09-11", "-349.00")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/code", base, card.Id), map[string]any{"key": "r",
		"lines": []map[string]any{{"accountId": b.acc("2400"), "amount": "349.00", "supplierId": b.aSupplier(t).Id}}}),
		http.StatusBadRequest, "bank_open_item_required")
}

// TestReconcile_VATTermPaidOnce: a VAT term paid through the bank is not
// offered to a second payment of the same amount.
func TestReconcile_VATTermPaidOnce(t *testing.T) {
	b, op, _ := vatPaid(t)
	vat := lineOf(t, reconcile(t, b, op.Id), "2026-09-10", "-21512.00")
	accepted(t, b, vat.Id, first(t, vat, "vat").Key)
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.statement_matches WHERE vat_closing_id IS NOT NULL AND undone_at IS NULL`); n != 1 {
		t.Fatalf("matches naming the closing = %d", n)
	}
	csvLines(t, b, op.Id, "again.csv", "20.09.2026;Skatteetaten mva;21 512,00;;")
	if l := lineOf(t, reconcile(t, b, op.Id), "2026-09-20", "-21512.00"); hasSuggestion(l, "vat") {
		t.Fatalf("a paid term is offered again: %+v", l.Suggestions)
	}
}

// TestReconcile_PagesAndAccountsInUse: the lines come in pages with their
// total; a bank account with lines keeps its number and ledger account; an
// inactive one with open lines still counts in the status.
func TestReconcile_PagesAndAccountsInUse(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	imported(t, b, "sb1.xml", fixture(t, "sparebank1-camt053-001-08.xml"), 0)
	page := func(offset, limit int) gen.AccountingBankReconciliation {
		res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d/reconciliation?from=2026-09-01&to=2026-09-30&offset=%d&limit=%d",
			base, op.Id, offset, limit), nil)
		if res.Status != http.StatusOK {
			t.Fatalf("page = %d %s", res.Status, res.Body)
		}
		var out gen.AccountingBankReconciliation
		res.JSON(&out)
		return out
	}
	if p := page(0, 4); len(p.Lines) != 4 || p.Total != 11 || p.Lines[0].BookedOn.String() != "2026-09-02" {
		t.Fatalf("first page = %d lines of %d", len(p.Lines), p.Total)
	}
	if p := page(8, 4); len(p.Lines) != 3 || p.Total != 11 {
		t.Fatalf("last page = %d lines of %d", len(p.Lines), p.Total)
	}
	if p := page(20, 4); len(p.Lines) != 0 || p.Total != 11 {
		t.Fatalf("past the end = %d lines of %d", len(p.Lines), p.Total)
	}
	got := func() gen.AccountingBankAccount {
		res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), nil)
		var out gen.AccountingBankAccount
		res.JSON(&out)
		return out
	}
	a := got()
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), map[string]any{"name": "Drift",
		"accountNumber": operating, "ledgerAccountId": b.acc("1950"), "revision": a.Revision}), http.StatusConflict, "bank_account_in_use")
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), map[string]any{"name": "Drift",
		"accountNumber": savings, "ledgerAccountId": b.acc("1920"), "revision": a.Revision}), http.StatusConflict, "bank_account_in_use")
	res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), map[string]any{"name": "Gammel drift",
		"accountNumber": operating, "ledgerAccountId": b.acc("1920"), "active": false, "revision": a.Revision})
	if res.Status != http.StatusOK {
		t.Fatalf("rename and make inactive = %d %s", res.Status, res.Body)
	}
	res = b.c.Do(http.MethodGet, base+"/bank/status?through=2026-09-30", nil)
	var st gen.AccountingBankStatus
	res.JSON(&st)
	if st.Reconciled || len(st.Accounts) != 1 || st.Accounts[0].BankAccountId != op.Id || st.Accounts[0].OpenLines != 11 {
		t.Fatalf("status = %+v", st)
	}
}

// TestReconcile_RecoveryRemovedOnItsAccount: a recovered payment received on
// the savings account is removed on that account, and the line matched to it
// opens.
func TestReconcile_RecoveryRemovedOnItsAccount(t *testing.T) {
	b := newBooks(t)
	bankAccount(t, b, "Drift", operating, "1920")
	sp := bankAccount(t, b, "Sparing", savings, "1950")
	b.h.journal.addDocument(invoice(9501, 501, day(t, "2026-06-02"), []contracts.JournalLine{jline(1, "12000.00", "3")},
		jsum("3", "25.00", "12000.00", "3000.00")))
	plain := &contracts.JournalWriteOff{ID: 41, Reason: contracts.WriteOffLoss, AmountNOK: "15000.00"}
	fact := func(kind string, id int64, date string) {
		b.h.journal.addFact(contracts.JournalFact{Kind: kind, SubjectID: id, Date: day(t, date), InvoiceID: 9501, InvoiceNumber: 501,
			CustomerID: customerAcme, AmountNOK: "15000.00", Source: contracts.PaymentSourceManual, WriteOff: plain,
			BankAccount: savings})
	}
	fact(contracts.FactWriteOff, 41, "2026-08-31")
	fact(contracts.FactRecoveredPayment, 601, "2026-09-06")
	catchUp(t, b.h)
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	l := lineOf(t, reconcile(t, b, sp.Id), "2026-09-06", "15000.00")
	accepted(t, b, l.Id, first(t, l, "posting").Key)
	fact(contracts.FactRecoveredPaymentRemoved, 601, "2026-09-07")
	catchUp(t, b.h)
	expectVoucher(t, b, "recovered_payment_removed", 601, "1950 -15000.00", "7830 15000.00")
	if l := lineOf(t, reconcile(t, b, sp.Id), "2026-09-06", "15000.00"); l.State != "open" || l.Note == "" {
		t.Fatalf("the line after the removal = %+v", l)
	}
}
