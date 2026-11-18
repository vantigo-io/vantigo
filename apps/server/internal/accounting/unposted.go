package accounting

// Needs attention (A15) and the catch-up's state: the source facts that could
// not be posted, each with Retry — read again through Invoices' sales journal
// and posted when its cause is fixed, then what waited behind it, in order
// (R15) — and where each stream stands. There is no dismissal: a fact the
// books cannot hold is fixed, never waved away.

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// codeUnpostedPosted is a retry of a fact that has posted.
const codeUnpostedPosted = "unposted_already_posted"

const (
	defaultUnpostedLimit = 100
	maxUnpostedLimit     = 500
)

// unpostedView is a row as the contract answers it, with the customers' names.
func unpostedView(r store.GetUnpostedRow, names map[int32]string) gen.AccountingUnposted {
	out := gen.AccountingUnposted{Id: r.ID, Stream: r.Stream, Position: r.Position,
		Source:   gen.AccountingSource{Module: r.SourceModule, Kind: r.SourceKind, Id: r.SourceID, Part: r.SourcePart},
		FactDate: apiDatePtr(r.FactDate), DocumentId: r.DocumentID, DocumentNumber: r.DocumentNumber, CustomerId: r.CustomerID,
		Reason: r.Reason, Detail: r.Detail, Attempts: r.Attempts, FirstSeenAt: r.FirstSeenAt, LastTriedAt: r.LastTriedAt,
		PostedAt: r.PostedAt, InvoiceId: r.InvoiceID, WaitingOn: r.WaitingOn}
	if r.Amount != "" {
		out.Amount = ptr(money(mustRat(r.Amount)))
	}
	if r.CustomerID != nil {
		if name, ok := names[*r.CustomerID]; ok {
			out.CustomerName = &name
		}
	}
	if r.PostedVoucherID != nil && r.VoucherYear != nil && r.VoucherNumber != nil {
		out.PostedVoucher = &gen.AccountingVoucherRef{Id: *r.PostedVoucherID, Display: display(*r.VoucherYear, *r.VoucherNumber)}
	}
	out.OrderKey, out.Informational, out.ResolvedAt, out.SupplierInvoiceId = r.OrderKey, r.Informational, r.ResolvedAt, r.SupplierInvoiceID
	out.ExpenseLines = []gen.AccountingUnpostedExpenseLine{}
	notes := parseLineNotes(r.NokAmounts)
	for _, k := range sortedEntryIDs(notes) {
		n := notes[k]
		id, _ := strconv.ParseInt(k, 10, 64)
		out.ExpenseLines = append(out.ExpenseLines, gen.AccountingUnpostedExpenseLine{EntryId: id, Kind: n.Kind,
			Currency: n.Currency, Gross: n.Gross, AmountNok: n.AmountNOK})
	}
	return out
}

// ListAccountingUnposted is the facts under Needs attention, oldest first.
func (s *server) ListAccountingUnposted(ctx context.Context, req gen.ListAccountingUnpostedRequestObject) (gen.ListAccountingUnpostedResponseObject, error) {
	p := req.Params
	limit, offset := int32(defaultUnpostedLimit), int32(0)
	if p.Limit != nil {
		limit = min(max(*p.Limit, 1), maxUnpostedLimit)
	}
	if p.Offset != nil && *p.Offset > 0 {
		offset = *p.Offset
	}
	open := p.Open == nil || *p.Open
	rows, err := store.New(s.deps.Pool).ListUnposted(ctx, store.ListUnpostedParams{OpenOnly: open, RowLimit: limit, RowOffset: offset})
	if err != nil {
		return nil, err
	}
	var ids []int32
	for _, r := range rows {
		if r.CustomerID != nil {
			ids = append(ids, *r.CustomerID)
		}
	}
	names := s.customerNames(ctx, ids)
	out := gen.ListAccountingUnposted200JSONResponse{Items: []gen.AccountingUnposted{}}
	for _, r := range rows {
		out.Total = int32(r.Total)
		out.Items = append(out.Items, unpostedView(store.GetUnpostedRow{ID: r.ID, Stream: r.Stream, Position: r.Position,
			SourceModule: r.SourceModule, SourceKind: r.SourceKind, SourceID: r.SourceID, SourcePart: r.SourcePart,
			FactDate: r.FactDate, DocumentID: r.DocumentID, DocumentNumber: r.DocumentNumber, CustomerID: r.CustomerID,
			Amount: r.Amount, Reason: r.Reason, Detail: r.Detail, Attempts: r.Attempts, FirstSeenAt: r.FirstSeenAt,
			LastTriedAt: r.LastTriedAt, PostedVoucherID: r.PostedVoucherID, PostedAt: r.PostedAt, VoucherYear: r.VoucherYear,
			VoucherNumber: r.VoucherNumber, InvoiceID: r.InvoiceID, WaitingOn: r.WaitingOn, OrderKey: r.OrderKey,
			Informational: r.Informational, ResolvedAt: r.ResolvedAt, SupplierInvoiceID: r.SupplierInvoiceID,
			NokAmounts: r.NokAmounts}, names))
	}
	return out, nil
}

