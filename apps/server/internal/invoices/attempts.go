package invoices

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/payments"
)

// This file is a Vipps payment attempt's create (invoices payments and
// reminders design D16): two transactions around the provider's call, never
// the call inside either. The first locks the invoice, judges again under the
// lock what the caller judged before it — the pay link still live, the
// invoice still payable, Vipps still available (paymentsAvailable through
// the transaction's own queries, m15) — shares the credentials row and
// compares it with the version the provider was built from (plan reading 8),
// counts the cap and inserts the attempt as creating, its idempotency keys
// stored with it. After the commit the provider is asked, outside any lock.
// The second records the answer under the attempt's own lock: created with
// the address the payer is sent to, or failed on a definitive refusal; an
// unknown outcome leaves it creating for the payments worker, which reads the
// reference back.

const (
	flowPayLink = "pay_link"
	flowOnSite  = "on_site"

	// attemptLifetime is how long Vipps keeps a payment request open (R4 §4.2).
	attemptLifetime = 10 * time.Minute
	// attemptFirstPoll is when an attempt is first read back (R4 §4.5).
	attemptFirstPoll = 5 * time.Second
	// maxLiveAttempts is the cap of attempts that may still pay one invoice (D15).
	maxLiveAttempts = 5

	cannotPayTitle      = "The invoice cannot be paid with Vipps now"
	providerFailedTitle = "Vipps did not create the payment"
)

var (
	// minPayable and maxPayable are Vipps' limits on one payment (R4 §4.4).
	minPayable = big.NewRat(1, 1)
	maxPayable = big.NewRat(650_000, 1)
)

// attemptAfterInvoiceLock, when a test sets it (export_test.go), runs inside
// an attempt's create right after the credentials row is shared under the
// invoice's lock, before the attempt is inserted; nil in production.
var attemptAfterInvoiceLock func(ctx context.Context, tx pgx.Tx)

// abandonAfterAttemptLock, when a test sets it (export_test.go), runs inside
// an abandon's transaction right after the attempt's lock; nil in
// production.
var abandonAfterAttemptLock func(ctx context.Context, tx pgx.Tx)

// abandonAfterRow and abandonAfterRead, when a test sets them
// (export_test.go), run in an abandon right after it read the attempt on the
// pool and right after its read of Vipps, outside any lock; nil in
// production.
var abandonAfterRow, abandonAfterRead func()

// payNonce, when a test sets it (export_test.go), answers every new return
// nonce in place of 16 random bytes; nil in production.
var payNonce func() string

