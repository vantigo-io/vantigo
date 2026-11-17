package accounting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The supplier invoices as another module reads them and drafts one
// (purchasing design P6, A1, A9; module-boundaries rule 26):
// contracts.SupplierInvoices. Invoice answers the header and each line's net,
// VAT and gross as the supplier-invoice page computes them; CreateDraft makes
// a draft through the draft's own write path — checkDraft and insertDraft —
// so the books' rules judge it as they judge a person's, and nothing is
// posted by it.

// supplierInvoicesContract is the module's contracts.SupplierInvoices.
type supplierInvoicesContract struct{ s *server }

var _ contracts.SupplierInvoices = supplierInvoicesContract{}

// newSupplierInvoices is Module's SupplierInvoices.
func newSupplierInvoices(d module.Deps) contracts.SupplierInvoices {
	return supplierInvoicesContract{s: newServer(d)}
}

// Invoice is one supplier invoice with its lines computed, nil when none.
func (c supplierInvoicesContract) Invoice(ctx context.Context, id int64) (*contracts.SupplierInvoice, error) {
	var out *contracts.SupplierInvoice
	err := c.s.withReadTx(ctx, func(q *store.Queries) error {
		rows, err := q.GetSupplierInvoices(ctx, []int64{id})
		if err != nil || len(rows) == 0 {
			return err
		}
		r := rows[0]
		lines, err := q.SupplierInvoiceLines(ctx, []int64{id})
		if err != nil {
			return err
		}
		var codeIDs []int32
		for _, l := range lines {
			if l.TaxCodeID != nil {
				codeIDs = append(codeIDs, *l.TaxCodeID)
			}
		}
		codes, err := taxCodesOn(ctx, q, codeIDs, r.InvoiceDate)
		if err != nil {
			return err
		}
		inv := &contracts.SupplierInvoice{ID: r.ID, SupplierID: r.SupplierID, Kind: r.Kind, Number: r.SupplierNumber,
			InvoiceDate: r.InvoiceDate, DueDate: r.DueDate, Currency: r.Currency, Total: money(mustRat(r.Total)),
			Reference: deref(r.Reference), State: r.State, CreditsInvoiceID: r.CreditsInvoiceID, CopiedFromID: r.CopiedFromID,
			Lines: make([]contracts.SupplierInvoiceLine, 0, len(lines))}
		for _, l := range lines {
			in := purchaseLineIn{AccountID: l.AccountID, TaxCodeID: l.TaxCodeID, Amount: mustRat(l.Amount), Gross: l.Entry == "gross",
				ProjectID: l.ProjectID, Text: l.Text}
			var code *store.TaxCodesForPostingRow
			if l.TaxCodeID != nil {
				if tc, ok := codes[*l.TaxCodeID]; ok {
					code = &tc
				}
			}
			computed, _, _ := computePurchaseLine(in, code)
			inv.Lines = append(inv.Lines, contracts.SupplierInvoiceLine{ID: l.ID, Position: l.Position, Text: l.Text,
				AccountID: l.AccountID, TaxCodeID: l.TaxCodeID, Entry: l.Entry, Amount: money(in.Amount),
				Net: money(computed.net), VAT: money(computed.vat), Gross: money(computed.gross)})
		}
		out = inv
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("accounting: read supplier invoice %d: %w", id, err)
	}
	return out, nil
}

// CreateDraft makes a draft from d through the draft's own write path: each
// line entered net, its account and tax code the supplier's defaults when
// the line names none.
func (c supplierInvoicesContract) CreateDraft(ctx context.Context, d contracts.SupplierInvoiceDraft) (int64, error) {
	const title = "The supplier invoice cannot be saved"
	if d.By == uuid.Nil {
		return 0, errors.New("accounting: a supplier invoice draft names no person it is made for")
	}
	s := c.s
	q := store.New(s.deps.Pool)
	if _, err := q.GetSettings(ctx); noRows(err) {
		return 0, &contracts.SupplierInvoiceRefusal{Code: contracts.SupplierInvoiceBooksNotSetUp,
			Detail: "A supplier invoice is entered once the books are set up."}
	} else if err != nil {
		return 0, err
	}
	var defaultAccount, defaultCode *int32
	if sup, err := q.GetSupplier(ctx, d.SupplierID); noRows(err) {
		// Not the lines' missing account: the supplier itself is unknown.
		return 0, &contracts.SupplierInvoiceRefusal{Code: contracts.SupplierInvoiceInvalid, Detail: "No such supplier.",
			Fields: []contracts.SupplierInvoiceField{{Field: "supplierId", Message: "No such supplier."}}}
	} else if err != nil {
		return 0, err
	} else {
		defaultAccount, defaultCode = sup.DefaultAccountID, sup.DefaultTaxCodeID
	}
	in := gen.AccountingSupplierInvoiceInput{SupplierId: d.SupplierID, Kind: gen.AccountingSupplierInvoiceKind(d.Kind),
		InvoiceNumber: d.Number, InvoiceDate: openapi_types.Date{Time: d.InvoiceDate}, Total: d.Total,
		Lines: make([]gen.AccountingSupplierInvoiceLineInput, 0, len(d.Lines))}
	if d.DueDate != nil {
		in.DueDate = &openapi_types.Date{Time: *d.DueDate}
	}
	for _, p := range []struct {
		to   **string
		from string
	}{{&in.Currency, d.Currency}, {&in.ExchangeRate, d.ExchangeRate}, {&in.TotalNok, d.TotalNOK}, {&in.Reference, d.Reference},
		{&in.Description, d.Description}} {
		if strings.TrimSpace(p.from) != "" {
			*p.to = ptr(p.from)
		}
	}
	var missing []contracts.SupplierInvoiceField
	for i, l := range d.Lines {
		account, code := l.AccountID, l.TaxCodeID
		if account == nil {
			account = defaultAccount
		}
		if code == nil {
			code = defaultCode
		}
		if account == nil {
			missing = append(missing, contracts.SupplierInvoiceField{Field: fmt.Sprintf("lines[%d].accountId", i),
				Code: contracts.SupplierInvoiceAccountMissing, Message: "The line has no account, and the supplier no default account."})
			continue
		}
		in.Lines = append(in.Lines, gen.AccountingSupplierInvoiceLineInput{AccountId: *account, TaxCodeId: code, Amount: l.Amount,
			Entry: ptr(gen.Net), Text: ptr(l.Text)})
	}
	if len(missing) > 0 {
		return 0, &contracts.SupplierInvoiceRefusal{Code: contracts.SupplierInvoiceAccountMissing,
			Detail: "Set the supplier's default account, or give each line its account.", Fields: missing}
	}
	var id int64
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		draft, err := checkDraft(ctx, q, in, true)
		if err != nil {
			return err
		}
		id, err = insertDraft(ctx, q, draft, nil, nil, d.By, s.now())
		return err
	})
	if err != nil {
		return 0, draftRefusal(err, title)
	}
	return id, nil
}

