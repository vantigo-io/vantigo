package invoices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/payments"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-payments worker (invoices payments and reminders
// design D16): every Vipps payment attempt is carried by it from the pay
// page's or the counter's create to its end, one claim at a time under a row
// lease — the shape of the reminder worker's (reminder_worker.go), but
// draining every due attempt each cycle with no pacing: each attempt paces
// itself by next_poll_at, and Vipps' limits are per reference (R4 §4.5, m5).
//
// A claim is one statement (ClaimPaymentAttempt, FOR NO KEY UPDATE SKIP
// LOCKED, outside the lock helpers, m11) and one clock read; the credentials
// are read and opened on the pool, and an attempt made under another sales
// unit is left alone. Then one step of the machine, every provider call
// outside any transaction, every write under the attempt's lock naming the
// claim's lease:
//
//   - creating, created: Get. CREATED is read again in 2 s, and past the
//     request's ten minutes every minute — an attempt is never expired
//     locally, only by Vipps' own ABORTED, EXPIRED or TERMINATED. A 404 for
//     a create whose outcome was never learned is no proof nothing was made:
//     past the ten minutes the create is sent again under its stored key, so
//     a payment that exists conflicts and one that does not is made — and
//     ends at Vipps; after ten such tries in a row the attempt is parked for
//     a person. AUTHORIZED → authorized, due at once.
//   - authorized: the capture decision, under the attempt's lock and then the
//     invoice's: capture = min(authorized, open), open counting every other
//     live reservation. Above zero, capturing with the amount reserved and a
//     capture key; else cancelling. No call is made inside it.
//   - capturing, capture_unknown: Get first — anything captured is
//     registered. Only Vipps' own read ends it: ABORTED, EXPIRED,
//     TERMINATED or a released authorization with nothing captured is a
//     capture failure. Our clock passing captureGuaranteedUntil is not:
//     with no capture sent the capture is sent, with one sent the attempt
//     waits as an unknown outcome. Else, in one transaction, the capture is
//     counted with its key
//     stored — while no capture was sent yet the decision is taken again
//     under the attempt's and the invoice's locks, and a credit note or a
//     payment landed since lowers the reservation under a new key or cancels
//     it (m3) — and then Capture, outside it. Captured → registered.
//     ErrInsufficientFunds (6260) ends it after three 6260 answers, counted
//     on their own, and ErrCaptureFailed (6280) is tried until the capture's
//     guarantee or 30 days after the authorization, then capture_failed.
//     Every other error is an unknown outcome: retried with the same key,
//     backing off to ten minutes by the failures in a row, and
//     after seven days capture_unknown — the reservation kept, read hourly
//     to 180 days after the authorization, then daily.
//   - the registration: one transaction, the attempt and then the invoice,
//     the payment inserted (payment_attempt_id is the exactly-once guard)
//     and the attempt captured with its reservation released in the same
//     commit, dated by the capture event's Oslo day. A registration that
//     fails keeps the state and the reservation, records why in last_error
//     and is tried again on the backoff.
//   - cancelling, a release pending: money Vipps shows captured on an attempt
//     not yet captured — a failed capture that landed after all, a capture
//     made in the portal — is registered, never closed as a release (00046
//     lets capture_failed become captured). Otherwise Cancel under the
//     cancel key until Vipps has released the rest. A release Vipps refuses for good while its read
//     shows an amount still unreleased is parked — next_poll_at NULL, never
//     polled again, still counted live against a change of the credentials —
//     for a person to settle in the Vipps portal.
//   - captured: the refund watch (the spec's "gap"): read daily to
//     refund_watch_until, a refund Vipps shows above the recorded one stored
//     — a refund made in the Vipps portal, which raises vippsRefundSeen until
//     the person removes the payment. A read that fails changes nothing but
//     when it is read again; no payment is ever removed by the watch.

const (
	// paymentWorkerName is what the runner logs this worker as.
	paymentWorkerName = "invoices-payments"
	// paymentWorkerInterval is the poll cadence between cycles.
	paymentWorkerInterval = time.Second
	// paymentWorkerDefaultLease is how long a claim keeps an attempt from
	// every other worker: longer than its calls' timeouts together.
	paymentWorkerDefaultLease = 2 * time.Minute

	// createdPoll is how often a request the payer has not answered is read
	// (R4 §4.5); expiredPoll once its ten minutes are over.
	createdPoll = 2 * time.Second
	expiredPoll = time.Minute
	// providerUnavailableDelay is how long an attempt waits when no provider
	// can be built for it, or it was made under another sales unit.
	providerUnavailableDelay = time.Hour
	// unknownAfter is how long a capture's outcome may stay unknown before
	// the attempt is capture_unknown; unknownPoll and lateUnknownPoll are
	// its cadence then, the first until captureLimit after the authorization
	// (Vipps' capture limit, R4 §4.4).
	unknownAfter    = 7 * 24 * time.Hour
	unknownPoll     = time.Hour
	lateUnknownPoll = 24 * time.Hour
	captureLimit    = 180 * 24 * time.Hour
	// captureFailureCutoff is how long after the authorization a capture
	// Vipps fails (6260, 6280) is tried again at most; insufficientTries how
	// many times a 6260 is.
	captureFailureCutoff = 30 * 24 * time.Hour
	insufficientTries    = 3
	// recreateTries is how many times in a row a create is sent again
	// before the attempt is parked for a person.
	recreateTries = 10
	// refundWatch is how long a captured attempt is watched for a refund
	// made in the Vipps portal, and watchPoll its cadence.
	refundWatch = 30 * 24 * time.Hour
	watchPoll   = 24 * time.Hour
	// maxPaymentBackoff caps the wait after a failed call.
	maxPaymentBackoff = 10 * time.Minute
)

