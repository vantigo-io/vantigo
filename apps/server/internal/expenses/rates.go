package expenses

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is decision X8's dated rate table: expenses:manage's CRUD over
// expenses.rates, which the contract's access rule enforces on every operation,
// plus rateFor, the lookup every priced line makes. A row is in force from its
// valid_from until the next one of its kind; what it prices is snapshotted onto
// an expense when it is submitted, so changing or removing a row never moves an
// expense already submitted.

// The rate kinds (design §3.4). The mileage three are money per kilometre, the
// per diem four money per day, and the meal three percentages of a day's rate.
const (
	rateKindMileage              = "mileage"
	rateKindMileagePassenger     = "mileage_passenger"
	rateKindMileageCustomer      = "mileage_customer"
	rateKindPerDiem6To12         = "per_diem_6_12"
	rateKindPerDiemOver12        = "per_diem_over_12"
	rateKindPerDiemHotel         = "per_diem_overnight_hotel"
	rateKindPerDiemOther         = "per_diem_overnight_other"
	rateKindMealBreakfastPercent = "meal_breakfast_percent"
	rateKindMealLunchPercent     = "meal_lunch_percent"
	rateKindMealDinnerPercent    = "meal_dinner_percent"
)

// rateKinds is every kind a rate row may carry, in the order design §3.4 names
// them. A kind outside it is a kind field error.
var rateKinds = []string{
	rateKindMileage, rateKindMileagePassenger, rateKindMileageCustomer,
	rateKindPerDiem6To12, rateKindPerDiemOver12, rateKindPerDiemHotel, rateKindPerDiemOther,
	rateKindMealBreakfastPercent, rateKindMealLunchPercent, rateKindMealDinnerPercent,
}

// percentKinds are the kinds whose value is a percentage of a day's rate:
// 0 to 100, and no currency at all.
var percentKinds = []string{rateKindMealBreakfastPercent, rateKindMealLunchPercent, rateKindMealDinnerPercent}

// isPercentKind reports whether kind's value is a percentage rather than money.
func isPercentKind(kind string) bool { return slices.Contains(percentKinds, kind) }

// stateRateSource is the label a seeded row carries, so an administrator can
// see at a glance which rows came with the product and which are the company's
// own (decision X8). A row they edit keeps whatever source they give it.
const stateRateSource = "State rate"

// seedRate is one row the product ships with. The value is exact decimal text,
// never a float, so nothing rounds it on its way into the column.
type seedRate struct {
	Kind      string
	ValidFrom string
	Value     string
	Currency  string
	Source    string
}

// seededRates is the single place the shipped rates are written down. The
// migration inserts exactly these rows and POST /rates/reset puts back whichever
// of them is missing; TestSeededRates_AreExactlyWhatTheMigrationInserted holds
// the two against each other, so the table and the migration cannot drift.
//
// The two mileage rates were verified against Skatteetaten's published rates on
// 2026-09-19. The customer rate per kilometre is deliberately absent — what a
// customer is charged is the company's own price — and the per diem rates are a
// later delivery's, to be verified against the official source at that time and
// never written from memory.
var seededRates = []seedRate{
	{Kind: rateKindMileage, ValidFrom: "2026-01-01", Value: "5.30", Currency: "NOK", Source: stateRateSource},
	{Kind: rateKindMileagePassenger, ValidFrom: "2026-01-01", Value: "1.00", Currency: "NOK", Source: stateRateSource},
}

// errNoRate is the typed "the table holds no rate of this kind in force on that
// day" answer of rateFor. It is a value the caller acts on — the entry code
// turns it into a field error naming the rate that is missing — not a failure.
var errNoRate = errors.New("expenses: no rate in force")

// effectiveRate is the rate in force on a day: its exact value, the day it
// started, and the currency and label the row carries.
type effectiveRate struct {
	Kind      string
	ValidFrom time.Time
	Value     *big.Rat
	Currency  *string
	Source    *string
}

