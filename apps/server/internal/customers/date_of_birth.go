package customers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is a person's date of birth (invoices phase 6 design I30,
// delivery D): its own sub-resource, GET and PUT /customers/{id}/date-of-birth,
// read by Invoices through the billing profile to find the person for
// eFaktura. It is never printed, no timeline event names it, the personal-data
// file carries it, and anonymisation blanks it. Only a person has one: 00202's
// CHECK holds it, a change of type to business clears it, and the PUT refuses
// one on a business first.

const (
	codeDateOfBirthPersonOnly = "date_of_birth_person_only"
	invalidDateOfBirthTitle   = "Invalid date of birth"
)

// earliestDateOfBirth is the earliest date taken.
var earliestDateOfBirth = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

// dateOfBirthResponse is the wire's shape of a read or a write.
func dateOfBirthResponse(dob pgtype.Date, revision int32) gen.CustomerDateOfBirth {
	out := gen.CustomerDateOfBirth{Revision: revision}
	if dob.Valid {
		out.DateOfBirth = &openapi_types.Date{Time: dob.Time}
	}
	return out
}

// GetCustomersByIdDateOfBirth Get a person's date of birth
// (GET /api/v1/customers/{id}/date-of-birth)
func (s *server) GetCustomersByIdDateOfBirth(ctx context.Context, req gen.GetCustomersByIdDateOfBirthRequestObject) (gen.GetCustomersByIdDateOfBirthResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetCustomerDateOfBirth(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdDateOfBirth404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read the date of birth: %w", err)
	}
	return gen.GetCustomersByIdDateOfBirth200JSONResponse(dateOfBirthResponse(row.DateOfBirth, row.Revision)), nil
}

// PutCustomersByIdDateOfBirth Set a person's date of birth
// (PUT /api/v1/customers/{id}/date-of-birth)
//
// The order of the contact info's PUT: the field, 400; the customer, 404; a
// stale revision, 409; a business, 400 date_of_birth_person_only; the write
// under the customer's lock, which refuses a merged-away or anonymised one.
func (s *server) PutCustomersByIdDateOfBirth(ctx context.Context, req gen.PutCustomersByIdDateOfBirthRequestObject) (gen.PutCustomersByIdDateOfBirthResponseObject, error) {
	body := gen.PutCustomerDateOfBirthRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	var dob pgtype.Date
	if body.DateOfBirth != nil {
		d := time.Date(body.DateOfBirth.Year(), body.DateOfBirth.Month(), body.DateOfBirth.Day(), 0, 0, 0, 0, time.UTC)
		if d.Before(earliestDateOfBirth) || !d.Before(s.deps.Clock().UTC().Truncate(24*time.Hour)) {
			return gen.PutCustomersByIdDateOfBirth400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidDateOfBirthTitle,
				map[string][]string{"dateOfBirth": {"Must be a past date on or after 1900-01-01."}})), nil
		}
		dob = pgtype.Date{Time: d, Valid: true}
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomerDateOfBirth(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdDateOfBirth404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read the date of birth: %w", err)
	}
	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdDateOfBirth409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}
	if dob.Valid && existing.Type != "person" {
		return gen.PutCustomersByIdDateOfBirth400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidDateOfBirthTitle,
			map[string][]string{"dateOfBirth": {codeDateOfBirthPersonOnly + ": only a person has a date of birth."}})), nil
	}
	if existing.DateOfBirth == dob {
		return gen.PutCustomersByIdDateOfBirth200JSONResponse(dateOfBirthResponse(existing.DateOfBirth, existing.Revision)), nil
	}

	var updated store.SetCustomerDateOfBirthRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := lockWritableCustomer(ctx, txq, req.Id); err != nil {
			return err
		}
		var err error
		updated, err = txq.SetCustomerDateOfBirth(ctx, store.SetCustomerDateOfBirthParams{
			DateOfBirth: dob, UpdatedAt: s.deps.Clock(), ID: req.Id, ExpectedRevision: body.Revision,
		})
		return err
	})
	switch {
	case isReadOnlyCustomer(err):
		return gen.PutCustomersByIdDateOfBirth409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, pgx.ErrNoRows):
		fresh, ferr := q.GetCustomerDateOfBirth(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdDateOfBirth404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read the date of birth: %w", ferr)
		}
		return gen.PutCustomersByIdDateOfBirth409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case err != nil:
		return nil, fmt.Errorf("customers: set the date of birth: %w", err)
	}
	return gen.PutCustomersByIdDateOfBirth200JSONResponse(dateOfBirthResponse(updated.DateOfBirth, updated.Revision)), nil
}

// exportedDateOfBirth is the date as the personal-data file carries it.
func exportedDateOfBirth(dob pgtype.Date) *openapi_types.Date {
	if !dob.Valid {
		return nil
	}
	return &openapi_types.Date{Time: dob.Time}
}