// newPayNonce is a fresh return nonce: 16 random bytes, base64url without
// padding, so the return address needs no escaping.
func newPayNonce() (string, error) {
	if payNonce != nil {
		return payNonce(), nil
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("invoices: draw a return nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// minorOf is an amount in øre, refusing anything that is not a whole number
// of øre: the only road from the module's exact decimals to the port.
func minorOf(v *big.Rat) (int64, error) {
	minor := new(big.Rat).Mul(v, big.NewRat(100, 1))
	if !minor.IsInt() || !minor.Num().IsInt64() {
		return 0, fmt.Errorf("invoices: %s is not a whole number of øre", v.FloatString(4))
	}
	return minor.Num().Int64(), nil
}

// ratOfMinor is øre from the port as the module's exact decimal.
func ratOfMinor(minor int64) *big.Rat { return big.NewRat(minor, 100) }

// openAndReserved is what is open on an issued invoice and what of it the
// live reservations hold (D2, D16): open = gross − credited − live payments
// − every live reservation — the captures decided and not yet registered or
// disproved, which may already have moved money — and reserved their sum.
// It is the one place the reservations are subtracted: openOf answers its
// open. Read with q, which holds the invoice's lock, or a plain read.
func openAndReserved(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (open, reserved *big.Rat, err error) {
	_, left, err := uncredited(ctx, q, inv)
	if err != nil {
		return nil, nil, err
	}
	sum, err := q.LivePaymentsSum(ctx, inv.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: read what document %d is paid: %w", inv.ID, err)
	}
	paid, err := ratFromNumeric(sum)
	if err != nil {
		return nil, nil, err
	}
	held, err := q.LiveReservationsSum(ctx, inv.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: read what document %d's reservations hold: %w", inv.ID, err)
	}
	if reserved, err = ratFromNumeric(held); err != nil {
		return nil, nil, err
	}
	open = left.Sub(left, paid)
	return open.Sub(open, reserved), reserved, nil
}

// payFacts is what decides whether an invoice can be paid with Vipps now.
type payFacts struct {
	revoked, erased, handedOff, available bool
	// left is gross less credited; open is left less paid and less the
	// live reservations, which reserved holds.
	left, open, reserved *big.Rat
}

// payableOf judges the facts in the pay page's order: payable, or why not.
func payableOf(f payFacts) (bool, gen.InvoicesPayReason) {
	switch {
	case f.revoked || f.erased:
		return false, gen.InvoicesPayReasonRevoked
	case f.left.Sign() <= 0:
		return false, gen.InvoicesPayReasonCredited
	case new(big.Rat).Add(f.open, f.reserved).Sign() <= 0:
		return false, gen.InvoicesPayReasonSettled
	case f.handedOff:
		return false, gen.InvoicesPayReasonHandedOff
	case !f.available:
		return false, gen.InvoicesPayReasonUnavailable
	case f.reserved.Sign() > 0 && f.open.Cmp(minPayable) < 0:
		return false, gen.InvoicesPayReasonPaymentInProgress
	case f.open.Cmp(minPayable) < 0 || f.open.Cmp(maxPayable) > 0:
		return false, gen.InvoicesPayReasonAmountOutOfRange
	}
	return true, ""
}

// payFactsOf reads the facts of inv on q — the pool, a snapshot or the
// invoice's own lock. link is the pay link of a pay-link payment, nil on
// site; availability is the flow's.
func (s *server) payFactsOf(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, link *store.InvoicesPayLink, flow string) (payFacts, error) {
	var f payFacts
	if link != nil {
		live, err := q.GetPayLink(ctx, link.ID)
		if err != nil {
			return f, fmt.Errorf("invoices: read pay link %d: %w", link.ID, err)
		}
		f.revoked = live.RevokedAt != nil || live.InvoiceID != inv.ID
	}
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return f, fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	f.erased = erased
	if _, f.left, err = uncredited(ctx, q, inv); err != nil {
		return f, err
	}
	if f.open, f.reserved, err = openAndReserved(ctx, q, inv); err != nil {
		return f, err
	}
	handoffs, err := q.LiveHandoffs(ctx, []int64{inv.ID})
	if err != nil {
		return f, fmt.Errorf("invoices: read document %d's hand-off: %w", inv.ID, err)
	}
	f.handedOff = len(handoffs) > 0
	avail, err := s.paymentsAvailable(ctx, q)
	if err != nil {
		return f, err
	}
	f.available = avail.payLinks
	if flow == flowOnSite {
		f.available = avail.onSite
	}
	return f, nil
}

// payRefusal is the 409 an attempt's create answers for a reason: the code
// the reason maps to, and the reason beside it.
func payRefusal(reason gen.InvoicesPayReason) gen.InvoicesConflictProblem {
	var code, detail string
	switch reason {
	case gen.InvoicesPayReasonSettled:
		code, detail = codeInvoiceSettled, "Nothing of this invoice is open: it is paid."
	case gen.InvoicesPayReasonCredited:
		code, detail = codeInvoiceFullyCredited, "Credit notes took the whole invoice: nothing is to be paid."
	case gen.InvoicesPayReasonHandedOff:
		code, detail = codeInvoiceHandedOff, "The invoice is with a collection agency: contact the agency to pay it."
	case gen.InvoicesPayReasonPaymentInProgress:
		code, detail = codePaymentInProgress, "A Vipps payment of this invoice is being completed."
	case gen.InvoicesPayReasonAmountOutOfRange:
		code, detail = codeAmountOutOfRange, "Vipps takes payments of 1.00 to 650 000.00; the open amount is outside them."
	case gen.InvoicesPayReasonRevoked:
		code, detail = codePayLinksUnavailable, "This pay link no longer pays the invoice."
	default:
		code, detail = codePayLinksUnavailable, "Vipps payments are not available for this invoice now."
	}
	c := conflict(code, cannotPayTitle, detail)
	c.Reason = &reason
	return c
}

// attemptInput is what an attempt is made of: the invoice, its flow, the pay
// link and the return nonce (pay_link), or the user on site (on_site).
type attemptInput struct {
	invoiceID int64
	flow      string
	payLinkID *int64
	// nonce is the return address's nonce, in clear; only its hash is stored.
	nonce     string
	createdBy *uuid.UUID
}

// createAttempt makes one attempt: one locked transaction — the invoice, the
// facts judged again, the credentials FOR SHARE and their version compared,
// the cap, the insert as creating with amount the open amount — then, after
// the commit and outside any lock, Provider.Create, then the answer recorded
// under the attempt's lock. It answers the attempt and what Vipps created, or
// a refusal (409, 503 or 502 by its status); errDocumentGone when the invoice
// is gone.
func (s *server) createAttempt(ctx context.Context, in attemptInput, now time.Time) (store.InvoicesPaymentAttempt, payments.Created, *gen.InvoicesConflictProblem, error) {
	provider, creds, err := s.paymentProvider(ctx)
	if err != nil {
		if problem, ok := paymentsUnavailable(err); ok {
			return store.InvoicesPaymentAttempt{}, payments.Created{}, &problem, nil
		}
		return store.InvoicesPaymentAttempt{}, payments.Created{}, nil, err
	}
	var refusal *gen.InvoicesConflictProblem
	var attempt store.InvoicesPaymentAttempt
	var inv store.InvoicesInvoice
	var settings store.InvoicesPaymentSetting
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if inv, err = lockInvoice(ctx, txq, in.invoiceID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errDocumentGone
			}
			return fmt.Errorf("invoices: lock document %d: %w", in.invoiceID, err)
		}
		if in.flow == flowOnSite {
			// D17's hard rules, judged again under the invoice's lock
			// (onsite.go), before the facts every flow shares.
			onSite, err := s.onSiteRefusal(ctx, txq, inv)
			if err != nil {
				return err
			}
			if onSite != nil {
				refusal = onSite
				return errRefused
			}
		}
		var link *store.InvoicesPayLink
		if in.payLinkID != nil {
			link = &store.InvoicesPayLink{ID: *in.payLinkID}
		}
		facts, err := s.payFactsOf(ctx, txq, inv, link, in.flow)
		if err != nil {
			return err
		}
		if ok, reason := payableOf(facts); !ok {
			refusal = ptr(payRefusal(reason))
			return errRefused
		}
		live, err := txq.LiveAttemptsOfInvoice(ctx, store.LiveAttemptsOfInvoiceParams{InvoiceID: inv.ID, PayLinkID: in.payLinkID, Now: now})
		if err != nil {
			return fmt.Errorf("invoices: count document %d's live attempts: %w", inv.ID, err)
		}
		if live >= maxLiveAttempts {
			refusal = ptr(conflict(codeTooManyAttempts, cannotPayTitle,
				"Five Vipps payments of this invoice are in progress already. Try again later."))
			return errRefused
		}
		row, err := sharePaymentCredentials(ctx, txq)
		if errors.Is(err, pgx.ErrNoRows) {
			problem, _ := paymentsUnavailable(errNoPaymentCredentials)
			refusal = &problem
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: share the Vipps credentials: %w", err)
		}
		if row.RejectedAt != nil {
			c := conflict(codePaymentsUnavailable, paymentsUnavailableTitle, "Vipps refused the stored credentials.")
			c.Status = ptr(int32(http.StatusServiceUnavailable))
			refusal = &c
			return errRefused
		}
		if !row.UpdatedAt.Equal(creds.version) {
			c := conflict(codePaymentsUnavailable, paymentsUnavailableTitle,
				"The Vipps credentials were changed while this payment was being made. Try again.")
			c.Status = ptr(int32(http.StatusServiceUnavailable))
			refusal = &c
			return errRefused
		}
		vs, err := vippsSettingsOf(row)
		if err != nil {
			return err
		}
		if hook := attemptAfterInvoiceLock; hook != nil {
			hook(ctx, tx)
		}
		if settings, err = txq.GetPaymentSettings(ctx); err != nil {
			return fmt.Errorf("invoices: read the payment settings: %w", err)
		}
		amount, err := numericFromRat(facts.open, 2)
		if err != nil {
			return err
		}
		params := store.InsertPaymentAttemptParams{
			InvoiceID: inv.ID, Msn: vs.MSN, Flow: in.flow, PayLinkID: in.payLinkID, Amount: amount,
			CreateKey: ptr(uuid.New()), CancelKey: uuid.New(), Now: now, CreatedByUserID: in.createdBy,
			ExpiresAt: now.Add(attemptLifetime), NextPollAt: now.Add(attemptFirstPoll),
		}
		if in.flow == flowPayLink {
			params.ReturnNonceSha256 = ptr(tokenSHA256(in.nonce))
		}
		if attempt, err = txq.InsertPaymentAttempt(ctx, params); err != nil {
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" && pgErr.ConstraintName == "ux_payment_attempts_on_site_live" {
				// The index is the rule's last word; under the invoice's
				// lock onSiteRefusal has already said it.
				refusal = ptr(conflict(codePaymentRequestActive, cannotOnSiteTitle, "A payment request of this invoice is in progress already."))
				return errRefused
			}
			return fmt.Errorf("invoices: insert document %d's payment attempt: %w", inv.ID, err)
		}
		return nil
	})
	switch {
	case refusal != nil:
		return attempt, payments.Created{}, refusal, nil
	case err != nil:
		return attempt, payments.Created{}, nil, err
	}

	created, err := s.callCreate(ctx, provider, attempt, inv, settings, in)
	if err == nil {
		return attempt, created, nil, s.recordCreated(ctx, attempt.ID, created, now)
	}
	detail := "Vipps did not answer; the payment may still appear. Try again in a minute."
	if payments.IsDefinitiveCreateRefusal(err) {
		detail = "Vipps refused the payment."
		if rerr := s.recordFailed(ctx, attempt.ID, err, now); rerr != nil {
			return attempt, payments.Created{}, nil, rerr
		}
	} else {
		s.deps.Logger.WarnContext(ctx, "invoices: Vipps' answer to a payment's create is unknown; the payments worker reads it back",
			"attempt_id", attempt.ID, "error", err.Error())
	}
	c := conflict(codeProviderFailed, providerFailedTitle, detail)
	c.Status = ptr(int32(http.StatusBadGateway))
	return attempt, payments.Created{}, &c, nil
}