// RetryAccountingUnposted reads the fact again through the journal, with no
// transaction open, and posts it in one transaction (A15). When it posts, the
// rows of its invoice that waited behind it are retried the same way, one
// transaction each, in the order they were met, until one does not post
// (R15). The answer is the row: with its voucher when it posted, with its new
// reason when it did not.
func (s *server) RetryAccountingUnposted(ctx context.Context, req gen.RetryAccountingUnpostedRequestObject) (gen.RetryAccountingUnpostedResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetUnposted(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if row.PostedAt != nil {
		return nil, conflict(codeUnpostedPosted, "The fact has posted", "")
	}
	if row.ResolvedAt != nil {
		return nil, conflict(codeUnpostedWrongReason, "The fact is resolved", "It was made into a supplier invoice, or undone before it posted.")
	}
	out, err := s.retryAndDependants(ctx, row)
	if err != nil {
		return nil, err
	}
	return gen.RetryAccountingUnposted200JSONResponse(out), nil
}

// retryAndDependants retries a row and, when it posts, the rows of its
// invoice — or its entry or claim — recorded after it, in order, until one
// does not post (R15); it answers the row as it stands then.
func (s *server) retryAndDependants(ctx context.Context, row store.GetUnpostedRow) (gen.AccountingUnposted, error) {
	q := store.New(s.deps.Pool)
	got, err := s.retry(ctx, row)
	if err != nil {
		return gen.AccountingUnposted{}, err
	}
	if got == outcomePosted && (row.InvoiceID != nil || row.OrderKey != nil) {
		var dependants []store.GetUnpostedRow
		if row.InvoiceID != nil {
			rows, err := q.UnpostedDependants(ctx, store.UnpostedDependantsParams{InvoiceID: row.InvoiceID, AfterID: row.ID})
			if err != nil {
				return gen.AccountingUnposted{}, err
			}
			for _, d := range rows {
				dependants = append(dependants, store.GetUnpostedRow(d))
			}
		}
		// A credit fact of an invoice waits behind both its invoice and its
		// customer's credit: what waits behind either goes, in order.
		if row.OrderKey != nil {
			rows, err := q.UnpostedDependantsByKey(ctx, store.UnpostedDependantsByKeyParams{OrderKey: row.OrderKey, AfterID: row.ID})
			if err != nil {
				return gen.AccountingUnposted{}, err
			}
			for _, d := range rows {
				if !slices.ContainsFunc(dependants, func(o store.GetUnpostedRow) bool { return o.ID == d.ID }) {
					dependants = append(dependants, store.GetUnpostedRow(d))
				}
			}
			slices.SortFunc(dependants, func(a, b store.GetUnpostedRow) int { return cmp.Compare(a.ID, b.ID) })
		}
		for i := 0; i < len(dependants); i++ {
			d := dependants[i]
			if got, err := s.retry(ctx, d); err != nil {
				return gen.AccountingUnposted{}, err
			} else if got != outcomePosted {
				break
			}
			// A credit fact of an invoice that waited behind it may hold a
			// refund behind the customer's credit in turn: that goes next.
			if d.OrderKey == nil || (row.OrderKey != nil && *d.OrderKey == *row.OrderKey) {
				continue
			}
			rows, err := q.UnpostedDependantsByKey(ctx, store.UnpostedDependantsByKeyParams{OrderKey: d.OrderKey, AfterID: d.ID})
			if err != nil {
				return gen.AccountingUnposted{}, err
			}
			for _, r := range rows {
				if !slices.ContainsFunc(dependants, func(o store.GetUnpostedRow) bool { return o.ID == r.ID }) {
					dependants = append(dependants, store.GetUnpostedRow(r))
				}
			}
		}
	}
	row, err = q.GetUnposted(ctx, row.ID)
	if err != nil {
		return gen.AccountingUnposted{}, err
	}
	var ids []int32
	if row.CustomerID != nil {
		ids = append(ids, *row.CustomerID)
	}
	return unpostedView(row, s.customerNames(ctx, ids)), nil
}

// retry reads a row's fact again, with no transaction open, and posts it in
// one transaction, answering what it came to.
func (s *server) retry(ctx context.Context, row store.GetUnpostedRow) (outcome, error) {
	it, err := s.reread(ctx, row)
	if err != nil {
		return outcomeSkipped, err
	}
	now := s.now()
	got := outcomeUnposted
	err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if it == nil && row.Stream == streamExpenses {
			subject, occurrence, _ := strings.Cut(row.SourceKind, ".")
			return recordUnposted(ctx, q, sourceItem{stream: row.Stream, position: row.Position,
				expense: &contracts.ExpenseFact{Seq: row.Position, Subject: subject, Occurrence: occurrence,
					SubjectID: row.SourceID, Date: deref(row.FactDate)}},
				reasonDocumentUnknown, "Expenses' journal no longer answers this fact.", nil, now)
		}
		if it == nil {
			return recordUnposted(ctx, q, sourceItem{stream: row.Stream, position: row.Position, fact: &contracts.JournalFact{
				Seq: row.Position, Kind: row.SourceKind, SubjectID: row.SourceID, AmountNOK: row.Amount,
				InvoiceID: deref(row.InvoiceID)}},
				reasonDocumentUnknown, "Invoices' journal no longer answers this fact.", nil, now)
		}
		var err error
		got, err = postSource(ctx, tx, *it, now)
		return err
	})
	return got, err
}