// paymentWorkerLease is the lease a claim takes. A test shortens it
// (export_test.go).
var paymentWorkerLease = paymentWorkerDefaultLease

// captureAfterAttemptLock, when a test sets it (export_test.go), runs inside
// the capture decision's and the pre-capture transaction right after the
// attempt's lock, before the invoice's; nil in production.
var captureAfterAttemptLock func(ctx context.Context, tx pgx.Tx)

// registerAfterInvoiceLock, when a test sets it (export_test.go), runs
// inside the registration's transaction right after the invoice's lock,
// before the payment is inserted; nil in production.
var registerAfterInvoiceLock func(ctx context.Context, tx pgx.Tx)

// errClaimLost is a write that found the attempt no longer this claim's —
// abandoned, or claimed again after the lease ran out: the claim ends.
var errClaimLost = errors.New("invoices: the payment attempt moved under the claim")

// paymentBackoff is the wait after the n-th failed call: 2^n seconds, at
// most ten minutes (D16).
func paymentBackoff(n int32) time.Duration {
	if n < 1 {
		n = 1
	}
	if n >= 10 {
		return maxPaymentBackoff
	}
	return min(maxPaymentBackoff, time.Duration(1<<n)*time.Second)
}

// PaymentWorker carries Vipps payment attempts to their end. It implements
// worker.Worker; Module.Workers registers it only while
// INVOICES_VIPPS_ENABLED is on.
type PaymentWorker struct {
	srv *server
	// srvErr is newServer's failure, kept so the worker still starts and
	// every cycle says why it does nothing.
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*PaymentWorker)(nil)

// NewPaymentWorker builds the worker over worker mode's Deps: the pool, the
// clock, the configuration and the HTTP transport the provider dials.
func NewPaymentWorker(d module.Deps) *PaymentWorker {
	srv, err := newServer(d)
	return &PaymentWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *PaymentWorker) Name() string { return paymentWorkerName }