// callCreate asks the provider for the attempt's payment, outside any lock.
func (s *server) callCreate(ctx context.Context, provider payments.Provider, attempt store.InvoicesPaymentAttempt,
	inv store.InvoicesInvoice, settings store.InvoicesPaymentSetting, in attemptInput) (payments.Created, error) {
	amount, err := ratFromNumeric(attempt.Amount)
	if err != nil {
		return payments.Created{}, err
	}
	minor, err := minorOf(amount)
	if err != nil {
		return payments.Created{}, err
	}
	req := payments.CreateRequest{
		Reference: attempt.Reference, IdempotencyKey: attempt.CreateKey.String(),
		Description: fmt.Sprintf("Faktura %d", deref64(inv.Number)), Amount: payments.NOK(minor),
		Flow: payments.FlowQR, CustomerPresent: true,
	}
	if settings.TermsUrl != nil {
		req.TermsURL = *settings.TermsUrl
	}
	if settings.PrivacyUrl != nil {
		req.PrivacyURL = *settings.PrivacyUrl
	}
	if in.flow == flowPayLink {
		req.Flow, req.CustomerPresent = payments.FlowRedirect, false
		req.ReturnURL = s.deps.Config.PublicBaseURL + "/pay?attempt=" + attempt.Reference + "&nonce=" + in.nonce
	}
	return provider.Create(ctx, req)
}

