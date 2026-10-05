package contracts

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WorkSourceKind names a kind of row an invoice line is built from, as
// "<module>.<what>": "time.entry", "expenses.entry", "projects.milestone".
type WorkSourceKind string

// The kinds of work an invoice is built from.
const (
	WorkSourceMilestone WorkSourceKind = "projects.milestone"
	WorkSourceExpense   WorkSourceKind = "expenses.entry"
	WorkSourceHours     WorkSourceKind = "time.entry"
)

// InvoicedWorkOrder is the cross-module lock order the issue calls holders
// in, after Invoices' own locks: Projects (the project rows, then their
// milestones), then Expenses (the claims, then the lines), then Time (the
// entries). Every transaction that locks rows of more than one of these
// modules takes them in this order, so no two can wait on each other.
var InvoicedWorkOrder = []WorkSourceKind{WorkSourceMilestone, WorkSourceExpense, WorkSourceHours}

// InvoicedWorkHolder is a module whose rows an invoice is built from, and the
// third sanctioned cross-module WRITE (module-boundaries rule 10), beside
// CustomerReferenceHolder (rule 8) and CustomerPersonalData (rule 9). Invoices
// calls it INSIDE its issue's transaction, which already holds the document,
// the settings row, the number counter and, for a credit note, its original
// locked: after every check and the number, before the document is written.
// MarkInvoiced judges each source as it stands under the holder's own lock and
// stamps it with the invoice's id, number and date; ReleaseInvoiced takes the
// stamp back in the issue of the credit note that returns the source's line
// in full. Every module shares one database and one pool, so the stamp is in
// the issue's own transaction: an issued invoice and its stamps commit
// together or not at all.
//
// tx is a platform type, not a store type, so rule 3 (contracts carry no
// store types) still holds: the holder builds its own store over it.
//
// The rules a holder keeps, rule 8's and more, none of which the signature
// shows:
//
//   - It never begins, commits or rolls back a transaction, and never touches
//     a pool: tx is the caller's, and so is the decision. Its SQL is its own,
//     on its own schema, in its own package's queries/.
//   - It never reads a directory or any other contract, and never takes a
//     clock: no call that takes its own connection or leaves the process is
//     made while a transaction holds locks. Every timestamp it writes is
//     ref.IssuedAt, and the issuer's name for a timeline event arrives in
//     ref.IssuedByDisplay, read by Invoices before its transaction.
//   - It locks its rows in its module's own order, at its place in
//     InvoicedWorkOrder, and never locks a row of invoices.
//   - MarkInvoiced judges each source in one order — already invoiced
//     (SourceAlreadyInvoiced), then no longer invoiceable
//     (SourceNotInvoiceable), then changed since the draft read it
//     (SourceChanged) — so a row stamped by hand is named for what it is.
//     Amounts are compared exactly, WorkSource.Amount against its own exact
//     figure, by value and never as text.
//   - It answers a source it will not stamp only as *WorkSourceRefusal;
//     anything else is a failure, which the issue answers 500. Either rolls
//     the whole issue back, the number and every holder's write with it.
//   - The period lock applies to neither direction: a stamp is not an edit of
//     the work.
//   - ReleaseInvoiced takes the same locks in the same order as MarkInvoiced
//     before it writes, and tolerates a source that no longer carries ref's
//     stamp: it writes nothing for it and logs a warning, because a credit
//     note must never be blocked.
//   - It runs whether or not its module is enabled: every schema is migrated
//     whatever MODULES says, so Compose and Workers collect the holder of
//     every module given, and a module switched off still has its rows
//     stamped and released. Its constructor needs Deps.Logger and nothing
//     else.
type InvoicedWorkHolder interface {
	// Kinds are the source kinds this holder stamps. No kind has two
	// holders: Compose refuses a composition where two modules claim one.
	Kinds() []WorkSourceKind
	// MarkInvoiced judges and stamps sources — every one of this holder's
	// kinds the invoice holds — with ref, inside tx.
	MarkInvoiced(ctx context.Context, tx pgx.Tx, ref InvoiceRef, sources []WorkSource) error
	// ReleaseInvoiced takes ref's stamp off sources inside tx. ref is the
	// original invoice's id, number and date — the stamp being taken back —
	// with the credit note's issue time and issuer.
	ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref InvoiceRef, sources []WorkSource) error
}

// InvoiceRef is the stamp a holder writes, or the one it takes back.
type InvoiceRef struct {
	ID, Number int64
	IssueDate  time.Time
	// IssuedAt is the issue's own clock, read once: every stamp's and every
	// release's timestamp, never the holder's clock or SQL now().
	IssuedAt time.Time
	// IssuedBy is the stamp's "by", IssuedByDisplay the issuer's name, read
	// before the transaction; "" when the user directory did not know them.
	IssuedBy        uuid.UUID
	IssuedByDisplay string
}

// WorkSource is one row an invoice line is built from, as the draft read it
// through the billable reads (billable.go).
type WorkSource struct {
	Kind WorkSourceKind
	ID   int64
	// Revision is judged by Time and Projects; for Expenses it is display
	// only — a payroll reimbursement bumps it and changes nothing billed.
	Revision  int32
	ProjectID int32
	Currency  string
	// Amount is the billable amount as read for the draft, exact decimal
	// text, so no float ever rounds money on its way between modules.
	Amount string
	// ExpenseKind is the expense's kind, a billing fact the line text
	// depends on; "" for every other kind.
	ExpenseKind string
}

// The codes a WorkSourceRefusal carries.
const (
	SourceNotInvoiceable  = "source_not_invoiceable"
	SourceChanged         = "source_changed"
	SourceAlreadyInvoiced = "source_already_invoiced"
)

// WorkSourceRefusal is the one error a holder answers for a source it will
// not stamp, Code one of the three codes above; the issue finds it with
// errors.As and refuses with its code and the line's position. Anything else
// a holder returns is a failure.
type WorkSourceRefusal struct {
	Source WorkSource
	Code   string
	Detail string
}

func (r *WorkSourceRefusal) Error() string {
	return fmt.Sprintf("contracts: %s %d refused: %s: %s", r.Source.Kind, r.Source.ID, r.Code, r.Detail)
}
