package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the collection rates (invoices payments and reminders design
// D6): the statutory late interest rate, the § 3a compensation and the
// inkassosats as dated rows, each in force from its valid_from until the
// next row of its kind. The release seeds them (invoices.seed_collection_rate);
// a manager adds a row ahead of a release and deletes one that nothing has
// relied on yet. The engine reads them through ratesOf.

// The codes, titles and warning of the collection rates.
const (
	codeCollectionRateExists                = "collection_rate_exists"
	collectionRateExistsTitle               = "A collection rate exists for that day"
	codeCollectionRateInForce               = "collection_rate_in_force"
	collectionRateInForceTitle              = "The collection rate cannot be deleted"
	warningCollectionRateDiffersFromRelease = "collection_rate_differs_from_release"
	invalidCollectionRateTitle              = "Invalid collection rate"
)

// rateBounds is each kind's allowed values, inclusive (D6): the interest in
// percent a year, the compensation and the inkassosats in NOK.
var rateBounds = map[string][2]*big.Rat{
	reminderrules.KindLateInterest: {big.NewRat(1, 100), big.NewRat(30, 1)},
	reminderrules.KindCompensation: {big.NewRat(100, 1), big.NewRat(2000, 1)},
	reminderrules.KindInkassosats:  {big.NewRat(100, 1), big.NewRat(5000, 1)},
}

// halfYearly reports whether kind is set per half-year: its rows start on
// 1 January or 1 July (ck_collection_rates_half_year).
func halfYearly(kind string) bool {
	return kind == reminderrules.KindLateInterest || kind == reminderrules.KindCompensation
}

// ratesOf is the rate rows as the engine reads them (D6, D8): each day a UTC
// midnight, each value exact. An error is a value the column cannot hold as
// a number.
func ratesOf(rows []store.InvoicesCollectionRate) ([]reminderrules.Rate, error) {
	rates := make([]reminderrules.Rate, 0, len(rows))
	for _, r := range rows {
		value, err := ratFromNumeric(r.Value)
		if err != nil {
			return nil, fmt.Errorf("invoices: collection rate %d: %w", r.ID, err)
		}
		rates = append(rates, reminderrules.Rate{ID: r.ID, Kind: r.Kind, ValidFrom: utcDay(r.ValidFrom.Time), Value: value})
	}
	return rates, nil
}

// collectionRateWire is one rate row on the wire; inForce and used are the
// list's judgement of it on the request's day.
func collectionRateWire(r store.InvoicesCollectionRate, inForce, used bool) (gen.InvoicesCollectionRate, error) {
	value, err := floatFromNumeric(r.Value)
	if err != nil {
		return gen.InvoicesCollectionRate{}, err
	}
	out := gen.InvoicesCollectionRate{
		Id: r.ID, Kind: gen.InvoicesCollectionRateKind(r.Kind), ValidFrom: wireDate(r.ValidFrom.Time), Value: value,
		SourceRef: r.SourceRef, Seeded: r.CreatedByUserID == nil, InForce: inForce, Usable: !used,
		CreatedBy: r.CreatedByUserID, CreatedAt: r.CreatedAt, ReleaseSourceRef: r.ReleaseSourceRef,
	}
	if r.ReleaseValue.Valid {
		release, err := floatFromNumeric(r.ReleaseValue)
		if err != nil {
			return gen.InvoicesCollectionRate{}, err
		}
		out.ReleaseValue = &release
	}
	return out, nil
}

// differsFromRelease reports whether a release seeded a value over a user's
// row that is not the user's (D6, Task 1's review): compared by value.
func differsFromRelease(r store.InvoicesCollectionRate) (bool, error) {
	if !r.ReleaseValue.Valid {
		return false, nil
	}
	release, err := ratFromNumeric(r.ReleaseValue)
	if err != nil {
		return false, err
	}
	value, err := ratFromNumeric(r.Value)
	if err != nil {
		return false, err
	}
	return release.Cmp(value) != 0, nil
}

// GetInvoicesCollectionRates List the collection rates
// (GET /api/v1/invoices/collection-rates)
func (s *server) GetInvoicesCollectionRates(ctx context.Context, _ gen.GetInvoicesCollectionRatesRequestObject) (gen.GetInvoicesCollectionRatesResponseObject, error) {
	today := businessDay(s.deps.Clock())
	rows, err := store.New(s.deps.Pool).ListCollectionRates(ctx, pgDate(today))
	if err != nil {
		return nil, fmt.Errorf("invoices: list the collection rates: %w", err)
	}
	list := gen.InvoicesCollectionRateList{
		Rates: make([]gen.InvoicesCollectionRate, 0, len(rows)), Warnings: []gen.InvoicesCollectionRateListWarnings{},
	}
	differs := false
	for _, r := range rows {
		row := store.InvoicesCollectionRate{
			ID: r.ID, Kind: r.Kind, ValidFrom: r.ValidFrom, Value: r.Value, ReleaseValue: r.ReleaseValue,
			ReleaseSourceRef: r.ReleaseSourceRef, SourceRef: r.SourceRef, CreatedByUserID: r.CreatedByUserID, CreatedAt: r.CreatedAt,
		}
		wire, err := collectionRateWire(row, r.InForce, r.Used)
		if err != nil {
			return nil, err
		}
		list.Rates = append(list.Rates, wire)
		d, err := differsFromRelease(row)
		if err != nil {
			return nil, err
		}
		differs = differs || d
	}
	if differs {
		list.Warnings = append(list.Warnings, warningCollectionRateDiffersFromRelease)
	}
	return gen.GetInvoicesCollectionRates200JSONResponse(list), nil
}

// parseCollectionRate runs D6's field rules over an add, every failure
// collected: the kind; validFrom after today, after latestLetter — the
// sent_on of the latest printed or sent letter, nil when none, so a new row
// never contradicts a letter already printed or posted — and, for a
// half-yearly kind, on 1 January or 1 July; the value within its kind's
// bounds with at most two decimals; the regulation 1-100 characters.
func parseCollectionRate(body gen.InvoicesCollectionRateRequest, today time.Time, latestLetter *time.Time) (store.InsertCollectionRateParams, map[string][]string) {
	var errs map[string][]string
	kind := string(body.Kind)
	validFrom := utcDay(body.ValidFrom.Time)
	sourceRef := strings.TrimSpace(body.SourceRef)
	bounds, known := rateBounds[kind]
	if !known {
		errs = withFieldError(errs, "kind", "A collection rate is late_interest_percent, b2b_compensation_nok or inkassosats")
	}
	switch {
	case !validFrom.After(today):
		errs = withFieldError(errs, "validFrom", fmt.Sprintf("A new rate takes effect after today, %s", today.Format(time.DateOnly)))
	case latestLetter != nil && !validFrom.After(*latestLetter):
		errs = withFieldError(errs, "validFrom", fmt.Sprintf(
			"A letter dated %s is already printed or sent; a new rate takes effect after it", latestLetter.Format(time.DateOnly)))
	case halfYearly(kind) && (validFrom.Day() != 1 || (validFrom.Month() != time.January && validFrom.Month() != time.July)):
		errs = withFieldError(errs, "validFrom", "This rate is set per half-year: it takes effect on 1 January or 1 July")
	}
	value := ratFromFloat(body.Value)
	switch {
	case !finite(body.Value) || decimalPlaces(body.Value) > 2:
		errs = withFieldError(errs, "value", "A rate has at most two decimals")
	case known && (value.Cmp(bounds[0]) < 0 || value.Cmp(bounds[1]) > 0):
		errs = withFieldError(errs, "value", fmt.Sprintf("This rate is between %s and %s",
			bounds[0].FloatString(2), bounds[1].FloatString(2)))
	}
	switch {
	case sourceRef == "":
		errs = withFieldError(errs, "sourceRef", "Name the regulation the rate comes from, such as FOR-2026-06-25-1372")
	case utf8.RuneCountInString(sourceRef) > 100:
		errs = withFieldError(errs, "sourceRef", "A regulation holds at most 100 characters")
	}
	if errs != nil {
		return store.InsertCollectionRateParams{}, errs
	}
	n, err := numericFromRat(value, 2)
	if err != nil {
		return store.InsertCollectionRateParams{}, withFieldError(nil, "value", "This is not a rate")
	}
	return store.InsertCollectionRateParams{Kind: kind, ValidFrom: pgDate(validFrom), Value: n, SourceRef: sourceRef}, nil
}

// PostInvoicesCollectionRates Add a collection rate
// (POST /api/v1/invoices/collection-rates)
//
// A manager adds a row ahead of a release (D6). A second row of the kind on
// the same day is the unique key's to refuse, so two adds racing each other
// cannot both land.
func (s *server) PostInvoicesCollectionRates(ctx context.Context, req gen.PostInvoicesCollectionRatesRequestObject) (gen.PostInvoicesCollectionRatesResponseObject, error) {
	now := s.deps.Clock()
	q := store.New(s.deps.Pool)
	latest, err := q.LatestLetterDay(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the latest letter's date: %w", err)
	}
	params, errs := parseCollectionRate(*req.Body, businessDay(now), pgDateOf(latest))
	if errs != nil {
		return gen.PostInvoicesCollectionRates400ApplicationProblemPlusJSONResponse(invalid(invalidCollectionRateTitle, errs)), nil
	}
	params.CreatedByUserID, params.Now = callerID(ctx), now
	row, err := q.InsertCollectionRate(ctx, params)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_collection_rates_kind_valid_from" {
		return gen.PostInvoicesCollectionRates409ApplicationProblemPlusJSONResponse(conflict(codeCollectionRateExists, collectionRateExistsTitle,
			fmt.Sprintf("A %s rate already takes effect on %s; delete it first if it is wrong.",
				params.Kind, params.ValidFrom.Time.Format(time.DateOnly)))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: add a collection rate: %w", err)
	}
	read, err := q.GetCollectionRate(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read collection rate %d: %w", row.ID, err)
	}
	wire, err := collectionRateWire(row, false, read.Used)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesCollectionRates201JSONResponse(wire), nil
}

// DeleteInvoicesCollectionRatesById Delete a collection rate
// (DELETE /api/v1/invoices/collection-rates/{id})
//
// Only a user's row, not yet in force, that no printed or sent letter has
// relied on (D6, plan reading 6). The row is locked FOR UPDATE before that is
// judged (the Task 6 review's M1): a print batch holds the rates its letters
// rely on FOR KEY SHARE until it commits (Task 13), so a letter printed
// meanwhile is waited for and then seen — a statement after the lock sees
// every commit before it. A user's row a release seeded over is
// replaced in the same transaction by a seeded row of the release's value
// and regulation (m6), so its half-year never goes empty and refuses every
// interest letter.
func (s *server) DeleteInvoicesCollectionRatesById(ctx context.Context, req gen.DeleteInvoicesCollectionRatesByIdRequestObject) (gen.DeleteInvoicesCollectionRatesByIdResponseObject, error) {
	now := s.deps.Clock()
	today := businessDay(now)
	notFound := false
	var refusal *gen.InvoicesConflictProblem
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		_, err := lockCollectionRate(ctx, txq, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock collection rate %d: %w", req.Id, err)
		}
		r, err := txq.GetCollectionRate(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("invoices: read collection rate %d: %w", req.Id, err)
		}
		day := r.ValidFrom.Time.Format(time.DateOnly)
		switch {
		case r.CreatedByUserID == nil:
			refusal = ptr(conflict(codeCollectionRateInForce, collectionRateInForceTitle,
				"This rate came with a release; a release's rates are never deleted."))
		case !utcDay(r.ValidFrom.Time).After(today):
			refusal = ptr(conflict(codeCollectionRateInForce, collectionRateInForceTitle,
				fmt.Sprintf("This rate has been in force since %s; only a rate not yet in force can be deleted.", day)))
		case r.Used:
			refusal = ptr(conflict(codeCollectionRateInForce, collectionRateInForceTitle,
				fmt.Sprintf("A printed or sent letter dated on or after %s relied on this rate.", day)))
		}
		if refusal != nil {
			return errRefused
		}
		n, err := txq.DeleteCollectionRate(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("invoices: delete collection rate %d: %w", r.ID, err)
		}
		if n == 0 {
			notFound = true
			return errRefused
		}
		if !r.ReleaseValue.Valid || r.ReleaseSourceRef == nil {
			return nil
		}
		if _, err := txq.ReseedCollectionRate(ctx, store.ReseedCollectionRateParams{
			Kind: r.Kind, ValidFrom: r.ValidFrom, Value: r.ReleaseValue, SourceRef: *r.ReleaseSourceRef, Now: now,
		}); err != nil {
			return fmt.Errorf("invoices: put the release's %s rate of %s back: %w", r.Kind, day, err)
		}
		return nil
	})
	switch {
	case notFound:
		return gen.DeleteInvoicesCollectionRatesById404Response{}, nil
	case refusal != nil:
		return gen.DeleteInvoicesCollectionRatesById409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	return gen.DeleteInvoicesCollectionRatesById204Response{}, nil
}
