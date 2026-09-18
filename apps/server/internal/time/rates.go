package timetracking

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
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
// always in the card's. A project bills in one currency: a card in another
// cannot price its hours, so that step answers nothing rather than storing,
// say, a NOK rate on a EUR project.

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
//     the person's bill rate in effect on the date, when their card is in the
//     project's currency (or the project has none, and the card's is taken)
//     → none;
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
	case cardBill != nil && (req.Project.Currency == nil || *req.Project.Currency == card.Currency):
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
			amount = discounted(amount, *line.DiscountPercent)
		}
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

// discounted is a discount line's arithmetic (D3): the list price less
// percent, rounded half up to cents, the columns' scale. It is done in exact
// decimal — each number read from its shortest decimal text, the same text
// the numeric columns store — because in float64 a half-cent result can land
// a hair under the half and round the wrong way (101.10 at 15 % is 85.935,
// which float arithmetic rounds to 85.93).
func discounted(list, percent float64) float64 {
	amount := exactDecimal(list)
	remaining := new(big.Rat).Sub(big.NewRat(100, 1), exactDecimal(percent))
	amount.Mul(amount, remaining)
	amount.Quo(amount, big.NewRat(100, 1))
	return roundHalfUpCents(amount)
}

// exactDecimal is v as the exact decimal its shortest text spells, never the
// binary fraction the float64 happens to hold.
func exactDecimal(v float64) *big.Rat {
	r, _ := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	return r
}

// roundHalfUpCents rounds r to two decimals, a half cent away from zero,
// and answers the nearest float64 — whose shortest text is then exactly those
// two decimals, which is what the column stores.
func roundHalfUpCents(r *big.Rat) float64 {
	cents := new(big.Rat).Mul(r, big.NewRat(100, 1))
	half := big.NewRat(1, 2)
	if cents.Sign() < 0 {
		half.Neg(half)
	}
	cents.Add(cents, half)
	whole := new(big.Int).Quo(cents.Num(), cents.Denom()) // truncates toward zero
	f, _ := new(big.Rat).SetFrac(whole, big.NewInt(100)).Float64()
	return f
}
