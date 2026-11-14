package contracts

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

// ExpenseJournal is the one sanctioned way a module reads what Expenses has
// approved and reimbursed, in an order it can resume from (accounting design
// A20 as amended by R2): a single-provider, read-only, in-process port served
// by Expenses. Compose and Workers wire it into every module's Deps (see
// Module.ExpenseJournal); it is nil when expenses is disabled.
//
// It is the source a ledger posts from; Expenses' events (expenses.entry.*
// and expenses.claim.*) only make a reader prompt. A reader keeps its own
// cursor, the last seq, and asks after it again until a page comes back
// short. The cursor never skips: a fact's seq is taken at commit, by a
// deferred trigger, from a counter row held for that instant, so seqs become
// visible in order. Facts exist for every approval, undone approval,
// reimbursement and undone reimbursement since Expenses' migration 00184, and
// for every approval and reimbursement standing before it, numbered then in
// the order they happened.
//
// A fact is about a standalone expense (SubjectEntry) or a whole travel claim
// (SubjectClaim): the unit that moves through Expenses' flow. A line of a
// claim is in its claim's facts only, so each expense's cost is in one
// approved fact per approval and one unapproved fact per undoing.
//
// Every method runs on the pool, holds no lock and is called with no
// transaction of the caller's open; it performs no authorization. Amounts are
// exact decimal text with two decimals, in the line's own currency: Expenses
// stores no exchange rate. No value carries a person's name or a free-text
// description; the person is UserID, an opaque id.
type ExpenseJournal interface {
	// Facts is up to limit facts with a seq after afterSeq, in seq order (0
	// reads from the first).
	Facts(ctx context.Context, afterSeq int64, limit int) ([]ExpenseFact, error)
	// Attachments is the receipts of one expense, oldest first: none when it
	// has none or there is no such expense.
	Attachments(ctx context.Context, entryID int64) ([]ExpenseAttachment, error)
	// OpenAttachment opens one receipt of that expense for reading, with its
	// metadata; the caller closes the reader. ErrExpenseAttachmentNotFound
	// when the expense has no such receipt.
	OpenAttachment(ctx context.Context, entryID, attachmentID int64) (io.ReadCloser, ExpenseAttachment, error)
}

// ErrExpenseAttachmentNotFound is OpenAttachment's answer for a receipt the
// expense does not have.
var ErrExpenseAttachmentNotFound = errors.New("contracts: no such expense receipt")

// MaxExpenseJournalPage is the most a Facts page answers; a larger limit is
// read as this, and a limit below 1 as 1.
const MaxExpenseJournalPage = 500

// The occurrences of ExpenseFact.Occurrence. Later ones are added to the end;
// a reader leaves one it does not know unposted.
const (
	ExpenseApproved            = "approved"
	ExpenseUnapproved          = "unapproved"
	ExpenseReimbursed          = "reimbursed"
	ExpenseReimbursementUndone = "reimbursement_undone"
)

// The subjects of ExpenseFact.Subject.
const (
	SubjectEntry = "entry"
	SubjectClaim = "claim"
)

// The kinds of ExpenseLine.Kind, as Expenses stores them.
const (
	ExpenseKindOutlay          = "outlay"
	ExpenseKindMileage         = "mileage"
	ExpenseKindPerDiem         = "per_diem"
	ExpenseKindSupplierInvoice = "supplier_invoice"
)

// The payers of ExpenseLine.PaidBy: an outlay's; "" on a mileage or per diem
// line, which the employee is always owed, and company on a supplier
// invoice.
const (
	PaidByEmployee = "employee"
	PaidByCompany  = "company"
)

// ExpenseFact is one occurrence of an expense or a claim. Dates are UTC
// midnight of the calendar day.
type ExpenseFact struct {
	Seq int64
	// Occurrence is one of the Expense* occurrence constants.
	Occurrence string
	// Subject is SubjectEntry or SubjectClaim; SubjectID its id.
	Subject   string
	SubjectID int64
	// UserID is the person the expense is about.
	UserID uuid.UUID
	// Date is the business day the fact belongs to: for an approval and its
	// undoing the cost's day — the expense's date, a claim's latest line
	// date — and for a reimbursement and its undoing the reimbursement's own
	// date.
	Date time.Time
	// UndoesSeq is, on an unapproved or reimbursement_undone fact, the seq of
	// the approved or reimbursed fact of the same subject it undoes; 0 when
	// there is none (the approval predates the log) and on the other two.
	UndoesSeq int64
	// ReimbursementReference is a reimbursement's reference (a payroll run's,
	// as the person entered it), "" when none.
	ReimbursementReference string
	// Lines are the subject's lines as they stood when the fact committed:
	// an expense's one, a claim's every line, by date.
	Lines []ExpenseLine
}

// ExpenseLine is one expense of a fact.
type ExpenseLine struct {
	EntryID int64
	// Kind is one of the ExpenseKind constants.
	Kind string
	Date time.Time
	// CategoryID and CategoryName are the expense's category, nil and ""
	// when it has none (mileage and per diem); the name is the category's
	// current one.
	CategoryID   *int32
	CategoryName string
	// PaidBy is one of the PaidBy constants, "" when none was recorded.
	PaidBy   string
	Currency string
	// Gross is the amount; VAT the VAT amount the receipt shows, "" when none
	// was recorded. Mileage and per diem carry none.
	Gross, VAT string
	// ProjectID is the project the expense belongs to, nil for none.
	ProjectID *int32
	// Supplier is the supplier's name as typed on the expense, "" for none.
	Supplier string
	// SupplierInvoiceNumber and SupplierDueDate are a supplier_invoice kind's
	// number and due date.
	SupplierInvoiceNumber string
	SupplierDueDate       *time.Time
}

// OwesEmployee reports whether the line is money the company owes the
// employee: an outlay the employee paid, mileage and per diem — Expenses'
// own expenses.owes_employee.
func (l ExpenseLine) OwesEmployee() bool {
	switch l.Kind {
	case ExpenseKindOutlay:
		return l.PaidBy == PaidByEmployee
	case ExpenseKindSupplierInvoice:
		return false
	}
	return true
}

// ExpenseAttachment is one receipt of an expense.
type ExpenseAttachment struct {
	ID          int64
	EntryID     int64
	FileName    string
	ContentType string
	SizeBytes   int64
}
