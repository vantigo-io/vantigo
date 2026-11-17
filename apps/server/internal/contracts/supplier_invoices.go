package contracts

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SupplierInvoices is Accounting's supplier invoices as another module reads
// them, and the one way another module makes one (purchasing design P6, A1,
// A9; module-boundaries rule 26): a single-provider slot Accounting fills,
// nil when Accounting is off.
//
//   - Invoice reads on the provider's pool, before the caller's transaction,
//     and checks no user permission: the caller decides who sees an
//     invoice's lines.
//   - CreateDraft writes a draft in Accounting's own schema, on Accounting's
//     own transaction, through the same write path a person's draft takes, so
//     the same rules judge it. The caller makes it on the pool before it opens
//     a transaction of its own, and owns the cleanup of what it made: a
//     draft is the person's to complete and post, or to delete. Nothing is
//     posted by it.
type SupplierInvoices interface {
	// Invoice is one supplier invoice with its lines, nil when none has the
	// id.
	Invoice(ctx context.Context, id int64) (*SupplierInvoice, error)
	// CreateDraft makes a draft and answers its id. What the books' rules
	// refuse is a *SupplierInvoiceRefusal; anything else is a failure.
	CreateDraft(ctx context.Context, d SupplierInvoiceDraft) (int64, error)
}

// SupplierInvoice is a supplier invoice as SupplierInvoices answers it.
// Amounts are exact decimal text with two decimals, in the invoice's
// currency.
type SupplierInvoice struct {
	ID               int64
	SupplierID       int64
	Kind             string // "invoice" | "credit_note"
	Number           string // the supplier's own number
	InvoiceDate      time.Time
	DueDate          *time.Time
	Currency         string
	Total            string
	Reference        string
	State            string // "draft" | "posted" | "reversed"
	CreditsInvoiceID *int64
	// CopiedFromID is the reversed invoice a Reverse and copy made this
	// draft from.
	CopiedFromID *int64
	Lines        []SupplierInvoiceLine
}

// SupplierInvoiceLine is one line of a supplier invoice: as entered, and its
// net, VAT and gross as Accounting computes them at the invoice's date.
type SupplierInvoiceLine struct {
	ID        int64
	Position  int32
	Text      string
	AccountID int32
	TaxCodeID *int32
	Entry     string // "net" | "gross": how Amount was entered
	Amount    string
	Net       string
	VAT       string
	Gross     string
}

// SupplierInvoiceDraft is a draft CreateDraft makes. Dates are calendar
// dates at midnight UTC.
type SupplierInvoiceDraft struct {
	SupplierID int64
	Kind       string // "invoice" | "credit_note"
	// Number is the supplier's invoice number, unique per supplier among
	// the invoices not reversed.
	Number      string
	InvoiceDate time.Time
	DueDate     *time.Time // nil: the invoice date plus the supplier's terms
	// Currency is the supplier's when "". In another currency than NOK the
	// draft needs ExchangeRate or TotalNOK.
	Currency     string
	ExchangeRate string
	TotalNOK     string
	Total        string
	Reference    string
	Description  string
	Lines        []SupplierInvoiceDraftLine
	// By is the person the draft is made for; uuid.Nil is refused.
	By uuid.UUID
}

// SupplierInvoiceDraftLine is one line of a draft: its text and net amount,
// entered net. With no account the supplier's default account is taken, and
// with no tax code its default tax code; a line left with no account is
// refused (SupplierInvoiceAccountMissing).
type SupplierInvoiceDraftLine struct {
	Text      string
	Amount    string
	AccountID *int32
	TaxCodeID *int32
}

// The codes a SupplierInvoiceRefusal carries beside Accounting's own field
// codes.
const (
	// SupplierInvoiceBooksNotSetUp: the books are not set up.
	SupplierInvoiceBooksNotSetUp = "books_not_set_up"
	// SupplierInvoiceAccountMissing: a line has no account and the supplier
	// has no default account.
	SupplierInvoiceAccountMissing = "supplier_account_missing"
	// SupplierInvoiceDuplicate: the supplier has an invoice of that number
	// that is not reversed.
	SupplierInvoiceDuplicate = "duplicate_supplier_invoice"
	// SupplierInvoiceInvalid: the draft breaks a field rule; Fields says
	// which — supplierId for a supplier the register does not have.
	SupplierInvoiceInvalid = "supplier_invoice_invalid"
)

// SupplierInvoiceRefusal is CreateDraft's refusal: Code, a person's Detail,
// and the fields that failed, each with Accounting's own code when it has
// one.
type SupplierInvoiceRefusal struct {
	Code   string
	Detail string
	Fields []SupplierInvoiceField
}

// SupplierInvoiceField is one field a draft failed on, named as
// Accounting's contract names it ("lines[0].accountId").
type SupplierInvoiceField struct {
	Field   string
	Code    string
	Message string
}

func (r *SupplierInvoiceRefusal) Error() string {
	return fmt.Sprintf("contracts: the supplier invoice was refused: %s: %s", r.Code, r.Detail)
}

// SupplierInvoiceMatches is Purchasing's answer to which of its orders a
// supplier invoice is matched to (purchasing design P6): an optional
// single-provider slot Purchasing fills, nil when Purchasing is off, which
// Accounting reads as "no badge". It reads on the provider's pool, before any
// transaction, and is provider-gated: a caller who may not see the orders is
// answered no matches, never a refusal.
type SupplierInvoiceMatches interface {
	// Summary is, per invoice asked that is matched to anything, the orders
	// it is matched to and how many differences the match has. An invoice
	// matched to nothing has no entry. More than MaxSuppliers ids is a
	// failure.
	Summary(ctx context.Context, invoiceIDs []int64) ([]SupplierInvoiceMatch, error)
}

// SupplierInvoiceMatch is one invoice's matches.
type SupplierInvoiceMatch struct {
	InvoiceID   int64
	Orders      []MatchedOrder
	Differences int
}

// MatchedOrder is an order an invoice is matched to: its id, for the link,
// and its number as the provider shows it.
type MatchedOrder struct {
	ID     int64
	Number string
}
