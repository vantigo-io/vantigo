package invoices_test

import (
	"slices"
	"testing"
	"time"
)

// paymentJSON is one registration on a document, as the response carries it.
type paymentJSON struct {
	ID                 int64   `json:"id"`
	PaidOn             string  `json:"paidOn"`
	Amount             float64 `json:"amount"`
	Currency           string  `json:"currency"`
	Source             string  `json:"source"`
	BankTransactionID  *int64  `json:"bankTransactionId"`
	Reference          string  `json:"reference"`
	Note               string  `json:"note"`
	RegisteredAt       string  `json:"registeredAt"`
	RegisteredByUserID string  `json:"registeredByUserId"`
	RemovedAt          *string `json:"removedAt"`
	RemovedByUserID    *string `json:"removedByUserId"`
	RemovalReason      *string `json:"removalReason"`
}

// noMoney asserts a document answers none of an issued invoice's money (D3):
// a draft and a credit note carry no paidAmount, openAmount, refundDue or
// payments.
func noMoney(t *testing.T, what string, inv invoiceJSON) {
	t.Helper()
	if inv.PaidAmount != nil || inv.OpenAmount != nil || inv.RefundDue != nil || inv.Payments != nil {
		t.Errorf("%s = paid %v open %v refund %v payments %v, want none of them", what, inv.PaidAmount, inv.OpenAmount, inv.RefundDue, inv.Payments)
	}
}

// money is a figure the response may leave out, for a message.
func money(f *float64) any {
	if f == nil {
		return "absent"
	}
	return *f
}

// Every document answers its state (D3): a draft is draft, an issued credit
// note issued, and neither carries money; an issued invoice is open with
// nothing paid and its whole gross open, then paid, then — fully credited
// after it was paid — credited, with the paid amount a refund due. Its
// payments come with it, a removed one included with its removal.
func TestDocument_CarriesItsState(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 1000, vat25)))
	if draft.State != "draft" {
		t.Errorf("a draft's state = %q, want draft", draft.State)
	}
	noMoney(t, "a draft", draft)

	inv := issued(t, h, draft.ID)
	if inv.State != "open" || money(inv.PaidAmount) != 0.0 || money(inv.OpenAmount) != 12500.0 || inv.RefundDue != nil ||
		inv.Payments == nil || len(inv.Payments) != 0 {
		t.Errorf("the issued invoice = %q paid %v open %v refund %v payments %v, want open, 0, 12500, none, []",
			inv.State, money(inv.PaidAmount), money(inv.OpenAmount), money(inv.RefundDue), inv.Payments)
	}

	removed := plantPayment(t, h, inv.ID, "100.50", "2026-09-12")
	removePayment(t, h, removed, "Feil beløp")
	whole := plantPayment(t, h, inv.ID, "12500", "2026-09-12")
	paid := getInvoice(t, h, inv.ID)
	if paid.State != "paid" || money(paid.PaidAmount) != 12500.0 || money(paid.OpenAmount) != 0.0 || paid.RefundDue != nil {
		t.Errorf("paid in full = %q paid %v open %v refund %v, want paid, 12500, 0, none",
			paid.State, money(paid.PaidAmount), money(paid.OpenAmount), money(paid.RefundDue))
	}
	if got := []int64{}; len(paid.Payments) == 2 {
		for _, p := range paid.Payments {
			got = append(got, p.ID)
		}
		if !slices.Equal(got, []int64{removed, whole}) {
			t.Errorf("payments = %v, want %v in the order they were registered", got, []int64{removed, whole})
		}
	} else {
		t.Fatalf("payments = %+v, want the removed one and the live one", paid.Payments)
	}
	if p := paid.Payments[0]; p.Amount != 100.5 || p.Currency != "NOK" || p.PaidOn != "2026-09-12" || p.RemovedAt == nil ||
		p.RemovedByUserID == nil || p.RemovalReason == nil || *p.RemovalReason != "Feil beløp" || p.RegisteredByUserID == "" {
		t.Errorf("the removed payment = %+v, want 100.50 NOK with its removal and reason", p)
	}
	if p := paid.Payments[1]; p.Amount != 12500 || p.RemovedAt != nil || p.RemovedByUserID != nil || p.RemovalReason != nil {
		t.Errorf("the live payment = %+v, want 12500 with no removal", p)
	}

	credit := issued(t, h, creditDraft(t, h, inv.ID).ID)
	if credit.State != "issued" {
		t.Errorf("an issued credit note's state = %q, want issued", credit.State)
	}
	noMoney(t, "an issued credit note", credit)

	after := getInvoice(t, h, inv.ID)
	if after.State != "credited" || money(after.PaidAmount) != 12500.0 || money(after.OpenAmount) != -12500.0 || money(after.RefundDue) != 12500.0 {
		t.Errorf("paid, then fully credited = %q paid %v open %v refund %v, want credited, 12500, -12500, 12500",
			after.State, money(after.PaidAmount), money(after.OpenAmount), money(after.RefundDue))
	}
}

// An invoice falls overdue the day after its due date, in Oslo (D3): on the
// due date it is open, and it is overdue from Oslo's midnight — while the
// UTC day is still the due date — in the response and in the list alike.
func TestDocument_OverdueTurnsOnTheDayAfterTheDueDate(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 1000, vat25))).ID)
	if inv.DueDate == nil || *inv.DueDate != "2026-10-12" {
		t.Fatalf("due date = %v, want 2026-10-12 (30 days' terms)", inv.DueDate)
	}
	listed := func(state string) bool { return slices.Contains(ids(list(t, h, "?state="+state)), inv.ID) }

	h.Advance(30 * 24 * time.Hour) // 2026-10-12 12:00 UTC, 14:00 in Oslo: the due date.
	if got := getInvoice(t, h, inv.ID).State; got != "open" || !listed("open") || listed("overdue") {
		t.Errorf("on the due date = %q, listed open %v, overdue %v; want open", got, listed("open"), listed("overdue"))
	}
	h.Advance(10 * time.Hour) // 2026-10-12 22:00 UTC, midnight in Oslo: the day after.
	if got := getInvoice(t, h, inv.ID).State; got != "overdue" || !listed("overdue") || listed("open") {
		t.Errorf("the day after = %q, listed overdue %v, open %v; want overdue", got, listed("overdue"), listed("open"))
	}
}
