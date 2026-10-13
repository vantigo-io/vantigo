package invoices

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the pay page's three anonymous operations (invoices payments
// and reminders design D15, plan reading 23): the read — a POST, so the
// token travels in the body and never in an API URL — the attempt's create
// and the return page's status read. None needs a session and none reads the
// caller: a signed-in caller is answered as anyone else. Each has its own
// rate-limit policy per client address (module.go's limits). Nothing the
// pay page is answered names the buyer; the token is only ever hashed, and
// the return address carries a nonce of the attempt's, never the token.

const invalidPayTitle = "Invalid pay page request"

// PostInvoicesPayRead Read an invoice for the pay page
// (POST /api/v1/invoices/pay/read)
func (s *server) PostInvoicesPayRead(ctx context.Context, req gen.PostInvoicesPayReadRequestObject) (gen.PostInvoicesPayReadResponseObject, error) {
	token := strings.TrimSpace(req.Body.Token)
	if token == "" {
		return gen.PostInvoicesPayRead400ApplicationProblemPlusJSONResponse(invalid(invalidPayTitle, fieldError("token", "The pay link's token is required."))), nil
	}
	var page *gen.InvoicesPayPage
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		link, err := q.PayLinkByToken(ctx, tokenSHA256(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invoices: look up a pay link: %w", err)
		}
		inv, err := q.GetInvoice(ctx, link.InvoiceID)
		if err != nil {
			return fmt.Errorf("invoices: read document %d: %w", link.InvoiceID, err)
		}
		facts, err := s.payFactsOf(ctx, q, inv, &link, flowPayLink)
		if err != nil {
			return err
		}
		settings, err := q.GetPaymentSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the payment settings: %w", err)
		}
		page = payPageOf(inv, facts, settings)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if page == nil {
		return gen.PostInvoicesPayRead404Response{}, nil
	}
	return gen.PostInvoicesPayRead200JSONResponse(*page), nil
}

// payPageOf is what the pay page shows of inv — the seller, never the buyer.
func payPageOf(inv store.InvoicesInvoice, facts payFacts, settings store.InvoicesPaymentSetting) *gen.InvoicesPayPage {
	open := facts.open
	if open.Sign() < 0 {
		open = new(big.Rat)
	}
	page := &gen.InvoicesPayPage{
		Seller:        gen.InvoicesPaySeller{Name: deref(inv.SellerLegalName), OrganisationNumber: deref(inv.SellerOrganisationNumber)},
		InvoiceNumber: deref64(inv.Number), IssueDate: wireDate(inv.IssueDate.Time), DueDate: wireDateOf(inv.DueDate),
		OpenAmount: floatFromRat(open, 2), Currency: inv.Currency, Kid: inv.Kid,
		TermsUrl: settings.TermsUrl, PrivacyUrl: settings.PrivacyUrl, Language: gen.Nb,
	}
	if deref(inv.BuyerLanguage) == "en" {
		page.Language = gen.En
	}
	ok, reason := payableOf(facts)
	page.Payable = ok
	if !ok {
		page.Reason = &reason
	}
	return page
}

