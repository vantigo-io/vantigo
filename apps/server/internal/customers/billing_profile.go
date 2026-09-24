package customers

import (
	"context"
	"errors"
	"fmt"
	"time"

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
//
// customers:billing-manage guards only the customer's OWN override. Since
// customer groups (design D2) a customer whose profile leaves paymentTermsDays
// unset inherits its group's default, and both halves of that are
// customers:update writes: choosing a customer's group, and editing a group's
// default. So a customers:update holder decides a customer's EFFECTIVE payment
// term whenever the customer's own profile leaves it unset — create a group
// with a 90-day default, move the customer in — and one edit to a group's
// default moves the effective term of every such member at once. That is a
// deliberate trade-off (a group's default is installation policy, the tag
// vocabulary's permission), not an oversight. If it is ever unwanted, the change
// is to require customers:billing-manage as well on a group create or update
// that sets or changes defaultPaymentTermsDays, and on a membership PUT whose
// before or after group carries a default: a per-request check, with no new key.

// billingProfileResponse is CustomerBillingProfile.FromDomain: p's eleven
// fields, revision, warnings, the resolved Peppol lookup (peppol lookup
// design D3, nil when none or stale) and the group default assembled from
// wherever the caller computed them — GET and PUT both call this once they
// have all five.
//
// groupDefault is the customer's group and the term it would give it (customer
// groups design D4) — nil for a customer in no group. Answered by the PUT as
// well as the GET: the card writes the PUT's own body into its cache, so a
// profile that came back without it would lose the inherited-term sentence
// until the next refetch.
func billingProfileResponse(p billingProfile, revision int32, warnings []string, lookup *gen.CustomerPeppolLookup, groupDefault *gen.CustomerBillingGroupDefault) gen.CustomerBillingProfile {
	return gen.CustomerBillingProfile{
		InvoiceEmail: p.InvoiceEmail, ReminderEmail: p.ReminderEmail, PaymentTermsDays: p.PaymentTermsDays,
		Currency: p.Currency, Language: p.Language, InvoiceDelivery: p.InvoiceDelivery, ReminderDelivery: p.ReminderDelivery,
		PeppolId: p.PeppolID, Gln: p.Gln, BuyerReference: p.BuyerReference, DefaultBillRate: p.DefaultBillRate,
		Revision: revision, Warnings: warnings, PeppolLookup: lookup, GroupDefault: groupDefault,
	}
}

// groupDefaultFrom is GET/PUT .../billing-profile's groupDefault: one
// CustomerGroupMembership row (queries/groups.sql — the same query PUT
// /customers/{id}/group reads its no-op check from) turned into the contract's
// block, nil when the customer belongs to no group. The group's own default may
// itself be absent: the block still stands, because "you are in Retail and
// Retail decides nothing" is a different thing to show than "you are in no
// group".
func groupDefaultFrom(row store.CustomerGroupMembershipRow) *gen.CustomerBillingGroupDefault {
	if row.GroupID == nil {
		return nil
	}
	return &gen.CustomerBillingGroupDefault{
		Group:            gen.CustomerGroupRef{Id: *row.GroupID, Name: deref(row.GroupName)},
		PaymentTermsDays: row.DefaultPaymentTermsDays,
	}
}

// billingProfileSnapshot is GET/PUT .../billing-profile's first step: the
// customer's billing row and its group, as ONE snapshot. They are two unlocked
// statements (GetCustomerBillingProfile, then CustomerGroupMembership), and a
// group move landing between them bumps the revision, so without a check the
// response would pair the first read's revision with the second read's group.
// The membership read carries the revision it saw; when that differs from the
// profile's, the profile is read once more, which then answers at least the
// membership's revision. A GET has no revision of its own to conflict with, so
// it must simply answer a consistent pair; a PUT carrying a revision then meets
// the re-read revision in its own 409 check, exactly as it would have had the
// move landed before the first read. Once, not a loop: a third write inside the
// same few milliseconds leaves at worst a group sentence fresher than the
// revision beside it, and that revision still guards the next write.
//
// When the customer itself is gone at either read, the error wraps
// pgx.ErrNoRows, which both handlers answer as 404.
func billingProfileSnapshot(ctx context.Context, q *store.Queries, customerID int32) (store.GetCustomerBillingProfileRow, *gen.CustomerBillingGroupDefault, error) {
	row, err := q.GetCustomerBillingProfile(ctx, customerID)
	if err != nil {
		return row, nil, fmt.Errorf("customers: get customer billing profile: %w", err)
	}
	membership, err := q.CustomerGroupMembership(ctx, customerID)
	if err != nil {
		return row, nil, fmt.Errorf("customers: read customer group membership: %w", err)
	}
	if membership.Revision != row.Revision {
		row, err = q.GetCustomerBillingProfile(ctx, customerID)
		if err != nil {
			return row, nil, fmt.Errorf("customers: re-read customer billing profile: %w", err)
		}
	}
	return row, groupDefaultFrom(membership), nil
}

// billingWarnings is GET .../billing-profile's computed warnings
// (invoice-ready customer design D4, extended by can-this-customer-receive-
// EHF design D4): recomputed from profile plus whatever else decides
// whether a delivery method can actually be used — never stored — in the
// fixed order the controller ruling pins: ehf_without_recipient,
// email_without_address, efaktura_for_business, no_invoice_address,
// ehf_recipient_not_registered, ehf_available.
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
//
// lookup is the caller's already-resolved, non-stale Peppol answer
// (peppol_lookup.go's resolvedPeppolLookup — nil when none was ever made or
// the stored one no longer matches the participant that would be looked up
// now): ehf_recipient_not_registered fires when delivery is "ehf" and
// lookup says not_registered, or registered without canReceiveInvoice;
// ehf_available — the offer, not a problem — fires when lookup says
// registered with canReceiveInvoice and delivery is anything but "ehf"
// (unset counts as "anything but ehf").
func billingWarnings(profile billingProfile, customerType string, identity *legalIdentity, contactEmail *string, hasInvoiceAddress bool, lookup *gen.CustomerPeppolLookup) []string {
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
	if delivery == "ehf" && lookup != nil && (lookup.Status == peppolStatusNotRegistered || (lookup.Status == peppolStatusRegistered && !lookup.CanReceiveInvoice)) {
		warnings = append(warnings, "ehf_recipient_not_registered")
	}
	if delivery != "ehf" && lookup != nil && lookup.Status == peppolStatusRegistered && lookup.CanReceiveInvoice {
		warnings = append(warnings, "ehf_available")
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
	row, groupDefault, err := billingProfileSnapshot(ctx, q, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdBillingProfile404Response{}, nil
	}
	if err != nil {
		return nil, err
	}

	defaultBillRate, err := floatPtrFromNumeric(row.DefaultBillRate)
	if err != nil {
		return nil, err
	}
	profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, defaultBillRate)
	identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)

	hasInvoiceAddress, err := q.CustomerHasInvoiceAddress(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: customer has invoice address: %w", err)
	}

	lookup, err := s.resolvedPeppolLookupFor(ctx, q, req.Id, profile, identity, row.Type)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve peppol lookup: %w", err)
	}

	warnings := billingWarnings(profile, row.Type, identity, row.Email, hasInvoiceAddress, lookup)
	return gen.GetCustomersByIdBillingProfile200JSONResponse(billingProfileResponse(profile, row.Revision, warnings, lookup, groupDefault)), nil
}

