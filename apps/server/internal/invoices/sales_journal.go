package invoices

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.SalesJournal (accounting design A12,
// R1): its issued documents in number order with their lines and VAT
// summaries, the fact log (invoices.journal_facts, migration 00178) in seq
// order, and the invoices open at a day.
//
// It serves from this module's own tables and nothing else, on the pool, and
// holds no lock and opens no transaction, as salesTax does. A moment becomes a
// day in the workspace's business time zone, read through the business
// calendar, as the module judges its own documents' days: a removal fact's
// day, and the end of OpenAt's day.
//
// A charge payment pays whatever an invoice's letters claimed, not one charge
// of its own, so its fact is split here (ChargePart): reminderrules.Allocate,
// the allocation the charges themselves use — oldest letter's fee or
// compensation first, then interest, then what is beyond every charge — over
// the letters sent and the waivers made by the time it was registered, with
// and without it, the charge payments registered before it and not removed by
// then counted first. Its removal answers the same split, so a ledger's
// reversal undoes exactly what the registration posted.

// salesJournal is this module's contracts.SalesJournal.
type salesJournal struct {
	q        *store.Queries
	calendar contracts.BusinessCalendar
}

var _ contracts.SalesJournal = (*salesJournal)(nil)

// newSalesJournal is Module's SalesJournal: the constructor Compose and
// Workers call. It takes the pool and the business calendar and nothing else.
func newSalesJournal(d module.Deps) contracts.SalesJournal {
	return &salesJournal{q: store.New(d.Pool), calendar: d.Calendar}
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
	var charged []int64
	for _, r := range rows {
		if r.RemovedAt != nil && loc == nil {
			if loc, err = j.zone(ctx); err != nil {
				return nil, err
			}
		}
		if (r.Kind == contracts.FactChargePayment || r.Kind == contracts.FactChargePaymentRemoved) && !slices.Contains(charged, r.InvoiceID) {
			charged = append(charged, r.InvoiceID)
		}
	}
	splits, err := j.chargeSplits(ctx, charged)
	if err != nil {
		return nil, err
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
		if r.Kind == contracts.FactChargePayment || r.Kind == contracts.FactChargePaymentRemoved {
			parts := splits[r.SubjectID]
			f.Charges = make([]contracts.ChargePart, 0, len(parts))
			for _, p := range parts {
				a := docmath.Round2(new(big.Rat).Mul(p.amount, mustRatOf(r.ExchangeRate))).FloatString(2)
				f.Charges = append(f.Charges, contracts.ChargePart{Kind: p.kind, AmountNOK: a})
			}
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// mustRatOf is an exchange rate as an exact decimal; a NOT NULL numeric
// column always reads, so a failure is 1 — never reached.
func mustRatOf(n pgtype.Numeric) *big.Rat {
	r, err := ratFromNumeric(n)
	if err != nil {
		return big.NewRat(1, 1)
	}
	return r
}

// chargePart is one kind of charge a charge payment paid, in its currency.
type chargePart struct {
	kind   string
	amount *big.Rat
}

// chargeSplits is every charge payment's split of the invoices named, by the
// charge payment's id (this file's doc comment).
func (j *salesJournal) chargeSplits(ctx context.Context, invoiceIDs []int64) (map[int64][]chargePart, error) {
	out := map[int64][]chargePart{}
	if len(invoiceIDs) == 0 {
		return out, nil
	}
	facts, err := j.q.JournalChargeFactsOf(ctx, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the charge payments' facts: %w", err)
	}
	letterRows, err := j.q.JournalLettersOf(ctx, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the letters' charges: %w", err)
	}
	waiverRows, err := j.q.JournalWaiversOf(ctx, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the charge waivers: %w", err)
	}
	type sentLetter struct {
		at     time.Time
		letter reminderrules.Letter
	}
	type madeWaiver struct {
		at     time.Time
		waiver reminderrules.Waiver
	}
	letters := map[int64][]sentLetter{}
	for _, r := range letterRows {
		l, err := letterOf(r)
		if err != nil {
			return nil, err
		}
		if r.SentAt != nil {
			letters[r.InvoiceID] = append(letters[r.InvoiceID], sentLetter{at: *r.SentAt, letter: l})
		}
	}
	waivers := map[int64][]madeWaiver{}
	for _, r := range waiverRows {
		w, err := waiverOf(r)
		if err != nil {
			return nil, err
		}
		waivers[r.InvoiceID] = append(waivers[r.InvoiceID], madeWaiver{at: r.WaivedAt, waiver: w})
	}
	// Each charge payment's registration and removal seqs, in seq order.
	type payment struct {
		id, invoiceID       int64
		registered, removed int64
		at                  time.Time
		pay                 reminderrules.ChargePayment
	}
	var order []*payment
	byID := map[int64]*payment{}
	for _, f := range facts {
		p := byID[f.SubjectID]
		if p == nil {
			amount, err := ratFromNumeric(f.Amount)
			if err != nil {
				return nil, err
			}
			p = &payment{id: f.SubjectID, invoiceID: f.InvoiceID, at: f.RegisteredAt,
				pay: reminderrules.ChargePayment{ID: f.SubjectID, PaidOn: f.PaidOn.Time, Amount: amount}}
			byID[f.SubjectID] = p
			order = append(order, p)
		}
		if f.Kind == contracts.FactChargePayment {
			p.registered = f.Seq
		} else {
			p.removed = f.Seq
		}
	}
	for _, p := range order {
		var sent []reminderrules.Letter
		for _, l := range letters[p.invoiceID] {
			if !l.at.After(p.at) {
				sent = append(sent, l.letter)
			}
		}
		var made []reminderrules.Waiver
		for _, w := range waivers[p.invoiceID] {
			if !w.at.After(p.at) {
				made = append(made, w.waiver)
			}
		}
		var before []reminderrules.ChargePayment
		for _, o := range order {
			if o.invoiceID == p.invoiceID && o.registered < p.registered && (o.removed == 0 || o.removed > p.registered) {
				before = append(before, o.pay)
			}
		}
		out[p.id] = splitOf(sent, made, before, p.pay)
	}
	return out, nil
}

// splitOf is what pay adds to the allocation of before over the letters and
// waivers: per letter its fee or compensation, the interest, and the refund
// due, in that order, zero parts left out.
func splitOf(letters []reminderrules.Letter, waivers []reminderrules.Waiver, before []reminderrules.ChargePayment, pay reminderrules.ChargePayment) []chargePart {
	perBefore, interestBefore, refundBefore := reminderrules.Allocate(letters, waivers, before)
	perAfter, interestAfter, refundAfter := reminderrules.Allocate(letters, waivers, append(append([]reminderrules.ChargePayment{}, before...), pay))
	sums := map[string]*big.Rat{}
	add := func(kind string, after, before *big.Rat) {
		if after == nil {
			return
		}
		d := new(big.Rat).Set(after)
		if before != nil {
			d.Sub(d, before)
		}
		if d.Sign() == 0 {
			return
		}
		if sums[kind] == nil {
			sums[kind] = new(big.Rat)
		}
		sums[kind].Add(sums[kind], d)
	}
	for _, l := range letters {
		kind := contracts.ChargeReminderFee
		if l.FeeKind == reminderrules.FeeCompensation {
			kind = contracts.ChargeCompensation
		}
		add(kind, perAfter[l.ID], perBefore[l.ID])
	}
	add(contracts.ChargeInterest, interestAfter, interestBefore)
	add(contracts.ChargeRefundDue, refundAfter, refundBefore)
	var parts []chargePart
	for _, kind := range []string{contracts.ChargeReminderFee, contracts.ChargeCompensation, contracts.ChargeInterest, contracts.ChargeRefundDue} {
		if sums[kind] != nil {
			parts = append(parts, chargePart{kind: kind, amount: sums[kind]})
		}
	}
	return parts
}

// OpenAt is every invoice open at the end of day, in number order.
func (j *salesJournal) OpenAt(ctx context.Context, day time.Time) ([]contracts.OpenDocument, error) {
	loc, err := j.zone(ctx)
	if err != nil {
		return nil, err
	}
	d := utcDay(day)
	liveUntil := time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc)
	rows, err := j.q.JournalOpenAt(ctx, store.JournalOpenAtParams{Day: pgDate(d), LiveUntil: liveUntil})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the invoices open on %s: %w", d.Format(time.DateOnly), err)
	}
	out := make([]contracts.OpenDocument, 0, len(rows))
	for _, r := range rows {
		open, err := nok(r.OpenAmount, r.ExchangeRate)
		if err != nil {
			return nil, err
		}
		out = append(out, contracts.OpenDocument{
			ID: r.ID, Number: r.Number, CustomerID: r.CustomerID, IssueDate: r.IssueDate.Time, DueDate: r.DueDate.Time,
			Currency: r.Currency, OpenNOK: open,
		})
	}
	return out, nil
}