// rateFor is the rate of kind in force on date (design §3.4): the row with the
// greatest valid_from on or before it. A kind with no such row answers
// errNoRate. The value is exact decimal, because what is computed from it —
// kilometres times a rate, a percentage of a day rate — must round once, at the
// end, and not again on the way here.
func rateFor(ctx context.Context, q *store.Queries, kind string, date time.Time) (effectiveRate, error) {
	row, err := q.EffectiveRate(ctx, store.EffectiveRateParams{Kind: kind, OnDate: pgDate(date)})
	if errors.Is(err, pgx.ErrNoRows) {
		return effectiveRate{}, fmt.Errorf("%w: %s on %s", errNoRate, kind, date.Format(time.DateOnly))
	}
	if err != nil {
		return effectiveRate{}, fmt.Errorf("expenses: look up the %s rate: %w", kind, err)
	}
	value, err := ratFromNumeric(row.Value)
	if err != nil {
		return effectiveRate{}, err
	}
	return effectiveRate{
		Kind:      row.Kind,
		ValidFrom: row.ValidFrom.Time,
		Value:     value,
		Currency:  row.Currency,
		Source:    row.Source,
	}, nil
}

// rateKindTakenIndex is the unique index that keeps one row per kind and day; a
// create or a change it refuses is the validFrom field error.
const rateKindTakenIndex = "ux_rates_kind_valid_from"

// rateDayTaken is the validFrom message when the kind already has a row for the
// day.
func rateDayTaken(kind string, validFrom time.Time) string {
	return fmt.Sprintf("%s already has a rate from %s", kind, validFrom.Format(time.DateOnly))
}

// parsedRate is one validated rate body, in the shape the queries want.
type parsedRate struct {
	Kind      string
	ValidFrom time.Time
	Value     pgtype.Numeric
	Currency  *string
	Source    *string
}

// parseRate runs §3.4's rules over a rate body. kind is given on a create and
// is the row's own on a change, so it is passed in rather than read from the
// body. A money kind needs a currency and a value greater than zero the column
// can hold; a percentage kind refuses a currency and takes a value from 0 to
// 100. Every failure is collected.
func parseRate(kind string, validFrom openapi_types.Date, value float64, rawCurrency, rawSource *string) (parsedRate, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}

	if validFrom.IsZero() {
		add("validFrom", "A valid-from date is required")
	}
	given := ""
	if rawCurrency != nil {
		given = strings.TrimSpace(*rawCurrency)
	}
	var currency *string
	switch {
	case isPercentKind(kind):
		add("value", validateDecimal("A percentage", value, 0, maxPercent))
		if given != "" {
			add("currency", fmt.Sprintf("A %s rate is a percentage and carries no currency", kind))
		}
	default:
		add("value", validateAboveZero("A rate", value, maxRateValue))
		code, msg := validateCurrency(given)
		add("currency", msg)
		if msg == "" {
			currency = &code
		}
	}
	if len(errs) > 0 {
		return parsedRate{}, errs, nil
	}

	stored, err := numericFromFloat(value)
	if err != nil {
		return parsedRate{}, nil, err
	}
	var source *string
	if rawSource != nil {
		if trimmed := strings.TrimSpace(*rawSource); trimmed != "" {
			source = &trimmed
		}
	}
	return parsedRate{Kind: kind, ValidFrom: validFrom.Time, Value: stored, Currency: currency, Source: source}, nil, nil
}

// listRates is every rate as the contract answers it: flat, by kind and then
// the latest first, because the client groups it. withCustomerPrice keeps the
// mileage_customer rows in; a caller without expenses:manage reads the list
// without them.
func (s *server) listRates(ctx context.Context, withCustomerPrice bool) ([]gen.ExpensesRateResponse, error) {
	rows, err := store.New(s.deps.Pool).ListRates(ctx)
	if err != nil {
		return nil, fmt.Errorf("expenses: list the rates: %w", err)
	}
	if !withCustomerPrice {
		rows = slices.DeleteFunc(rows, func(row store.ExpensesRate) bool {
			return row.Kind == rateKindMileageCustomer
		})
	}
	return rateResponses(rows)
}

