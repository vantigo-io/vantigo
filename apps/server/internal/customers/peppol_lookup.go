package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// This file is POST /customers/{id}/peppol-lookup (can-this-customer-
// receive-EHF design D3): asks the Peppol network whether a customer can
// receive an EHF invoice, and remembers the answer on
// customers.customer_peppol_lookups — its own table, off the customer row,
// so recording an answer never bumps customers.revision (D3's own
// reasoning, 00020_customers_peppol_lookup.sql). GET/PUT
// .../billing-profile (billing_profile.go) read the same stored row to
// surface CustomerBillingProfile.peppolLookup and the two warnings D4 adds;
// this file owns the shapes and rules both share.

// The three outcomes a lookup answers with (design D3): mirrors
// peppol.Result's own three-outcome contract (peppol/lookup.go) one level
// up, as a wire string rather than a bool plus context.
const (
	peppolStatusRegistered    = "registered"
	peppolStatusNotRegistered = "not_registered"
	peppolStatusNoIdentifier  = "no_identifier"
)

// lookupParticipant is the participant identifier a lookup checks (design
// D3): the billing profile's explicit peppolId when set, else
// derivedPeppolID (billing_values.go) from the customer's own legal
// identity and type, else "" — there is nothing to look up. derived reports
// whether the returned id came from derivedPeppolID rather than an explicit
// peppolId: server.go's withholding rule (participantId omitted from a
// derived answer unless the caller holds customers:legal-identity-view)
// turns on exactly this flag.
func lookupParticipant(profile billingProfile, identity *legalIdentity, customerType string) (participant string, derived bool) {
	if profile.PeppolID != nil {
		return *profile.PeppolID, false
	}
	if d := derivedPeppolID(identity, customerType); d != "" {
		return d, true
	}
	return "", false
}

// peppolResultStatus maps a peppol.Result (never itself an error — lookup.go's
// own doc comment) onto the wire's status string: peppol.Result{Registered:
// false} is always "not_registered" (peppol/lookup.go's own doc: NXDOMAIN
// and an SMP 404 are both definitive negatives, never distinguished from
// each other), never "no_identifier" — that outcome is decided before a
// network call is ever made, by lookupParticipant returning "".
func peppolResultStatus(r peppol.Result) string {
	if !r.Registered {
		return peppolStatusNotRegistered
	}
	return peppolStatusRegistered
}

// customerPeppolLookupResponse builds the wire CustomerPeppolLookup GET,
// PUT and POST .../peppol-lookup all share: participantID is included only
// when showParticipantID is true (server.go's withholding rule, resolved by
// each caller before this is built) and non-empty (never sent for
// no_identifier, whose caller passes "").
func customerPeppolLookupResponse(status string, canReceiveInvoice, canReceiveCreditNote bool, participantID string, showParticipantID bool, smpHost *string, checkedAt time.Time) gen.CustomerPeppolLookup {
	resp := gen.CustomerPeppolLookup{
		Status:               status,
		CanReceiveInvoice:    canReceiveInvoice,
		CanReceiveCreditNote: canReceiveCreditNote,
		CheckedAt:            checkedAt,
		SmpHost:              smpHost,
	}
	if participantID != "" && showParticipantID {
		id := participantID
		resp.ParticipantId = &id
	}
	return resp
}

// fetchStoredPeppolLookup reads customer_peppol_lookups' row for customerID,
// nil when it has never been checked (pgx.ErrNoRows — "never checked" is
// not an error).
func fetchStoredPeppolLookup(ctx context.Context, q *store.Queries, customerID int32) (*store.CustomersCustomerPeppolLookup, error) {
	row, err := q.GetCustomerPeppolLookup(ctx, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// resolvedPeppolLookup is GET/PUT .../billing-profile's shared read (design
// D3, D4's controller ruling: "GET .../billing-profile gains an optional
// peppolLookup object … dropped from the response when the participant it
// was made for is no longer the one that would be looked up"): stored is
// dropped — treated the same as never checked, everywhere, response and
// warnings alike — whenever it is nil or its participant_id no longer
// equals participant (the org number or an explicit peppolId changed since
// the check was made).
func resolvedPeppolLookup(stored *store.CustomersCustomerPeppolLookup, participant string, showParticipantID bool) *gen.CustomerPeppolLookup {
	if stored == nil || participant == "" || stored.ParticipantID != participant {
		return nil
	}
	resp := customerPeppolLookupResponse(stored.Status, stored.CanReceiveInvoice, stored.CanReceiveCreditNote, stored.ParticipantID, showParticipantID, stored.SmpHost, stored.CheckedAt)
	return &resp
}

// peppolLookupUnavailableResponse is the 502 POST .../peppol-lookup answers
// when the network call itself failed (design D3: "An upstream failure is
// 502, as Brreg's lookup") — shaped exactly like brregUnavailableResponse,
// this operation's own sibling.
func peppolLookupUnavailableResponse() gen.PostCustomersByIdPeppolLookup502ApplicationProblemPlusJSONResponse {
	return gen.PostCustomersByIdPeppolLookup502ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
		"Peppol lookup unavailable",
		"The Peppol network could not be reached",
		http.StatusBadGateway,
	))
}

