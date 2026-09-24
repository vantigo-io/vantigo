package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is PUT /customers/{id}/contact-info (invoice-ready customer
// design D1, D2): a customer's own email, phone and website — what reaches
// the customer itself, not one of its contacts. Shaped after
// legal_identity.go's PutCustomersByIdLegalIdentity and customer_type.go's
// PutCustomersByIdType: a revision-guarded sub-resource PUT on the same row,
// full-replace, no .NET ancestor.

// contactInfo is a customer's normalized contact info: each field nil when
// the customer has none, exactly the shape customers.customers' three
// nullable columns store. json tags let it double as the timeline payload's
// before/after shape (timeline_events.go's recordCustomerContactInfoUpdated).
type contactInfo struct {
	Email   *string `json:"email"`
	Phone   *string `json:"phone"`
	Website *string `json:"website"`
}

// contactInfoFromRow is a persisted customer row's contact info, the
// contact_info.go analogue of identityFromRow — unlike a legal identity,
// there is no all-or-nothing invariant here: each of the three columns is
// independently nullable, so this simply carries the three pointers through
// unchanged.
func contactInfoFromRow(email, phone, website *string) contactInfo {
	return contactInfo{Email: email, Phone: phone, Website: website}
}

// contactInfoEqual reports whether a and b are the same contact info: every
// field equal, nil included. UpdateCustomerContactInfo's caller uses it to
// decide whether the request is a no-op (customers foundation design D5's
// no-op rule, the same shape identityEqual gives PutCustomersByIdLegalIdentity).
func contactInfoEqual(a, b contactInfo) bool {
	return stringPtrEqual(a.Email, b.Email) && stringPtrEqual(a.Phone, b.Phone) && stringPtrEqual(a.Website, b.Website)
}

// normalizedOrNil is one field of validateContactInfo: nil (absent or
// blank — both mean "clear", the controller ruling for this sub-resource)
// passes straight through; a non-blank value is validated by validate, and
// its error, if any, is reported under field. Blank/whitespace-only is
// never itself an error here — only what validate rejects is.
func normalizedOrNil(raw *string, field string, validate func(string) (string, string), errs map[string][]string) *string {
	if raw == nil {
		return nil
	}
	if strings.TrimSpace(*raw) == "" {
		return nil
	}
	v, err := validate(*raw)
	if err != "" {
		errs[field] = []string{err}
		return nil
	}
	return &v
}

// validateContactInfo is PutCustomersByIdContactInfo/PostCustomers's shared
// validator (invoice-ready customer design D2): every field validated
// independently and every error reported together, keyed "email"/"phone"/
// "website" — never short-circuited on the first failure, the same
// all-errors-at-once shape validateLegalIdentity gives its five fields.
// Blank or whitespace-only is stored as NULL, never a validation error (the
// controller ruling for this sub-resource): normalizedOrNil applies that
// rule per field before validateEmail/validatePhone/validateWebsite ever
// see a value, so those three functions only ever validate a string already
// known non-blank.
func validateContactInfo(email, phone, website *string) (contactInfo, map[string][]string) {
	errs := map[string][]string{}
	info := contactInfo{
		Email:   normalizedOrNil(email, "email", validateEmail, errs),
		Phone:   normalizedOrNil(phone, "phone", validatePhone, errs),
		Website: normalizedOrNil(website, "website", validateWebsite, errs),
	}
	if len(errs) > 0 {
		return contactInfo{}, errs
	}
	return info, nil
}

// writeContactInfo is PutCustomersByIdContactInfo's transaction body — the
// guarded UPDATE (pgx.ErrNoRows on a stale expectedRevision) and
// customer.contact_info_updated — and the one the CSV importer writes a row's
// contact info through. The caller has decided before and after differ.
func writeContactInfo(ctx context.Context, txq *store.Queries, id int32, before, after contactInfo, expectedRevision *int32, now time.Time, act actor) (store.UpdateCustomerContactInfoRow, error) {
	// The customer's lock and the merged-away refusal first (customers merge
	// design D2): every caller, the CSV importer's included, gets both.
	if _, err := lockWritableCustomer(ctx, txq, id); err != nil {
		return store.UpdateCustomerContactInfoRow{}, err
	}
	updated, err := txq.UpdateCustomerContactInfo(ctx, store.UpdateCustomerContactInfoParams{
		ID: id, Email: after.Email, Phone: after.Phone, Website: after.Website,
		UpdatedAt: now, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return store.UpdateCustomerContactInfoRow{}, err
	}
	return updated, recordCustomerContactInfoUpdated(ctx, txq, now, id, before, after, act.Kind, act.Display, act.UserID)
}

// PutCustomersByIdContactInfo Replace a customer's contact info
// (PUT /api/v1/customers/{id}/contact-info)
//
// Ordering follows the controller ruling for this sub-resource, the same
// shape PutCustomersByIdType's own doc comment describes: (1) field
// validation, 400; (2) the customer lookup, 404; (3) a supplied revision
// that disagrees with the row just read, 409 — ahead of the no-op check
// below, so resubmitting the current contact info with a stale revision is
// still a conflict, not a free pass; (4) the no-op check itself: identical
// in all three fields writes nothing at all (customers foundation design D5)
// — no revision bump, no updated_at move, no timeline event; (5) the write,
// guarded the same way UpdateCustomer/SetCustomerType's are.
//
// The whole body is a full replace (design D1): every field present or
// null, absent and null both meaning "clear" — oapi-codegen's generated
// *string fields already collapse that distinction (a missing JSON key and
// an explicit null both decode to a nil pointer), so validateContactInfo
// never has to tell them apart itself.
func (s *server) PutCustomersByIdContactInfo(ctx context.Context, req gen.PutCustomersByIdContactInfoRequestObject) (gen.PutCustomersByIdContactInfoResponseObject, error) {
	body := gen.PutCustomerContactInfoRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	after, errs := validateContactInfo(body.Email, body.Phone, body.Website)
	if errs != nil {
		return gen.PutCustomersByIdContactInfo400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid contact info", errs)), nil
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdContactInfo404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdContactInfo409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}

	before := contactInfoFromRow(existing.Email, existing.Phone, existing.Website)
	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	if contactInfoEqual(before, after) {
		summary, err := q.CustomerTimelineSummary(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("customers: timeline summary: %w", err)
		}
		row := fromCustomerRow(existing, summary)
		dec, err := s.decorate(ctx, q, row)
		if err != nil {
			return nil, err
		}
		return gen.PutCustomersByIdContactInfo200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
	}

	now := s.deps.Clock()

	// Resolved before the transaction opens: this handler always records a
	// customer.contact_info_updated event once it reaches here (the unchanged
	// resubmit returned above), so the actor is always needed (customers
	// foundation design D1, actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	var updated store.UpdateCustomerContactInfoRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		updated, err = writeContactInfo(ctx, store.New(tx), req.Id, before, after, body.Revision, now, act)
		return err
	})
	switch {
	case isMergedAway(err):
		return gen.PutCustomersByIdContactInfo409ApplicationProblemPlusJSONResponse(mergedAwayProblem(err)), nil
	case errors.Is(err, pgx.ErrNoRows):
		// The guarded UPDATE's WHERE clause matched no row: a concurrent writer
		// moved the revision between our read above and this write, the same
		// race PutCustomersById/PutCustomersByIdType's own guarded writes
		// answer by re-reading and reporting the row's now-current revision.
		fresh, ferr := q.GetCustomer(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdContactInfo404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer after conflict: %w", ferr)
		}
		return gen.PutCustomersByIdContactInfo409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update customer contact info: %w", err)
	}

	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	row := fromUpdateCustomerContactInfoRow(updated, summary)
	dec, err := s.decorate(ctx, q, row)
	if err != nil {
		return nil, err
	}
	return gen.PutCustomersByIdContactInfo200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
}