// GetExpensesRates List the expense rates
// (GET /api/v1/expenses/rates)
//
// Readable by everyone with expenses:access: what a kilometre is reimbursed at
// is what the person driving it is being paid, and their own expense form
// previews a mileage line with it before they save. The one kind an employee
// does not read is mileage_customer — what the company charges its customer
// per kilometre is a commercial price, and nothing in an employee's form shows
// it. Writing rates stays expenses:manage's, which the contract's access rule
// enforces on every other operation here.
func (s *server) GetExpensesRates(ctx context.Context, _ gen.GetExpensesRatesRequestObject) (gen.GetExpensesRatesResponseObject, error) {
	out, err := s.listRates(ctx, s.has(ctx, "expenses:manage"))
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesRates200JSONResponse(out), nil
}

// PostExpensesRates Add an expense rate
// (POST /api/v1/expenses/rates)
//
// One row per kind and day is the unique index's rule, not a read-then-write
// check, so two administrators adding the same day race to one row and one
// validFrom error.
func (s *server) PostExpensesRates(ctx context.Context, req gen.PostExpensesRatesRequestObject) (gen.PostExpensesRatesResponseObject, error) {
	body := gen.ExpensesRateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	kind := strings.TrimSpace(body.Kind)
	if !slices.Contains(rateKinds, kind) {
		return gen.PostExpensesRates400ApplicationProblemPlusJSONResponse(
			invalidRate(fieldError("kind", unknownRateKind(body.Kind)))), nil
	}
	parsed, errs, err := parseRate(kind, body.ValidFrom, body.Value, body.Currency, body.Source)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return gen.PostExpensesRates400ApplicationProblemPlusJSONResponse(invalidRate(errs)), nil
	}

	created, err := store.New(s.deps.Pool).InsertRate(ctx, store.InsertRateParams{
		Kind:      parsed.Kind,
		ValidFrom: pgDate(parsed.ValidFrom),
		Value:     parsed.Value,
		Currency:  parsed.Currency,
		Source:    parsed.Source,
		Now:       s.deps.Clock(),
	})
	if db.IsUniqueViolation(err, rateKindTakenIndex) {
		return gen.PostExpensesRates400ApplicationProblemPlusJSONResponse(
			invalidRate(fieldError("validFrom", rateDayTaken(parsed.Kind, parsed.ValidFrom)))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("expenses: add a rate: %w", err)
	}
	out, err := rateResponse(created)
	if err != nil {
		return nil, err
	}
	return gen.PostExpensesRates201JSONResponse(out), nil
}

// PutExpensesRatesById Change an expense rate
// (PUT /api/v1/expenses/rates/{id})
//
// A full replace of the row's day, value, currency and source; the kind stays
// the row's own, so a change can never move a rate from one kind to another. An
// unknown id is a 404 before the body is judged.
func (s *server) PutExpensesRatesById(ctx context.Context, req gen.PutExpensesRatesByIdRequestObject) (gen.PutExpensesRatesByIdResponseObject, error) {
	body := gen.ExpensesRateUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	existing, err := q.GetRate(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutExpensesRatesById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("expenses: get a rate: %w", err)
	}
	parsed, errs, err := parseRate(existing.Kind, body.ValidFrom, body.Value, body.Currency, body.Source)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return gen.PutExpensesRatesById400ApplicationProblemPlusJSONResponse(invalidRate(errs)), nil
	}

	updated, err := q.UpdateRate(ctx, store.UpdateRateParams{
		ID:        req.Id,
		ValidFrom: pgDate(parsed.ValidFrom),
		Value:     parsed.Value,
		Currency:  parsed.Currency,
		Source:    parsed.Source,
		Now:       s.deps.Clock(),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Deleted since the read.
		return gen.PutExpensesRatesById404Response{}, nil
	case db.IsUniqueViolation(err, rateKindTakenIndex):
		return gen.PutExpensesRatesById400ApplicationProblemPlusJSONResponse(
			invalidRate(fieldError("validFrom", rateDayTaken(parsed.Kind, parsed.ValidFrom)))), nil
	case err != nil:
		return nil, fmt.Errorf("expenses: change a rate: %w", err)
	}
	out, err := rateResponse(updated)
	if err != nil {
		return nil, err
	}
	return gen.PutExpensesRatesById200JSONResponse(out), nil
}