// recordCreated records Vipps' answer under the attempt's lock, on a context
// the request's cancellation does not reach: the payment exists at Vipps.
func (s *server) recordCreated(ctx context.Context, id int64, created payments.Created, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := lockPaymentAttempt(ctx, txq, id); err != nil {
			return fmt.Errorf("invoices: lock payment attempt %d: %w", id, err)
		}
		_, err := txq.MarkAttemptCreated(ctx, store.MarkAttemptCreatedParams{
			ID: id, RedirectUrl: ptr(created.RedirectURL), NextPollAt: now.Add(attemptFirstPoll),
		})
		if err != nil {
			return fmt.Errorf("invoices: record payment attempt %d as created: %w", id, err)
		}
		return nil
	})
}

// recordFailed records Vipps' definitive refusal under the attempt's lock.
func (s *server) recordFailed(ctx context.Context, id int64, cause error, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := lockPaymentAttempt(ctx, txq, id); err != nil {
			return fmt.Errorf("invoices: lock payment attempt %d: %w", id, err)
		}
		if _, err := txq.MarkAttemptFailed(ctx, store.MarkAttemptFailedParams{ID: id, Now: now, LastError: &msg}); err != nil {
			return fmt.Errorf("invoices: record payment attempt %d as failed: %w", id, err)
		}
		return nil
	})
}

