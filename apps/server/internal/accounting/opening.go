package accounting

// The opening balances (A9, R5): one voucher of kind opening dated
// books_start, against the opening account. A receivables or payables line is
// one open document, so the party ledgers start with real open items; for a
// company already invoicing in Vantigo the form offers the invoices open at
// the day before books_start, read from Invoices' journal, and the
// customers' credit then (invoices phase 6 design I16), each a credit line on
// the customer's credit item. The opening can be
// reversed and entered again until the first lock — the
// first ever: lifting the lock does not open it again.

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// openInvoice is one invoice Invoices' OpenAt answered, with the AsOfSeq of
// the page it was read on: the facts its open amount reflects (R14).
type openInvoice struct {
	contracts.OpenDocument
	asOfSeq int64
}

// openAtSnapshot is Invoices' open invoices at the end of day, read page by
// page until a short page, and the lowest AsOfSeq of the pages read; nil, nil
// without Invoices. Each page is its own snapshot, so each invoice carries
// its page's AsOfSeq. The one call site of SalesJournal.OpenAt, made with no
// transaction open.
func (s *server) openAtSnapshot(ctx context.Context, day time.Time) ([]openInvoice, *int64, error) {
	if s.deps.SalesJournal == nil {
		return nil, nil, nil
	}
	var out []openInvoice
	var lowest *int64
	after := int64(0)
	for {
		page, err := s.deps.SalesJournal.OpenAt(ctx, day, after, contracts.MaxJournalPage)
		if err != nil {
			return nil, nil, err
		}
		if lowest == nil || page.AsOfSeq < *lowest {
			lowest = ptr(page.AsOfSeq)
		}
		for _, d := range page.Documents {
			if d.Number <= after {
				return nil, nil, fmt.Errorf("accounting: OpenAt answered invoice %d after %d", d.Number, after)
			}
			after = d.Number
			out = append(out, openInvoice{OpenDocument: d, asOfSeq: page.AsOfSeq})
		}
		if len(page.Documents) < contracts.MaxJournalPage {
			return out, lowest, nil
		}
	}
}

// openCredit is one customer's credit Invoices' CreditsAt answered, with the
// AsOfSeq of the page it was read on (invoices phase 6 design I16).
type openCredit struct {
	contracts.CustomerCredit
	asOfSeq int64
}

// creditsAtSnapshot is Invoices' customers' credit at the end of day, read
// page by page until a short page, as openAtSnapshot reads the invoices; nil
// without Invoices. The one call site of SalesJournal.CreditsAt, made with no
// transaction open.
func (s *server) creditsAtSnapshot(ctx context.Context, day time.Time) ([]openCredit, error) {
	if s.deps.SalesJournal == nil {
		return nil, nil
	}
	var out []openCredit
	after := int32(0)
	for {
		page, err := s.deps.SalesJournal.CreditsAt(ctx, day, after, contracts.MaxJournalPage)
		if err != nil {
			return nil, err
		}
		customers := 0
		for _, c := range page.Credits {
			if c.CustomerID < after {
				return nil, fmt.Errorf("accounting: CreditsAt answered customer %d after %d", c.CustomerID, after)
			}
			if c.CustomerID != after {
				customers++
			}
			after = c.CustomerID
			out = append(out, openCredit{CustomerCredit: c, asOfSeq: page.AsOfSeq})
		}
		if customers < contracts.MaxJournalPage {
			return out, nil
		}
	}
}

// openingReads is how many times openingSnapshot reads Invoices before it
// gives up on a consistent pair.
const openingReads = 3

// openingSnapshot is openAtSnapshot and creditsAtSnapshot read together so
// that the fact both an invoice's item and a customer's credit item reflect
// is in both or in neither (B-1 review M1): a credit or credit_merged fact —
// a credit note's surplus moved, credit applied, a merge — touches two items
// read in two snapshots, and one committed between them would be in one
// item's opening amount and posted again, or missed, on the other. When such
// a fact has a seq between the lowest and the highest AsOfSeq of the pages,
// both are read again, up to openingReads times; consistent is false when
// the last read still straddles one.
func (s *server) openingSnapshot(ctx context.Context, day time.Time) (open []openInvoice, lowest *int64, credits []openCredit, consistent bool, err error) {
	for range openingReads {
		if open, lowest, err = s.openAtSnapshot(ctx, day); err != nil {
			return nil, nil, nil, false, err
		}
		if credits, err = s.creditsAtSnapshot(ctx, day); err != nil {
			return nil, nil, nil, false, err
		}
		if lowest == nil {
			return open, lowest, credits, true, nil
		}
		lo, hi := *lowest, *lowest
		for _, c := range credits {
			lo, hi = min(lo, c.asOfSeq), max(hi, c.asOfSeq)
		}
		for _, d := range open {
			hi = max(hi, d.asOfSeq)
		}
		straddled, err := s.creditFactBetween(ctx, lo, hi)
		if err != nil {
			return nil, nil, nil, false, err
		}
		if !straddled {
			return open, lowest, credits, true, nil
		}
	}
	return open, lowest, credits, false, nil
}