// Interval is the poll cadence between cycles.
func (w *PaymentWorker) Interval() time.Duration { return paymentWorkerInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done. A
// failing cycle is logged and the loop goes on.
func (w *PaymentWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(paymentWorkerInterval)
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: a payments worker cycle failed", "worker", paymentWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle handles every attempt due, one claim after another with no
// pause, until none is due (m5).
func (w *PaymentWorker) RunCycle(ctx context.Context) error {
	for ctx.Err() == nil {
		processed, err := w.ProcessOne(ctx)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

// ProcessOne claims at most one due attempt and takes it one step, answering
// whether an attempt was claimed.
func (w *PaymentWorker) ProcessOne(ctx context.Context) (bool, error) {
	if w.srvErr != nil {
		return false, w.srvErr
	}
	now := w.deps.Clock().UTC() // the claim's one clock read (D18)
	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	row, err := store.New(w.deps.Pool).ClaimPaymentAttempt(ctx, store.ClaimPaymentAttemptParams{
		LeaseID: lease, LeaseUntil: now.Add(paymentWorkerLease), Now: now,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: claim a payment attempt: %w", err)
	}
	c := &paymentClaim{s: w.srv, w: w, row: row, lease: lease, now: now}
	if err := c.step(ctx); err != nil && !errors.Is(err, errClaimLost) {
		return true, err
	}
	return true, nil
}

func (w *PaymentWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

// paymentClaim is one claimed attempt: the row as the claim saw it, its
// lease and the claim's time.
type paymentClaim struct {
	s        *server
	w        *PaymentWorker
	row      store.InvoicesPaymentAttempt
	lease    string
	now      time.Time
	provider payments.Provider
}

// step takes the claimed attempt one step of D16's machine.
func (c *paymentClaim) step(ctx context.Context) error {
	provider, creds, err := c.s.paymentProvider(ctx)
	if c.row.State == "captured" && !c.row.ReleasePending && (err != nil || c.row.Msn != creds.msn) &&
		(c.row.RefundWatchUntil == nil || !c.now.Before(*c.row.RefundWatchUntil)) {
		// The refund watch is over and Vipps cannot be read for it — the
		// keys removed or another sales unit stored: it ends unread rather
		// than being rescheduled for ever. With Vipps readable, watch()
		// makes its last read.
		c.w.logger().WarnContext(ctx, "invoices: the refund watch ends unread; Vipps cannot be read for this payment from this installation",
			"worker", paymentWorkerName, "attempt_id", c.row.ID)
		return c.reschedule(ctx, time.Time{}, "")
	}
	if err != nil {
		reason := "Vipps cannot be reached from this installation: " + err.Error()
		if rerr := c.reschedule(ctx, c.now.Add(providerUnavailableDelay), reason); rerr != nil {
			return rerr
		}
		if _, known := paymentsUnavailable(err); known {
			return nil
		}
		return err
	}
	if c.row.Msn != creds.msn {
		// Never sent to another sales unit (I20): the attempt and its
		// reservation stay as they are for a person to abandon.
		return c.reschedule(ctx, c.now.Add(providerUnavailableDelay),
			fmt.Sprintf("Made under the sales unit %s, which is no longer the stored one.", c.row.Msn))
	}
	c.provider = provider
	switch {
	case c.row.ReleasePending:
		return c.release(ctx, false)
	case c.row.State == "creating" || c.row.State == "created":
		return c.poll(ctx)
	case c.row.State == "authorized":
		return c.decide(ctx)
	case c.row.State == "capturing" || c.row.State == "capture_unknown":
		return c.capture(ctx)
	case c.row.State == "cancelling":
		return c.release(ctx, true)
	case c.row.State == "captured":
		return c.watch(ctx)
	}
	return c.reschedule(ctx, time.Time{}, "")
}

// attemptTx runs fn in one locked transaction: the attempt FOR NO KEY
// UPDATE, still this claim's; with withInvoice, then its invoice FOR
// UPDATE (D18: own row first). afterAttempt and afterInvoice are the test
// seams of the decision and the registration.
func (c *paymentClaim) attemptTx(ctx context.Context, withInvoice bool, afterAttempt, afterInvoice func(context.Context, pgx.Tx),
	fn func(ctx context.Context, txq *store.Queries, a store.InvoicesPaymentAttempt, inv store.InvoicesInvoice) error) error {
	return c.s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		a, err := lockPaymentAttempt(ctx, txq, c.row.ID)
		if err != nil {
			return fmt.Errorf("invoices: lock payment attempt %d: %w", c.row.ID, err)
		}
		if a.LeaseID == nil || *a.LeaseID != c.lease {
			return errClaimLost
		}
		if afterAttempt != nil {
			afterAttempt(ctx, tx)
		}
		var inv store.InvoicesInvoice
		if withInvoice {
			if inv, err = lockInvoice(ctx, txq, a.InvoiceID); err != nil {
				return fmt.Errorf("invoices: lock document %d: %w", a.InvoiceID, err)
			}
			if afterInvoice != nil {
				afterInvoice(ctx, tx)
			}
		}
		return fn(ctx, txq, a, inv)
	})
}

// one is a write's row count as the claim expects it: one row, or the
// attempt moved under the claim.
func one(n int64, err error, what string, id int64) error {
	if err != nil {
		return fmt.Errorf("invoices: %s payment attempt %d: %w", what, id, err)
	}
	if n != 1 {
		return errClaimLost
	}
	return nil
}

// lastError is err as the 500 characters the attempt keeps.
func lastError(msg string) *string {
	if msg == "" {
		return nil
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return &msg
}

// reschedule ends a claim that learned nothing new and failed nothing:
// read again at next (zero parks the attempt), reason recorded ("" clears
// it), the failures in a row cleared.
func (c *paymentClaim) reschedule(ctx context.Context, next time.Time, reason string) error {
	return c.rescheduleAs(ctx, next, reason, false, 0)
}

// retry ends a claim whose call failed: one more failure in a row, read
// again after the backoff of that many, or after wait — a throttle's
// Retry-After — when longer, but never beyond maxPaymentBackoff: no header
// may hold a reservation unread for longer than the backoff would.
func (c *paymentClaim) retry(ctx context.Context, reason string, wait time.Duration) error {
	return c.rescheduleAs(ctx, c.now.Add(min(maxPaymentBackoff, max(wait, paymentBackoff(c.row.Failures+1)))), reason, true, 0)
}

// rescheduleAs is reschedule and retry's write: failed counts a failure in a
// row, insufficient adds 6260 answers.
func (c *paymentClaim) rescheduleAs(ctx context.Context, next time.Time, reason string, failed bool, insufficient int32) error {
	var at *time.Time
	if !next.IsZero() {
		at = &next
	}
	return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, _ store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
		n, err := txq.ReschedulePaymentAttempt(ctx, store.ReschedulePaymentAttemptParams{
			NextPollAt: at, LastError: lastError(reason), Failed: failed, InsufficientFunds: insufficient,
			ID: c.row.ID, LeaseID: c.lease,
		})
		return one(n, err, "reschedule", c.row.ID)
	})
}

// poll reads a request not yet authorized.
func (c *paymentClaim) poll(ctx context.Context) error {
	p, err := c.provider.Get(ctx, c.row.Reference)
	expired := !c.now.Before(c.row.ExpiresAt)
	switch {
	case err == nil:
	case errors.Is(err, payments.ErrNotFound) && c.row.State == "creating" && expired:
		return c.recreate(ctx)
	case errors.Is(err, payments.ErrNotFound) && !expired:
		return c.reschedule(ctx, c.now.Add(createdPoll), "Vipps does not know the payment yet.")
	default:
		return c.retry(ctx, err.Error(), 0)
	}
	switch p.State {
	case payments.StateAuthorized:
		if p.Authorized.Minor <= 0 {
			return c.retry(ctx, "Vipps answered AUTHORIZED with nothing authorized.", 0)
		}
		amount, err := numericFromRat(ratOfMinor(p.Authorized.Minor), 2)
		if err != nil {
			return err
		}
		return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, _ store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
			n, err := txq.AuthorizeAttempt(ctx, store.AuthorizeAttemptParams{
				AuthorizedAmount: amount, Now: c.now, PspReference: nonEmpty(p.PSPReference),
				CaptureGuaranteedUntil: p.CaptureGuaranteedUntil, ID: c.row.ID, LeaseID: c.lease,
			})
			return one(n, err, "authorize", c.row.ID)
		})
	case payments.StateAborted, payments.StateExpired, payments.StateTerminated:
		return c.end(ctx, strings.ToLower(string(p.State)), nonEmpty(p.PSPReference), "")
	}
	// CREATED: the payer has not answered. Past its ten minutes Vipps ends
	// it on its own; it is read until it does.
	if expired {
		return c.reschedule(ctx, c.now.Add(expiredPoll), "")
	}
	return c.reschedule(ctx, c.now.Add(createdPoll), "")
}

// end records Vipps' own end of a request never authorized.
func (c *paymentClaim) end(ctx context.Context, state string, psp *string, reason string) error {
	return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, _ store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
		n, err := txq.EndUnauthorizedAttempt(ctx, store.EndUnauthorizedAttemptParams{
			State: state, PspReference: psp, Now: c.now, LastError: lastError(reason), ID: c.row.ID, LeaseID: c.lease,
		})
		return one(n, err, "end", c.row.ID)
	})
}