// reread is the fact a row stands for, read again through the journal: nil
// when the journal no longer answers it (or Invoices is off).
func (s *server) reread(ctx context.Context, row store.GetUnpostedRow) (*sourceItem, error) {
	if row.Stream == streamExpenses {
		items, err := readExpenseStream(ctx, s.deps.ExpenseJournal, row.Position-1, 1)
		if err != nil {
			return nil, err
		}
		if len(items) != 1 || items[0].expense.Seq != row.Position || items[0].expense.SubjectID != row.SourceID {
			return nil, nil
		}
		items[0].nok, items[0].nokBy = noteAmounts(row.NokAmounts)
		return &items[0], nil
	}
	journal := s.deps.SalesJournal
	if journal == nil {
		return nil, nil
	}
	switch row.Stream {
	case streamDocuments:
		doc, err := journal.Document(ctx, row.SourceID)
		if err != nil || doc == nil {
			return nil, err
		}
		return &sourceItem{stream: row.Stream, position: doc.Number, doc: doc}, nil
	case streamFacts:
		items, err := readStream(ctx, journal, streamFacts, row.Position-1, 1)
		if err != nil {
			return nil, err
		}
		if len(items) != 1 || items[0].fact.Seq != row.Position || items[0].fact.SubjectID != row.SourceID {
			return nil, nil
		}
		return &items[0], nil
	}
	return nil, nil
}

// GetAccountingCatchUp is where each stream stands: its cursor, its last
// run, a wake-up waiting, and how many items the journal has after the
// cursor, counted up to a page — read with no transaction open.
func (s *server) GetAccountingCatchUp(ctx context.Context, _ gen.GetAccountingCatchUpRequestObject) (gen.GetAccountingCatchUpResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListCursors(ctx)
	if err != nil {
		return nil, err
	}
	unposted, err := q.CountUnposted(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]store.ListCursorsRow{}
	for _, r := range rows {
		known[r.Stream] = r
	}
	out := gen.GetAccountingCatchUp200JSONResponse{Streams: []gen.AccountingCatchUpStream{}, Unposted: unposted}
	for _, stream := range catchupStreams {
		r := known[stream]
		v := gen.AccountingCatchUpStream{Stream: gen.AccountingCatchUpStreamStream(stream), Position: r.Position,
			LastRunAt: r.LastRunAt, LastError: r.LastError, RequestedAt: r.RequestedAt}
		if r.Stream == "" {
			if at, err := q.PendingRequest(ctx, stream); err == nil {
				v.RequestedAt = &at
			}
		}
		if composed(s.deps, stream) {
			deps := s.deps
			if deps.SalesJournal != nil {
				deps.SalesJournal = countingJournal{deps.SalesJournal}
			}
			items, err := readAny(ctx, deps, stream, r.Position, contracts.MaxJournalPage)
			if err != nil {
				s.logger().WarnContext(ctx, "accounting: read how far the books are behind", "stream", stream, "error", err)
			} else {
				v.Behind = ptr(int32(len(items)))
				v.BehindMore = len(items) >= contracts.MaxJournalPage
			}
		} else {
			v.Behind = ptr(int32(0))
		}
		out.Streams = append(out.Streams, v)
	}
	return out, nil
}

// countingJournal reads a journal for counting only: it never fetches a
// write-off's invoice.
type countingJournal struct{ contracts.SalesJournal }

func (countingJournal) Document(context.Context, int64) (*contracts.JournalDocument, error) {
	return nil, nil
}