// creditFactBetween reports whether Invoices' journal holds a credit or
// credit_merged fact with a seq after lo and up to hi.
func (s *server) creditFactBetween(ctx context.Context, lo, hi int64) (bool, error) {
	for after := lo; after < hi; {
		facts, err := s.deps.SalesJournal.Facts(ctx, after, contracts.MaxJournalPage)
		if err != nil {
			return false, err
		}
		if len(facts) == 0 {
			return false, nil
		}
		for _, f := range facts {
			if f.Seq > hi {
				return false, nil
			}
			if f.Kind == contracts.FactCredit || f.Kind == contracts.FactCreditMerged {
				return true, nil
			}
			after = f.Seq
		}
	}
	return false, nil
}

// openingFinal reports whether the opening balances are final (A9): the
// books are locked, or have been locked once, even if the lock was lifted
// since.
func openingFinal(ctx context.Context, q *store.Queries, settings store.AccountingSetting) (bool, error) {
	if settings.LockedThrough != nil {
		return true, nil
	}
	return q.EverLocked(ctx)
}

// GetAccountingOpening is the standing opening voucher, whether one may be
// entered, and the open invoices to offer.
func (s *server) GetAccountingOpening(ctx context.Context, _ gen.GetAccountingOpeningRequestObject) (gen.GetAccountingOpeningResponseObject, error) {
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	final, err := openingFinal(ctx, q, settings)
	if err != nil {
		return nil, err
	}
	out := gen.GetAccountingOpening200JSONResponse{BooksStart: apiDate(settings.BooksStart), Locked: final,
		InvoicesAvailable: s.deps.SalesJournal != nil, OpenInvoices: []gen.AccountingOpenInvoice{},
		OpenCredits: []gen.AccountingOpenCredit{}}
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
	// An offer: what a straddling fact leaves inconsistent the entry reads
	// again and refuses.
	docs, _, credits, _, err := s.openingSnapshot(ctx, settings.BooksStart.AddDate(0, 0, -1))
	if err != nil {
		s.logger().WarnContext(ctx, "accounting: Invoices' open invoices and customers' credit could not be read", "error", err.Error())
		out.InvoicesAvailable = false
		return out, nil
	}
	ids := make([]int32, 0, len(docs)+len(credits))
	for _, d := range docs {
		ids = append(ids, d.CustomerID)
	}
	for _, c := range credits {
		ids = append(ids, c.CustomerID)
	}
	names := s.customerNames(ctx, ids)
	for _, c := range credits {
		out.OpenCredits = append(out.OpenCredits, gen.AccountingOpenCredit{CustomerId: c.CustomerID, CustomerName: names[c.CustomerID],
			Currency: c.Currency, Amount: money(neg(mustRat(c.BalanceNOK)))})
	}
	for _, d := range docs {
		out.OpenInvoices = append(out.OpenInvoices, gen.AccountingOpenInvoice{DocumentId: d.ID, Number: d.Number,
			CustomerId: d.CustomerID, CustomerName: names[d.CustomerID], IssueDate: apiDate(d.IssueDate),
			DueDate: apiDate(d.DueDate), Amount: money(mustRat(d.OpenNOK))})
	}
	return out, nil
}

