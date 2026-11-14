package expenses_test

import (
	"errors"
	"io"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
)

// contracts.ExpenseJournal (accounting design A20, R2), driven through the
// module's own flow: the facts of an expense's approval, undoing,
// reapproval, reimbursement and undoing, a travel claim's as one subject,
// the lines with their categories' names, and the receipts a ledger copies.

func expenseJournal(h *harness) contracts.ExpenseJournal {
	return expenses.Module().ExpenseJournal(h.Deps())
}

func TestExpenseJournal_TheFlowsFacts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	journal := expenseJournal(h)

	entry := createEntry(t, owner, outlayBody(map[string]any{"vatAmount": 250.00}))
	uploadReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", []byte("%PDF-1.4 kvittering"))
	submitEntries(t, owner, entry.Id)
	approveEntries(t, boss, entry.Id)
	unapproveEntries(t, boss, entry.Id)
	submitEntries(t, owner, entry.Id)
	approveEntries(t, boss, entry.Id)
	markReimbursed(t, boss, reimbursedBody([]int64{entry.Id}, map[string]any{"reference": "Lønn april"}))
	undoReimbursed(t, boss, entry.Id)

	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, mileageBody(nil))
	submitClaims(t, owner, claim.Id)
	approveClaims(t, boss, claim.Id)

	facts, err := journal.Facts(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range facts {
		got = append(got, f.Subject+"."+f.Occurrence)
	}
	want := []string{"entry.approved", "entry.unapproved", "entry.approved", "entry.reimbursed", "entry.reimbursement_undone",
		"claim.approved"}
	if !equalStringSlices(got, want) {
		t.Fatalf("facts = %v, want %v", got, want)
	}
	first := facts[0]
	if first.Seq != 1 || first.SubjectID != entry.Id || first.UserID != ownerID || first.Date.Format("2006-01-02") != "2026-03-10" ||
		len(first.Lines) != 1 {
		t.Fatalf("first = %+v", first)
	}
	l := first.Lines[0]
	if l.EntryID != entry.Id || l.Kind != "outlay" || l.PaidBy != "employee" || l.Currency != "NOK" || l.Gross != "1250.00" ||
		l.VAT != "250.00" || l.CategoryID == nil || *l.CategoryID != materialsCategory || l.CategoryName != "Materials" || !l.OwesEmployee() {
		t.Fatalf("line = %+v", l)
	}
	if facts[1].UndoesSeq != 1 || facts[4].UndoesSeq != facts[3].Seq {
		t.Fatalf("undoes = %d, %d", facts[1].UndoesSeq, facts[4].UndoesSeq)
	}
	if facts[3].Date.Format("2006-01-02") != "2026-04-02" || facts[3].ReimbursementReference != "Lønn april" {
		t.Fatalf("reimbursed = %+v", facts[3])
	}
	if c := facts[5]; c.Subject != contracts.SubjectClaim || c.SubjectID != claim.Id || len(c.Lines) != 1 ||
		c.Lines[0].Kind != "mileage" || c.Lines[0].VAT != "" || !c.Lines[0].OwesEmployee() {
		t.Fatalf("claim = %+v", c)
	}
	// Paging by seq resumes after a cursor.
	if page, err := journal.Facts(t.Context(), 4, 1); err != nil || len(page) != 1 || page[0].Seq != 5 {
		t.Fatalf("page after 4 = %+v, %v", page, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM platform.events WHERE module = 'expenses'`); n != 6 {
		t.Fatalf("%d events, want one per fact", n)
	}

	// The receipts, for a ledger to copy.
	receipts, err := journal.Attachments(t.Context(), entry.Id)
	if err != nil || len(receipts) != 1 || receipts[0].FileName != "kvittering.pdf" {
		t.Fatalf("receipts = %+v, %v", receipts, err)
	}
	body, meta, err := journal.OpenAttachment(t.Context(), entry.Id, receipts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	_ = body.Close()
	if string(data) != "%PDF-1.4 kvittering" || meta.ContentType != "application/pdf" {
		t.Fatalf("receipt = %q %+v", data, meta)
	}
	if _, _, err := journal.OpenAttachment(t.Context(), entry.Id+1, receipts[0].ID); !errors.Is(err, contracts.ErrExpenseAttachmentNotFound) {
		t.Fatalf("another expense's receipt: %v", err)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
