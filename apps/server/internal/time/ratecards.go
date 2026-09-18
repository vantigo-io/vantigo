package timetracking

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is §4.3's person rate cards: time:manage's CRUD over
// time.person_rates, which the contract's access rule enforces on every
// operation. A row is in effect from its validFrom until the person's next
// one (EffectivePersonRate); what it prices is snapshotted onto entries as
// they are saved (D3), so changing or deleting a row never moves an entry
// already submitted, and a draft only on its next save.

// personRateDayIndex is the unique index that keeps one row per person and
// day; a create or an update it refuses is the validFrom field error.
const personRateDayIndex = "ux_person_rates_user_id_valid_from"

// currencyPattern is an ISO 4217 alphabetic code's shape, as projects checks
// a project's currency; the set of codes itself is not enforced.
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// maxRate is the largest amount the rate columns (numeric(12,2)) hold.
const maxRate = 9_999_999_999.99

// parsedRate is one validated rate body, in the shape the queries want.
type parsedRate struct {
	ValidFrom          time.Time
	BillRate, CostRate pgtype.Numeric
	Currency           string
}

// parseRate runs §4.3's rules over a rate body's fields: a validFrom, at
// least one rate, each greater than zero with at most two decimals and
// within the column, and a three-letter currency, upper-cased. Every failure
// is collected.
func parseRate(validFrom openapi_types.Date, bill, cost *float64, rawCurrency string) (parsedRate, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	if validFrom.IsZero() {
		add("validFrom", "A valid-from date is required")
	}
	add("billRate", validateRate("A bill rate", bill))
	add("costRate", validateRate("A cost rate", cost))
	if bill == nil && cost == nil {
		add("billRate", "A bill rate or a cost rate is required")
		add("costRate", "A bill rate or a cost rate is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(rawCurrency))
	switch {
	case currency == "":
		add("currency", "A currency is required")
	case !currencyPattern.MatchString(currency):
		add("currency", fmt.Sprintf("A currency must be a three-letter ISO 4217 code, but was '%s'", rawCurrency))
	}
	if len(errs) > 0 {
		return parsedRate{}, errs, nil
	}
	billRate, err := numericFromFloatPtr(bill)
	if err != nil {
		return parsedRate{}, nil, err
	}
	costRate, err := numericFromFloatPtr(cost)
	if err != nil {
		return parsedRate{}, nil, err
	}
	return parsedRate{ValidFrom: validFrom.Time, BillRate: billRate, CostRate: costRate, Currency: currency}, nil, nil
}

// validateRate is one optional rate's rule, labelled for its message: absent
// is fine; given, it is greater than zero, has at most two decimals (judged on
// its shortest decimal text, as hours are) and fits the column.
func validateRate(label string, v *float64) string {
	switch {
	case v == nil:
		return ""
	case math.IsNaN(*v) || *v <= 0:
		return label + " must be greater than zero"
	case *v > maxRate:
		return fmt.Sprintf("%s cannot be more than %s", label, strconv.FormatFloat(maxRate, 'f', 2, 64))
	}
	text := strconv.FormatFloat(*v, 'f', -1, 64)
	if _, decimals, ok := strings.Cut(text, "."); ok && len(decimals) > 2 {
		return fmt.Sprintf("%s can have at most two decimals, but was %s", label, text)
	}
	return ""
}

// rateDayTaken is the validFrom message when the person already has a row
// for the day.
func rateDayTaken(validFrom time.Time) string {
	return fmt.Sprintf("This person already has a rate from %s", validFrom.Format(time.DateOnly))
}

// rateResponses renders rows with their people's names, resolved in one
// directory call, in the order given.
func (s *server) rateResponses(ctx context.Context, rows []store.TimePersonRate) ([]gen.TimeRateResponse, error) {
	var ids []uuid.UUID
	for _, row := range rows {
		if !slices.Contains(ids, row.UserID) {
			ids = append(ids, row.UserID)
		}
	}
	users, err := s.userEntries(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]gen.TimeRateResponse, 0, len(rows))
	for _, row := range rows {
		bill, err := floatPtrFromNumeric(row.BillRate)
		if err != nil {
			return nil, err
		}
		cost, err := floatPtrFromNumeric(row.CostRate)
		if err != nil {
			return nil, err
		}
		out = append(out, gen.TimeRateResponse{
			Id:          row.ID,
			UserId:      row.UserID,
			DisplayName: users[row.UserID].DisplayName,
			ValidFrom:   openapi_types.Date{Time: row.ValidFrom.Time},
			BillRate:    bill,
			CostRate:    cost,
			Currency:    row.Currency,
		})
	}
	return out, nil
}

// listRates is the rows of one person (userID) or everyone's (nil), by
// display name and then each person's latest first.
func (s *server) listRates(ctx context.Context, userID *uuid.UUID) ([]gen.TimeRateResponse, error) {
	rows, err := store.New(s.deps.Pool).ListPersonRates(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("time: list person rates: %w", err)
	}
	out, err := s.rateResponses(ctx, rows)
	if err != nil {
		return nil, err
	}
	// Stable: the query already has each person's rows latest first.
	slices.SortStableFunc(out, func(a, b gen.TimeRateResponse) int {
		return compareNames(
			contracts.UserEntry{ID: a.UserId, DisplayName: a.DisplayName},
			contracts.UserEntry{ID: b.UserId, DisplayName: b.DisplayName})
	})
	return out, nil
}

// GetTimeRates List person rates
// (GET /api/v1/time/rates)
func (s *server) GetTimeRates(ctx context.Context, req gen.GetTimeRatesRequestObject) (gen.GetTimeRatesResponseObject, error) {
	out, err := s.listRates(ctx, req.Params.UserId)
	if err != nil {
		return nil, err
	}
	return gen.GetTimeRates200JSONResponse(out), nil
}

// GetTimeRatesUsersByUserId Get a person's rates
// (GET /api/v1/time/rates/users/{userId})
//
// A person with no rows — or one nobody knows — has an empty card, not a
// 404: a rate card outlives the account it prices, and "no rows" is what an
// admin setting up a new person sees first.
func (s *server) GetTimeRatesUsersByUserId(ctx context.Context, req gen.GetTimeRatesUsersByUserIdRequestObject) (gen.GetTimeRatesUsersByUserIdResponseObject, error) {
	userID := req.UserId
	out, err := s.listRates(ctx, &userID)
	if err != nil {
		return nil, err
	}
	return gen.GetTimeRatesUsersByUserId200JSONResponse(out), nil
}

// PostTimeRates Add a person rate
// (POST /api/v1/time/rates)
//
// The person must be one the user directory knows — existence, not an active
// account, so a disabled person's history stays editable. One row per person
// and day is the unique index's rule, not a read-then-write check, so two
// admins adding the same day race to one row and one validFrom error.
func (s *server) PostTimeRates(ctx context.Context, req gen.PostTimeRatesRequestObject) (gen.PostTimeRatesResponseObject, error) {
	body := gen.TimeRateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	parsed, errs, err := parseRate(body.ValidFrom, body.BillRate, body.CostRate, body.Currency)
	if err != nil {
		return nil, err
	}
	if body.UserId == uuid.Nil {
		errs = withFieldError(errs, "userId", "A user is required")
	} else {
		user, err := s.deps.Users.User(ctx, body.UserId)
		if err != nil {
			return nil, fmt.Errorf("time: look up the rate's user: %w", err)
		}
		if user == nil {
			errs = withFieldError(errs, "userId", fmt.Sprintf("User %s was not found", body.UserId))
		}
	}
	if len(errs) > 0 {
		return gen.PostTimeRates400ApplicationProblemPlusJSONResponse(invalidRate(errs)), nil
	}

	created, err := store.New(s.deps.Pool).InsertPersonRate(ctx, store.InsertPersonRateParams{
		UserID:    body.UserId,
		ValidFrom: pgDate(parsed.ValidFrom),
		BillRate:  parsed.BillRate,
		CostRate:  parsed.CostRate,
		Currency:  parsed.Currency,
		Now:       s.deps.Clock(),
	})
	if db.IsUniqueViolation(err, personRateDayIndex) {
		return gen.PostTimeRates400ApplicationProblemPlusJSONResponse(
			invalidRate(fieldError("validFrom", rateDayTaken(parsed.ValidFrom)))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("time: add a person rate: %w", err)
	}
	out, err := s.rateResponses(ctx, []store.TimePersonRate{created})
	if err != nil {
		return nil, err
	}
	return gen.PostTimeRates201JSONResponse(out[0]), nil
}

// PutTimeRatesById Change a person rate
// (PUT /api/v1/time/rates/{id})
//
// A full replace of the row's day, rates and currency; the person stays the
// row's own. An unknown id is a 404 before the body is judged.
func (s *server) PutTimeRatesById(ctx context.Context, req gen.PutTimeRatesByIdRequestObject) (gen.PutTimeRatesByIdResponseObject, error) {
	body := gen.TimeRateUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetPersonRate(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutTimeRatesById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("time: get a person rate: %w", err)
	}
	parsed, errs, err := parseRate(body.ValidFrom, body.BillRate, body.CostRate, body.Currency)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return gen.PutTimeRatesById400ApplicationProblemPlusJSONResponse(invalidRate(errs)), nil
	}

	updated, err := q.UpdatePersonRate(ctx, store.UpdatePersonRateParams{
		ID:        req.Id,
		ValidFrom: pgDate(parsed.ValidFrom),
		BillRate:  parsed.BillRate,
		CostRate:  parsed.CostRate,
		Currency:  parsed.Currency,
		Now:       s.deps.Clock(),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Deleted since the read.
		return gen.PutTimeRatesById404Response{}, nil
	case db.IsUniqueViolation(err, personRateDayIndex):
		return gen.PutTimeRatesById400ApplicationProblemPlusJSONResponse(
			invalidRate(fieldError("validFrom", rateDayTaken(parsed.ValidFrom)))), nil
	case err != nil:
		return nil, fmt.Errorf("time: change a person rate: %w", err)
	}
	out, err := s.rateResponses(ctx, []store.TimePersonRate{updated})
	if err != nil {
		return nil, err
	}
	return gen.PutTimeRatesById200JSONResponse(out[0]), nil
}

// DeleteTimeRatesById Delete a person rate
// (DELETE /api/v1/time/rates/{id})
func (s *server) DeleteTimeRatesById(ctx context.Context, req gen.DeleteTimeRatesByIdRequestObject) (gen.DeleteTimeRatesByIdResponseObject, error) {
	deleted, err := store.New(s.deps.Pool).DeletePersonRate(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("time: delete a person rate: %w", err)
	}
	if deleted == 0 {
		return gen.DeleteTimeRatesById404Response{}, nil
	}
	return gen.DeleteTimeRatesById204Response{}, nil
}
