package accounting

// The opening balances (A9, R5): one voucher of kind opening dated
// books_start, against the opening account. A receivables or payables line is
// one open document, so the party ledgers start with real open items; for a
// company already invoicing in Vantigo the form offers the invoices open at
// the day before books_start, read from Invoices' journal. The opening can be
// reversed and entered again until the first lock.

import (
	"context"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// openAtSnapshot is Invoices' open invoices at the end of day and the highest
// journal fact the answer reflects (nil while the contract does not say).
// The one call site of SalesJournal.OpenAt, made with no transaction open.
func (s *server) openAtSnapshot(ctx context.Context, day time.Time) ([]contracts.OpenDocument, *int64, error) {
	if s.deps.SalesJournal == nil {
		return nil, nil, nil
	}
	docs, err := s.deps.SalesJournal.OpenAt(ctx, day)
	if err != nil {
		return nil, nil, err
	}
	return docs, nil, nil
}

// GetAccountingOpening is the standing opening voucher, whether one may be
// entered, and the open invoices to offer.
func (s *server) GetAccountingOpening(ctx context.Context, _ gen.GetAccountingOpeningRequestObject) (gen.GetAccountingOpeningResponseObject, error) {
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.GetAccountingOpening200JSONResponse{BooksStart: apiDate(settings.BooksStart), Locked: settings.LockedThrough != nil,
		InvoicesAvailable: s.deps.SalesJournal != nil, OpenInvoices: []gen.AccountingOpenInvoice{}}
	id, err := q.StandingOpening(ctx)
	switch {
	case noRows(err):
	case err != nil:
		return nil, err
	default:
		v, err := s.voucher(ctx, id)
		if err != nil {
			return nil, err
		}
		out.Voucher = &gen.AccountingVoucherSummary{Id: v.Id, FiscalYear: v.FiscalYear, Number: v.Number, Display: v.Display,
			VoucherDate: v.VoucherDate, DocumentDate: v.DocumentDate, Description: v.Description, Kind: v.Kind, Amount: v.Amount,
			Source: v.Source, System: v.System, Reverses: v.Reverses, ReversedBy: v.ReversedBy, PostedAt: v.PostedAt}
	}
	out.CanEnter = out.Voucher == nil && !out.Locked
	docs, _, err := s.openAtSnapshot(ctx, settings.BooksStart.AddDate(0, 0, -1))
	if err != nil {
		s.logger().WarnContext(ctx, "accounting: Invoices' open invoices could not be read", "error", err.Error())
		out.InvoicesAvailable = false
		return out, nil
	}
	ids := make([]int32, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.CustomerID)
	}
	names := s.customerNames(ctx, ids)
	for _, d := range docs {
		out.OpenInvoices = append(out.OpenInvoices, gen.AccountingOpenInvoice{DocumentId: d.ID, Number: d.Number,
			CustomerId: d.CustomerID, CustomerName: names[d.CustomerID], IssueDate: apiDate(d.IssueDate),
			DueDate: apiDate(d.DueDate), Amount: money(mustRat(d.OpenNOK))})
	}
	return out, nil
}

// PostAccountingOpening enters the opening balances (A9).
func (s *server) PostAccountingOpening(ctx context.Context, req gen.PostAccountingOpeningRequestObject) (gen.PostAccountingOpeningResponseObject, error) {
	const title = "The opening balances cannot be posted"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	var f fieldErrors
	lines := make([]Line, 0, len(req.Body.Lines)+1)
	total := new(big.Rat)
	fromInvoices := false
	for i, l := range req.Body.Lines {
		amount, ok := parseAmount(l.Amount)
		if !ok || amount.Sign() == 0 {
			f.addCoded("lines["+itoa(i)+"].amount", codeAmountInvalid, "An amount other than zero, with at most two decimals.")
			continue
		}
		total.Add(total, amount)
		line := Line{AccountID: l.AccountId, Amount: amount, CustomerID: l.CustomerId, SupplierID: l.SupplierId,
			SourceDocumentID: l.SourceDocumentId, Text: strings.TrimSpace(deref(l.Text)), origin: i + 1}
		if l.SourceDocumentId != nil {
			fromInvoices = true
		}
		if l.DocumentNumber != nil || l.DocumentDate != nil || l.DueDate != nil || l.SourceDocumentId != nil {
			line.Item = &OpenItemSpec{DocumentNumber: strings.TrimSpace(deref(l.DocumentNumber)),
				DocumentDate: datePtr(l.DocumentDate), DueDate: datePtr(l.DueDate)}
		}
		lines = append(lines, line)
	}
	if len(req.Body.Lines) == 0 {
		f.addCoded("lines", codeVoucherEmpty, "Enter at least one balance.")
	}
	if err := f.refusal(title); err != nil {
		return nil, err
	}
	var asOfSeq *int64
	if fromInvoices {
		settings, err := s.settings(ctx)
		if err != nil {
			return nil, err
		}
		if _, asOfSeq, err = s.openAtSnapshot(ctx, settings.BooksStart.AddDate(0, 0, -1)); err != nil {
			return nil, err
		}
	}
	var posted Posted
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		settings, err := q.GetSettingsForShare(ctx)
		if err != nil {
			return err
		}
		if settings.LockedThrough != nil {
			return conflict(codeOpeningLocked, "The opening balances are final", "They cannot be entered after the first lock.")
		}
		if _, err := q.StandingOpening(ctx); err == nil {
			return conflict(codeOpeningExists, "The opening balances are entered", "Reverse the opening voucher to enter them again.")
		} else if !noRows(err) {
			return err
		}
		description := "Opening balances"
		if settings.ChartLanguage == "nb" {
			description = "Inngående balanse"
		}
		v := Voucher{Kind: "opening", Date: settings.BooksStart, Description: description, PostedBy: ptr(callerID(ctx)), At: s.now(),
			Lines: lines}
		if total.Sign() != 0 {
			v.Lines = append(v.Lines, Line{Role: "opening", Amount: neg(total)})
		}
		if posted, err = Post(ctx, tx, v); err != nil {
			return err
		}
		if asOfSeq != nil {
			return q.SetOpeningAsOfSeq(ctx, store.SetOpeningAsOfSeqParams{Seq: asOfSeq, Now: s.now()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := s.voucher(ctx, posted.ID)
	if err != nil {
		return nil, err
	}
	return gen.PostAccountingOpening201JSONResponse(out), nil
}
