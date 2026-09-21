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

// This file is GET/PUT /customers/{id}/billing-profile (invoice-ready
// customer design D1, D4): payment terms, currency, document language,
// delivery methods and the identifiers used to send a customer invoices.
// Shaped after contact_info.go's PUT: a revision-guarded, full-replace
// sub-resource PUT on the same row, no .NET ancestor. Unlike every other
// sub-resource in this module, writing this one needs its own permission —
// customers:billing-manage — deliberately narrower than customers:update
// (module.go, D1): payment terms and delivery channel decide when and how
// money arrives, and the person who may rename a customer is not thereby
// the person who may give it 90 days' credit. Reading it needs only
// customers:view, the same as every other sub-resource's GET.

// billingProfileResponse is CustomerBillingProfile.FromDomain: p's ten
// fields, revision and warnings assembled from wherever the caller computed
// them — GET and PUT both call this once they have all three.
func billingProfileResponse(p billingProfile, revision int32, warnings []string) gen.CustomerBillingProfile {
	return gen.CustomerBillingProfile{
		InvoiceEmail: p.InvoiceEmail, ReminderEmail: p.ReminderEmail, PaymentTermsDays: p.PaymentTermsDays,
		Currency: p.Currency, Language: p.Language, InvoiceDelivery: p.InvoiceDelivery, ReminderDelivery: p.ReminderDelivery,
		PeppolId: p.PeppolID, Gln: p.Gln, BuyerReference: p.BuyerReference,
		Revision: revision, Warnings: warnings,
	}
}

// billingWarnings is GET .../billing-profile's computed warnings
// (invoice-ready customer design D4): recomputed from profile plus whatever
// else decides whether a delivery method can actually be used — never
// stored — in the fixed order the controller ruling pins: ehf_without_
// recipient, email_without_address, efaktura_for_business, no_invoice_address.
//
// identity is read for the ehf_without_recipient check even when the caller
// lacks customers:legal-identity-view (this sub-resource's own read gate is
// only customers:view) — the only thing that leaks through the warning is
// whether an EHF recipient can or cannot be derived, which is acceptable:
// the caller already learns exactly that by setting invoiceDelivery to
// "ehf" (customers:billing-manage) and reading the warning back, and GET
// .../billing-profile never exposes the identity's own fields (country/id/
// name) to a caller who cannot see them elsewhere.
//
// ehf_without_recipient fires exactly when derivedPeppolID (billing_values.go,
// final review fix I1) finds nothing to derive: the same predicate
// resolveBillingProfile (directory.go) uses, so a stored row can never make
// the directory and this warning disagree about whether a recipient exists
// — before this fix, this check read identity.Type while the directory read
// the customer's own type, which a legacy row with legal_type NULL (or a
// malformed legal_id) could make answer oppositely.
func billingWarnings(profile billingProfile, customerType string, identity *legalIdentity, contactEmail *string, hasInvoiceAddress bool) []string {
	warnings := []string{}

	delivery := ""
	if profile.InvoiceDelivery != nil {
		delivery = *profile.InvoiceDelivery
	}

	if delivery == "ehf" && profile.PeppolID == nil && derivedPeppolID(identity, customerType) == "" {
		warnings = append(warnings, "ehf_without_recipient")
	}
	if delivery == "email" && profile.InvoiceEmail == nil && contactEmail == nil {
		warnings = append(warnings, "email_without_address")
	}
	if delivery == "efaktura" && customerType == "business" {
		warnings = append(warnings, "efaktura_for_business")
	}
	if !hasInvoiceAddress {
		warnings = append(warnings, "no_invoice_address")
	}
	return warnings
}

// GetCustomersByIdBillingProfile Get a customer's billing profile
// (GET /api/v1/customers/{id}/billing-profile)
//
// 200 with every field absent — the wire omits an unset field, it never
// sends null — and whatever warnings the empty profile still raises
// (no_invoice_address at least, D4's controller ruling) for a customer that
// has none: a billing profile always exists conceptually,
// unlike the legal identity's own GET, which answers 204. 404 when the
// customer itself does not exist.
func (s *server) GetCustomersByIdBillingProfile(ctx context.Context, req gen.GetCustomersByIdBillingProfileRequestObject) (gen.GetCustomersByIdBillingProfileResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetCustomerBillingProfile(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdBillingProfile404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer billing profile: %w", err)
	}

	profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference)
	identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)

	hasInvoiceAddress, err := q.CustomerHasInvoiceAddress(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: customer has invoice address: %w", err)
	}

	warnings := billingWarnings(profile, row.Type, identity, row.Email, hasInvoiceAddress)
	return gen.GetCustomersByIdBillingProfile200JSONResponse(billingProfileResponse(profile, row.Revision, warnings)), nil
}