// paymentAttemptWire is an attempt as the wire answers it — never the
// address the payer is sent to.
func paymentAttemptWire(a store.InvoicesPaymentAttempt) (gen.InvoicesPaymentAttempt, error) {
	optional := func(n pgtype.Numeric) (*float64, error) {
		if !n.Valid {
			return nil, nil
		}
		r, err := ratFromNumeric(n)
		if err != nil {
			return nil, err
		}
		return ptr(floatFromRat(r, 2)), nil
	}
	amount, err := ratFromNumeric(a.Amount)
	if err != nil {
		return gen.InvoicesPaymentAttempt{}, err
	}
	w := gen.InvoicesPaymentAttempt{
		Id: a.ID, Reference: a.Reference, Flow: gen.InvoicesPaymentAttemptFlow(a.Flow),
		State: gen.InvoicesPaymentAttemptState(a.State), Amount: floatFromRat(amount, 2),
		ReleasePending: a.ReleasePending, CreatedAt: a.CreatedAt, CreatedByUserId: a.CreatedByUserID,
		ExpiresAt: a.ExpiresAt, AuthorizedAt: a.AuthorizedAt, CapturedAt: a.CapturedAt, FinishedAt: a.FinishedAt,
		AbandonedAt: a.AbandonedAt, AbandonedByUserId: a.AbandonedByUserID, AbandonReason: a.AbandonReason,
	}
	for _, f := range []struct {
		n   pgtype.Numeric
		out **float64
	}{
		{a.AuthorizedAmount, &w.AuthorizedAmount}, {a.CapturedAmount, &w.CapturedAmount},
		{a.CancelledAmount, &w.CancelledAmount}, {a.RefundedAmount, &w.RefundedAmount},
		{a.ReservedAmount, &w.ReservedAmount},
	} {
		if *f.out, err = optional(f.n); err != nil {
			return gen.InvoicesPaymentAttempt{}, err
		}
	}
	return w, nil
}

// deref64 is *p, or 0.
func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// withPaymentAttempts answers an issued invoice's attempts, oldest first —
// never the address the payer is sent to.
func withPaymentAttempts(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, resp *gen.InvoicesInvoiceResponse) error {
	rows, err := q.PaymentAttemptsOf(ctx, inv.ID)
	if err != nil {
		return fmt.Errorf("invoices: read document %d's payment attempts: %w", inv.ID, err)
	}
	out := make([]gen.InvoicesPaymentAttempt, 0, len(rows))
	for _, a := range rows {
		w, err := paymentAttemptWire(a)
		if err != nil {
			return err
		}
		out = append(out, w)
	}
	resp.PaymentAttempts = &out
	return nil
}

const (
	invalidAbandon         = "Invalid abandon"
	cannotAbandonTitle     = "The Vipps payment cannot be abandoned"
	abandonLease           = 2 * time.Minute
	maxAbandonReasonLength = 500
)

// abandonable is an attempt an abandon may take as read on the pool: a
// capture of unknown outcome, a capture in progress (stranded only, judged
// later), or a create the worker gave up on.
func abandonable(a store.InvoicesPaymentAttempt) bool {
	switch a.State {
	case "capturing", "capture_unknown":
		return true
	case "creating", "created":
		return a.NextPollAt == nil
	}
	return false
}