// recreate sends a create whose outcome was never learned again, under its
// stored key and reference (the review's decision 5): Vipps' 404 is no proof
// that nothing was made. A payment the first create made conflicts and is
// read on; one it never made is made now — the payer has long been told the
// create failed, so it is never paid and Vipps expires it. Only a refusal
// that names this request — never refused keys, which say nothing of the
// first create — fails the attempt.
func (c *paymentClaim) recreate(ctx context.Context) error {
	q := store.New(c.s.deps.Pool)
	inv, err := q.GetInvoice(ctx, c.row.InvoiceID)
	if err != nil {
		return fmt.Errorf("invoices: read document %d: %w", c.row.InvoiceID, err)
	}
	settings, err := q.GetPaymentSettings(ctx)
	if err != nil {
		return fmt.Errorf("invoices: read the payment settings: %w", err)
	}
	amount, err := ratFromNumeric(c.row.Amount)
	if err != nil {
		return err
	}
	minor, err := minorOf(amount)
	if err != nil {
		return err
	}
	if c.row.CreateKey == nil {
		return c.reschedule(ctx, c.now.Add(expiredPoll), "The attempt has no create key to send its create again under.")
	}
	req := payments.CreateRequest{
		Reference: c.row.Reference, IdempotencyKey: c.row.CreateKey.String(),
		Description: fmt.Sprintf("Faktura %d", deref64(inv.Number)), Amount: payments.NOK(minor),
		Flow: payments.FlowQR, CustomerPresent: true,
		TermsURL: deref(settings.TermsUrl), PrivacyURL: deref(settings.PrivacyUrl),
	}
	if c.row.Flow == flowPayLink {
		// The return nonce is kept only hashed: this return address carries
		// none, and no payer is sent to it.
		req.Flow, req.CustomerPresent = payments.FlowRedirect, false
		req.ReturnURL = c.s.deps.Config.PublicBaseURL + "/pay?attempt=" + c.row.Reference
	}
	created, err := c.provider.Create(ctx, req)
	switch {
	case err == nil:
		return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, _ store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
			n, err := txq.RecreatedAttempt(ctx, store.RecreatedAttemptParams{
				RedirectUrl: createdAddress(created), NextPollAt: c.now.Add(createdPoll), ID: c.row.ID, LeaseID: c.lease,
			})
			return one(n, err, "record the create of", c.row.ID)
		})
	case payments.IsDefinitiveCreateRefusal(err) && !errors.Is(err, payments.ErrUnauthorized):
		return c.end(ctx, "failed", nil, err.Error())
	}
	if c.row.Failures+1 >= recreateTries {
		c.w.logger().WarnContext(ctx, "invoices: a payment's create, sent again, keeps failing; the attempt is parked",
			"worker", paymentWorkerName, "attempt_id", c.row.ID, "error", err.Error())
		return c.rescheduleAs(ctx, time.Time{}, fmt.Sprintf(
			"The create was sent again %d times without an answer that settles it (%s). Look the reference up in the Vipps portal.",
			recreateTries, err.Error()), true, 0)
	}
	return c.retry(ctx, err.Error(), expiredPoll)
}

