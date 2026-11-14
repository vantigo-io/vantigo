package invoices

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.SalesJournal (accounting design A12,
// R1): its issued documents in number order with their lines and VAT
// summaries, the fact log (invoices.journal_facts, migration 00178) in seq
// order, and the invoices open at a day.
//
// It serves from this module's own tables and nothing else, on the pool, and
// holds no lock, as salesTax does; OpenAt alone opens a transaction, read-only
// and REPEATABLE READ, so its invoices and its AsOfSeq are one snapshot. A
// moment becomes a day in the workspace's business time zone, read through
// the business calendar, as the module judges its own documents' days: a
// removal fact's day, and the end of OpenAt's day.
//
// A charge payment's fact carries its amount and nothing of what it paid: a
// charge payment pays whatever the invoice's letters claimed, and that
// allocation moves as later letters, waivers and removals change it, so it is
// not a fact a ledger could post once and reverse exactly.

// salesJournal is this module's contracts.SalesJournal.
type salesJournal struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	calendar contracts.BusinessCalendar
}

var _ contracts.SalesJournal = (*salesJournal)(nil)

// newSalesJournal is Module's SalesJournal: the constructor Compose and
// Workers call. It takes the pool and the business calendar and nothing else.
func newSalesJournal(d module.Deps) contracts.SalesJournal {
	return &salesJournal{pool: d.Pool, q: store.New(d.Pool), calendar: d.Calendar}
}

// zone is the workspace's business time zone, UTC when no calendar is
// composed.
func (j *salesJournal) zone(ctx context.Context) (*time.Location, error) {
	if j.calendar == nil {
		return time.UTC, nil
	}
	noteContractCall(ctx, "Calendar.Zone")
	loc, err := j.calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the business time zone: %w", err)
	}
	return loc, nil
}

// journalLimit is a page's limit within [1, contracts.MaxJournalPage].
func journalLimit(limit int) int32 {
	return int32(min(max(limit, 1), contracts.MaxJournalPage))
}

// nok is amount at rate in NOK, two decimals, docmath's rounding.
func nok(amount, rate pgtype.Numeric) (string, error) {
	a, err := ratFromNumeric(amount)
	if err != nil {
		return "", err
	}
	r, err := ratFromNumeric(rate)
	if err != nil {
		return "", err
	}
	return docmath.Round2(new(big.Rat).Mul(a, r)).FloatString(2), nil
}

// decimal2 is a numeric as two-decimal text, "" for NULL.
func decimal2(n pgtype.Numeric) (string, error) {
	if !n.Valid {
		return "", nil
	}
	r, err := ratFromNumeric(n)
	if err != nil {
		return "", err
	}
	return r.FloatString(2), nil
}

// Documents is up to limit issued documents after afterNumber, in number
// order, with their lines and VAT summaries.
func (j *salesJournal) Documents(ctx context.Context, afterNumber int64, limit int) ([]contracts.JournalDocument, error) {
	rows, err := j.q.JournalDocuments(ctx, store.JournalDocumentsParams{AfterNumber: afterNumber, RowLimit: journalLimit(limit)})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the journal's documents: %w", err)
	}
	if len(rows) == 0 {
		return []contracts.JournalDocument{}, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	lines, err := j.q.JournalLines(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the journal's lines: %w", err)
	}
	summaries, err := j.q.JournalVatSummaries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the journal's VAT summaries: %w", err)
	}
	rates := make(map[int64]pgtype.Numeric, len(rows))
	for _, r := range rows {
		rates[r.ID] = r.ExchangeRate
	}
	linesOf := map[int64][]contracts.JournalLine{}
	for _, l := range lines {
		out, err := journalLine(l, rates[l.InvoiceID])
		if err != nil {
			return nil, err
		}
		linesOf[l.InvoiceID] = append(linesOf[l.InvoiceID], out)
	}
	summariesOf := map[int64][]contracts.JournalVATSummary{}
	for _, v := range summaries {
		out, err := journalSummary(v, rates[v.InvoiceID])
		if err != nil {
			return nil, err
		}
		summariesOf[v.InvoiceID] = append(summariesOf[v.InvoiceID], out)
	}
	docs := make([]contracts.JournalDocument, 0, len(rows))
	for _, r := range rows {
		d := contracts.JournalDocument{
			ID: r.ID, Number: r.Number, Kind: r.Kind, IssueDate: r.IssueDate.Time, DueDate: pgDateOf(r.DueDate),
			CustomerID: r.CustomerID, Currency: r.Currency,
			Lines: linesOf[r.ID], VATSummaries: summariesOf[r.ID],
		}
		if r.CreditsInvoiceID != nil {
			d.CreditsID = *r.CreditsInvoiceID
		}
		if r.CreditsNumber != nil {
			d.CreditsNumber = *r.CreditsNumber
		}
		for _, f := range []struct {
			dst *string
			n   pgtype.Numeric
		}{{&d.Net, r.NetTotal}, {&d.VAT, r.VatTotal}, {&d.Gross, r.GrossTotal}, {&d.VATNOK, r.VatTotalNok}} {
			if *f.dst, err = decimal2(f.n); err != nil {
				return nil, err
			}
		}
		docs = append(docs, d)
	}
	return docs, nil
}

