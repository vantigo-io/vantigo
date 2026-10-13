package invoices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is on-site Vipps payment (invoices payments and reminders design
// D17): the customer, standing in front of the person who did the job, scans
// a one-time QR code. That is kontantsalg, lawful without a cash register
// only as a kontantfaktura (bokføringsforskriften § 5-4-1 tredje ledd, R4 §9),
// so the request is held to hard rules that no setting relaxes, each judged
// here, on the server, under the invoice's lock:
//
//  1. on-site payment available — paymentsAvailable's on-site answer: the
//     INVOICES_VIPPS_ENABLED switch, credentials stored and not refused, and
//     the payment settings' switch, which is on only with the kontantsalg
//     acknowledgement (ck_payment_settings_on_site) — never the setting alone;
//  2. an issued invoice, never a draft or a credit note: every payment is
//     tied to one full delkapittel 5-1 invoice in the ordinary series, and no
//     anonymous, "kontantkunde" or receipt-only path exists;
//  3. the buyer identified on the stored snapshot — a name, and an
//     organisation number or a complete address (buyerComplete) — and the
//     customer not anonymised since, which blanks the snapshot (m9);
//  4. one payment, one invoice: the whole open amount, and one live on-site
//     attempt per invoice (ux_payment_attempts_on_site_live) — a create the
//     payments worker gave up on holds it until a manager abandons it.
//
// The attempt itself is Task 7's create (createAttempt): inserted creating
// under the invoice's lock with its keys, the credentials shared and their
// version compared, Vipps asked after the commit, the answer recorded under
// the attempt's lock. Vipps' QR is asked for as its target URL and rendered
// by the adapter (payments/qrsvg); it is answered once, to the request that
// made it, and never read back from Vipps.

const (
	invalidOnSiteTitle = "Invalid payment request"
	cannotOnSiteTitle  = "The payment cannot be taken on site"
	cannotCancelTitle  = "The payment request cannot be cancelled"
)

// onSiteUnavailable is the refusal when on-site payment is not available.
func onSiteUnavailable() gen.InvoicesConflictProblem {
	return conflict(codeOnSitePaymentsUnavailable, cannotOnSiteTitle,
		"On-site payment is not available: Vipps is switched off, its keys are missing or refused, or on-site payment is not turned on in the payment settings.")
}

// buyerIdentified is D17 rule 1 on an issued document's stored snapshot:
// the buyer named, and either an organisation number or a complete address.
func buyerIdentified(inv store.InvoicesInvoice) bool {
	if inv.BuyerName == nil || strings.TrimSpace(*inv.BuyerName) == "" {
		return false
	}
	org := inv.BuyerOrganisationNumber
	if org != nil && strings.TrimSpace(*org) == "" {
		org = nil
	}
	return buyerComplete(&store.IssueDocumentParams{
		BuyerOrganisationNumber: org, BuyerAddressLine1: inv.BuyerAddressLine1, BuyerPostalCode: inv.BuyerPostalCode,
		BuyerCity: inv.BuyerCity, BuyerCountry: inv.BuyerCountry,
	})
}

// onSiteRefusal judges D17's rules on a locked invoice, in the operation's
// order, before the payable facts: the refusal, or nil.
func (s *server) onSiteRefusal(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (*gen.InvoicesConflictProblem, error) {
	avail, err := s.paymentsAvailable(ctx, q)
	if err != nil {
		return nil, err
	}
	if !avail.onSite {
		return ptr(onSiteUnavailable()), nil
	}
	if inv.Kind != kindInvoice {
		return ptr(conflict(codeCreditNoteNoPayments, cannotOnSiteTitle, "A credit note is never paid.")), nil
	}
	if inv.Status != statusIssued {
		return ptr(conflict(codeInvoiceDraft, cannotOnSiteTitle, "A draft is not a sales document: issue it first.")), nil
	}
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	if erased || !buyerIdentified(inv) {
		return ptr(conflict(codeBuyerIdentityMissing, cannotOnSiteTitle,
			"A payment on site is a cash sale, allowed only on an invoice naming the buyer with an address or an organisation number. This invoice does not, or its customer was anonymised.")), nil
	}
	live, err := q.LiveOnSiteAttempt(ctx, inv.ID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("invoices: read document %d's on-site request: %w", inv.ID, err)
	}
	if (live.State == "creating" || live.State == "created") && live.NextPollAt == nil {
		return ptr(conflict(codePaymentRequestActive, cannotOnSiteTitle, fmt.Sprintf(
			"An earlier payment request (%s) was never confirmed by Vipps and the payments worker gave up on it. Check it in the Vipps portal; someone with invoices:manage must abandon it before a new one can be made.",
			live.Reference))), nil
	}
	return ptr(conflict(codePaymentRequestActive, cannotOnSiteTitle, fmt.Sprintf(
		"A payment request of this invoice is in progress (%s, %s). Cancel it, or wait for it to end, before making a new one.",
		live.Reference, live.State))), nil
}

// requestOnSite makes an on-site payment request of invoice id: on-site
// availability judged on the pool first, so a switch that is off never
// builds a provider, then the create (createAttempt) with every rule judged
// again under the invoice's lock. errDocumentGone when there is no
// document.
func (s *server) requestOnSite(ctx context.Context, id int64, now time.Time) (*gen.InvoicesOnSitePayment, *gen.InvoicesConflictProblem, error) {
	avail, err := s.paymentsAvailable(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, nil, err
	}
	if !avail.onSite {
		return nil, ptr(onSiteUnavailable()), nil
	}
	by := callerID(ctx)
	attempt, created, refusal, err := s.createAttempt(ctx, attemptInput{invoiceID: id, flow: flowOnSite, createdBy: &by}, now)
	if err != nil || refusal != nil {
		return nil, refusal, err
	}
	return &gen.InvoicesOnSitePayment{
		AttemptId: attempt.ID, Reference: attempt.Reference, QrSvg: ptr(created.QRSVG), ExpiresAt: attempt.ExpiresAt,
	}, nil, nil
}

// PostInvoicesByIdPaymentRequests Take a Vipps payment on site
// (POST /api/v1/invoices/{id}/payment-requests)
func (s *server) PostInvoicesByIdPaymentRequests(ctx context.Context, req gen.PostInvoicesByIdPaymentRequestsRequestObject) (gen.PostInvoicesByIdPaymentRequestsResponseObject, error) {
	if req.Body.Flow != gen.InvoicesOnSitePaymentRequestBodyFlowOnSite {
		return gen.PostInvoicesByIdPaymentRequests400ApplicationProblemPlusJSONResponse(invalid(invalidOnSiteTitle,
			fieldError("flow", "flow is on_site"))), nil
	}
	now := s.deps.Clock() // the request's one clock read
	out, refusal, err := s.requestOnSite(ctx, req.Id, now)
	switch {
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesByIdPaymentRequests404Response{}, nil
	case err != nil:
		return nil, err
	case refusal != nil && refusal.Status != nil && *refusal.Status == http.StatusBadGateway:
		return gen.PostInvoicesByIdPaymentRequests502ApplicationProblemPlusJSONResponse(*refusal), nil
	case refusal != nil && refusal.Status != nil && *refusal.Status == http.StatusServiceUnavailable:
		return gen.PostInvoicesByIdPaymentRequests503ApplicationProblemPlusJSONResponse(*refusal), nil
	case refusal != nil:
		return gen.PostInvoicesByIdPaymentRequests409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	return gen.PostInvoicesByIdPaymentRequests201JSONResponse(*out), nil
}

// onSiteStatusWire is an attempt's state as the tablet reads it.
func onSiteStatusWire(a store.InvoicesPaymentAttempt) gen.InvoicesOnSitePaymentStatus {
	return gen.InvoicesOnSitePaymentStatus{State: gen.InvoicesOnSitePaymentStatusState(a.State), CapturedAt: a.CapturedAt}
}

// GetInvoicesByIdPaymentRequestsByAttemptId Read an on-site payment request
// (GET /api/v1/invoices/{id}/payment-requests/{attemptId})
//
// The database only, never Vipps: the payments worker keeps the state.
func (s *server) GetInvoicesByIdPaymentRequestsByAttemptId(ctx context.Context, req gen.GetInvoicesByIdPaymentRequestsByAttemptIdRequestObject) (gen.GetInvoicesByIdPaymentRequestsByAttemptIdResponseObject, error) {
	a, err := store.New(s.deps.Pool).GetPaymentAttempt(ctx, req.AttemptId)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.GetInvoicesByIdPaymentRequestsByAttemptId404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("invoices: read payment attempt %d: %w", req.AttemptId, err)
	case a.InvoiceID != req.Id || a.Flow != flowOnSite:
		return gen.GetInvoicesByIdPaymentRequestsByAttemptId404Response{}, nil
	}
	return gen.GetInvoicesByIdPaymentRequestsByAttemptId200JSONResponse(onSiteStatusWire(a)), nil
}

// PostInvoicesByIdPaymentRequestsByAttemptIdCancel Cancel an on-site payment
// request (POST /api/v1/invoices/{id}/payment-requests/{attemptId}/cancel)
//
// The attempt alone is locked (D18). A request Vipps created and nobody was
// seen to approve becomes cancelling, due at once, and the payments worker
// cancels it at Vipps; a request already over is answered as it stands; one
// not confirmed yet or approved is refused.
func (s *server) PostInvoicesByIdPaymentRequestsByAttemptIdCancel(ctx context.Context, req gen.PostInvoicesByIdPaymentRequestsByAttemptIdCancelRequestObject) (gen.PostInvoicesByIdPaymentRequestsByAttemptIdCancelResponseObject, error) {
	now := s.deps.Clock() // the request's one clock read
	var out store.InvoicesPaymentAttempt
	var refusal *gen.InvoicesConflictProblem
	found := false
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		a, err := lockPaymentAttempt(ctx, txq, req.AttemptId)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invoices: lock payment attempt %d: %w", req.AttemptId, err)
		}
		if a.InvoiceID != req.Id || a.Flow != flowOnSite {
			return nil
		}
		found, out = true, a
		switch a.State {
		case "created":
			if out, err = txq.CancelOnSiteAttempt(ctx, store.CancelOnSiteAttemptParams{Now: now, ID: a.ID}); err != nil {
				return fmt.Errorf("invoices: cancel payment attempt %d: %w", a.ID, err)
			}
		case "creating":
			refusal = ptr(conflict(codePaymentRequestActive, cannotCancelTitle,
				"Vipps has not confirmed this request yet; the payments worker is finding out. Try again in a minute."))
		case "authorized", "capturing", "capture_unknown", "captured", "capture_failed":
			refusal = ptr(conflict(codePaymentRequestActive, cannotCancelTitle, fmt.Sprintf(
				"The customer approved this payment (%s): it is being completed, not cancelled.", a.State)))
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case !found:
		return gen.PostInvoicesByIdPaymentRequestsByAttemptIdCancel404Response{}, nil
	case refusal != nil:
		return gen.PostInvoicesByIdPaymentRequestsByAttemptIdCancel409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	return gen.PostInvoicesByIdPaymentRequestsByAttemptIdCancel200JSONResponse(onSiteStatusWire(out)), nil
}

// liveOnSiteRequest is an invoice's live on-site request as a quick
// invoice's replay reports it — read, never made, and without its QR,
// which is answered only to the request that made it. nil when none lives.
func liveOnSiteRequest(ctx context.Context, q *store.Queries, invoiceID int64) (*gen.InvoicesOnSitePayment, error) {
	a, err := q.LiveOnSiteAttempt(ctx, invoiceID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("invoices: read document %d's on-site request: %w", invoiceID, err)
	}
	return &gen.InvoicesOnSitePayment{AttemptId: a.ID, Reference: a.Reference, ExpiresAt: a.ExpiresAt}, nil
}
