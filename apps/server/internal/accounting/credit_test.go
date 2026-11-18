package accounting_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// creditItemBalance is the balance of customer's credit item, "" with none.
func creditItemBalance(t *testing.T, b *books, customer int32) string {
	t.Helper()
	var out string
	if err := b.h.Pool().QueryRow(t.Context(), `SELECT coalesce((SELECT sum(p.amount) FROM accounting.postings p
		JOIN accounting.open_items o ON o.id = p.open_item_id WHERE o.customer_credit AND o.customer_id = $1), 0)::text`,
		customer).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The customer's credit (invoices phase 6 design I16): each credit fact's
// rule on the customer's credit item, a payment of source credit posting
// nothing, a reversal turning back what was posted, and a merge moving the
// balance to the survivor's item. Nothing counts twice: the invoice's item
// a credit note took below zero is cleared by the move.
func TestSales_CustomerCredit(t *testing.T) {
	b := newBooks(t)
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "1000.00", "3")},
		jsum("3", "25.00", "1000.00", "250.00")))
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPayment, SubjectID: 501, Date: day(t, "2026-03-10"),
		InvoiceID: 9001, InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "1250.00", Source: contracts.PaymentSourceManual})
	b.h.journal.addDocument(contracts.JournalDocument{ID: 9002, Number: 102, Kind: contracts.JournalCreditNote,
		IssueDate: day(t, "2026-03-12"), CustomerID: customerAcme, Currency: "NOK", CreditsID: 9001, CreditsNumber: 101,
		Lines: []contracts.JournalLine{jline(1, "500.00", "3")}, VATSummaries: []contracts.JournalVATSummary{jsum("3", "25.00", "500.00", "125.00")}})
	b.h.journal.addDocument(invoice(9003, 103, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "1000.00", "3")},
		jsum("3", "25.00", "1000.00", "250.00")))
	credit := func(id, invoice int64, number int64, customer int32, date, amount string, c contracts.JournalCredit) {
		c.Currency = "NOK"
		if c.AmountNOK == "" {
			c.AmountNOK = amount
		}
		b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactCredit, SubjectID: id, Date: day(t, date), InvoiceID: invoice,
			InvoiceNumber: number, CustomerID: customer, AmountNOK: amount, Source: contracts.PaymentSourceManual, Credit: &c})
	}
	credit(701, 9001, 101, customerAcme, "2026-03-12", "625.00", contracts.JournalCredit{Kind: contracts.CreditNoteMoved, Amount: "625.00", DocumentID: 9002})
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactCredit, SubjectID: 702, Date: day(t, "2026-03-13"), InvoiceID: 9001,
		InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "100.00", Source: contracts.PaymentSourceOCR, BankAccount: "15030012345",
		Credit: &contracts.JournalCredit{Kind: contracts.CreditOverpayment, Currency: "NOK", Amount: "100.00", AmountNOK: "100.00", BankTransactionID: 41}})
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPayment, SubjectID: 503, Date: day(t, "2026-03-14"),
		InvoiceID: 9003, InvoiceNumber: 103, CustomerID: customerAcme, AmountNOK: "300.00", Source: contracts.PaymentSourceCredit})
	credit(703, 9003, 103, customerAcme, "2026-03-14", "300.00", contracts.JournalCredit{Kind: contracts.CreditApplied, Amount: "-300.00", PaymentID: 503})
	credit(704, 0, 0, customerAcme, "2026-03-15", "50.00", contracts.JournalCredit{Kind: contracts.CreditRefunded, Amount: "-50.00", RefundID: 1})
	credit(705, 0, 0, customerAcme, "2026-03-16", "50.00", contracts.JournalCredit{Kind: contracts.CreditReversed, Amount: "50.00",
		ReversesID: 704, ReversedKind: contracts.CreditRefunded, RefundID: 1})
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactCreditMerged, SubjectID: 801, Date: day(t, "2026-03-20"),
		CustomerID: customerBeta, AmountNOK: "425.00", Source: contracts.PaymentSourceManual,
		Credit: &contracts.JournalCredit{Currency: "NOK", Amount: "425.00", AmountNOK: "425.00", FromCustomerID: customerAcme}})
	catchUp(t, b.h)

	expectVoucher(t, b, "credit", 701, "1500 625.00", "1500 -625.00")
	if bal := itemBalance(t, b, 9001); bal != "0.00" {
		t.Errorf("the credited invoice's item = %s, want the move to clear it", bal)
	}
	v := expectVoucher(t, b, "credit", 702, "1920 100.00", "1500 -100.00")
	if deref(v.Rule) != "sales.credit_overpayment" {
		t.Errorf("the overpayment's rule = %v", v.Rule)
	}
	if _, ok := voucherOf(t, b, "payment", 503); ok {
		t.Error("a payment of source credit posted: the money would move twice")
	}
	expectVoucher(t, b, "credit", 703, "1500 300.00", "1500 -300.00")
	if bal := itemBalance(t, b, 9003); bal != "950.00" {
		t.Errorf("the invoice credit was applied to = %s, want 1250 less 300", bal)
	}
	expectVoucher(t, b, "credit", 704, "1500 50.00", "1920 -50.00")
	v = expectVoucher(t, b, "credit", 705, "1500 -50.00", "1920 50.00")
	if deref(v.Rule) != "sales.credit_reversed" {
		t.Errorf("the reversal's rule = %v", v.Rule)
	}
	expectVoucher(t, b, "credit_merged", 801, "1500 425.00", "1500 -425.00")
	if a, beta := creditItemBalance(t, b, customerAcme), creditItemBalance(t, b, customerBeta); a != "0.00" || beta != "-425.00" {
		t.Errorf("credit items = %s and %s, want the balance moved to the survivor", a, beta)
	}
	var items struct {
		Items []struct {
			CustomerCredit bool   `json:"customerCredit"`
			Balance        string `json:"balance"`
		} `json:"items"`
	}
	if res := b.c.Do(http.MethodGet, base+"/open-items?customerId=502", nil); res.Status != http.StatusOK {
		t.Fatalf("open items = %d %s", res.Status, res.Body)
	} else {
		res.JSON(&items)
	}
	if len(items.Items) != 1 || !items.Items[0].CustomerCredit || items.Items[0].Balance != "-425.00" {
		t.Errorf("the survivor's items = %+v, want its credit item", items.Items)
	}
}