// resolvedPeppolLookupFor is GET/PUT .../billing-profile's shared step
// (can-this-customer-receive-EHF design D3, D4): decide the participant
// profile/identity/customerType would be looked up under, resolve the
// withholding permission (customers:legal-identity-view — never a gate
// here, only response-shaping, the same as every other legalIdentityView
// check in this module), and read the stored answer for that exact
// participant, nil when there is none or it is stale.
func (s *server) resolvedPeppolLookupFor(ctx context.Context, q *store.Queries, customerID int32, profile billingProfile, identity *legalIdentity, customerType string) (*gen.CustomerPeppolLookup, error) {
	participant, derived := lookupParticipant(profile, identity, customerType)
	if participant == "" {
		return nil, nil
	}
	showParticipantID := !derived || s.hasPermission(ctx, legalIdentityView)
	stored, err := fetchStoredPeppolLookup(ctx, q, customerID)
	if err != nil {
		return nil, err
	}
	return resolvedPeppolLookup(stored, participant, showParticipantID), nil
}

// writeBillingProfile is PutCustomersByIdBillingProfile's transaction body —
// the rate's numeric column value, the guarded full-replace UPDATE
// (pgx.ErrNoRows on a stale expectedRevision) and
// customer.billing_profile_updated — and the one the CSV importer writes a
// row's billing profile through. The caller has decided before and after
// differ. The rate's conversion fails only for a value JSON cannot carry, and
// is an error, never a NULL.
func writeBillingProfile(ctx context.Context, txq *store.Queries, id int32, before, after billingProfile, expectedRevision *int32, now time.Time, act actor) (store.UpdateCustomerBillingProfileRow, error) {
	// The customer's lock and the merged-away refusal first (customers merge
	// design D2): every caller, the CSV importer's included, gets both.
	if _, err := lockWritableCustomer(ctx, txq, id); err != nil {
		return store.UpdateCustomerBillingProfileRow{}, err
	}
	defaultBillRate, err := numericFromFloatPtr(after.DefaultBillRate)
	if err != nil {
		return store.UpdateCustomerBillingProfileRow{}, err
	}
	updated, err := txq.UpdateCustomerBillingProfile(ctx, store.UpdateCustomerBillingProfileParams{
		ID: id, InvoiceEmail: after.InvoiceEmail, ReminderEmail: after.ReminderEmail,
		PaymentTermsDays: after.PaymentTermsDays, Currency: after.Currency, Language: after.Language,
		InvoiceDelivery: after.InvoiceDelivery, ReminderDelivery: after.ReminderDelivery,
		PeppolID: after.PeppolID, Gln: after.Gln, BuyerReference: after.BuyerReference, DefaultBillRate: defaultBillRate,
		UpdatedAt: now, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return store.UpdateCustomerBillingProfileRow{}, err
	}
	return updated, recordCustomerBillingProfileUpdated(ctx, txq, now, id, before, after, act.Kind, act.Display, act.UserID)
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
// eleven fields writes nothing at all (customers foundation design D5) — no
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

	// Read on the pool, before the revision check and the no-op branch: the
	// group is one snapshot with the row (billingProfileSnapshot), and neither
	// branch changes it, so one read serves both of the handler's 200s.
	q := store.New(s.deps.Pool)
	existing, groupDefault, err := billingProfileSnapshot(ctx, q, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdBillingProfile404Response{}, nil
	}
	if err != nil {
		return nil, err
	}

	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdBillingProfile409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}

	existingRate, err := floatPtrFromNumeric(existing.DefaultBillRate)
	if err != nil {
		return nil, err
	}
	before := billingProfileFromRow(existing.InvoiceEmail, existing.ReminderEmail, existing.PaymentTermsDays,
		existing.Currency, existing.Language, existing.InvoiceDelivery, existing.ReminderDelivery, existing.PeppolID, existing.Gln, existing.BuyerReference, existingRate)
	identity := identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType)

	hasInvoiceAddress, err := q.CustomerHasInvoiceAddress(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: customer has invoice address: %w", err)
	}

	if billingProfileEqual(before, after) {
		lookup, err := s.resolvedPeppolLookupFor(ctx, q, req.Id, before, identity, existing.Type)
		if err != nil {
			return nil, fmt.Errorf("customers: resolve peppol lookup: %w", err)
		}
		warnings := billingWarnings(before, existing.Type, identity, existing.Email, hasInvoiceAddress, lookup)
		return gen.PutCustomersByIdBillingProfile200JSONResponse(billingProfileResponse(before, existing.Revision, warnings, lookup, groupDefault)), nil
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
		var err error
		updated, err = writeBillingProfile(ctx, store.New(tx), req.Id, before, after, body.Revision, now, act)
		return err
	})
	switch {
	case isMergedAway(err):
		return gen.PutCustomersByIdBillingProfile409ApplicationProblemPlusJSONResponse(mergedAwayProblem(err)), nil
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

	// A body without a revision writes unguarded, so another write (a group
	// move) can land between the snapshot and the UPDATE: the revision this
	// response carries is then more than one past the snapshot's, and the group
	// is read again so the two still describe the same row. With a revision the
	// guarded UPDATE already refused that case above.
	if updated.Revision != existing.Revision+1 {
		membership, err := q.CustomerGroupMembership(ctx, req.Id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Defensive and untested: deleted after this write committed. The
			// write happened, so it is answered with the group it was made under
			// rather than as a 404.
		case err != nil:
			return nil, fmt.Errorf("customers: re-read customer group membership: %w", err)
		default:
			groupDefault = groupDefaultFrom(membership)
		}
	}

	lookup, err := s.resolvedPeppolLookupFor(ctx, q, req.Id, after, identity, updated.Type)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve peppol lookup: %w", err)
	}
	warnings := billingWarnings(after, updated.Type, identity, updated.Email, hasInvoiceAddress, lookup)
	return gen.PutCustomersByIdBillingProfile200JSONResponse(billingProfileResponse(after, updated.Revision, warnings, lookup, groupDefault)), nil
}