// PostInvoicesPayAttempts Start a Vipps payment from the pay page
// (POST /api/v1/invoices/pay/attempts)
func (s *server) PostInvoicesPayAttempts(ctx context.Context, req gen.PostInvoicesPayAttemptsRequestObject) (gen.PostInvoicesPayAttemptsResponseObject, error) {
	token := strings.TrimSpace(req.Body.Token)
	var errs map[string][]string
	if token == "" {
		errs = withFieldError(errs, "token", "The pay link's token is required.")
	}
	if !req.Body.AcceptTerms {
		errs = withFieldError(errs, "acceptTerms", "Accept the seller's sales terms and privacy notice to pay with Vipps.")
	}
	if errs != nil {
		return gen.PostInvoicesPayAttempts400ApplicationProblemPlusJSONResponse(invalid(invalidPayTitle, errs)), nil
	}
	now := s.deps.Clock()
	q := store.New(s.deps.Pool)
	link, err := q.PayLinkByToken(ctx, tokenSHA256(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesPayAttempts404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: look up a pay link: %w", err)
	}
	inv, err := q.GetInvoice(ctx, link.InvoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", link.InvoiceID, err)
	}
	// Judged on the pool first, so a link that cannot pay never builds a
	// provider; judged again under the invoice's lock by createAttempt.
	facts, err := s.payFactsOf(ctx, q, inv, &link, flowPayLink)
	if err != nil {
		return nil, err
	}
	if ok, reason := payableOf(facts); !ok {
		return gen.PostInvoicesPayAttempts409ApplicationProblemPlusJSONResponse(payRefusal(reason)), nil
	}
	nonce, err := newPayNonce()
	if err != nil {
		return nil, err
	}
	attempt, created, refusal, err := s.createAttempt(ctx, attemptInput{
		invoiceID: inv.ID, flow: flowPayLink, payLinkID: &link.ID, nonce: nonce,
	}, now)
	switch {
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesPayAttempts404Response{}, nil
	case err != nil:
		return nil, err
	case refusal != nil:
		switch *refusal.Status {
		case http.StatusServiceUnavailable:
			return gen.PostInvoicesPayAttempts503ApplicationProblemPlusJSONResponse(*refusal), nil
		case http.StatusBadGateway:
			return gen.PostInvoicesPayAttempts502ApplicationProblemPlusJSONResponse(*refusal), nil
		}
		return gen.PostInvoicesPayAttempts409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	return gen.PostInvoicesPayAttempts201JSONResponse(gen.InvoicesPayAttemptCreated{
		Reference: attempt.Reference, RedirectUrl: created.RedirectURL,
	}), nil
}

// GetInvoicesPayAttemptsByReference Read a pay-page Vipps payment's state
// (GET /api/v1/invoices/pay/attempts/{reference})
func (s *server) GetInvoicesPayAttemptsByReference(ctx context.Context, req gen.GetInvoicesPayAttemptsByReferenceRequestObject) (gen.GetInvoicesPayAttemptsByReferenceResponseObject, error) {
	if req.Params.Nonce == "" {
		return gen.GetInvoicesPayAttemptsByReference400ApplicationProblemPlusJSONResponse(invalid(invalidPayTitle, fieldError("nonce", "The return address's nonce is required."))), nil
	}
	attempt, err := store.New(s.deps.Pool).PayAttemptByReference(ctx, req.Reference)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesPayAttemptsByReference404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read a payment attempt: %w", err)
	}
	want := tokenSHA256(req.Params.Nonce)
	if attempt.ReturnNonceSha256 == nil || subtle.ConstantTimeCompare([]byte(*attempt.ReturnNonceSha256), []byte(want)) != 1 {
		return gen.GetInvoicesPayAttemptsByReference404Response{}, nil
	}
	return gen.GetInvoicesPayAttemptsByReference200JSONResponse(gen.InvoicesPayAttemptStatus{State: payStateOf(attempt.State)}), nil
}

// payStateOf is an attempt's state as the pay page shows it: pending until
// Vipps answered and the capture is registered, paid once it is, failed when
// Vipps refused it or nothing could be captured, cancelled when the payer
// stopped, it expired or nothing was left to capture.
func payStateOf(state string) gen.InvoicesPayAttemptStatusState {
	switch state {
	case "captured":
		return gen.InvoicesPayAttemptStatusStatePaid
	case "failed", "capture_failed", "abandoned":
		return gen.InvoicesPayAttemptStatusStateFailed
	case "cancelling", "cancelled", "aborted", "expired", "terminated":
		return gen.InvoicesPayAttemptStatusStateCancelled
	}
	return gen.InvoicesPayAttemptStatusStatePending
}
