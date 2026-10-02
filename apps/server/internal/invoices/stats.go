package invoices

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the dashboard's summary (D7), in the envelope every module's
// /stats/summary shares: what is outstanding and overdue now, and what was
// issued, credited and paid in the period. The period arrives as instants,
// half-open [periodFrom, periodTo); this module's facts are Oslo calendar
// dates (issue_date, paid_on), so the period becomes days here, in Go:
// fromDay is the day periodFrom falls on, toDayExclusive the day after the
// one the last instant inside the period falls on — so a period ending now,
// or at the end of today, includes today — and previousFromDay the day
// previousFrom falls on. The queries compare dates only.

// GetInvoicesStatsSummary Get the invoices dashboard summary
// (GET /api/v1/invoices/stats/summary)
func (s *server) GetInvoicesStatsSummary(ctx context.Context, req gen.GetInvoicesStatsSummaryRequestObject) (gen.GetInvoicesStatsSummaryResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, previousFrom, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetInvoicesStatsSummary400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}
	fromDay := businessDay(periodFrom)
	toDayExclusive := businessDay(periodTo.Add(-time.Nanosecond)).AddDate(0, 0, 1)
	previousFromDay := businessDay(previousFrom)

	q := store.New(s.deps.Pool)
	current, err := q.InvoiceStatsNow(ctx, pgDate(businessDay(now)))
	if err != nil {
		return nil, fmt.Errorf("invoices: stats now: %w", err)
	}
	period, err := q.InvoiceStatsPeriod(ctx, store.InvoiceStatsPeriodParams{
		FromDay: pgDate(fromDay), ToDayExclusive: pgDate(toDayExclusive), PreviousFromDay: pgDate(previousFromDay),
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: stats over the period: %w", err)
	}
	payments, err := q.PaymentsInPeriod(ctx, store.PaymentsInPeriodParams{
		FromDay: pgDate(fromDay), ToDayExclusive: pgDate(toDayExclusive),
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: payments over the period: %w", err)
	}

	outstanding, err := ratFromNumeric(current.OutstandingAmount)
	if err != nil {
		return nil, err
	}
	overdue, err := ratFromNumeric(current.OverdueAmount)
	if err != nil {
		return nil, err
	}
	issued, err := ratFromNumeric(period.IssuedGrossTotal)
	if err != nil {
		return nil, err
	}
	previous, err := ratFromNumeric(period.PreviousIssuedGrossTotal)
	if err != nil {
		return nil, err
	}
	credited, err := ratFromNumeric(period.CreditedGrossTotal)
	if err != nil {
		return nil, err
	}
	paid, err := ratFromNumeric(payments.PaidAmount)
	if err != nil {
		return nil, err
	}

	return gen.GetInvoicesStatsSummary200JSONResponse{
		From:                  periodFrom,
		To:                    periodTo,
		OutstandingAmount:     floatFromRat(outstanding, 2),
		OutstandingCount:      current.OutstandingCount,
		OverdueAmount:         floatFromRat(overdue, 2),
		OverdueCount:          current.OverdueCount,
		IssuedCount:           period.IssuedCount,
		IssuedGrossTotal:      floatFromRat(issued, 2),
		IssuedGrossTotalDelta: floatFromRat(new(big.Rat).Sub(issued, previous), 2),
		CreditedCount:         period.CreditedCount,
		CreditedGrossTotal:    floatFromRat(credited, 2),
		PaidAmount:            floatFromRat(paid, 2),
		PaidCount:             payments.PaidCount,
	}, nil
}