// PostInvoicesPaymentAttemptsByIdAbandon Abandon a stuck Vipps payment
// attempt (POST /api/v1/invoices/payment-attempts/{id}/abandon).
//
// Abandon must never lose money (D16, B5): wherever Vipps can be read for
// the attempt it is read first, outside any lock, under a lease of the
// abandon's own that keeps every worker claim off the attempt until the
// write — money captured is registered as the worker registers it, a payment
// still capturable is refused, a create Vipps knows is handed back to the
// worker. Where Vipps cannot be read — switched off, or the attempt made
// under another sales unit — only a capture never sent is abandoned. The
// write takes the attempt alone (D18).
func (s *server) PostInvoicesPaymentAttemptsByIdAbandon(ctx context.Context, req gen.PostInvoicesPaymentAttemptsByIdAbandonRequestObject) (gen.PostInvoicesPaymentAttemptsByIdAbandonResponseObject, error) {
	reason := strings.TrimSpace(req.Body.Reason)
	switch {
	case !req.Body.ConfirmNotCaptured:
		return gen.PostInvoicesPaymentAttemptsByIdAbandon400ApplicationProblemPlusJSONResponse(invalid(invalidAbandon,
			fieldError("confirmNotCaptured", "Confirm that you checked in the Vipps portal that nothing was captured"))), nil
	case reason == "":
		return gen.PostInvoicesPaymentAttemptsByIdAbandon400ApplicationProblemPlusJSONResponse(invalid(invalidAbandon,
			fieldError("reason", "An abandon needs a reason"))), nil
	case maxLength("The reason", reason, maxAbandonReasonLength) != "":
		return gen.PostInvoicesPaymentAttemptsByIdAbandon400ApplicationProblemPlusJSONResponse(invalid(invalidAbandon,
			fieldError("reason", maxLength("The reason", reason, maxAbandonReasonLength)))), nil
	}
	now := s.deps.Clock().UTC() // the request's one clock read (D18)
	a, err := store.New(s.deps.Pool).GetPaymentAttempt(ctx, req.Id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PostInvoicesPaymentAttemptsByIdAbandon404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("invoices: read payment attempt %d: %w", req.Id, err)
	}
	if hook := abandonAfterRow; hook != nil {
		hook()
	}
	refuse := func(detail string) (gen.PostInvoicesPaymentAttemptsByIdAbandonResponseObject, error) {
		return gen.PostInvoicesPaymentAttemptsByIdAbandon409ApplicationProblemPlusJSONResponse(
			conflict(codeAttemptNotAbandonable, cannotAbandonTitle, detail)), nil
	}
	if a.ReleasePending && a.NextPollAt == nil {
		// A release Vipps refused for good: never abandoned, but handed
		// back to the worker once the person released the rest in the portal.
		n, err := store.New(s.deps.Pool).UnparkAttempt(ctx, store.UnparkAttemptParams{Now: now, ID: a.ID})
		if err != nil {
			return nil, fmt.Errorf("invoices: hand payment attempt %d back: %w", a.ID, err)
		}
		if n == 1 {
			return refuse("The payment is registered; only the release of its uncaptured rest was stuck. It is handed back to the payments worker, which reads Vipps again now.")
		}
	}
	if !abandonable(a) {
		return refuse(fmt.Sprintf("The payment is %s. Only a payment whose capture's outcome is unknown, one the payments worker cannot complete, or a request it gave up on is abandoned.", a.State))
	}

	provider, creds, perr := s.paymentProvider(ctx)
	if perr != nil {
		if _, known := paymentsUnavailable(perr); !known {
			return nil, perr
		}
	}
	if perr != nil || creds.msn != a.Msn {
		// Stranded: Vipps cannot be read for this attempt. Only a capture
		// never sent can have moved no money.
		if a.State == "creating" || a.State == "created" || a.CaptureAttempts > 0 {
			return refuse("Vipps cannot be read for this payment from this installation now, and it may have moved money. It can be abandoned once Vipps can be read again.")
		}
		return s.abandonAttempt(ctx, a, "", reason, now)
	}
	if a.State == "capturing" {
		return refuse("The payments worker is completing this payment; it is abandoned only once its outcome is unknown.")
	}

	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	n, err := store.New(s.deps.Pool).LeaseAttemptForAbandon(ctx, store.LeaseAttemptForAbandonParams{
		LeaseID: lease, LeaseUntil: now.Add(abandonLease), ID: a.ID, State: a.State, CaptureAttempts: a.CaptureAttempts, Now: now,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: lease payment attempt %d: %w", a.ID, err)
	}
	if n != 1 {
		return refuse("The payments worker is reading this payment right now. Read it again in a minute.")
	}
	a.LeaseID = &lease
	release := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := store.New(s.deps.Pool).ReleaseAttemptLease(ctx, store.ReleaseAttemptLeaseParams{ID: a.ID, LeaseID: lease}); err != nil {
			s.deps.Logger.WarnContext(ctx, "invoices: an abandon's lease could not be given back; it runs out by itself",
				"attempt_id", a.ID, "error", err.Error())
		}
	}

	p, err := provider.Get(ctx, a.Reference)
	if hook := abandonAfterRead; hook != nil {
		hook()
	}
	creating := a.State == "creating" || a.State == "created"
	switch {
	case creating && errors.Is(err, payments.ErrNotFound):
		return s.abandonAttempt(ctx, a, lease, reason, now)
	case err != nil:
		release()
		c := conflict(codeProviderFailed, "Vipps could not be read", "Vipps could not be read; nothing was changed. Try again in a minute.")
		c.Status = ptr(int32(http.StatusBadGateway))
		return gen.PostInvoicesPaymentAttemptsByIdAbandon502ApplicationProblemPlusJSONResponse(c), nil
	case creating && (p.Captured.Minor > 0 || p.State == payments.StateCreated || p.State == payments.StateAuthorized):
		n, err := store.New(s.deps.Pool).UnparkAttempt(ctx, store.UnparkAttemptParams{Now: now, ID: a.ID, LeaseID: lease})
		if err != nil || n != 1 {
			release()
		}
		if err != nil {
			return nil, fmt.Errorf("invoices: hand payment attempt %d back: %w", a.ID, err)
		}
		return refuse(fmt.Sprintf("Vipps knows this payment (%s) and it is not over. It is handed back to the payments worker, which completes it and registers anything captured.", p.State))
	case p.Captured.Minor > 0:
		w := &PaymentWorker{srv: s, deps: s.deps}
		c := &paymentClaim{s: s, w: w, row: a, lease: lease, now: now, provider: provider}
		err := c.register(ctx, p.Captured.Minor)
		captured := ratOfMinor(p.Captured.Minor).FloatString(2)
		switch {
		case errors.Is(err, errClaimLost):
			return refuse(fmt.Sprintf("Vipps shows %s captured: it is being registered as a payment of the invoice, not abandoned.", captured))
		case err != nil:
			return nil, err
		}
		return refuse(fmt.Sprintf("Vipps shows %s captured: it is registered as a payment of the invoice, not abandoned.", captured))
	case !creating && !endedAtVipps(p):
		release()
		return refuse("Vipps still holds this payment's authorization, and it may yet be captured. Release it in the Vipps portal first.")
	}
	return s.abandonAttempt(ctx, a, lease, reason, now)
}