// PutCustomersByIdBillingProfile Replace a customer's billing profile
// (PUT /api/v1/customers/{id}/billing-profile)
//
// Ordering follows the controller ruling for this sub-resource, the same
// shape PutCustomersByIdContactInfo's own doc comment describes: (1) field
// validation, 400; (2) the customer lookup, 404; (3) a supplied revision
// that disagrees with the row just read, 409 — ahead of the no-op check
// below, so resubmitting the current profile with a stale revision is still
// a conflict, not a free pass; (4) the no-op check itself: identical in all
// ten fields writes nothing at all (customers foundation design D5) — no
// revision bump, no updated_at move, no timeline event; (5) the write,
// guarded the same way UpdateCustomerContactInfo's is.
//
// The whole body is a full replace (design D1): every field present or
// null, absent and null both meaning "clear" — oapi-codegen's generated
// *string/*int32 fields already collapse that distinction, so
// validateBillingProfile never has to tell them apart itself.
func (s *server) PutCustomersByIdBillingProfile(ctx context.Context, req gen.PutCustomersByIdBillingProfileRequestObject) (gen.PutCustomersByIdBillingProfileResponseObject, error) {
	body := gen.PutCustomerBillingProfileRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	after, errs := validateBillingProfile(body)
	if errs != nil {
		return gen.PutCustomersByIdBillingProfile400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid billing profile", errs)), nil
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomerBillingProfile(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdBillingProfile404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer billing profile: %w", err)
	}

	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdBillingProfile409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}

	before := billingProfileFromRow(existing.InvoiceEmail, existing.ReminderEmail, existing.PaymentTermsDays,
		existing.Currency, existing.Language, existing.InvoiceDelivery, existing.ReminderDelivery, existing.PeppolID, existing.Gln, existing.BuyerReference)
	identity := identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType)

	hasInvoiceAddress, err := q.CustomerHasInvoiceAddress(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: customer has invoice address: %w", err)
	}

	if billingProfileEqual(before, after) {
		warnings := billingWarnings(before, existing.Type, identity, existing.Email, hasInvoiceAddress)
		return gen.PutCustomersByIdBillingProfile200JSONResponse(billingProfileResponse(before, existing.Revision, warnings)), nil
	}

	now := s.deps.Clock()

	// Resolved before the transaction opens: this handler always records a
	// customer.billing_profile_updated event once it reaches here (the no-op
	// resubmit returned above), so the actor is always needed (customers
	// foundation design D1, actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	var updated store.UpdateCustomerBillingProfileRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		updated, err = txq.UpdateCustomerBillingProfile(ctx, store.UpdateCustomerBillingProfileParams{
			ID: req.Id, InvoiceEmail: after.InvoiceEmail, ReminderEmail: after.ReminderEmail,
			PaymentTermsDays: after.PaymentTermsDays, Currency: after.Currency, Language: after.Language,
			InvoiceDelivery: after.InvoiceDelivery, ReminderDelivery: after.ReminderDelivery,
			PeppolID: after.PeppolID, Gln: after.Gln, BuyerReference: after.BuyerReference,
			UpdatedAt: now, ExpectedRevision: body.Revision,
		})
		if err != nil {
			return err
		}
		return recordCustomerBillingProfileUpdated(ctx, txq, now, req.Id, before, after, act.Kind, act.Display, act.UserID)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The guarded UPDATE's WHERE clause matched no row: a concurrent writer
		// moved the revision between our read above and this write, the same
		// race PutCustomersByIdContactInfo's own guarded write answers by
		// re-reading and reporting the row's now-current revision.
		fresh, ferr := q.GetCustomerBillingProfile(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdBillingProfile404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer billing profile after conflict: %w", ferr)
		}
		return gen.PutCustomersByIdBillingProfile409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update customer billing profile: %w", err)
	}

	warnings := billingWarnings(after, updated.Type, identity, updated.Email, hasInvoiceAddress)
	return gen.PutCustomersByIdBillingProfile200JSONResponse(billingProfileResponse(after, updated.Revision, warnings)), nil
}
