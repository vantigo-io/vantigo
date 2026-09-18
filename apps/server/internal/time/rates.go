package timetracking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is D3's rate chain. It runs at every save while an entry is a
// draft or rejected, and what it answers is snapshotted onto the entry: from
// submitted on, a rate card, a project's default or a price list changing
// underneath never moves an entry's money. No currency is ever converted —
// the bill rate is in the project's currency when a line or the project
// priced it and in the person card's when the person did, the cost rate
// always in the card's.

// rateRequest is what the chain resolves from: whose hours, whether they are
// billable, on which project and billing line (nil for none), on which day.
// Date is the entry date as a UTC midnight.
type rateRequest struct {
	UserID   uuid.UUID
	Billable bool
	Project  contracts.ProjectEntry
	Line     *contracts.BillingLineEntry
	Date     time.Time
}

// rateSnapshot is the chain's answer, exactly the columns an entry stores:
// the bill rate and its currency, the step that produced it, and the cost
// rate and its currency. A nil rate is "none", never 0.
type rateSnapshot struct {
	Source       string
	BillRate     *float64
	BillCurrency *string
	CostRate     *float64
	CostCurrency *string
}

// resolveRates runs the chain (D3):
//
//   - bill, only when billable: the billing line's rule (a fixed amount, or
//     the variant's list price in the project's currency on the entry date,
//     discounted when the line says so) → the project's default bill rate →
//     the person's bill rate in effect on the date → none;
//   - cost, always: the person's cost rate in effect on the date → none.
//
// A step that cannot answer falls through to the next rather than ending the
// chain: a list line with products disabled, or with no price in the
// project's currency, is priced by the project's default or the person, the
// same as an entry with no line at all.
func (s *server) resolveRates(ctx context.Context, q *store.Queries, req rateRequest) (rateSnapshot, error) {
	card, err := personRate(ctx, q, req.UserID, req.Date)
	if err != nil {
		return rateSnapshot{}, err
	}
	snap := rateSnapshot{Source: sourceNone}
	var cardBill *float64
	if card != nil {
		cost, err := floatPtrFromNumeric(card.CostRate)
		if err != nil {
			return rateSnapshot{}, err
		}
		if cost != nil {
			currency := card.Currency
			snap.CostRate, snap.CostCurrency = cost, &currency
		}
		if cardBill, err = floatPtrFromNumeric(card.BillRate); err != nil {
			return rateSnapshot{}, err
		}
	}
	if !req.Billable {
		return snap, nil
	}

	lineRate, err := s.lineRate(ctx, req)
	if err != nil {
		return rateSnapshot{}, err
	}
	switch {
	case lineRate != nil:
		snap.Source, snap.BillRate, snap.BillCurrency = sourceLine, lineRate, req.Project.Currency
	case req.Project.DefaultBillRate != nil && req.Project.Currency != nil:
		rate := *req.Project.DefaultBillRate
		snap.Source, snap.BillRate, snap.BillCurrency = sourceProject, &rate, req.Project.Currency
	case cardBill != nil:
		currency := card.Currency
		snap.Source, snap.BillRate, snap.BillCurrency = sourcePerson, cardBill, &currency
	}
	return snap, nil
}

// lineRate is the chain's first step: what the entry's billing line prices an
// hour at in the project's currency, nil when it prices nothing — no line, a
// project with no currency (a line's amount would have none either), products
// disabled, or no list price in that currency on the day.
//
// The list price is asked for at noon UTC on the entry date, clear of either
// midnight a price change could sit on, so the price is the one in force on
// the day worked and not on the day the entry was saved.
func (s *server) lineRate(ctx context.Context, req rateRequest) (*float64, error) {
	line := req.Line
	if line == nil || req.Project.Currency == nil {
		return nil, nil
	}
	switch line.PricingMode {
	case pricingFixed:
		if line.FixedAmount == nil {
			return nil, nil
		}
		amount := *line.FixedAmount
		return &amount, nil
	case pricingList, pricingDiscount:
		if s.deps.Products == nil {
			return nil, nil
		}
		noon := req.Date.UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
		price, err := s.deps.Products.ListPrice(ctx, line.VariantID, *req.Project.Currency, noon)
		if err != nil {
			return nil, fmt.Errorf("time: resolve a billing line's list price: %w", err)
		}
		if price == nil {
			return nil, nil
		}
		amount := price.Amount
		if line.PricingMode == pricingDiscount && line.DiscountPercent != nil {
			amount *= 1 - *line.DiscountPercent/100
		}
		amount = roundCents(amount)
		return &amount, nil
	default:
		return nil, nil
	}
}

// personRate is the person rate card row in effect on date (design §4.3):
// the latest one whose valid_from is on or before it, nil when there is
// none.
func personRate(ctx context.Context, q *store.Queries, userID uuid.UUID, date time.Time) (*store.TimePersonRate, error) {
	row, err := q.EffectivePersonRate(ctx, store.EffectivePersonRateParams{UserID: userID, OnDate: pgDate(date)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("time: look up the person's rate card: %w", err)
	}
	return &row, nil
}