// abandonAttempt is an abandon's write, the attempt alone locked (D18): the
// attempt still as the abandon judged it — its state and captures sent, and
// leased by this abandon (lease) or by no live claim (no lease) — abandoned.
func (s *server) abandonAttempt(ctx context.Context, read store.InvoicesPaymentAttempt, lease, reason string, now time.Time) (gen.PostInvoicesPaymentAttemptsByIdAbandonResponseObject, error) {
	by := callerID(ctx)
	var out store.InvoicesPaymentAttempt
	var refusal *gen.InvoicesConflictProblem
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		a, err := lockPaymentAttempt(ctx, txq, read.ID)
		if err != nil {
			return fmt.Errorf("invoices: lock payment attempt %d: %w", read.ID, err)
		}
		if hook := abandonAfterAttemptLock; hook != nil {
			hook(ctx, tx)
		}
		leased := a.LeaseID != nil && a.LeaseUntil != nil && a.LeaseUntil.After(now)
		if !abandonable(a) {
			// A create handed back to the worker since it was read parked.
			refusal = ptr(conflict(codeAttemptNotAbandonable, cannotAbandonTitle,
				"The payment moved while it was being abandoned. Read it again."))
			return errRefused
		}
		ours := lease != "" && a.LeaseID != nil && *a.LeaseID == lease
		if a.State != read.State || a.CaptureAttempts != read.CaptureAttempts || (lease != "" && !ours) || (lease == "" && leased) {
			refusal = ptr(conflict(codeAttemptNotAbandonable, cannotAbandonTitle,
				"The payment moved while it was being abandoned. Read it again."))
			return errRefused
		}
		if out, err = txq.AbandonPaymentAttempt(ctx, store.AbandonPaymentAttemptParams{
			AbandonedByUserID: &by, Now: now, AbandonReason: reason, ID: a.ID,
		}); err != nil {
			return fmt.Errorf("invoices: abandon payment attempt %d: %w", a.ID, err)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesPaymentAttemptsByIdAbandon409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if err != nil {
		return nil, err
	}
	w, err := paymentAttemptWire(out)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesPaymentAttemptsByIdAbandon200JSONResponse(w), nil
}