// checkOpeningLines refuses an opening voucher's line on a VAT account — an
// account a tax code names as its VAT or counter account (H3): VAT owed or
// due from before the books start is a balance on the VAT settlement account,
// and a balance left on a VAT account would keep the first term's VAT
// accounts from reconciling. Every way an opening is built goes through it,
// a person's form and an import alike, on the caller's transaction.
func checkOpeningLines(ctx context.Context, q *store.Queries, lines []Line) error {
	ids, err := q.VatAccountIDs(ctx)
	if err != nil {
		return err
	}
	var f fieldErrors
	for i, l := range lines {
		if l.AccountID != 0 && slices.Contains(ids, l.AccountID) {
			f.addCoded(l.field(i)+".accountId", codeOpeningVatAccount,
				"A VAT account takes no opening balance: unpaid VAT from before the books start belongs on the VAT settlement account (2740).")
		}
	}
	return f.refusal("The opening balances cannot be posted")
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
	for i, l := range req.Body.Lines {
		amount, ok := parseAmount(l.Amount)
		if !ok || amount.Sign() == 0 {
			f.addCoded("lines["+itoa(i)+"].amount", codeAmountInvalid, "An amount other than zero, with at most two decimals.")
			continue
		}
		total.Add(total, amount)
		line := Line{AccountID: l.AccountId, Amount: amount, CustomerID: l.CustomerId, SupplierID: l.SupplierId,
			SourceDocumentID: l.SourceDocumentId, Text: strings.TrimSpace(deref(l.Text)), origin: i + 1}
		if l.CustomerCredit != nil && *l.CustomerCredit {
			if l.SourceDocumentId != nil || l.DocumentNumber != nil || l.SupplierId != nil {
				f.addCoded("lines["+itoa(i)+"].customerCredit", codeOpenItemUnknown,
					"A customer's credit stands for no document: name the customer and nothing else.")
			}
			line.Item = &OpenItemSpec{CustomerCredit: true}
		} else if l.DocumentNumber != nil || l.DocumentDate != nil || l.DueDate != nil || l.SourceDocumentId != nil {
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
	// With Invoices present the open invoices are read every time: their
	// lowest AsOfSeq is the settings' rule for every invoice without an
	// opening item, and a line naming one must be it, its customer's and its
	// amount (R5, R14).
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	open, asOfSeq, credits, consistent, err := s.openingSnapshot(ctx, settings.BooksStart.AddDate(0, 0, -1))
	if err != nil {
		return nil, err
	}
	if !consistent {
		return nil, conflict(codeRevisionConflict, "Invoices changed while the opening was read",
			"A customer's credit moved in Invoices while its open invoices and credit were read. Enter the opening again.")
	}
	byID := make(map[int64]openInvoice, len(open))
	for _, d := range open {
		byID[d.ID] = d
	}
	var creditCustomers []int32
	var creditSeqs []int64
	for i, l := range req.Body.Lines {
		if l.CustomerCredit == nil || !*l.CustomerCredit {
			continue
		}
		// The line is the customer's NOK credit, negated: its credit item
		// starts where Invoices' credit stood (I16, R14).
		field := "lines[" + itoa(i) + "].customerCredit"
		amount, _ := parseAmount(l.Amount)
		k := slices.IndexFunc(credits, func(c openCredit) bool { return l.CustomerId != nil && c.CustomerID == *l.CustomerId })
		switch {
		case l.CustomerId == nil || k < 0:
			f.addCoded(field, codeOpenItemUnknown, "Not a customer with credit in Invoices at the day before the books start.")
		case slices.Contains(creditCustomers, *l.CustomerId):
			f.addCoded(field, codeOpenItemUnknown, "The customer's credit is on another line already.")
		default:
			want := new(big.Rat)
			for _, c := range credits {
				if c.CustomerID == *l.CustomerId {
					want.Sub(want, mustRat(c.BalanceNOK))
				}
			}
			if amount == nil || amount.Cmp(want) != 0 {
				f.addCoded(field, codeOpenItemUnknown, "The customer's credit is "+money(want)+".")
				continue
			}
			creditCustomers, creditSeqs = append(creditCustomers, *l.CustomerId), append(creditSeqs, credits[k].asOfSeq)
		}
	}
	var documentIDs, documentSeqs []int64
	for i, l := range req.Body.Lines {
		if l.SourceDocumentId == nil {
			continue
		}
		field := "lines[" + itoa(i) + "].sourceDocumentId"
		d, ok := byID[*l.SourceDocumentId]
		amount, _ := parseAmount(l.Amount)
		switch {
		case !ok:
			f.addCoded(field, codeOpenItemUnknown, "Not an invoice open in Invoices at the day before the books start.")
		case l.CustomerId == nil || *l.CustomerId != d.CustomerID:
			f.addCoded(field, codeOpenItemUnknown, "The invoice is another customer's.")
		case amount == nil || amount.Cmp(mustRat(d.OpenNOK)) != 0:
			f.addCoded(field, codeOpenItemUnknown, "The invoice's open amount is "+money(mustRat(d.OpenNOK))+".")
		case slices.Contains(documentIDs, d.ID):
			f.addCoded(field, codeOpenItemUnknown, "The invoice is on another line already.")
		default:
			documentIDs, documentSeqs = append(documentIDs, d.ID), append(documentSeqs, d.asOfSeq)
		}
	}
	if err := f.refusal(title); err != nil {
		return nil, err
	}
	var posted Posted
	err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		// FOR UPDATE, not FOR SHARE: two openings entered at once wait for
		// each other here, and the second sees the first (H1).
		settings, err := q.GetSettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if final, err := openingFinal(ctx, q, settings); err != nil {
			return err
		} else if final {
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
		if err := checkOpeningLines(ctx, q, v.Lines); err != nil {
			return err
		}
		if posted, err = Post(ctx, tx, v); err != nil {
			return err
		}
		if err := q.SetOpeningAsOfSeq(ctx, store.SetOpeningAsOfSeqParams{Seq: asOfSeq, Now: s.now()}); err != nil {
			return err
		}
		if len(creditCustomers) > 0 {
			if err := q.SetCreditItemOpeningSeqs(ctx, store.SetCreditItemOpeningSeqsParams{CustomerIds: creditCustomers, Seqs: creditSeqs}); err != nil {
				return err
			}
		}
		if len(documentIDs) == 0 {
			return nil
		}
		return q.SetOpeningAsOfSeqs(ctx, store.SetOpeningAsOfSeqsParams{DocumentIds: documentIDs, Seqs: documentSeqs})
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