// decide is the capture decision (D16, m4): under the attempt's lock and
// then the invoice's, capture = min(authorized, open), open counting every
// other live reservation — this attempt holds none yet. No call is made.
func (c *paymentClaim) decide(ctx context.Context) error {
	return c.attemptTx(ctx, true, captureAfterAttemptLock, nil, func(ctx context.Context, txq *store.Queries, a store.InvoicesPaymentAttempt, inv store.InvoicesInvoice) error {
		authorized, err := ratFromNumeric(a.AuthorizedAmount)
		if err != nil {
			return err
		}
		open, err := openOf(ctx, txq, inv)
		if err != nil {
			return err
		}
		capture := minRat(authorized, open)
		if capture.Sign() <= 0 {
			n, err := txq.DecideAttemptCancel(ctx, store.DecideAttemptCancelParams{Now: c.now, ID: a.ID, LeaseID: c.lease})
			return one(n, err, "cancel", a.ID)
		}
		reserved, err := numericFromRat(capture, 2)
		if err != nil {
			return err
		}
		n, err := txq.DecideAttemptCapture(ctx, store.DecideAttemptCaptureParams{
			ReservedAmount: reserved, CaptureKey: ptr(uuid.New()), Now: c.now, ID: a.ID, LeaseID: c.lease,
		})
		return one(n, err, "reserve the capture of", a.ID)
	})
}

// minRat is the smaller of a and b, as a new value.
func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

// endedAtVipps reports whether Vipps' own read says p can never be
// captured: ended before authorization, or its authorization released, with
// nothing captured. Our clock passing captureGuaranteedUntil is no such
// answer: Vipps may still capture.
func endedAtVipps(p payments.Payment) bool {
	if p.Captured.Minor > 0 {
		return false
	}
	return p.State == payments.StateAborted || p.State == payments.StateExpired || p.State == payments.StateTerminated ||
		p.Cancelled.Minor > 0
}

// capture is a capturing or capture_unknown attempt's step: read, then
// count and capture, then register or judge the failure.
func (c *paymentClaim) capture(ctx context.Context) error {
	p, err := c.provider.Get(ctx, c.row.Reference)
	if err != nil {
		return c.unknown(ctx, err)
	}
	if p.Captured.Minor > 0 {
		return c.register(ctx, p.Captured.Minor)
	}
	if endedAtVipps(p) {
		return c.failCapture(ctx, "Vipps holds nothing captured, and has ended or released the payment.")
	}
	if c.row.CaptureAttempts > 0 && p.CaptureGuaranteedUntil != nil && !c.now.Before(*p.CaptureGuaranteedUntil) {
		// Past the guarantee with a capture sent: nothing is sent again, and
		// nothing is given up — the reservation stays until Vipps answers.
		return c.unknown(ctx, errors.New("invoices: past the capture guarantee, Vipps holds nothing captured yet"))
	}

	var amount *big.Rat
	var key uuid.UUID
	cancelled := false
	err = c.attemptTx(ctx, c.row.CaptureAttempts == 0, captureAfterAttemptLock, nil, func(ctx context.Context, txq *store.Queries, a store.InvoicesPaymentAttempt, inv store.InvoicesInvoice) error {
		reserved, err := ratFromNumeric(a.ReservedAmount)
		if err != nil {
			return err
		}
		if a.CaptureKey == nil {
			return fmt.Errorf("invoices: payment attempt %d is %s without a capture key", a.ID, a.State)
		}
		amount, key = reserved, *a.CaptureKey
		if a.CaptureAttempts == 0 && a.State == "capturing" && inv.ID == a.InvoiceID {
			// The re-decision (m3): no capture was sent, so the amount may
			// still follow what landed on the invoice since the decision.
			authorized, err := ratFromNumeric(a.AuthorizedAmount)
			if err != nil {
				return err
			}
			open, err := openOf(ctx, txq, inv)
			if err != nil {
				return err
			}
			capture := minRat(authorized, open.Add(open, reserved))
			if capture.Sign() <= 0 {
				cancelled = true
				n, err := txq.DecideAttemptCancel(ctx, store.DecideAttemptCancelParams{Now: c.now, ID: a.ID, LeaseID: c.lease})
				return one(n, err, "cancel", a.ID)
			}
			if capture.Cmp(reserved) < 0 {
				lowered, err := numericFromRat(capture, 2)
				if err != nil {
					return err
				}
				amount, key = capture, uuid.New()
				n, err := txq.RedecideAttemptCapture(ctx, store.RedecideAttemptCaptureParams{
					ReservedAmount: lowered, CaptureKey: &key, ID: a.ID, LeaseID: c.lease,
				})
				if err := one(n, err, "lower the reservation of", a.ID); err != nil {
					return err
				}
			}
		}
		n, err := txq.CountCaptureAttempt(ctx, store.CountCaptureAttemptParams{ID: a.ID, LeaseID: c.lease})
		return one(n, err, "count the capture of", a.ID)
	})
	if err != nil || cancelled {
		return err
	}
	minor, err := minorOf(amount)
	if err != nil {
		return err
	}
	p, err = c.provider.Capture(ctx, c.row.Reference, payments.NOK(minor), key.String())
	switch {
	case err == nil && p.Captured.Minor > 0:
		return c.register(ctx, p.Captured.Minor)
	case err == nil:
		return c.unknown(ctx, errors.New("invoices: Vipps answered the capture with nothing captured"))
	case payments.IsCaptureFailure(err):
		return c.captureFailure(ctx, err)
	}
	return c.unknown(ctx, err)
}

