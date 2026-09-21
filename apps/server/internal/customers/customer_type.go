package customers

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// PutCustomersByIdType Change a customer's type
// (PUT /api/v1/customers/{id}/type)
//
// The customer type — business or person, 00007_customers_type.sql — is
// chosen on create and deliberately absent from PutCustomersById's body:
// changing it on a customer with history is rarely right, so it is its own
// explicit operation the UI confirms separately. Same access rule as the
// update (update+view, enforced by module.Router).
//
// A legal identity of the previous type cannot survive the change — a Brreg
// business identity on a private person is never a consistent record, and
// PostCustomers/PutCustomersById refuse to create one — so it is removed in
// the same transaction, recorded as the customer.updated "legal identity
// removed" event PutCustomersById would have written, next to the
// customer.type_changed event itself. Resubmitting the current type is a
// no-op: no write, no event, updated_at and revision untouched.
//
// Ordering (customers foundation design D5's controller ruling): (1) type
// validation, 400; (2) the customer lookup, 404; (3) a supplied revision
// that disagrees with the row just read, 409 — ahead of the no-op check
// below, so resubmitting the current type with a stale revision is still a
// conflict, not a free pass; (4) the write, guarded the same way.
func (s *server) PutCustomersByIdType(ctx context.Context, req gen.PutCustomersByIdTypeRequestObject) (gen.PutCustomersByIdTypeResponseObject, error) {
	body := gen.CustomerTypeRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	customerType, typeErr := validateCustomerType(body.Type)
	if typeErr != "" {
		return gen.PutCustomersByIdType400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer type", map[string][]string{"type": {typeErr}})), nil
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdType404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdType409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}

	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	if existing.Type == customerType {
		return gen.PutCustomersByIdType200JSONResponse(safeCustomerResponse(fromCustomerRow(existing, summary), includeIdentity)), nil
	}

	before := identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType)
	after := before
	if before != nil && before.Type != customerType {
		after = nil
	}

	// Resolved before the transaction opens: this handler always records at
	// least the type-changed event once it reaches here (the resubmitted-type
	// no-op returned above), so the actor is always needed.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(after)
	var updated store.SetCustomerTypeRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		updated, err = txq.SetCustomerType(ctx, store.SetCustomerTypeParams{
			ID: req.Id, Type: customerType,
			LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
			UpdatedAt: now, ExpectedRevision: body.Revision,
		})
		if err != nil {
			return err
		}
		if err := recordCustomerTypeChanged(ctx, txq, now, req.Id, existing.Type, customerType, act.Kind, act.Display, act.UserID); err != nil {
			return err
		}
		if !identityEqual(before, after) {
			return recordCustomerUpdated(ctx, txq, now, req.Id, existing.Name, before, existing.Name, after, act.Kind, act.Display, act.UserID)
		}
		return nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Same race as PutCustomersById's guarded write (customers
		// foundation design D5): a concurrent writer moved the revision
		// between our read above and this write.
		fresh, ferr := q.GetCustomer(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdType404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer after conflict: %w", ferr)
		}
		return gen.PutCustomersByIdType409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case err != nil:
		return nil, fmt.Errorf("customers: change customer type: %w", err)
	}

	return gen.PutCustomersByIdType200JSONResponse(safeCustomerResponse(fromSetCustomerTypeRow(updated, summary), includeIdentity)), nil
}
