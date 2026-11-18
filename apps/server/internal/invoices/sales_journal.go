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
// summaries, one by its id, the fact log (invoices.journal_facts, migrations
// 00180, 00182 and 00197) in seq order, an invoice's credit notes, the
// invoices open at a day and the customers' credit at a day.
//
// It serves from this module's own tables and nothing else, on the pool, and
// holds no lock, as salesTax does; OpenAt and CreditsAt alone open a
// transaction, read-only and REPEATABLE READ, so their rows and their AsOfSeq
// are one snapshot. A
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
	return j.documents(ctx, rows)
}

// Document is the issued document with that id, nil when there is none.
func (j *salesJournal) Document(ctx context.Context, id int64) (*contracts.JournalDocument, error) {
	rows, err := j.q.JournalDocumentByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the journal's document %d: %w", id, err)
	}
	docs := make([]store.JournalDocumentsRow, 0, len(rows))
	for _, r := range rows {
		docs = append(docs, store.JournalDocumentsRow(r))
	}
	out, err := j.documents(ctx, docs)
	if err != nil || len(out) == 0 {
		return nil, err
	}
	return &out[0], nil
}

// CreditNotesOf is the issued credit notes of the invoice with that id, in
// number order.
func (j *salesJournal) CreditNotesOf(ctx context.Context, invoiceID int64) ([]contracts.JournalDocument, error) {
	rows, err := j.q.JournalCreditNotesOf(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the credit notes of %d: %w", invoiceID, err)
	}
	docs := make([]store.JournalDocumentsRow, 0, len(rows))
	for _, r := range rows {
		docs = append(docs, store.JournalDocumentsRow(r))
	}
	return j.documents(ctx, docs)
}

// documents is rows with their lines and VAT summaries, as the contract
// answers them.
func (j *salesJournal) documents(ctx context.Context, rows []store.JournalDocumentsRow) ([]contracts.JournalDocument, error) {
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
		rate, err := ratFromNumeric(r.ExchangeRate)
		if err != nil {
			return nil, err
		}
		d.ExchangeRate = rate.FloatString(ratesMaxDecimals)
		grossNOK := new(big.Rat)
		for _, v := range d.VATSummaries {
			b, _ := new(big.Rat).SetString(v.BaseNOK)
			x, _ := new(big.Rat).SetString(v.VATNOK)
			grossNOK.Add(grossNOK, b).Add(grossNOK, x)
		}
		d.GrossNOK = grossNOK.FloatString(2)
		docs = append(docs, d)
	}
	return docs, nil
}