// captureFailure judges a capture Vipps failed (6260, 6280): nothing was
// taken and Vipps asks for a retry, so it is tried again until the capture's
// guarantee or 30 days after the authorization — after three 6260 answers
// at most, counted on their own — and then the attempt is capture_failed.
func (c *paymentClaim) captureFailure(ctx context.Context, cause error) error {
	cutoff := c.now
	if c.row.AuthorizedAt != nil {
		cutoff = c.row.AuthorizedAt.Add(captureFailureCutoff)
	}
	if g := c.row.CaptureGuaranteedUntil; g != nil && g.Before(cutoff) {
		cutoff = *g
	}
	var insufficient int32
	if errors.Is(cause, payments.ErrInsufficientFunds) {
		insufficient = 1
	}
	if !c.now.Before(cutoff) || (insufficient > 0 && c.row.InsufficientFunds+1 >= insufficientTries) {
		return c.failCaptureAs(ctx, cause.Error(), insufficient)
	}
	return c.rescheduleAs(ctx, c.now.Add(paymentBackoff(c.row.Failures+1)), cause.Error(), true, insufficient)
}

// failCapture records a definitive capture failure: the reservation
// released, the authorization's release pending.
func (c *paymentClaim) failCapture(ctx context.Context, reason string) error {
	return c.failCaptureAs(ctx, reason, 0)
}

// failCaptureAs is failCapture counting insufficient 6260 answers more.
func (c *paymentClaim) failCaptureAs(ctx context.Context, reason string, insufficient int32) error {
	return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, _ store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
		n, err := txq.FailAttemptCapture(ctx, store.FailAttemptCaptureParams{
			Now: c.now, LastError: lastError(reason), InsufficientFunds: insufficient, ID: c.row.ID, LeaseID: c.lease,
		})
		return one(n, err, "fail the capture of", c.row.ID)
	})
}

// unknown is a capture whose outcome the claim could not learn: retried
// with the same key on the backoff (a throttle's Retry-After honoured), and
// after seven days capture_unknown, its reservation kept (B5, I2).
func (c *paymentClaim) unknown(ctx context.Context, cause error) error {
	since := c.row.CreatedAt
	if c.row.AuthorizedAt != nil {
		since = *c.row.AuthorizedAt
	}
	if c.row.State == "capture_unknown" {
		next := c.now.Add(unknownPoll)
		if !c.now.Before(since.Add(captureLimit)) {
			next = c.now.Add(lateUnknownPoll)
		}
		return c.rescheduleAs(ctx, next, cause.Error(), true, 0)
	}
	if !c.now.Before(since.Add(unknownAfter)) {
		c.w.logger().WarnContext(ctx, "invoices: a Vipps capture's outcome is unknown after seven days; its reservation is kept",
			"worker", paymentWorkerName, "attempt_id", c.row.ID, "error", cause.Error())
		return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, _ store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
			n, err := txq.AttemptCaptureUnknown(ctx, store.AttemptCaptureUnknownParams{
				NextPollAt: c.now.Add(unknownPoll), LastError: lastError(cause.Error()), ID: c.row.ID, LeaseID: c.lease,
			})
			return one(n, err, "record the unknown capture of", c.row.ID)
		})
	}
	var wait time.Duration
	if throttled, ok := errors.AsType[*payments.ErrThrottled](cause); ok {
		wait = throttled.RetryAfter
	}
	return c.retry(ctx, cause.Error(), wait)
}