// draftRefusal is a draft write's refusal as the contract's
// SupplierInvoiceRefusal; anything else is answered as it is.
func draftRefusal(err error, title string) error {
	if c := duplicateConflict(err); c != nil {
		return &contracts.SupplierInvoiceRefusal{Code: contracts.SupplierInvoiceDuplicate,
			Detail: "Another invoice of this supplier, not reversed, has the same number."}
	}
	var r *refusal
	if !errors.As(err, &r) {
		return err
	}
	out := &contracts.SupplierInvoiceRefusal{Code: deref(r.problem.Code), Detail: deref(r.problem.Detail)}
	if out.Detail == "" {
		out.Detail = deref(r.problem.Title)
	}
	if out.Detail == "" {
		out.Detail = title
	}
	if r.status == http.StatusBadRequest {
		if out.Code == "" {
			out.Code = contracts.SupplierInvoiceInvalid
		}
		if r.problem.Errors != nil {
			fields := make([]string, 0, len(*r.problem.Errors))
			for f := range *r.problem.Errors {
				fields = append(fields, f)
			}
			slices.Sort(fields)
			for _, f := range fields {
				out.Fields = append(out.Fields, contracts.SupplierInvoiceField{Field: f, Message: strings.Join((*r.problem.Errors)[f], " ")})
			}
		}
	}
	return out
}
