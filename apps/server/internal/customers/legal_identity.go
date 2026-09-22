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

// This file is LegalIdentityEndpoints.cs (customers inventory §1.1, §2.2):
// the three dedicated legal-identity operations. The permissions are flat —
// legal-identity-view for the GET, legal-identity-manage for the DELETE,
// both (AND-joined) for the PUT — never a payload-conditional "iff" clause
// like PostCustomers/PutCustomersById's, so module.Router's x-vantigo-access
// enforces every one of them by itself; unlike those two operations, none of
// these three handlers makes a second Access.Check.
//
// All three reuse GetCustomer/UpdateCustomer (customers.go, queries/customers.sql):
// the legal-identity columns are already part of every customer row, so
// there is nothing schema-specific for this file's operations alone.

// GetCustomersByIdLegalIdentity Get a customer's legal identity
// (GET /api/v1/customers/{id}/legal-identity)
//
// LegalIdentityEndpoints.Get (LegalIdentityEndpoints.cs:13-29): 404 when the
// customer does not exist, 200 with the identity when it has one, 204
// otherwise. Access to this endpoint at all is legal-identity-view, enforced
// by the router — unlike GetCustomer/GetCustomers's safe projection, the
// dedicated endpoint's own response never needs a second permission check to
// decide whether to show what the caller is already gated on seeing.
func (s *server) GetCustomersByIdLegalIdentity(ctx context.Context, req gen.GetCustomersByIdLegalIdentityRequestObject) (gen.GetCustomersByIdLegalIdentityResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdLegalIdentity404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	identity := identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType)
	if identity == nil {
		return gen.GetCustomersByIdLegalIdentity204Response{}, nil
	}
	return gen.GetCustomersByIdLegalIdentity200JSONResponse(legalIdentityResponse(*identity)), nil
}

// PutCustomersByIdLegalIdentity Replace a customer's legal identity
// (PUT /api/v1/customers/{id}/legal-identity)
//
// LegalIdentityEndpoints.Upsert (LegalIdentityEndpoints.cs:31-59): the whole
// body is one LegalIdentity, validated as one ValidationProblem titled
// "Invalid legal identity" — unlike PostCustomers/PutCustomersById, whose
// identity errors are nested under "identity.<field>" because their body
// carries an identity alongside a name, this operation's body *is* the
// identity, so its field keys are bare ("country", not "identity.country").
// 404 when the customer does not exist. The before/after comparison and the
// customer.updated timeline event it conditionally records are exactly
// PutCustomersById's own identity-replacement path (identityEqual,
// recordCustomerUpdated, timeline_events.go) — including its no-op rule
// (customers foundation design D5): a resubmit of the identity already stored,
// equal in all five fields, writes nothing whatsoever (no row update, so no
// revision bump and no updated_at move, and no event) and answers the same 200
// as a replace that did something.
//
// The duplicate-legal-identity check (customers foundation design D6) runs
// last, immediately before the write, inside the same transaction — after
// the type-mismatch 400 above, since an invalid pairing is worth reporting
// before a conflict with someone else's identity is. It is skipped when the
// request's (country, id) matches what the row already has
// (identityCountryAndIDEqual, duplicates.go — a bare name/source/type edit
// is never a conflict with itself) or when the request carries
// allowDuplicateIdentity: true. Unlike changed, which compares all five
// fields, the duplicate check never looks at name/source/type at all — so a
// bare name edit is a real write that the check still skips.
//
// The request body is gen.PutLegalIdentityRequest, not gen.LegalIdentityRequest
// (customers.yaml): the latter is also nested, via allOf, as
// CreateCustomerRequest.identity/UpdateCustomerRequest.identity, so giving it
// its own allowDuplicateIdentity would have made that field appear a second
// time — inert — under identity on those two requests, letting a caller
// believe nesting it there worked. PutLegalIdentityRequest repeats the five
// identity fields instead of sharing the schema, so this operation's flag
// exists in exactly one place.
func (s *server) PutCustomersByIdLegalIdentity(ctx context.Context, req gen.PutCustomersByIdLegalIdentityRequestObject) (gen.PutCustomersByIdLegalIdentityResponseObject, error) {
	body := gen.PutLegalIdentityRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	parsed, errs := validateLegalIdentity(body.Country, body.Type, body.Id, body.Name, body.Source)
	if errs != nil {
		return gen.PutCustomersByIdLegalIdentity400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid legal identity", errs)), nil
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdLegalIdentity404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	// The identity must agree with the customer's type (customer_type.go);
	// keyed bare "type" here, like every other field of this body.
	if mismatch := identityTypeMismatch(existing.Type, &parsed); mismatch != "" {
		return gen.PutCustomersByIdLegalIdentity400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid legal identity", map[string][]string{"type": {mismatch}})), nil
	}

	before := identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType)
	after := &parsed
	changed := !identityEqual(before, after)

	// No-op rule, mirroring PutCustomersById's (customers foundation design
	// D5): a resubmit of exactly the stored identity writes nothing at all —
	// no revision bump, no updated_at move, no timeline event — and answers
	// the same 200 as a real replace. The duplicate check cannot want to run
	// on such a request either: equal in all five fields implies an unchanged
	// (country, id), so it is skipped by its own rule below, which is why
	// returning before the transaction opens is safe.
	if !changed {
		return gen.PutCustomersByIdLegalIdentity200JSONResponse(legalIdentityResponse(parsed)), nil
	}

	allowDuplicateIdentity := body.AllowDuplicateIdentity != nil && *body.AllowDuplicateIdentity
	needsDuplicateCheck := !identityCountryAndIDEqual(before, after) && !allowDuplicateIdentity

	now := s.deps.Clock()

	// Resolved before the transaction opens: this handler always records a
	// customer.updated event once it reaches here (the unchanged resubmit
	// returned above), so the actor is always needed (customers foundation
	// design D1, actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	// Whether a duplicate conflict may name the other customer, resolved
	// before the transaction for the same reason: it is an access check, and
	// this operation's own permissions (legal-identity-view plus
	// legal-identity-manage) say nothing about reading a customer
	// (duplicates.go).
	nameHolders := needsDuplicateCheck && s.hasPermission(ctx, customersView)

	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(after)
	var conflict *gen.CustomerConflictProblem
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		// The duplicate check runs last, immediately before the write: a
		// conflict aborts the transaction via errDuplicateIdentity before
		// UpdateCustomer ever runs, so a refused replace leaves the row (and
		// its revision) untouched.
		if needsDuplicateCheck {
			problem, err := s.duplicateIdentityProblem(ctx, txq, *after, req.Id, nameHolders)
			if err != nil {
				return err
			}
			if problem != nil {
				conflict = problem
				return errDuplicateIdentity
			}
		}
		// ExpectedRevision is always nil here: the legal-identity sub-resource
		// stays an unconditional write, not a revision-guarded one (customers
		// foundation design D5) — a call that changes the identity bumps the
		// row's revision by one whatever revision the caller last read.
		if _, err := txq.UpdateCustomer(ctx, store.UpdateCustomerParams{
			ID: req.Id, Name: existing.Name, Status: existing.Status,
			LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
			UpdatedAt: now,
		}); err != nil {
			return err
		}
		return recordCustomerUpdated(ctx, txq, now, req.Id, existing.Name, before, existing.Name, after, act.Kind, act.Display, act.UserID)
	})
	if errors.Is(err, errDuplicateIdentity) {
		return gen.PutCustomersByIdLegalIdentity409ApplicationProblemPlusJSONResponse(*conflict), nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: replace legal identity: %w", err)
	}

	// The same after-commit registry fetch PostCustomers makes for a Brreg
	// pick (Brreg in full design D2): pointing a customer at a different
	// entity makes the record on file the wrong company's, so the new one is
	// read straight away. It runs only for source brreg — a manual identity
	// is enriched when a person asks, through the refresh endpoint — and its
	// failure is logged and dropped: the identity is already replaced, and a
	// registry outage must not turn a successful save into an error.
	//
	// A resubmit of the identity already stored never reaches here: the no-op
	// rule above returns before the transaction opens, so an unchanged PUT
	// makes no network call either, exactly as it writes no row and no event.
	if orgnr := brregPickOrganisationNumber(after, existing.Type); orgnr != "" {
		if err := s.fetchAndStoreRegistryRecord(ctx, req.Id, orgnr, after.Name, act); err != nil {
			s.deps.Logger.WarnContext(ctx, "customers: registry record fetch failed", "customerId", req.Id, "errorKind", registryErrorKind(err))
		}
	}

	return gen.PutCustomersByIdLegalIdentity200JSONResponse(legalIdentityResponse(parsed)), nil
}