// register is the registration (D16): the capture event's Oslo day read
// outside any lock — the claim's day when the log cannot be read — then one
// transaction, the attempt and then the invoice, the payment inserted and
// the attempt captured, its reservation released in the same commit.
func (c *paymentClaim) register(ctx context.Context, capturedMinor int64) error {
	paidOn := businessDay(c.now)
	at, ok, err := c.provider.CaptureTime(ctx, c.row.Reference)
	switch {
	case err != nil:
		c.w.logger().WarnContext(ctx, "invoices: Vipps' event log could not be read; the payment is dated today",
			"worker", paymentWorkerName, "attempt_id", c.row.ID, "error", err.Error())
	case ok:
		paidOn = businessDay(at)
	}
	captured := ratOfMinor(capturedMinor)
	amount, err := numericFromRat(captured, 2)
	if err != nil {
		return err
	}
	err = c.attemptTx(ctx, true, nil, registerAfterInvoiceLock, func(ctx context.Context, txq *store.Queries, a store.InvoicesPaymentAttempt, inv store.InvoicesInvoice) error {
		switch a.State {
		case "capturing", "capture_unknown", "capture_failed", "cancelling":
		default:
			return errClaimLost
		}
		authorized, err := ratFromNumeric(a.AuthorizedAmount)
		if err != nil {
			return err
		}
		if _, err := txq.InsertVippsPayment(ctx, store.InsertVippsPaymentParams{
			InvoiceID: inv.ID, PaidOn: pgDate(paidOn), Amount: amount, Currency: inv.Currency,
			Reference: a.Reference, PaymentAttemptID: &a.ID, RegisteredAt: c.now,
		}); err != nil {
			return fmt.Errorf("invoices: register payment attempt %d's capture against %d: %w", a.ID, inv.ID, err)
		}
		// The rest is released unless a release already took it (a failed
		// capture's, before the money showed).
		releasePending := authorized.Cmp(captured) > 0 && !a.CancelledAmount.Valid
		next := c.now.Add(watchPoll)
		if releasePending {
			next = c.now
		}
		n, err := txq.RegisterAttemptCaptured(ctx, store.RegisterAttemptCapturedParams{
			CapturedAmount: amount, Now: c.now, ReleasePending: releasePending,
			RefundWatchUntil: c.now.Add(refundWatch), NextPollAt: next, ID: a.ID, LeaseID: c.lease,
		})
		return one(n, err, "record the capture of", a.ID)
	})
	if err != nil && !errors.Is(err, errClaimLost) {
		// The money is captured and not registered: the attempt keeps its
		// state and reservation, says why, and is tried again.
		c.w.logger().ErrorContext(ctx, "invoices: a Vipps capture could not be registered; it is tried again",
			"worker", paymentWorkerName, "attempt_id", c.row.ID, "error", err.Error())
		if rerr := c.registrationFailed(ctx, "The capture could not be registered: "+err.Error()); rerr != nil && !errors.Is(rerr, errClaimLost) {
			return errors.Join(err, rerr)
		}
	}
	return err
}

// registrationFailed ends a claim whose registration failed: the state and
// the reservation kept, why recorded, registration_failed_at set for
// attention, and the registration tried again on the backoff.
func (c *paymentClaim) registrationFailed(ctx context.Context, reason string) error {
	return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, _ store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
		n, err := txq.RegistrationFailedAttempt(ctx, store.RegistrationFailedAttemptParams{
			NextPollAt: c.now.Add(paymentBackoff(c.row.Failures + 1)), LastError: lastError(reason), Now: c.now,
			ID: c.row.ID, LeaseID: c.lease,
		})
		return one(n, err, "record the failed registration of", c.row.ID)
	})
}