// journalLine is a line as the contract answers it.
func journalLine(l store.JournalLinesRow, rate pgtype.Numeric) (contracts.JournalLine, error) {
	// The NOK the issue snapshotted (I21); a line issued before it — NOK —
	// at its document's rate.
	net, err := nok(l.LineNet, rate)
	if err != nil {
		return contracts.JournalLine{}, err
	}
	if l.LineNetNok.Valid {
		if net, err = decimal2(l.LineNetNok); err != nil {
			return contracts.JournalLine{}, err
		}
	}
	own, err := decimal2(l.LineNet)
	if err != nil {
		return contracts.JournalLine{}, err
	}
	ratePercent, err := decimal2(l.VatRatePercent)
	if err != nil {
		return contracts.JournalLine{}, err
	}
	return contracts.JournalLine{
		ID: l.ID, Position: l.Position, NetNOK: net, Net: own, Kind: deref(l.LineKind),
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
	if v.TaxableAmountNok.Valid {
		if base, err = decimal2(v.TaxableAmountNok); err != nil {
			return contracts.JournalVATSummary{}, err
		}
	}
	ownBase, err := decimal2(v.TaxableAmount)
	if err != nil {
		return contracts.JournalVATSummary{}, err
	}
	ownVAT, err := decimal2(v.VatAmount)
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
	return contracts.JournalVATSummary{SAFT: v.SafTCode, Category: v.VatCategory, RatePercent: ratePercent, BaseNOK: base, VATNOK: vat,
		Base: ownBase, VAT: ownVAT}, nil
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
		if r.RemovedAt != nil || r.MergedAt != nil {
			if loc, err = j.zone(ctx); err != nil {
				return nil, err
			}
			break
		}
	}
	facts := make([]contracts.JournalFact, 0, len(rows))
	for _, r := range rows {
		// The NOK the row stored (I13), and a charge payment's amount at its
		// invoice's rate, as before the column.
		var amount string
		if r.AmountNok.Valid {
			a, err := ratFromNumeric(r.AmountNok)
			if err != nil {
				return nil, err
			}
			amount = a.Abs(a).FloatString(2)
		} else if amount, err = nok(r.Amount, r.ExchangeRate); err != nil {
			return nil, err
		}
		f := contracts.JournalFact{
			Seq: r.Seq, Kind: r.Kind, SubjectID: r.SubjectID, InvoiceID: deref64(r.InvoiceID), InvoiceNumber: r.InvoiceNumber,
			CustomerID: r.CustomerID, AmountNOK: amount, Source: r.Source, BankAccount: r.BankAccount, BankReference: r.BankReference,
			Currency: r.PaymentCurrency, Amount: amount, ExchangeDifferenceNOK: "0.00",
		}
		// A payment's own amount and its exchange difference (I24).
		if r.Amount.Valid && r.Kind != contracts.FactCredit && r.Kind != contracts.FactCreditMerged {
			a, err := ratFromNumeric(r.Amount)
			if err != nil {
				return nil, err
			}
			f.Amount = a.Abs(a).FloatString(2)
			if f.ExchangeDifferenceNOK, err = decimal2(r.ExchangeDifferenceNok); err != nil {
				return nil, err
			}
		}
		switch {
		case r.MergedAt != nil:
			// A merge's day is the business day it was made on, as a
			// removal's is.
			f.Date = holidays.DayIn(*r.MergedAt, loc)
		case r.BusinessDate.Valid:
			f.Date = r.BusinessDate.Time
		case r.RemovedAt != nil:
			f.Date = holidays.DayIn(*r.RemovedAt, loc)
		}
		if r.Kind == contracts.FactCredit || r.Kind == contracts.FactCreditMerged {
			if f.Credit, err = journalCredit(r); err != nil {
				return nil, err
			}
		}
		if r.Kind == contracts.FactExchangeDifference {
			if f.ExchangeDifferenceNOK, err = decimal2(r.ExchangeDifferenceNok); err != nil {
				return nil, err
			}
		}
		if r.WriteOffID != nil {
			// The NOK the write-off stored — off NOK the receivable it
			// closed (I24) — and what was recovered before in NOK.
			written, err := decimal2(r.WriteOffAmountNok)
			if err != nil {
				return nil, err
			}
			before, err := decimal2(r.RecoveredBeforeNok)
			if err != nil {
				return nil, err
			}
			f.WriteOff = &contracts.JournalWriteOff{ID: *r.WriteOffID, Reason: deref(r.WriteOffReason),
				VATCorrection: r.WriteOffVatCorrection != nil && *r.WriteOffVatCorrection, AmountNOK: written,
				RecoveredBeforeNOK: before}
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// journalCredit is a credit fact's movement, or a merge's, as the contract
// answers it.
func journalCredit(r store.JournalFactsRow) (*contracts.JournalCredit, error) {
	amount, err := decimal2(r.Amount)
	if err != nil {
		return nil, err
	}
	amountNOK, err := decimal2(r.AmountNok)
	if err != nil {
		return nil, err
	}
	c := &contracts.JournalCredit{
		Kind: deref(r.CreditKind), Currency: r.CreditCurrency, Amount: amount, AmountNOK: amountNOK,
		PaymentID: deref64(r.CreditPaymentID), BankTransactionID: deref64(r.CreditBankTransactionID),
		DocumentID: deref64(r.CreditDocumentID), RefundID: deref64(r.CreditRefundID), ReversesID: deref64(r.CreditReversesID),
		ReversedKind: deref(r.CreditReversedKind), RefundBankAccount: r.RefundBankAccount,
		FromCustomerID: derefInt32(r.MergeFromCustomerID),
	}
	if c.ExchangeDifferenceNOK, err = decimal2(r.CreditExchangeDifferenceNok); err != nil {
		return nil, err
	}
	return c, nil
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
		openNOK, err := decimal2(r.OpenNok)
		if err != nil {
			return contracts.OpenPage{}, err
		}
		open, err := decimal2(r.OpenAmount)
		if err != nil {
			return contracts.OpenPage{}, err
		}
		out.Documents = append(out.Documents, contracts.OpenDocument{
			ID: r.ID, Number: r.Number, CustomerID: r.CustomerID, IssueDate: r.IssueDate.Time, DueDate: r.DueDate.Time,
			Currency: r.Currency, OpenNOK: openNOK, Open: open,
		})
	}
	return out, nil
}

// CreditsAt is up to limit customers with an id after afterCustomerID whose
// credit at the end of day is not zero, per currency, with the highest seq
// of a fact seen in the same snapshot (I16): OpenAt's shape, the business
// time zone read first.
func (j *salesJournal) CreditsAt(ctx context.Context, day time.Time, afterCustomerID int32, limit int) (contracts.CreditPage, error) {
	loc, err := j.zone(ctx)
	if err != nil {
		return contracts.CreditPage{}, err
	}
	d := utcDay(day)
	liveUntil := time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc)
	var asOf int64
	var rows []store.JournalCreditsAtRow
	err = db.WithTx(ctx, j.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := j.q.WithTx(tx)
		var err error
		if rows, err = q.JournalCreditsAt(ctx, store.JournalCreditsAtParams{
			Day: pgDate(d), LiveUntil: liveUntil, AfterCustomerID: afterCustomerID, RowLimit: journalLimit(limit),
		}); err != nil {
			return err
		}
		asOf, err = q.JournalLastSeq(ctx)
		return err
	})
	if err != nil {
		return contracts.CreditPage{}, fmt.Errorf("invoices: read the customers' credit on %s: %w", d.Format(time.DateOnly), err)
	}
	out := contracts.CreditPage{AsOfSeq: asOf, Credits: make([]contracts.CustomerCredit, 0, len(rows))}
	for _, r := range rows {
		balance, err := decimal2(r.Balance)
		if err != nil {
			return contracts.CreditPage{}, err
		}
		balanceNOK, err := decimal2(r.BalanceNok)
		if err != nil {
			return contracts.CreditPage{}, err
		}
		out.Credits = append(out.Credits, contracts.CustomerCredit{
			CustomerID: r.CustomerID, Currency: r.Currency, Balance: balance, BalanceNOK: balanceNOK,
		})
	}
	return out, nil
}
