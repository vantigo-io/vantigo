package invoices

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the invoice journal (D11): what proves complete registration —
// Skatteetaten wants it visible that "det ikke er brudd i nummerserien". It
// lists the issued documents of a range of issue dates in number order, totals
// them per SAF-T code, category and rate over the whole range, and checks the
// series for gaps. Credit notes are stored positive and signed negative here,
// in every amount.

// maxJournalGaps is how many missing numbers the journal lists (D11).
const maxJournalGaps = 1000

// signed is a document's amount as the journal shows it.
func signed(kind string, n pgtype.Numeric) (float64, error) {
	r, err := ratFromNumeric(n)
	if err != nil {
		return 0, err
	}
	if kind == kindCreditNote {
		r.Neg(r)
	}
	return floatFromRat(r, 2), nil
}

// GetInvoicesJournal The invoice journal
// (GET /api/v1/invoices/journal)
func (s *server) GetInvoicesJournal(ctx context.Context, req gen.GetInvoicesJournalRequestObject) (gen.GetInvoicesJournalResponseObject, error) {
	p := req.Params
	errs := validatePageParams(p.Page, p.PageSize)
	if p.From.After(p.To.Time) {
		errs = append(errs, "'from' must be on or before 'to'.")
	}
	if len(errs) > 0 {
		return gen.GetInvoicesJournal400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	from, to := pgDate(utcDay(p.From.Time)), pgDate(utcDay(p.To.Time))

	// One snapshot, so the page, the totals and the gaps agree with each other
	// even while documents are being issued.
	var resp gen.InvoicesJournalResponse
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		settings, err := q.GetSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the settings: %w", err)
		}
		rows, err := q.JournalPage(ctx, store.JournalPageParams{IssuedFrom: from, IssuedTo: to, PageOffset: (page - 1) * pageSize, PageSize: pageSize})
		if err != nil {
			return fmt.Errorf("invoices: read the journal: %w", err)
		}
		total, err := q.JournalCount(ctx, store.JournalCountParams{IssuedFrom: from, IssuedTo: to})
		if err != nil {
			return fmt.Errorf("invoices: count the journal: %w", err)
		}
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		sums, err := q.JournalSummaries(ctx, ids)
		if err != nil {
			return fmt.Errorf("invoices: read the journal's VAT: %w", err)
		}
		kinds := map[int64]string{}
		for _, r := range rows {
			kinds[r.ID] = r.Kind
		}
		byDoc := map[int64][]gen.InvoicesJournalCode{}
		for _, sm := range sums {
			code := gen.InvoicesJournalCode{SafTCode: sm.SafTCode, Category: sm.VatCategory}
			if code.RatePercent, err = floatFromNumeric(sm.RatePercent); err != nil {
				return err
			}
			if code.TaxableAmount, err = signed(kinds[sm.InvoiceID], sm.TaxableAmount); err != nil {
				return err
			}
			if code.VatAmount, err = signed(kinds[sm.InvoiceID], sm.VatAmount); err != nil {
				return err
			}
			byDoc[sm.InvoiceID] = append(byDoc[sm.InvoiceID], code)
		}

		resp.Data = make([]gen.InvoicesJournalRow, 0, len(rows))
		for _, r := range rows {
			row := gen.InvoicesJournalRow{
				Id: r.ID, Number: *r.Number, Kind: r.Kind, IssueDate: wireDate(r.IssueDate.Time),
				DeliveryDate: wireDateOf(r.DeliveryDate), DeliveryFrom: wireDateOf(r.DeliveryFrom), DeliveryTo: wireDateOf(r.DeliveryTo),
				DueDate: wireDateOf(r.DueDate), BuyerCustomerNumber: r.BuyerCustomerNumber, BuyerName: r.BuyerName,
				BuyerOrganisationNumber: r.BuyerOrganisationNumber, Currency: r.Currency, CreditsNumber: r.CreditsNumber,
				VatSummaries: byDoc[r.ID],
			}
			if row.VatSummaries == nil {
				row.VatSummaries = []gen.InvoicesJournalCode{}
			}
			for _, c := range []struct {
				dst *float64
				n   pgtype.Numeric
			}{{&row.NetTotal, r.NetTotal}, {&row.VatTotal, r.VatTotal}, {&row.GrossTotal, r.GrossTotal}} {
				if *c.dst, err = signed(r.Kind, c.n); err != nil {
					return err
				}
			}
			resp.Data = append(resp.Data, row)
		}
		resp.Pagination = apicommon.Pagination(page, pageSize, total)

		codes, err := q.JournalTotalsByCode(ctx, store.JournalTotalsByCodeParams{IssuedFrom: from, IssuedTo: to})
		if err != nil {
			return fmt.Errorf("invoices: total the journal's VAT: %w", err)
		}
		resp.Totals.ByCode = make([]gen.InvoicesJournalCode, 0, len(codes))
		for _, c := range codes {
			code := gen.InvoicesJournalCode{SafTCode: c.SafTCode, Category: c.VatCategory}
			for _, f := range []struct {
				dst *float64
				n   pgtype.Numeric
			}{{&code.RatePercent, c.RatePercent}, {&code.TaxableAmount, c.TaxableAmount}, {&code.VatAmount, c.VatAmount}} {
				if *f.dst, err = floatFromNumeric(f.n); err != nil {
					return err
				}
			}
			resp.Totals.ByCode = append(resp.Totals.ByCode, code)
		}
		totals, err := q.JournalTotals(ctx, store.JournalTotalsParams{IssuedFrom: from, IssuedTo: to})
		if err != nil {
			return fmt.Errorf("invoices: total the journal: %w", err)
		}
		for _, f := range []struct {
			dst *float64
			n   pgtype.Numeric
		}{{&resp.Totals.NetTotal, totals.NetTotal}, {&resp.Totals.VatTotal, totals.VatTotal}, {&resp.Totals.GrossTotal, totals.GrossTotal}} {
			if *f.dst, err = floatFromNumeric(f.n); err != nil {
				return err
			}
		}

		checked, err := q.JournalCheckedRange(ctx, store.JournalCheckedRangeParams{
			IssuedFrom: from, IssuedTo: to, SeriesStart: settings.SeriesStart,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("invoices: find the range the gap check covers: %w", err)
		default:
			resp.CheckedFrom, resp.CheckedTo = ptr(checked.CheckedFrom), ptr(checked.CheckedTo)
			gaps, err := q.JournalGaps(ctx, store.JournalGapsParams{
				CheckedFrom: checked.CheckedFrom, CheckedTo: checked.CheckedTo, MaxGaps: maxJournalGaps + 1,
			})
			if err != nil {
				return fmt.Errorf("invoices: check the series for gaps: %w", err)
			}
			resp.GapsTruncated = len(gaps) > maxJournalGaps
			if resp.GapsTruncated {
				gaps = gaps[:maxJournalGaps]
			}
			resp.Gaps = gaps
		}
		if resp.Gaps == nil {
			resp.Gaps = []int64{}
		}
		highest, err := q.HighestIssuedNumber(ctx)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("invoices: read the highest issued number: %w", err)
		default:
			resp.HighestIssued = highest
		}
		resp.SeriesStart = settings.SeriesStart
		next, err := q.CounterNextValue(ctx)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("invoices: read the document counter: %w", err)
		default:
			resp.CounterLast = ptr(next - 1)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesJournal200JSONResponse(resp), nil
}