// The opening offers each customer's credit at the day before the books
// start as a credit line on its credit item, refuses a line that is not
// it, and records the page's AsOfSeq on the item.
func TestOpening_OffersTheCustomersCredit(t *testing.T) {
	b := newBooks(t)
	b.h.journal.credits = []contracts.CustomerCredit{{CustomerID: customerAcme, Currency: "NOK", Balance: "200.00", BalanceNOK: "200.00"}}
	b.h.journal.seqs = []int64{9}
	var opening struct {
		OpenCredits []struct {
			CustomerID int32  `json:"customerId"`
			Amount     string `json:"amount"`
		} `json:"openCredits"`
	}
	b.c.Do(http.MethodGet, base+"/opening", nil).JSON(&opening)
	if len(opening.OpenCredits) != 1 || opening.OpenCredits[0].Amount != "-200.00" || opening.OpenCredits[0].CustomerID != customerAcme {
		t.Fatalf("openCredits = %+v, want Acme's -200.00", opening.OpenCredits)
	}
	if res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1500"), "amount": "-150", "customerId": customerAcme, "customerCredit": true},
	}}); res.Status != http.StatusBadRequest {
		t.Errorf("a credit line of another amount = %d %s, want 400", res.Status, res.Body)
	}
	if res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1500"), "amount": "-200", "customerId": customerAcme, "customerCredit": true},
	}}); res.Status != http.StatusCreated {
		t.Fatalf("the opening = %d %s", res.Status, res.Body)
	}
	if bal := creditItemBalance(t, b, customerAcme); bal != "-200.00" {
		t.Errorf("the credit item = %s, want -200.00", bal)
	}
	if n := b.h.Count(t, `SELECT count(*) FROM accounting.open_items WHERE customer_credit AND opening_as_of_seq = 9`); n != 1 {
		t.Errorf("credit items with the page's seq = %d, want 1", n)
	}
}