// journalLine is a line as the contract answers it.
func journalLine(l store.JournalLinesRow, rate pgtype.Numeric) (contracts.JournalLine, error) {
	net, err := nok(l.LineNet, rate)
	if err != nil {
		return contracts.JournalLine{}, err
	}
	ratePercent, err := decimal2(l.VatRatePercent)
	if err != nil {
		return contracts.JournalLine{}, err
	}
	return contracts.JournalLine{
		ID: l.ID, Position: l.Position, NetNOK: net, Kind: deref(l.LineKind),
		TaxCategoryID: l.TaxCategoryID, ProjectID: l.ProjectID,
		SAFT: deref(l.SafTCode), Category: deref(l.VatCategory), RatePercent: ratePercent,
	}, nil
}

// journalSummary is a VAT summary row as the contract answers it.
func journalSummary(v store.JournalVatSummariesRow, rate pgtype.Numeric) (contracts.JournalVATSummary, error) {
	base, err := nok(v.TaxableAmount, rate)
	if err != nil {
		return contracts.JournalVATSummary{}, err
	}
	vat, err := decimal2(v.VatAmountNok)
	if err != nil {
		return contracts.JournalVATSummary{}, err
	}
	ratePercent, err := decimal2(v.RatePercent)
	if err != nil {
		return contracts.JournalVATSummary{}, err
	}
	return contracts.JournalVATSummary{SAFT: v.SafTCode, Category: v.VatCategory, RatePercent: ratePercent, BaseNOK: base, VATNOK: vat}, nil
}

// Facts is up to limit facts after afterSeq, in seq order.
func (j *salesJournal) Facts(ctx context.Context, afterSeq int64, limit int) ([]contracts.JournalFact, error) {
	rows, err := j.q.JournalFacts(ctx, store.JournalFactsParams{AfterSeq: afterSeq, RowLimit: journalLimit(limit)})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the journal's facts: %w", err)
	}
	if len(rows) == 0 {
		return []contracts.JournalFact{}, nil
	}
	var loc *time.Location
	for _, r := range rows {
		if r.RemovedAt != nil {
			if loc, err = j.zone(ctx); err != nil {
				return nil, err
			}
			break
		}
	}
	facts := make([]contracts.JournalFact, 0, len(rows))
	for _, r := range rows {
		amount, err := nok(r.Amount, r.ExchangeRate)
		if err != nil {
			return nil, err
		}
		f := contracts.JournalFact{
			Seq: r.Seq, Kind: r.Kind, SubjectID: r.SubjectID, InvoiceID: r.InvoiceID, InvoiceNumber: r.InvoiceNumber,
			CustomerID: r.CustomerID, AmountNOK: amount, Source: r.Source, BankAccount: r.BankAccount,
		}
		switch {
		case r.BusinessDate.Valid:
			f.Date = r.BusinessDate.Time
		case r.RemovedAt != nil:
			f.Date = holidays.DayIn(*r.RemovedAt, loc)
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// OpenAt is up to limit invoices numbered after afterNumber open at the end
// of day, in number order, with the highest seq of a fact seen in the same
// snapshot. The business time zone is read first, before the transaction.
func (j *salesJournal) OpenAt(ctx context.Context, day time.Time, afterNumber int64, limit int) (contracts.OpenPage, error) {
	loc, err := j.zone(ctx)
	if err != nil {
		return contracts.OpenPage{}, err
	}
	d := utcDay(day)
	liveUntil := time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc)
	var asOf int64
	var rows []store.JournalOpenAtRow
	err = db.WithTx(ctx, j.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := j.q.WithTx(tx)
		var err error
		if rows, err = q.JournalOpenAt(ctx, store.JournalOpenAtParams{
			Day: pgDate(d), LiveUntil: liveUntil, AfterNumber: afterNumber, RowLimit: journalLimit(limit),
		}); err != nil {
			return err
		}
		asOf, err = q.JournalLastSeq(ctx)
		return err
	})
	if err != nil {
		return contracts.OpenPage{}, fmt.Errorf("invoices: read the invoices open on %s: %w", d.Format(time.DateOnly), err)
	}
	out := contracts.OpenPage{AsOfSeq: asOf, Documents: make([]contracts.OpenDocument, 0, len(rows))}
	for _, r := range rows {
		open, err := nok(r.OpenAmount, r.ExchangeRate)
		if err != nil {
			return contracts.OpenPage{}, err
		}
		out.Documents = append(out.Documents, contracts.OpenDocument{
			ID: r.ID, Number: r.Number, CustomerID: r.CustomerID, IssueDate: r.IssueDate.Time, DueDate: r.DueDate.Time,
			Currency: r.Currency, OpenNOK: open,
		})
	}
	return out, nil
}
