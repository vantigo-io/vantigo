package invoices

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the AvtaleGiro mandates (invoices phase 6 design I32, plan
// readings 8 and 10): synced from the provider's delta queue by the
// invoices-efaktura worker, each resolved to a customer by the customer
// number its KID carries — through this module's own snapshots, never the
// directory, which reads by id only — and listed for a person to assign
// when none matches.

const (
	codeMandateResolved          = "mandate_resolved"
	codeMandateKidMismatch       = "mandate_kid_mismatch"
	codeMandateCustomerNotPerson = "mandate_customer_not_person"
	codeMandateNotResolved       = "mandate_not_resolved"
	codeMandateHasLiveClaim      = "mandate_has_live_claim"
	mandateRefusedTitle          = "The mandate cannot be assigned"
)

// beforeMandateUpsert, when a test sets it (export_test.go), is called before
// each mandate of a page is stored; an error fails the page. nil in
// production.
var beforeMandateUpsert func(kid string) error

// mandateStatusOf is the stored status of a mandate the queue delivered.
func mandateStatusOf(m consumerinvoice.Mandate) string {
	switch {
	case m.Deleted:
		return string(consumerinvoice.MandateInactive)
	default:
		return string(m.Status)
	}
}

// customerOfKID resolves a customer KID to the customer it names: the
// newest issued document whose buyer carried the number, followed through
// a merge. nil when nothing matches.
func (s *server) customerOfKID(ctx context.Context, q *store.Queries, k string) (*int32, error) {
	number, ok := kid.CustomerNumberOf(k)
	if !ok {
		return nil, nil
	}
	id, err := q.CustomerOfNumber(ctx, &number)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("invoices: resolve a customer number: %w", err)
	}
	profile, err := s.customerProfile(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's billing profile: %w", id, err)
	}
	if profile != nil && profile.MergedInto != nil {
		id = *profile.MergedInto
	}
	return &id, nil
}

// upsertMandates stores one page of the queue. position is where the page
// starts in the drain's order: each mandate's queue position is it plus its
// index, so a later delivery of a KID overwrites an earlier one and never
// the other way round. A KID that is no KID — empty, too long, not digits —
// is logged and skipped, never a page that fails for good.
func (s *server) upsertMandates(ctx context.Context, q *store.Queries, ms []consumerinvoice.Mandate, position int64) error {
	now := s.deps.Clock()
	for i, m := range ms {
		if _, _, _, ok := kid.Parse(m.KID); !ok {
			s.deps.Logger.WarnContext(ctx, "invoices: the mandate queue delivered a KID that is no KID; skipped", "length", len(m.KID))
			continue
		}
		if beforeMandateUpsert != nil {
			if err := beforeMandateUpsert(m.KID); err != nil {
				return err
			}
		}
		customer, err := s.customerOfKID(ctx, q, m.KID)
		if err != nil {
			return err
		}
		_, err = q.UpsertMandate(ctx, store.UpsertMandateParams{
			Kid: m.KID, CustomerID: customer, Status: mandateStatusOf(m), NotifyByMail: m.NotifyByMail, Now: now,
			QueuePosition: position + int64(i),
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("invoices: store a mandate: %w", err)
		}
	}
	return nil
}

func mandateResponse(m store.InvoicesAvtalegiroMandate) gen.InvoicesAvtaleGiroMandate {
	return gen.InvoicesAvtaleGiroMandate{
		Id: m.ID, Kid: m.Kid, CustomerId: m.CustomerID, Status: m.Status, NotifyByMail: m.NotifyByMail,
		ProviderSeenAt: m.ProviderSeenAt, UpdatedAt: m.UpdatedAt, ResolutionNote: m.ResolutionNote,
	}
}

// GetInvoicesAvtalegiroMandates List the AvtaleGiro mandates
// (GET /api/v1/invoices/avtalegiro/mandates)
func (s *server) GetInvoicesAvtalegiroMandates(ctx context.Context, req gen.GetInvoicesAvtalegiroMandatesRequestObject) (gen.GetInvoicesAvtalegiroMandatesResponseObject, error) {
	unresolved := req.Params.Unresolved != nil && *req.Params.Unresolved
	rows, err := store.New(s.deps.Pool).ListMandates(ctx, unresolved)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the mandates: %w", err)
	}
	out := gen.InvoicesAvtaleGiroMandateList{Items: make([]gen.InvoicesAvtaleGiroMandate, 0, len(rows))}
	for _, m := range rows {
		out.Items = append(out.Items, mandateResponse(m))
	}
	return gen.GetInvoicesAvtalegiroMandates200JSONResponse(out), nil
}