// release releases what Vipps authorized and did not capture — a
// cancelling attempt's whole authorization, or a captured or failed
// attempt's rest — under the cancel key until Vipps has (an "already
// cancelled" answer is success). A cancelled amount of zero is taken only
// from Vipps' read; a release Vipps refuses for good while its read still
// shows an amount unreleased is parked (the review's decision 6).
func (c *paymentClaim) release(ctx context.Context, cancelling bool) error {
	// Money Vipps shows captured on an attempt not yet captured — a failed
	// capture that landed after all, a capture made in the portal — is
	// registered, never closed as a release.
	unregistered := c.row.State != "captured"
	if unregistered {
		read, err := c.provider.Get(ctx, c.row.Reference)
		if err != nil {
			return c.retry(ctx, err.Error(), 0)
		}
		if read.Captured.Minor > 0 {
			return c.register(ctx, read.Captured.Minor)
		}
	}
	p, err := c.provider.Cancel(ctx, c.row.Reference, c.row.CancelKey.String())
	if err == nil && unregistered && p.Captured.Minor > 0 {
		return c.register(ctx, p.Captured.Minor)
	}
	if err == nil && p.Cancelled.Minor > 0 {
		return c.released(ctx, cancelling, p.Cancelled.Minor)
	}
	refused, definitive := errors.AsType[*payments.ErrRefused](err)
	definitive = definitive && refused.Code != ""
	if err != nil && !definitive {
		return c.retry(ctx, err.Error(), 0)
	}
	read, gerr := c.provider.Get(ctx, c.row.Reference)
	if gerr != nil {
		return c.retry(ctx, gerr.Error(), 0)
	}
	if unregistered && read.Captured.Minor > 0 {
		return c.register(ctx, read.Captured.Minor)
	}
	if read.Cancelled.Minor > 0 || read.Captured.Minor+read.Cancelled.Minor >= read.Authorized.Minor || read.State != payments.StateAuthorized {
		return c.released(ctx, cancelling, read.Cancelled.Minor)
	}
	if definitive {
		c.w.logger().WarnContext(ctx, "invoices: Vipps refuses to release a payment's uncaptured rest; the attempt is parked",
			"worker", paymentWorkerName, "attempt_id", c.row.ID, "error", err.Error())
		return c.rescheduleAs(ctx, time.Time{},
			"Vipps refuses to release the uncaptured amount ("+err.Error()+"). Release it in the Vipps portal.", true, 0)
	}
	return c.retry(ctx, "Vipps released nothing yet.", 0)
}

// released records what Vipps released: a cancelling attempt cancelled, or
// a pending release ended — a captured attempt read on for its refund
// watch.
func (c *paymentClaim) released(ctx context.Context, cancelling bool, minor int64) error {
	amount, err := numericFromRat(ratOfMinor(minor), 2)
	if err != nil {
		return err
	}
	return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, a store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
		if cancelling {
			n, err := txq.AttemptCancelled(ctx, store.AttemptCancelledParams{CancelledAmount: amount, Now: c.now, ID: a.ID, LeaseID: c.lease})
			return one(n, err, "record the cancel of", a.ID)
		}
		var next *time.Time
		if a.State == "captured" && a.RefundWatchUntil != nil && c.now.Before(*a.RefundWatchUntil) {
			next = ptr(c.now.Add(watchPoll))
		}
		n, err := txq.AttemptReleased(ctx, store.AttemptReleasedParams{CancelledAmount: amount, NextPollAt: next, ID: a.ID, LeaseID: c.lease})
		return one(n, err, "record the release of", a.ID)
	})
}

// watch is a captured attempt's refund watch (D16, I6): Vipps read once a
// day until refund_watch_until — the last read on that day, then never — and
// a refund above the recorded one stored. A read that fails is tried again
// on the backoff and changes no money; past the watch it ends it.
func (c *paymentClaim) watch(ctx context.Context) error {
	if c.row.RefundWatchUntil == nil {
		return c.reschedule(ctx, time.Time{}, "")
	}
	over := !c.now.Before(*c.row.RefundWatchUntil)
	next := c.now.Add(watchPoll)
	if over {
		next = time.Time{}
	}
	p, err := c.provider.Get(ctx, c.row.Reference)
	if err != nil {
		if over {
			c.w.logger().WarnContext(ctx, "invoices: the refund watch ends on a read Vipps failed; a refund made since is not seen",
				"worker", paymentWorkerName, "attempt_id", c.row.ID, "error", err.Error())
			return c.rescheduleAs(ctx, time.Time{}, err.Error(), true, 0)
		}
		return c.retry(ctx, err.Error(), 0)
	}
	recorded := new(big.Rat)
	if c.row.RefundedAmount.Valid {
		if recorded, err = ratFromNumeric(c.row.RefundedAmount); err != nil {
			return err
		}
	}
	refunded := ratOfMinor(p.Refunded.Minor)
	if refunded.Cmp(recorded) <= 0 {
		return c.reschedule(ctx, next, "")
	}
	amount, err := numericFromRat(refunded, 2)
	if err != nil {
		return err
	}
	c.w.logger().WarnContext(ctx, "invoices: Vipps shows a captured payment refunded; its payment is left for a person",
		"worker", paymentWorkerName, "attempt_id", c.row.ID, "refunded", refunded.FloatString(2))
	var at *time.Time
	if !next.IsZero() {
		at = &next
	}
	return c.attemptTx(ctx, false, nil, nil, func(ctx context.Context, txq *store.Queries, _ store.InvoicesPaymentAttempt, _ store.InvoicesInvoice) error {
		n, err := txq.RecordAttemptRefund(ctx, store.RecordAttemptRefundParams{RefundedAmount: amount, NextPollAt: at, ID: c.row.ID, LeaseID: c.lease})
		return one(n, err, "record the refund of", c.row.ID)
	})
}