// DeleteExpensesRatesById Delete an expense rate
// (DELETE /api/v1/expenses/rates/{id})
func (s *server) DeleteExpensesRatesById(ctx context.Context, req gen.DeleteExpensesRatesByIdRequestObject) (gen.DeleteExpensesRatesByIdResponseObject, error) {
	deleted, err := store.New(s.deps.Pool).DeleteRate(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("expenses: delete a rate: %w", err)
	}
	if deleted == 0 {
		return gen.DeleteExpensesRatesById404Response{}, nil
	}
	return gen.DeleteExpensesRatesById204Response{}, nil
}

// PostExpensesRatesReset Restore a rate kind's seeded rows
// (POST /api/v1/expenses/rates/reset)
//
// Puts back whichever of the kind's shipped rows is no longer there and changes
// nothing else: a row an administrator edited on a seeded day keeps their value
// (the insert is ON CONFLICT DO NOTHING), and rows they added themselves are
// untouched. A kind that ships with nothing — the customer rate per kilometre,
// and the per diem rates until a later delivery seeds them — restores nothing.
//
// The writes go through one transaction so a reset either puts every missing
// row back or none; the list it answers with is read afterwards, outside it.
func (s *server) PostExpensesRatesReset(ctx context.Context, req gen.PostExpensesRatesResetRequestObject) (gen.PostExpensesRatesResetResponseObject, error) {
	body := gen.ExpensesRateResetRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	kind := strings.TrimSpace(body.Kind)
	if !slices.Contains(rateKinds, kind) {
		return gen.PostExpensesRatesReset400ApplicationProblemPlusJSONResponse(
			invalidRate(fieldError("kind", unknownRateKind(body.Kind)))), nil
	}

	now := s.deps.Clock()
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		for _, seed := range seededRates {
			if seed.Kind != kind {
				continue
			}
			validFrom, err := time.Parse(time.DateOnly, seed.ValidFrom)
			if err != nil {
				return fmt.Errorf("expenses: the %s seed's valid-from %q is not a date: %w", seed.Kind, seed.ValidFrom, err)
			}
			value, err := numericFromText(seed.Value)
			if err != nil {
				return err
			}
			if _, err := txq.InsertRateIfMissing(ctx, store.InsertRateIfMissingParams{
				Kind:      seed.Kind,
				ValidFrom: pgDate(validFrom),
				Value:     value,
				Currency:  ptrTo(seed.Currency),
				Source:    ptrTo(seed.Source),
				Now:       now,
			}); err != nil {
				return fmt.Errorf("expenses: restore the %s rate of %s: %w", seed.Kind, seed.ValidFrom, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Reset is expenses:manage's alone, so the list it answers with is the
	// administrator's own: every kind, the customer price included.
	out, err := s.listRates(ctx, true)
	if err != nil {
		return nil, err
	}
	return gen.PostExpensesRatesReset200JSONResponse(out), nil
}

// unknownRateKind is the kind field's message, naming both the value and the
// set, as the platform's own unknown-value problems do.
func unknownRateKind(kind string) string {
	return fmt.Sprintf("'%s' is not a rate kind; must be one of %s", kind, strings.Join(rateKinds, ", "))
}
