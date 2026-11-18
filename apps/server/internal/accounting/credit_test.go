package accounting_test

import (
	"net/http"
	"testing"

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