// peppolLookupDisabledResponse is the 503 POST .../peppol-lookup answers
// when PEPPOL_LOOKUP_ENABLED is off (design D3, D5): s.peppolLookup is nil
// in exactly that case (server.go's newServer), so the handler never even
// has a function to call.
func peppolLookupDisabledResponse() gen.PostCustomersByIdPeppolLookup503ApplicationProblemPlusJSONResponse {
	return gen.PostCustomersByIdPeppolLookup503ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
		"Peppol lookup disabled",
		"Peppol lookup is disabled on this installation",
		http.StatusServiceUnavailable,
	))
}

// PostCustomersByIdPeppolLookup Ask Peppol whether this customer can
// receive EHF invoices (POST /api/v1/customers/{id}/peppol-lookup)
//
// Ordering (design D3's controller ruling): 404 (the same
// GetCustomerBillingProfile read GET .../billing-profile uses, which also
// gives the type, identity and peppolId a participant is resolved from) →
// 503 when the feature is disabled → decide the participant, answering
// no_identifier immediately with nothing stored and no network call when
// there is none → resolve the timeline actor → the network call itself,
// outside any transaction, bounded by Config.PeppolTimeout → 502 on
// failure, nothing stored → one transaction: a locked read of the stored
// row (first lookup ever counts as "changed"), the upsert, and the
// customer.peppol_lookup event only when the status or either capability
// changed. The customer row itself is never touched.
func (s *server) PostCustomersByIdPeppolLookup(ctx context.Context, req gen.PostCustomersByIdPeppolLookupRequestObject) (gen.PostCustomersByIdPeppolLookupResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetCustomerBillingProfile(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCustomersByIdPeppolLookup404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer billing profile: %w", err)
	}

	if s.peppolLookup == nil {
		return peppolLookupDisabledResponse(), nil
	}

	profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference)
	identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)

	participant, derived := lookupParticipant(profile, identity, row.Type)
	now := s.deps.Clock()
	if participant == "" {
		return gen.PostCustomersByIdPeppolLookup200JSONResponse(
			customerPeppolLookupResponse(peppolStatusNoIdentifier, false, false, "", true, nil, now),
		), nil
	}

	// Resolved before the network call, which is itself outside any
	// transaction (design D3's controller ruling; customers foundation
	// design D1, actor.go): the directory lookup actorFor can make is an
	// out-of-process call this module never wants to make while holding the
	// stored row's FOR UPDATE lock, or racing the Peppol network's own
	// round trip.
	showParticipantID := !derived || s.hasPermission(ctx, legalIdentityView)
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	lookupCtx := ctx
	if s.deps.Config.PeppolTimeout > 0 {
		var cancel context.CancelFunc
		lookupCtx, cancel = context.WithTimeout(ctx, s.deps.Config.PeppolTimeout)
		defer cancel()
	}
	result, err := s.peppolLookup(lookupCtx, participant)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "customers: peppol lookup failed", "customerId", req.Id, "error", err.Error())
		return peppolLookupUnavailableResponse(), nil
	}

	status := peppolResultStatus(result)
	var smpHost *string
	if result.SMPHost != "" {
		host := result.SMPHost
		smpHost = &host
	}

	var (
		previous    store.CustomersCustomerPeppolLookup
		hadPrevious bool
		changed     bool
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var perr error
		previous, perr = txq.GetCustomerPeppolLookupForUpdate(ctx, req.Id)
		switch {
		case errors.Is(perr, pgx.ErrNoRows):
			changed = true
		case perr != nil:
			return perr
		default:
			hadPrevious = true
			changed = previous.Status != status ||
				previous.CanReceiveInvoice != result.CanReceiveInvoice ||
				previous.CanReceiveCreditNote != result.CanReceiveCreditNote
		}

		if err := txq.UpsertCustomerPeppolLookup(ctx, store.UpsertCustomerPeppolLookupParams{
			CustomerID: req.Id, ParticipantID: participant, Status: status,
			CanReceiveInvoice: result.CanReceiveInvoice, CanReceiveCreditNote: result.CanReceiveCreditNote,
			SmpHost: smpHost, CheckedAt: now,
		}); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		var previousStatus *string
		if hadPrevious {
			previousStatus = &previous.Status
		}
		return recordCustomerPeppolLookup(ctx, txq, now, req.Id, status, result.CanReceiveInvoice, result.CanReceiveCreditNote, smpHost, previousStatus, act.Kind, act.Display, act.UserID)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: record peppol lookup: %w", err)
	}

	return gen.PostCustomersByIdPeppolLookup200JSONResponse(
		customerPeppolLookupResponse(status, result.CanReceiveInvoice, result.CanReceiveCreditNote, participant, showParticipantID, smpHost, now),
	), nil
}
