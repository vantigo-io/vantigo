package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-efaktura worker (invoices phase 6 plan D6): the
// invoices-ehf worker's shape — claim one due row by a conditional UPDATE
// with a lease, make one provider call for it, and complete it with an
// UPDATE that names the lease and the status the claim saw. MPS has no event
// queue (reading 4), so the outcome is polled, on [BP]'s back-off. A cycle:
//
//   - every due eFaktura transmission: submit a queued one, poll a
//     submitted or unconfirmed one, or poll a delivered one whose AvtaleGiro
//     claim has not settled, until it has, then store the last answer as
//     its evidence (reading 3);
//   - every claim asked to cancel (reading 15): Cancel until 14:00 the day
//     before its due date, then back to accepted;
//   - once a day, under a lease on the settings row: drain the mandate
//     queue, and refresh the lookups that are due.
//
// Every time is Deps.Clock()'s. No call is made inside a transaction, and
// no connection is held across one: the claims are single statements, and
// each completion with its events is one short transaction.

const (
	efakturaWorkerName     = "invoices-efaktura"
	efakturaWorkerInterval = 30 * time.Second
	efakturaLease          = 60 * time.Second
	efakturaCallTimeout    = efakturaLease / 2
	// efakturaHousekeepingLease bounds the daily housekeeping's claim.
	// efakturaHousekeepingLease bounds the daily housekeeping's claim: the
	// most pages a drain reads, each a call of at most efakturaCallTimeout,
	// and the lookups after them, with room to spare.
	efakturaHousekeepingLease = efakturaMandatePages*efakturaCallTimeout + 20*time.Minute
	// efakturaHousekeepingEvery is how often the mandate queue is drained;
	// MPS asks for at least daily.
	efakturaHousekeepingEvery = 24 * time.Hour
	// efakturaMandatePages bounds one drain.
	efakturaMandatePages = 100
	// efakturaClaimPoll is how often a delivered eFaktura whose claim is
	// accepted is polled again, until its due date passes.
	efakturaClaimPoll = 6 * time.Hour
	// efakturaCancelRetry is the wait after TRY_LATER.
	efakturaCancelRetry = 15 * time.Minute

	efakturaMachineNote = "Resolved by the provider's status."
	// efakturaUnconfirmedProbing is how long an unconfirmed eFaktura is
	// asked about daily, after its queueing; then it waits for a person.
	efakturaUnconfirmedProbing = 37 * 24 * time.Hour
	// efakturaUnpaidAfter is how many business days past its due date an
	// accepted claim waits for its debit before it is unpaid.
	efakturaUnpaidAfter     = 5
	reasonNeverReceived     = "The network has no shipment by this id: it never received the eFaktura."
	reasonEfakturaCancelled = "The network cancelled the eFaktura."
)