// The opening reads the open invoices and the customers' credit as one pair
// (B-1 review M1): a credit fact committed between the two reads — its seq
// after the invoices' AsOfSeq and up to the credit's — would be in one
// item's opening amount and not the other's, so both are read again, and an
// opening that never reads a consistent pair is refused, revision_conflict.
func TestOpening_ACreditFactBetweenTheReadsReadsAgain(t *testing.T) {
	b := newBooks(t)
	b.h.journal.open = []contracts.OpenDocument{{ID: 9001, Number: 101, CustomerID: customerAcme, Currency: "NOK", OpenNOK: "700.00"}}
	b.h.journal.credits = []contracts.CustomerCredit{{CustomerID: customerAcme, Currency: "NOK", Balance: "300.00", BalanceNOK: "300.00"}}
	b.h.journal.addFact(contracts.JournalFact{Seq: 11, Kind: contracts.FactCredit, SubjectID: 701, Date: day(t, "2020-01-01"), InvoiceID: 9001,
		InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "300.00", Source: contracts.PaymentSourceManual,
		Credit: &contracts.JournalCredit{Kind: contracts.CreditNoteMoved, Currency: "NOK", Amount: "300.00", AmountNOK: "300.00", DocumentID: 9002}})
	lines := map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1500"), "amount": "700", "customerId": customerAcme, "documentNumber": "101", "sourceDocumentId": 9001},
		{"accountId": b.acc("1500"), "amount": "-300", "customerId": customerAcme, "customerCredit": true},
		{"accountId": b.acc("2000"), "amount": "-400"},
	}}

	// Every read straddles the move: refused, nothing entered.
	b.h.journal.seqs = []int64{10, 12, 10, 12, 10, 12}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/opening", lines), http.StatusConflict, "revision_conflict")
	if n := b.h.Count(t, `SELECT count(*) FROM accounting.vouchers WHERE kind = 'opening'`); n != 0 {
		t.Fatalf("%d openings entered from an inconsistent read", n)
	}

	// The second read sees the move on both sides: entered, both items at 12.
	b.h.journal.calls, b.h.journal.seqs = 0, []int64{10, 12, 12}
	if res := b.c.Do(http.MethodPost, base+"/opening", lines); res.Status != http.StatusCreated {
		t.Fatalf("the opening = %d %s", res.Status, res.Body)
	}
	if n := b.h.Count(t, `SELECT count(*) FROM accounting.open_items WHERE opening_as_of_seq = 12`); n != 2 {
		t.Errorf("items at the second read's seq = %d, want the invoice's and the credit's", n)
	}
}

// Every credit fact waits behind the customer's credit (B-1 review m1): an
// overpayment kept beside a payment of an invoice under Needs attention
// waits on the invoice, and a refund of that credit waits on the
// overpayment, never posting before the credit it pays back; fixing the
// invoice posts all three in order.
func TestSales_ARefundWaitsForTheOverpaymentItRefunds(t *testing.T) {
	b := newBooks(t)
	// Code 1 is a purchase code: no sales rule matches it.
	b.h.journal.addDocument(invoice(9001, 101, day(t, "2026-03-02"), []contracts.JournalLine{jline(1, "100.00", "1")},
		jsum("1", "0.00", "100.00", "0.00")))
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactCredit, SubjectID: 701, Date: day(t, "2026-03-05"), InvoiceID: 9001,
		InvoiceNumber: 101, CustomerID: customerAcme, AmountNOK: "50.00", Source: contracts.PaymentSourceManual,
		Credit: &contracts.JournalCredit{Kind: contracts.CreditOverpayment, Currency: "NOK", Amount: "50.00", AmountNOK: "50.00", PaymentID: 501}})
	b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactCredit, SubjectID: 702, Date: day(t, "2026-03-06"),
		CustomerID: customerAcme, AmountNOK: "50.00", Source: contracts.PaymentSourceManual,
		Credit: &contracts.JournalCredit{Kind: contracts.CreditRefunded, Currency: "NOK", Amount: "-50.00", AmountNOK: "50.00", RefundID: 1}})
	catchUp(t, b.h)

	var list gen.AccountingUnpostedList
	b.c.Do(http.MethodGet, base+"/unposted", nil).JSON(&list)
	open := map[string]gen.AccountingUnposted{}
	for _, u := range list.Items {
		open[fmt.Sprintf("%s %d", u.Source.Kind, u.Source.Id)] = u
	}
	inv, over, refunded := open["invoice 9001"], open["credit 701"], open["credit 702"]
	if len(open) != 3 || over.Reason != "waiting_on" || deref(over.WaitingOn) != inv.Id ||
		refunded.Reason != "waiting_on" || deref(refunded.WaitingOn) != over.Id {
		t.Fatalf("needs attention = %+v, want the refund behind the overpayment behind the invoice", open)
	}
	if _, ok := voucherOf(t, b, "credit", 702); ok {
		t.Fatal("the refund posted before the credit it pays back")
	}

	if res := b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Code 1 sales", "scope": "sales",
		"match": "tax_code", "taxCode": "1", "accountId": b.acc("3000")}); res.Status != http.StatusCreated {
		t.Fatalf("rule = %d %s", res.Status, res.Body)
	}
	if res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/retry", base, inv.Id), nil); res.Status != http.StatusOK {
		t.Fatalf("retry of the invoice = %d %s", res.Status, res.Body)
	}
	overVoucher := expectVoucher(t, b, "credit", 701, "1920 50.00", "1500 -50.00")
	refundVoucher := expectVoucher(t, b, "credit", 702, "1500 50.00", "1920 -50.00")
	if refundVoucher.Number <= overVoucher.Number {
		t.Errorf("the refund is voucher %s, the overpayment %s: want the overpayment first", refundVoucher.Display, overVoucher.Display)
	}
}