// DeleteCustomersByIdLegalIdentity Remove a customer's legal identity
// (DELETE /api/v1/customers/{id}/legal-identity)
//
// LegalIdentityEndpoints.Delete (LegalIdentityEndpoints.cs:61-83): 404 when
// the customer does not exist; idempotent otherwise — a customer with no
// identity is left untouched and answers 204 with no write and no timeline
// event, exactly DeleteCustomersById's archival idempotence (inventory §4).
func (s *server) DeleteCustomersByIdLegalIdentity(ctx context.Context, req gen.DeleteCustomersByIdLegalIdentityRequestObject) (gen.DeleteCustomersByIdLegalIdentityResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersByIdLegalIdentity404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	before := identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType)
	if before == nil {
		return gen.DeleteCustomersByIdLegalIdentity204Response{}, nil
	}

	// Resolved before the transaction opens: this handler always records a
	// customer.updated event once it reaches here (the no-identity no-op
	// returned above), so the actor is always needed.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		// ExpectedRevision is always nil here, the same unconditional write as
		// the PUT above (customers foundation design D5).
		if _, err := txq.UpdateCustomer(ctx, store.UpdateCustomerParams{
			ID: req.Id, Name: existing.Name, Status: existing.Status,
			LegalCountry: nil, LegalID: nil, LegalName: nil, LegalSource: nil, LegalType: nil,
			UpdatedAt: now,
		}); err != nil {
			return err
		}
		return recordCustomerUpdated(ctx, txq, now, req.Id, existing.Name, before, existing.Name, nil, act.Kind, act.Display, act.UserID)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: remove legal identity: %w", err)
	}
	return gen.DeleteCustomersByIdLegalIdentity204Response{}, nil
}

// legalIdentityResponse is LegalIdentityResponse.FromDomain
// (LegalIdentityResponse.cs:19-26).
func legalIdentityResponse(identity legalIdentity) gen.LegalIdentityResponse {
	return gen.LegalIdentityResponse{
		Country: identity.Country, Type: identity.Type, Id: identity.ID, Name: identity.Name, Source: identity.Source,
	}
}