// efakturaPollDelay is [BP]'s back-off by polls made: 1, 5, 15 and 30
// minutes, then hourly.
func efakturaPollDelay(polls int32) time.Duration {
	switch {
	case polls <= 0:
		return time.Minute
	case polls == 1:
		return 5 * time.Minute
	case polls == 2:
		return 15 * time.Minute
	case polls == 3:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}

// EfakturaWorker submits and follows eFaktura transmissions and AvtaleGiro
// claims, and syncs the mandates. Module.Workers registers it only when
// INVOICES_EFAKTURA_ENABLED is on.
type EfakturaWorker struct {
	srv    *server
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*EfakturaWorker)(nil)

// NewEfakturaWorker builds the worker over worker mode's Deps.
func NewEfakturaWorker(d module.Deps) *EfakturaWorker {
	srv, err := newServer(d)
	return &EfakturaWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *EfakturaWorker) Name() string { return efakturaWorkerName }

// Interval is the poll cadence between cycles.
func (w *EfakturaWorker) Interval() time.Duration { return efakturaWorkerInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done.
func (w *EfakturaWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(efakturaWorkerInterval)
	defer ticker.Stop()
	for {
		cycleCtx, endCycle := worker.StartCycle(ctx, w.Name())
		err := w.RunCycle(cycleCtx)
		endCycle(err)
		if err != nil && ctx.Err() == nil {
			w.logger().ErrorContext(cycleCtx, "invoices: an eFaktura worker cycle failed", "worker", efakturaWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle carries every due row one step, every claim asked to cancel, and
// the housekeeping when it is due.
//
// A phase that fails is reported and the next ones still run: a database
// error claiming a transmission does not stop the cancellations, the unpaid
// claims or the housekeeping.
func (w *EfakturaWorker) RunCycle(ctx context.Context) error {
	if w.srvErr != nil {
		return w.srvErr
	}
	var errs []error
	for ctx.Err() == nil {
		processed, err := w.ProcessOne(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		if !processed {
			break
		}
	}
	if err := w.CancelSettled(ctx); err != nil {
		errs = append(errs, err)
	}
	for ctx.Err() == nil {
		processed, err := w.CancelOne(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		if !processed {
			break
		}
	}
	if err := w.MarkUnpaid(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := w.Housekeeping(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// CancelSettled asks every live claim to cancel whose invoice was settled
// another way — paid by hand, with credit, by a split, a queue apply or a
// rounding, or written off (RequestSettledClaimsCancel) — so CancelOne
// stops the debit before the bank's deadline. One past it the provider
// refuses: the claim stays, and its debit is kept as credit when it comes
// (settleClaimDebit).
func (w *EfakturaWorker) CancelSettled(ctx context.Context) error {
	now := w.now()
	return w.tx(ctx, func(tx pgx.Tx, txq *store.Queries) error {
		asked, err := txq.RequestSettledClaimsCancel(ctx, now)
		if err != nil {
			return fmt.Errorf("invoices: ask the claims of settled invoices to cancel: %w", err)
		}
		for _, c := range asked {
			if err := publishClaimChanged(ctx, tx, c, c.Status, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkUnpaid ends the accepted claims no debit paid within five business
// days of their due date (unpaid): the reminders take over, and a late
// debit still pays them.
func (w *EfakturaWorker) MarkUnpaid(ctx context.Context) error {
	loc, err := w.srv.businessZone(ctx)
	if err != nil {
		return err
	}
	now := w.now()
	today := holidays.DayIn(now, loc)
	claims, err := w.q().AcceptedClaimsDueBefore(ctx, pgDate(today.AddDate(0, 0, -efakturaUnpaidAfter)))
	if err != nil {
		return fmt.Errorf("invoices: read the accepted claims past due: %w", err)
	}
	for _, c := range claims {
		cutoff := utcDay(c.DueDate.Time)
		for range efakturaUnpaidAfter {
			cutoff = holidays.NextBusinessDay(cutoff.AddDate(0, 0, 1))
		}
		if !today.After(cutoff) {
			continue
		}
		if err := w.tx(ctx, func(tx pgx.Tx, txq *store.Queries) error {
			n, err := txq.MarkClaimUnpaid(ctx, store.MarkClaimUnpaidParams{Now: now, ID: c.ID})
			if err != nil || n == 0 {
				return err
			}
			return publishClaimChanged(ctx, tx, c, "unpaid", now)
		}); err != nil {
			return fmt.Errorf("invoices: mark claim %d unpaid: %w", c.ID, err)
		}
	}
	return nil
}

func (w *EfakturaWorker) now() time.Time { return w.deps.Clock().UTC() }

func (w *EfakturaWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

func (w *EfakturaWorker) q() *store.Queries { return store.New(w.deps.Pool) }

// tx runs fn in one short transaction on the pool: a completion and its
// events.
func (w *EfakturaWorker) tx(ctx context.Context, fn func(tx pgx.Tx, txq *store.Queries) error) error {
	return db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error { return fn(tx, store.New(tx)) })
}

// ProcessOne claims at most one due transmission and carries it one step.
func (w *EfakturaWorker) ProcessOne(ctx context.Context) (bool, error) {
	if w.srvErr != nil {
		return false, w.srvErr
	}
	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	now := w.now()
	row, err := w.q().ClaimEfakturaTransmission(ctx, store.ClaimEfakturaTransmissionParams{
		LeaseID: &lease, LeaseUntil: now.Add(efakturaLease), Now: now,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: claim an eFaktura: %w", err)
	}
	c := &efakturaClaim{w: w, row: row, lease: lease, now: now}
	p, _, openErr := w.srv.consumerProvider(ctx)
	if openErr != nil {
		w.logger().ErrorContext(ctx, "invoices: the consumer provider cannot be opened; the eFaktura waits an hour",
			"worker", efakturaWorkerName, "transmission_id", row.ID, "error", openErr.Error())
		return true, c.reschedule(ctx, now.Add(time.Hour), 0, 0, openErr.Error(), false)
	}
	c.p = p
	switch row.Status {
	case "queued":
		err = c.submit(ctx)
	case "submitted", "unconfirmed", "delivered":
		err = c.poll(ctx)
	default:
		err = fmt.Errorf("invoices: claimed eFaktura %d in status %q", row.ID, row.Status)
	}
	if err != nil {
		// The step failed — a read or a completion the database refused:
		// the row is put back with the back-off, so the cycle goes on to the
		// next one and this one is not claimed again at once.
		w.logger().ErrorContext(ctx, "invoices: an eFaktura step failed; the row waits", "worker", efakturaWorkerName,
			"transmission_id", row.ID, "error", err.Error())
		if rerr := c.reschedule(ctx, now.Add(efakturaPollDelay(row.PollAttempts+1)), 0, 1, err.Error(), false); rerr != nil {
			return true, errors.Join(err, rerr)
		}
	}
	return true, nil
}

// efakturaClaim is one claimed row.
type efakturaClaim struct {
	w     *EfakturaWorker
	p     consumerinvoice.Provider
	row   store.InvoicesTransmission
	lease string
	now   time.Time
}

func (c *efakturaClaim) done(ctx context.Context, what string, n int64, err error) error {
	if err != nil {
		return fmt.Errorf("invoices: %s eFaktura %d: %w", what, c.row.ID, err)
	}
	if n == 0 {
		c.w.logger().DebugContext(ctx, "invoices: an eFaktura moved under the claim; nothing recorded",
			"worker", efakturaWorkerName, "transmission_id", c.row.ID, "step", what)
		return nil
	}
	c.w.logger().InfoContext(ctx, "invoices: an eFaktura moved", "worker", efakturaWorkerName,
		"transmission_id", c.row.ID, "step", what)
	return nil
}

func (c *efakturaClaim) reschedule(ctx context.Context, next time.Time, submitDelta, pollDelta int32, reason string, park bool) error {
	n, err := c.w.q().RescheduleLeased(ctx, store.RescheduleLeasedParams{
		Park: park, NextAttemptAt: next, SubmitAttemptsDelta: submitDelta, PollAttemptsDelta: pollDelta,
		LastError: reasonOrNil(reason), ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
	})
	return c.done(ctx, "reschedule", n, err)
}

// moved runs a completion and, when it moved the row, its event, in one
// transaction.
func (c *efakturaClaim) moved(ctx context.Context, what, status string, update func(txq *store.Queries) (int64, error)) error {
	var n int64
	err := c.w.tx(ctx, func(tx pgx.Tx, txq *store.Queries) error {
		var err error
		if n, err = update(txq); err != nil || n == 0 {
			return err
		}
		return publishTransmissionChanged(ctx, tx, c.row, status, c.now)
	})
	return c.done(ctx, what, n, err)
}

func (c *efakturaClaim) fail(ctx context.Context, reason, providerStatus string) error {
	if err := c.moved(ctx, "fail", "failed", func(txq *store.Queries) (int64, error) {
		return txq.MarkEfakturaFailedLeased(ctx, store.MarkEfakturaFailedLeasedParams{
			Now: c.now, LastError: reasonOrNil(reason), ProviderStatus: nilIfEmpty(providerStatus),
			MachineNote: ptr(efakturaMachineNote), ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
		})
	}); err != nil {
		return err
	}
	// A claim that never went is cancelled with its eFaktura.
	return c.settleClaim(ctx, consumerinvoice.TransactionStatus{AvtaleGiro: consumerinvoice.AvtaleGiroCancelled})
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// transactionID is the row's transaction id at the network: its idempotency
// key, persisted before the first call.
func (c *efakturaClaim) transactionID() string { return c.row.IdempotencyKey.String() }

// loadPDF is the stored PDF the shipment carries, checked against its hash.
func (c *efakturaClaim) loadPDF(ctx context.Context) ([]byte, error) {
	key := deref(c.row.PdfObjectKey)
	rc, err := c.w.srv.objectGet(ctx, key)
	if errors.Is(err, storage.ErrNotExist) {
		return nil, errUBLBroken
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errObjectStore, err)
	}
	body, err := io.ReadAll(io.LimitReader(rc, 2*maxStoredPDF+1))
	_ = rc.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errObjectStore, err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != c.row.PdfSha256 {
		return nil, errUBLBroken
	}
	return body, nil
}

// shipmentOf is the row's one-transaction shipment: the invoice's figures,
// and for both the claim's KID — the customer KID of the mandate.
func (c *efakturaClaim) shipmentOf(ctx context.Context, pdf []byte) (consumerinvoice.Shipment, error) {
	q := c.w.q()
	inv, err := q.GetInvoice(ctx, c.row.InvoiceID)
	if err != nil {
		return consumerinvoice.Shipment{}, fmt.Errorf("invoices: read eFaktura %d's invoice: %w", c.row.ID, err)
	}
	row, err := q.GetConsumerProviderCredentials(ctx)
	if err != nil {
		return consumerinvoice.Shipment{}, fmt.Errorf("invoices: read the consumer provider settings: %w", err)
	}
	settings, err := consumerProviderSettingsOf(row)
	if err != nil {
		return consumerinvoice.Shipment{}, err
	}
	gross, err := ratFromNumeric(inv.GrossTotal)
	if err != nil {
		return consumerinvoice.Shipment{}, err
	}
	paymentType := consumerinvoice.PaymentEfaktura
	k := deref(inv.Kid)
	if deref(c.row.PaymentType) == paymentTypeBoth {
		claim, err := q.ClaimOfTransmission(ctx, c.row.ID)
		if err != nil {
			return consumerinvoice.Shipment{}, fmt.Errorf("invoices: read eFaktura %d's claim: %w", c.row.ID, err)
		}
		if claim.Status == "submitted" {
			paymentType, k = consumerinvoice.PaymentBoth, claim.Kid
			if gross, err = ratFromNumeric(claim.Amount); err != nil {
				return consumerinvoice.Shipment{}, err
			}
		}
	}
	minor, err := minorOf(gross)
	if err != nil {
		return consumerinvoice.Shipment{}, err
	}
	number := strconv.FormatInt(deref64(inv.Number), 10)
	reference := ""
	if inv.BuyerCustomerNumber != nil {
		reference = strconv.FormatInt(*inv.BuyerCustomerNumber, 10)
	}
	return consumerinvoice.Shipment{
		ID: c.row.ShipmentID.String(), MessageID: fmt.Sprintf("vantigo-%d", c.row.ID),
		Transactions: []consumerinvoice.Transaction{{
			ID: c.transactionID(), PaymentType: paymentType, KID: k, Account: deref(inv.SellerBankAccount),
			Amount: consumerinvoice.NOK(minor), DueDate: inv.DueDate.Time, Identifier: deref(c.row.EfakturaIdentifier),
			Reference: reference, BrandName: settings.BrandName, AbbreviatedName: settings.AbbreviatedName,
			ExternalReference: number, PDF: pdf,
		}},
	}, nil
}

// submit is a queued row's claim.
func (c *efakturaClaim) submit(ctx context.Context) error {
	previous := c.row.SubmitAttemptedAt
	if c.now.Sub(c.row.QueuedAt) > ehfAgeCap {
		if previous != nil {
			return c.moved(ctx, "unconfirm", "unconfirmed", func(txq *store.Queries) (int64, error) {
				return txq.MarkUnconfirmedLeased(ctx, store.MarkUnconfirmedLeasedParams{
					NextAttemptAt: c.now.Add(ehfUnconfirmedProbe), LastError: reasonOrNil(reasonNoAnswer),
					ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
				})
			})
		}
		return c.fail(ctx, reasonNeverSubmitted, "")
	}
	pdf, err := c.loadPDF(ctx)
	switch {
	case errors.Is(err, errUBLBroken):
		c.w.logger().ErrorContext(ctx, "invoices: an eFaktura's stored PDF is missing or altered", "worker", efakturaWorkerName, "transmission_id", c.row.ID)
		return c.reschedule(ctx, c.now.Add(time.Hour), 0, 0, "The stored PDF is missing or does not match its hash.", false)
	case err != nil:
		return c.reschedule(ctx, c.now.Add(ehfBackoff(c.row.SubmitAttempts+1)), 1, 0, "The document store could not be read.", false)
	}
	shipment, err := c.shipmentOf(ctx, pdf)
	if err != nil {
		return err
	}
	n, err := c.w.q().MarkSubmitAttempted(ctx, store.MarkSubmitAttemptedParams{Now: c.now, ID: c.row.ID, LeaseID: &c.lease})
	if err != nil || n == 0 {
		return c.done(ctx, "stamp the marker of", n, err)
	}
	callCtx, cancel := context.WithTimeout(ctx, efakturaCallTimeout)
	_, err = c.p.Send(callCtx, shipment)
	cancel()

	var refused *consumerinvoice.ErrRefused
	var throttled *consumerinvoice.ErrThrottled
	switch {
	case err == nil, errors.Is(err, consumerinvoice.ErrDuplicate):
		// The shipment id is ours: a duplicate is the earlier send that went.
		c.w.clearRejected(ctx)
		return c.moved(ctx, "submit", "submitted", func(txq *store.Queries) (int64, error) {
			return txq.MarkEfakturaSubmitted(ctx, store.MarkEfakturaSubmittedParams{
				ProviderRef: ptr(shipment.ID), Now: c.now, NextAttemptAt: c.now.Add(efakturaPollDelay(0)),
				ID: c.row.ID, LeaseID: &c.lease,
			})
		})
	case errors.As(err, &refused):
		c.w.clearRejected(ctx)
		return c.fail(ctx, refused.Text+" ("+refused.Code+")", "")
	case errors.As(err, &throttled):
		if _, err := c.w.q().RestoreSubmitAttempted(ctx, store.RestoreSubmitAttemptedParams{Previous: previous, ID: c.row.ID, LeaseID: &c.lease}); err != nil {
			return c.done(ctx, "restore the marker of", 0, err)
		}
		wait := throttled.RetryAfter
		if wait <= 0 {
			wait = ehfDefaultThrottle
		}
		return c.reschedule(ctx, c.now.Add(wait), 0, 0, err.Error(), false)
	case errors.Is(err, consumerinvoice.ErrUnauthorized):
		c.w.logger().ErrorContext(ctx, "invoices: the provider refused the submitter; the eFaktura waits an hour",
			"worker", efakturaWorkerName, "transmission_id", c.row.ID)
		if _, err := c.w.q().RestoreSubmitAttempted(ctx, store.RestoreSubmitAttemptedParams{Previous: previous, ID: c.row.ID, LeaseID: &c.lease}); err != nil {
			return c.done(ctx, "restore the marker of", 0, err)
		}
		c.w.flagRejected(ctx)
		return c.reschedule(ctx, c.now.Add(time.Hour), 0, 0, err.Error(), false)
	default:
		c.w.logger().WarnContext(ctx, "invoices: an eFaktura shipment failed; retrying", "worker", efakturaWorkerName,
			"transmission_id", c.row.ID, "error", redactReason(err.Error()))
		return c.reschedule(ctx, c.now.Add(ehfBackoff(c.row.SubmitAttempts+1)), 1, 0, err.Error(), false)
	}
}

// poll is a submitted, unconfirmed or delivered row's claim: one Status.
//
// The shipment id is ours, stored before the first call, so a row whose
// submission's answer was lost — unconfirmed, with no reference — is asked
// about by it too. An unconfirmed row is asked about daily for 37 days after
// its queueing, then waits for a person.
func (c *efakturaClaim) poll(ctx context.Context) error {
	if c.row.Status == "unconfirmed" && !c.now.Before(c.row.QueuedAt.Add(efakturaUnconfirmedProbing)) {
		return c.reschedule(ctx, c.now, 0, 0, deref(c.row.LastError), true)
	}
	ref := c.row.ShipmentID.String()
	if c.row.ProviderRef != nil {
		ref = *c.row.ProviderRef
	}
	callCtx, cancel := context.WithTimeout(ctx, efakturaCallTimeout)
	statuses, err := c.p.Status(callCtx, consumerinvoice.Ref(ref))
	cancel()
	var throttled *consumerinvoice.ErrThrottled
	var refused *consumerinvoice.ErrRefused
	switch {
	case errors.As(err, &refused) && c.row.Status == "unconfirmed" && c.row.ProviderRef == nil:
		// The network does not know the shipment we may have sent: it never
		// received it.
		return c.fail(ctx, reasonNeverReceived, "")
	case errors.Is(err, consumerinvoice.ErrUnauthorized):
		c.w.flagRejected(ctx)
		return c.reschedule(ctx, c.now.Add(time.Hour), 0, 0, err.Error(), false)
	case errors.As(err, &throttled):
		return c.reschedule(ctx, c.now.Add(max(throttled.RetryAfter, ehfDefaultThrottle)), 0, 0, err.Error(), false)
	case err != nil:
		return c.reschedule(ctx, c.now.Add(efakturaPollDelay(c.row.PollAttempts+1)), 0, 1, err.Error(), false)
	}
	c.w.clearRejected(ctx)
	var st *consumerinvoice.TransactionStatus
	for i := range statuses {
		if statuses[i].TransactionID == c.transactionID() {
			st = &statuses[i]
		}
	}
	if st == nil {
		return c.reschedule(ctx, c.now.Add(efakturaPollDelay(c.row.PollAttempts+1)), 0, 1, "The network does not list the transaction yet.", false)
	}
	if err := c.settleClaim(ctx, *st); err != nil {
		return err
	}
	if c.row.Status == "delivered" {
		return c.evidenceWhenSettled(ctx, *st)
	}
	switch st.Efaktura {
	case consumerinvoice.EfakturaDelivered:
		return c.delivered(ctx, *st)
	case consumerinvoice.EfakturaFailed:
		return c.fail(ctx, strings.TrimSpace(st.RejectionText+" "+st.RejectionCode), st.ProviderEfaktura)
	case consumerinvoice.EfakturaCancelled:
		return c.fail(ctx, reasonEfakturaCancelled, st.ProviderEfaktura)
	}
	if c.row.Status == "submitted" && c.row.SubmittedAt != nil && !c.now.Before(c.row.SubmittedAt.Add(ehfSubmittedLimit)) {
		return c.moved(ctx, "unconfirm", "unconfirmed", func(txq *store.Queries) (int64, error) {
			return txq.MarkUnconfirmedLeased(ctx, store.MarkUnconfirmedLeasedParams{
				NextAttemptAt: c.now.Add(ehfUnconfirmedProbe), LastError: reasonOrNil(reasonNoOutcome),
				ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
			})
		})
	}
	next := c.now.Add(efakturaPollDelay(c.row.PollAttempts + 1))
	if c.row.Status == "unconfirmed" {
		next = c.now.Add(ehfUnconfirmedProbe)
	}
	n, err := c.w.q().SetProviderStatusLeased(ctx, store.SetProviderStatusLeasedParams{
		ProviderStatus: nilIfEmpty(st.ProviderEfaktura), NextAttemptAt: next, ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
	})
	return c.done(ctx, "poll", n, err)
}

// claimUnsettled is whether the row's claim still waits on the network:
// submitted, or accepted with its due date not yet past.
func (c *efakturaClaim) claimUnsettled(ctx context.Context) (bool, error) {
	if deref(c.row.PaymentType) != paymentTypeBoth {
		return false, nil
	}
	claim, err := c.w.q().ClaimOfTransmission(ctx, c.row.ID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: read eFaktura %d's claim: %w", c.row.ID, err)
	}
	switch claim.Status {
	case "submitted", "cancel_requested":
		return true, nil
	case "accepted":
		return !c.now.After(utcDay(claim.DueDate.Time).AddDate(0, 0, 1)), nil
	}
	return false, nil
}

// delivered records the eFaktura in the consumer's bank; with no claim
// left to wait for, the answer is stored as the evidence at once.
func (c *efakturaClaim) delivered(ctx context.Context, st consumerinvoice.TransactionStatus) error {
	unsettled, err := c.claimUnsettled(ctx)
	if err != nil {
		return err
	}
	var key, hash *string
	next := c.now.Add(efakturaClaimPoll)
	if !unsettled {
		k, h, err := c.storeEvidence(ctx, st)
		if err != nil {
			c.w.logger().WarnContext(ctx, "invoices: an eFaktura's evidence could not be stored", "worker", efakturaWorkerName,
				"transmission_id", c.row.ID, "error", err.Error())
			next = c.now.Add(efakturaPollDelay(0))
		} else {
			key, hash = &k, &h
		}
	}
	return c.moved(ctx, "deliver", "delivered", func(txq *store.Queries) (int64, error) {
		return txq.MarkEfakturaDeliveredLeased(ctx, store.MarkEfakturaDeliveredLeasedParams{
			Now: c.now, NextAttemptAt: next, ProviderStatus: nilIfEmpty(st.ProviderEfaktura),
			EvidenceObjectKey: key, EvidenceSha256: hash, MachineNote: ptr(efakturaMachineNote),
			ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
		})
	})
}

// evidenceWhenSettled is a delivered row's poll: its claim settled, the
// answer is the evidence; not yet, the row is polled again.
func (c *efakturaClaim) evidenceWhenSettled(ctx context.Context, st consumerinvoice.TransactionStatus) error {
	unsettled, err := c.claimUnsettled(ctx)
	if err != nil {
		return err
	}
	if unsettled {
		return c.reschedule(ctx, c.now.Add(efakturaClaimPoll), 0, 1, "", false)
	}
	key, hash, err := c.storeEvidence(ctx, st)
	if err != nil {
		return c.reschedule(ctx, c.now.Add(efakturaPollDelay(c.row.PollAttempts+1)), 0, 1, "The document store could not store the evidence.", false)
	}
	n, err := c.w.q().SetEvidenceLeased(ctx, store.SetEvidenceLeasedParams{
		EvidenceObjectKey: &key, EvidenceSha256: &hash, ID: c.row.ID, LeaseID: &c.lease,
	})
	return c.done(ctx, "store the evidence of", n, err)
}

// storeEvidence stores the network's answer beside the invoice's PDF.
func (c *efakturaClaim) storeEvidence(ctx context.Context, st consumerinvoice.TransactionStatus) (key, hash string, err error) {
	key = fmt.Sprintf("documents/%d/%d-efaktura-status.xml", c.row.InvoiceID, c.row.ID)
	body := st.Raw
	if len(body) == 0 {
		body = []byte("<transaction><transactionId>" + c.transactionID() + "</transactionId><efakturaStatus>" + st.ProviderEfaktura + "</efakturaStatus></transaction>")
	}
	callCtx, cancel := context.WithTimeout(ctx, efakturaCallTimeout)
	defer cancel()
	if err := c.w.srv.objectPut(callCtx, key, bytes.NewReader(body), "application/xml"); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(body)
	return key, hex.EncodeToString(sum[:]), nil
}

// settleClaim moves the row's claim by the AvtaleGiro half of st.
func (c *efakturaClaim) settleClaim(ctx context.Context, st consumerinvoice.TransactionStatus) error {
	if deref(c.row.PaymentType) != paymentTypeBoth {
		return nil
	}
	claim, err := c.w.q().ClaimOfTransmission(ctx, c.row.ID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("invoices: read eFaktura %d's claim: %w", c.row.ID, err)
	}
	var status string
	update := func(txq *store.Queries) (int64, error) { return 0, nil }
	switch {
	case st.AvtaleGiro == consumerinvoice.AvtaleGiroAccepted && claim.Status == "submitted":
		status = "accepted"
		update = func(txq *store.Queries) (int64, error) {
			return txq.AcceptClaim(ctx, store.AcceptClaimParams{Now: c.now, Attention: st.Attention, ID: claim.ID})
		}
	case st.AvtaleGiro == consumerinvoice.AvtaleGiroAccepted && st.Attention && !claim.Attention:
		update = func(txq *store.Queries) (int64, error) { return txq.SetClaimAttention(ctx, claim.ID) }
	case st.AvtaleGiro == consumerinvoice.AvtaleGiroRejected:
		status = "rejected"
		update = func(txq *store.Queries) (int64, error) {
			return txq.RejectClaim(ctx, store.RejectClaimParams{
				Now: c.now, Attention: st.Attention, RejectionCode: nilIfEmpty(st.RejectionCode),
				RejectionText: reasonOrNil(st.RejectionText), ID: claim.ID,
			})
		}
	case st.AvtaleGiro == consumerinvoice.AvtaleGiroCancelled:
		status = "cancelled"
		update = func(txq *store.Queries) (int64, error) {
			return txq.CancelClaim(ctx, store.CancelClaimParams{Now: c.now, ID: claim.ID})
		}
	default:
		return nil
	}
	return c.w.tx(ctx, func(tx pgx.Tx, txq *store.Queries) error {
		n, err := update(txq)
		if err != nil || n == 0 || status == "" {
			return err
		}
		return publishClaimChanged(ctx, tx, claim, status, c.now)
	})
}

// CancelOne carries at most one claim asked to cancel to the provider.
func (w *EfakturaWorker) CancelOne(ctx context.Context) (bool, error) {
	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	now := w.now()
	row, err := w.q().ClaimCancelDue(ctx, store.ClaimCancelDueParams{LeaseID: &lease, LeaseUntil: now.Add(efakturaLease), Now: now})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: claim a cancellation: %w", err)
	}
	claimed, err := w.q().ClaimsOfInvoice(ctx, row.InvoiceID)
	if err != nil {
		return true, fmt.Errorf("invoices: read claim %d: %w", row.ID, err)
	}
	var claim store.InvoicesAvtalegiroClaim
	for _, c := range claimed {
		if c.ID == row.ID {
			claim = c
		}
	}
	refuse := func() error {
		return w.tx(ctx, func(tx pgx.Tx, txq *store.Queries) error {
			n, err := txq.RefuseClaimCancel(ctx, store.RefuseClaimCancelParams{Now: now, ID: row.ID, LeaseID: &lease})
			if err != nil || n == 0 {
				return err
			}
			back := "submitted"
			if row.AcceptedAt != nil {
				back = "accepted"
			}
			return publishClaimChanged(ctx, tx, claim, back, now)
		})
	}
	reschedule := func(next time.Time) error {
		_, err := w.q().RescheduleClaimCancel(ctx, store.RescheduleClaimCancelParams{NextAttemptAt: next, ID: row.ID, LeaseID: &lease})
		return err
	}
	cancelLocally := func() error {
		return w.tx(ctx, func(tx pgx.Tx, txq *store.Queries) error {
			n, err := txq.CancelRequestedClaim(ctx, store.CancelRequestedClaimParams{Now: now, ID: row.ID, LeaseID: &lease})
			if err != nil || n == 0 {
				return err
			}
			return publishClaimChanged(ctx, tx, claim, "cancelled", now)
		})
	}
	switch {
	case row.TransmissionStatus == "queued" && row.SubmitAttemptedAt == nil,
		row.TransmissionStatus == "failed" || row.TransmissionStatus == "cancelled":
		// The bank never got it, or the eFaktura that carried it failed:
		// nothing to ask the network to stop.
		return true, cancelLocally()
	case row.ProviderRef == nil:
		// A submission whose answer is pending: the poll decides first.
		return true, reschedule(now.Add(efakturaCancelRetry))
	case !now.Before(avtaleGiroCancelDeadline(row.DueDate.Time)):
		// Past the deadline the payment comes; its surplus is credit.
		return true, refuse()
	}
	p, _, err := w.srv.consumerProvider(ctx)
	if err != nil {
		w.logger().ErrorContext(ctx, "invoices: the consumer provider cannot be opened; the cancellation waits", "worker", efakturaWorkerName, "error", err.Error())
		return true, reschedule(now.Add(time.Hour))
	}
	callCtx, cancel := context.WithTimeout(ctx, efakturaCallTimeout)
	outcome, err := p.Cancel(callCtx, consumerinvoice.Ref(*row.ProviderRef), row.IdempotencyKey.String())
	cancel()
	switch {
	case err != nil:
		w.logger().WarnContext(ctx, "invoices: a cancellation failed; retrying", "worker", efakturaWorkerName, "claim_id", row.ID, "error", redactReason(err.Error()))
		return true, reschedule(now.Add(efakturaCancelRetry))
	case outcome == consumerinvoice.CancelRequested:
		return true, cancelLocally()
	case outcome == consumerinvoice.CancelTryLater:
		return true, reschedule(now.Add(efakturaCancelRetry))
	default:
		return true, refuse()
	}
}

// Housekeeping drains the mandate queue and refreshes the due lookups once a
// day, under a lease on the settings row so two workers never both do it.
func (w *EfakturaWorker) Housekeeping(ctx context.Context) error {
	now := w.now()
	if _, err := w.q().ClaimEfakturaHousekeeping(ctx, store.ClaimEfakturaHousekeepingParams{
		LeaseUntil: now.Add(efakturaHousekeepingLease), DueBefore: now.Add(-efakturaHousekeepingEvery), Now: now,
	}); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("invoices: claim the eFaktura housekeeping: %w", err)
	}
	p, settings, err := w.srv.consumerProvider(ctx)
	if err != nil {
		_ = w.q().ReleaseEfakturaHousekeeping(ctx)
		return nil
	}
	if settings.AvtalegiroAgreement != agreementNone && settings.CreditorAccount != "" {
		if err := w.drainMandates(ctx, p); err != nil {
			w.logger().WarnContext(ctx, "invoices: the mandate queue could not be drained", "worker", efakturaWorkerName, "error", redactReason(err.Error()))
			_ = w.q().ReleaseEfakturaHousekeeping(ctx)
			return nil
		}
	}
	due, err := w.q().ConsumerLookupsDue(ctx, store.ConsumerLookupsDueParams{Now: now, Max: consumerinvoice.MaxLookupBatch})
	if err != nil {
		return fmt.Errorf("invoices: read the lookups due: %w", err)
	}
	if len(due) > 0 {
		if _, err := w.srv.refreshLookups(ctx, p, due); err != nil {
			w.logger().WarnContext(ctx, "invoices: the due lookups could not be refreshed", "worker", efakturaWorkerName, "error", redactReason(err.Error()))
		}
	}
	return w.q().FinishEfakturaHousekeeping(ctx, now)
}

// drainMandates reads the delta queue page by page until it is empty. Each
// page's request id is stored before it is asked for and cleared only once
// the page is stored, so a lost answer or a failed store asks again under the
// same id — which replays the same page — and no page is ever skipped. The
// queue positions grow with the drain's start and the order within it.
func (w *EfakturaWorker) drainMandates(ctx context.Context, p consumerinvoice.Provider) error {
	position := w.now().UnixMicro() * 1000
	for range efakturaMandatePages {
		row, err := w.q().GetConsumerProviderCredentials(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the pending mandate page: %w", err)
		}
		requestID := uuid.New()
		if row.MandateRequestID != nil {
			requestID = *row.MandateRequestID
		} else if err := w.q().SetMandateRequestID(ctx, &requestID); err != nil {
			return fmt.Errorf("invoices: record the mandate page asked for: %w", err)
		}
		callCtx, cancel := context.WithTimeout(ctx, efakturaCallTimeout)
		page, err := p.Mandates(callCtx, requestID.String())
		cancel()
		if err != nil {
			return err
		}
		if err := w.srv.upsertMandates(ctx, w.q(), page.Mandates, position); err != nil {
			return err
		}
		position += int64(len(page.Mandates))
		if err := w.q().ClearMandateRequestID(ctx); err != nil {
			return fmt.Errorf("invoices: forget the stored mandate page: %w", err)
		}
		if !page.More {
			return nil
		}
	}
	return nil
}

func (w *EfakturaWorker) flagRejected(ctx context.Context) {
	if err := w.q().MarkConsumerProviderRejected(ctx, w.now()); err != nil {
		w.logger().ErrorContext(ctx, "invoices: flag the consumer provider as rejected", "worker", efakturaWorkerName, "error", err.Error())
	}
}

func (w *EfakturaWorker) clearRejected(ctx context.Context) {
	if err := w.q().ClearConsumerProviderRejected(ctx); err != nil {
		w.logger().WarnContext(ctx, "invoices: clear the consumer provider's refusal", "worker", efakturaWorkerName, "error", err.Error())
	}
}