// PostInvoicesAvtalegiroMandatesByMandateIdResolve Assign a mandate to a customer
// (POST /api/v1/invoices/avtalegiro/mandates/{mandateId}/resolve)
func (s *server) PostInvoicesAvtalegiroMandatesByMandateIdResolve(ctx context.Context, req gen.PostInvoicesAvtalegiroMandatesByMandateIdResolveRequestObject) (gen.PostInvoicesAvtalegiroMandatesByMandateIdResolveResponseObject, error) {
	if req.Body.CustomerId < 1 {
		return gen.PostInvoicesAvtalegiroMandatesByMandateIdResolve400ApplicationProblemPlusJSONResponse(invalid("Invalid mandate assignment",
			fieldError("customerId", "Must be a customer's id."))), nil
	}
	var note *string
	if req.Body.Reason != nil {
		if r := strings.TrimSpace(*req.Body.Reason); r != "" {
			if msg := maxLength("A reason", r, maxReason); msg != "" {
				return gen.PostInvoicesAvtalegiroMandatesByMandateIdResolve400ApplicationProblemPlusJSONResponse(invalid("Invalid mandate assignment",
					fieldError("reason", msg))), nil
			}
			note = &r
		}
	}
	refused := func(code, detail string) (gen.PostInvoicesAvtalegiroMandatesByMandateIdResolveResponseObject, error) {
		return gen.PostInvoicesAvtalegiroMandatesByMandateIdResolve409ApplicationProblemPlusJSONResponse(conflict(code, mandateRefusedTitle, detail)), nil
	}
	q := store.New(s.deps.Pool)
	mandate, err := q.GetMandate(ctx, req.MandateId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesAvtalegiroMandatesByMandateIdResolve404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("invoices: read mandate %d: %w", req.MandateId, err)
	}
	profile, err := s.customerProfile(ctx, req.Body.CustomerId)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's billing profile: %w", req.Body.CustomerId, err)
	}
	if profile == nil {
		return gen.PostInvoicesAvtalegiroMandatesByMandateIdResolve404Response{}, nil
	}
	erased, err := q.CustomerErased(ctx, profile.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read whether customer %d was erased: %w", profile.ID, err)
	}
	switch {
	case profile.MergedInto != nil:
		return refused(codeCustomerMerged, fmt.Sprintf("The customer was merged into customer %d; assign the mandate to that one.", *profile.MergedInto))
	case erased:
		return refused(codeCustomerAnonymised, "The customer has been anonymised.")
	case profile.Type != "person":
		return refused(codeMandateCustomerNotPerson, "AvtaleGiro mandates are a person's; this customer is a business.")
	}
	// The KID carries a customer number; another customer takes it only
	// when a person confirms it and says why.
	if number, ok := kid.CustomerNumberOf(mandate.Kid); ok && number != profile.CustomerNumber {
		if req.Body.ConfirmMismatch == nil || !*req.Body.ConfirmMismatch || note == nil {
			return refused(codeMandateKidMismatch, fmt.Sprintf(
				"The mandate's KID carries customer number %d, and this customer is number %d. Confirm the mismatch with a reason to assign it anyway.",
				number, profile.CustomerNumber))
		}
	}
	resolved, err := q.ResolveMandate(ctx, store.ResolveMandateParams{
		CustomerID: &req.Body.CustomerId, ResolvedByUserID: ptr(callerID(ctx)), ResolutionNote: note, Now: s.deps.Clock(), ID: req.MandateId,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PostInvoicesAvtalegiroMandatesByMandateIdResolve409ApplicationProblemPlusJSONResponse(conflict(codeMandateResolved,
			"The mandate is assigned already", "The mandate already names a customer.")), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: assign mandate %d: %w", req.MandateId, err)
	}
	s.deps.Logger.InfoContext(ctx, "invoices: a mandate is assigned to a customer", "mandate_id", resolved.ID, "customer_id", req.Body.CustomerId)
	return gen.PostInvoicesAvtalegiroMandatesByMandateIdResolve200JSONResponse(mandateResponse(resolved)), nil
}

// PostInvoicesAvtalegiroMandatesByMandateIdUnassign Take a mandate's customer away
// (POST /api/v1/invoices/avtalegiro/mandates/{mandateId}/unassign)
func (s *server) PostInvoicesAvtalegiroMandatesByMandateIdUnassign(ctx context.Context, req gen.PostInvoicesAvtalegiroMandatesByMandateIdUnassignRequestObject) (gen.PostInvoicesAvtalegiroMandatesByMandateIdUnassignResponseObject, error) {
	refused := func(code, detail string) (gen.PostInvoicesAvtalegiroMandatesByMandateIdUnassignResponseObject, error) {
		return gen.PostInvoicesAvtalegiroMandatesByMandateIdUnassign409ApplicationProblemPlusJSONResponse(conflict(code, "The mandate cannot be unassigned", detail)), nil
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetMandate(ctx, req.MandateId); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesAvtalegiroMandatesByMandateIdUnassign404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("invoices: read mandate %d: %w", req.MandateId, err)
	}
	live, err := q.MandateHasLiveClaim(ctx, req.MandateId)
	if err != nil {
		return nil, fmt.Errorf("invoices: read mandate %d's claims: %w", req.MandateId, err)
	}
	if live {
		return refused(codeMandateHasLiveClaim, "A claim on the mandate is submitted, accepted or being cancelled. Wait until it is settled.")
	}
	m, err := q.UnassignMandate(ctx, store.UnassignMandateParams{Now: s.deps.Clock(), ID: req.MandateId})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return refused(codeMandateNotResolved, "The mandate names no customer.")
	case err != nil:
		return nil, fmt.Errorf("invoices: unassign mandate %d: %w", req.MandateId, err)
	}
	s.deps.Logger.InfoContext(ctx, "invoices: a mandate is unassigned", "mandate_id", m.ID)
	return gen.PostInvoicesAvtalegiroMandatesByMandateIdUnassign200JSONResponse(mandateResponse(m)), nil
}
