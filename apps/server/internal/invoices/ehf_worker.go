package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-ehf worker (EHF and KID design D9): the
// communications outbox's shape — claim one due transmission by a conditional
// UPDATE with a lease, make exactly one provider call for it, and complete it
// with an UPDATE that names the lease and the status the claim saw, a no-op
// when either changed. What a claim does depends on the row:
//
//   - queued: the age cap first; the receiver looked up again when the stored
//     lookup is a day old; the key opened; the stored UBL read and checked;
//     the crash marker stamped; Submit.
//   - submitted with a reference: one Evidence probe on the cadence; without
//     one, never probed, only rescheduled until the seven days run out.
//   - delivered without its evidence: one Evidence, stored once.
//   - unconfirmed with a reference: one Evidence a day for thirty days.
//
// Every time is Deps.Clock()'s, passed as @now; nothing here reads the wall
// clock or SQL now(). Nothing here locks a document, and no call is made
// inside a transaction: the claim and every completion are single statements
// on the pool.

const (
	// ehfWorkerName is what the runner logs this worker as.
	ehfWorkerName = "invoices-ehf"
	// ehfWorkerInterval is the poll cadence between drains (D9).
	ehfWorkerInterval = 5 * time.Second
	// ehfLease is how long a claim keeps a row from every other worker.
	// Nothing renews it: one provider call per claim, bounded by
	// ehfCallTimeout, keeps a claim well inside it.
	ehfLease = 60 * time.Second
	// ehfDefaultCallTimeout is half the lease, the outbox's rule: a call never
	// outlives the lease protecting it.
	ehfDefaultCallTimeout = ehfLease / 2

	// ehfAgeCap is how long a queued row may wait for a submission the
	// provider took (reading 11): then it is unconfirmed when the marker is
	// set, failed when it never was.
	ehfAgeCap = 48 * time.Hour
	// ehfLookupMaxAge is how old the stored receiver lookup may be before the
	// submit claim asks the network again.
	ehfLookupMaxAge = 24 * time.Hour
	// ehfBackoffCeiling is the transport backoff's ceiling.
	ehfBackoffCeiling = time.Hour
	// ehfRefusedKeyDelay is how long a row waits after the provider refused
	// the key, or the key could not be opened at all.
	ehfRefusedKeyDelay = time.Hour
	// ehfDefaultThrottle is the wait after a 429 that named no Retry-After.
	ehfDefaultThrottle = time.Minute
	// ehfUnreferencedDelay is how often a submitted row without a reference
	// is looked at: it is never probed, only waited out.
	ehfUnreferencedDelay = time.Hour
	// ehfSubmittedLimit is how long a row may stay submitted before a person
	// is asked: seven days after submitted_at.
	ehfSubmittedLimit = 7 * 24 * time.Hour
	// ehfUnconfirmedProbe is how often an unconfirmed row with a reference is
	// probed, and ehfUnconfirmedProbing for how long after it became
	// unconfirmed — the seven days after submitted_at.
	ehfUnconfirmedProbe   = 24 * time.Hour
	ehfUnconfirmedProbing = 30 * 24 * time.Hour

	// ehfMachineNote is the resolution note of an unconfirmed row the
	// machine resolved itself: resolved_by_user_id stays NULL.
	ehfMachineNote = "Resolved by the provider's evidence."

	// reasonReceiverNotReceivable is last_error when the submit claim's
	// fresh lookup says the receiver no longer takes the document.
	reasonReceiverNotReceivable = "receiver_not_receivable"
	// reasonNeverSubmitted and reasonNoAnswer are last_error at the age cap.
	reasonNeverSubmitted = "The document could not be handed to the access point within 48 hours."
	reasonNoAnswer       = "The access point did not confirm the submission within 48 hours; it may have the document."
	// reasonNoOutcome is last_error when seven days pass without an outcome.
	reasonNoOutcome = "The access point has not reported an outcome within seven days."
)

// ehfCallTimeout bounds every provider call a claim makes. A test shortens
// it (export_test.go) to reach a transport timeout without waiting for one.
var ehfCallTimeout = ehfDefaultCallTimeout

// ehfAfterCall, when a test sets it (export_test.go), is called after every
// provider call a claim makes and before its completion, with the row's id:
// the moment another actor can move the row under the claim. nil in
// production.
var ehfAfterCall func(ctx context.Context, transmissionID int64)

// afterCall runs ehfAfterCall.
func (c *ehfClaim) afterCall(ctx context.Context) {
	if ehfAfterCall != nil {
		ehfAfterCall(ctx, c.row.ID)
	}
}

// ehfProbeDelay is the probe cadence (D9) by poll_attempts as counted after
// the probe: 5 minutes before the first probe, 15 after it, then hourly.
func ehfProbeDelay(polls int32) time.Duration {
	switch {
	case polls <= 0:
		return 5 * time.Minute
	case polls == 1:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}

// ehfBackoff is the wait after the n-th failed submit attempt: 2^n seconds,
// at most an hour.
func ehfBackoff(n int32) time.Duration {
	if n >= 12 {
		return ehfBackoffCeiling
	}
	return min(ehfBackoffCeiling, time.Duration(1<<max(n, 0))*time.Second)
}

// EhfWorker submits queued transmissions and probes the provider for their
// outcome and evidence. It implements worker.Worker; Module.Workers registers
// it only when INVOICES_EHF_ENABLED is on.
type EhfWorker struct {
	srv *server
	// srvErr is newServer's failure — an object store the configuration
	// cannot build — kept so the worker still starts and every cycle says
	// why it does nothing, as the outbox's store error is kept.
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*EhfWorker)(nil)

// NewEhfWorker builds the worker over worker mode's Deps: the pool, the
// clock, the secrets box, the configuration and an object store built from
// it. It needs no directory.
func NewEhfWorker(d module.Deps) *EhfWorker {
	srv, err := newServer(d)
	return &EhfWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *EhfWorker) Name() string { return ehfWorkerName }

// Interval is the poll cadence between drains.
func (w *EhfWorker) Interval() time.Duration { return ehfWorkerInterval }

// Run drains every due row, sleeps the interval, and repeats until ctx is
// done. A failing cycle is logged and the loop goes on.
func (w *EhfWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(ehfWorkerInterval)
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: an EHF worker cycle failed", "worker", ehfWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle processes rows until none is due. An installation whose Peppol
// lookup is disabled claims no queued row and says once that it left them.
func (w *EhfWorker) RunCycle(ctx context.Context) error {
	if w.srvErr != nil {
		return w.srvErr
	}
	if w.srv.peppolLookup == nil {
		n, err := store.New(w.deps.Pool).QueuedTransmissionsDue(ctx, w.now())
		if err != nil {
			return fmt.Errorf("invoices: count the queued transmissions: %w", err)
		}
		if n > 0 {
			w.logger().InfoContext(ctx, "invoices: queued EHF transmissions are left alone: the Peppol lookup is disabled",
				"worker", ehfWorkerName, "queued", n)
		}
	}
	for ctx.Err() == nil {
		processed, err := w.ProcessOne(ctx)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

// ProcessOne claims at most one due row and carries it one step, answering
// whether a row was claimed.
func (w *EhfWorker) ProcessOne(ctx context.Context) (bool, error) {
	if w.srvErr != nil {
		return false, w.srvErr
	}
	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	now := w.now()
	row, err := store.New(w.deps.Pool).ClaimTransmission(ctx, store.ClaimTransmissionParams{
		LeaseID: &lease, LeaseUntil: now.Add(ehfLease), Now: now, SkipQueued: w.srv.peppolLookup == nil,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: claim a transmission: %w", err)
	}
	c := &ehfClaim{w: w, row: row, lease: lease, now: now}
	switch row.Status {
	case "queued":
		err = c.submit(ctx)
	case "submitted":
		err = c.probe(ctx)
	case "delivered":
		err = c.storeEvidence(ctx)
	case "unconfirmed":
		err = c.probeUnconfirmed(ctx)
	default:
		err = fmt.Errorf("invoices: claimed transmission %d in status %q", row.ID, row.Status)
	}
	return true, err
}

// ehfClaim is one claimed row: the row as the claim saw it, its lease and the
// claim's time.
type ehfClaim struct {
	w     *EhfWorker
	row   store.InvoicesTransmission
	lease string
	now   time.Time
}

func (c *ehfClaim) q() *store.Queries { return store.New(c.w.deps.Pool) }

// done reports a completion's rows: 0 is another actor's move (the events
// worker, a person's cancel, a newer lease) and only worth a debug line.
func (c *ehfClaim) done(ctx context.Context, what string, n int64, err error) error {
	if err != nil {
		return fmt.Errorf("invoices: %s transmission %d: %w", what, c.row.ID, err)
	}
	if n == 0 {
		c.w.logger().DebugContext(ctx, "invoices: a transmission moved under the claim; nothing recorded",
			"worker", ehfWorkerName, "transmission_id", c.row.ID, "step", what)
		return nil
	}
	c.w.logger().InfoContext(ctx, "invoices: a transmission moved", "worker", ehfWorkerName,
		"transmission_id", c.row.ID, "provider_ref", deref(c.row.ProviderRef), "step", what)
	return nil
}

// reschedule puts the row back at next with the counters moved by the deltas
// and last_error set (redacted), or parked at 'infinity'.
func (c *ehfClaim) reschedule(ctx context.Context, next time.Time, submitDelta, pollDelta int32, reason string, park bool) error {
	n, err := c.q().RescheduleLeased(ctx, store.RescheduleLeasedParams{
		Park: park, NextAttemptAt: next, SubmitAttemptsDelta: submitDelta, PollAttemptsDelta: pollDelta,
		LastError: reasonOrNil(reason), ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
	})
	return c.done(ctx, "reschedule", n, err)
}

func (c *ehfClaim) fail(ctx context.Context, reason string) error {
	n, err := c.q().MarkFailedLeased(ctx, store.MarkFailedLeasedParams{
		Now: c.now, LastError: reasonOrNil(reason), ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
	})
	return c.done(ctx, "fail", n, err)
}

// unconfirm hands the row to a person; it keeps being probed daily when it
// has a reference, and never when it has none.
func (c *ehfClaim) unconfirm(ctx context.Context, reason string) error {
	n, err := c.q().MarkUnconfirmedLeased(ctx, store.MarkUnconfirmedLeasedParams{
		Park: c.row.ProviderRef == nil, NextAttemptAt: c.now.Add(ehfUnconfirmedProbe), LastError: reasonOrNil(reason),
		ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
	})
	return c.done(ctx, "unconfirm", n, err)
}

// restoreMarker puts the crash marker back to its pre-claim value: the
// outcome proved the provider did not take the document.
func (c *ehfClaim) restoreMarker(ctx context.Context, previous *time.Time) error {
	n, err := c.q().RestoreSubmitAttempted(ctx, store.RestoreSubmitAttemptedParams{Previous: previous, ID: c.row.ID, LeaseID: &c.lease})
	return c.done(ctx, "restore the marker of", n, err)
}

// accessPoint opens the provider for this claim. A failure — no credentials,
// a key the box cannot open — leaves the row as it is an hour out, flags the
// key as rejected and logs at error; ok is false and the claim ends.
func (c *ehfClaim) accessPoint(ctx context.Context) (ap accesspoint.AccessPoint, ok bool, err error) {
	ap, openErr := c.w.srv.accessPoint(ctx)
	if openErr == nil {
		return ap, true, nil
	}
	c.w.logger().ErrorContext(ctx, "invoices: the access point cannot be opened; the transmission waits an hour",
		"worker", ehfWorkerName, "transmission_id", c.row.ID, "error", openErr.Error())
	c.w.flagRejected(ctx)
	return nil, false, c.reschedule(ctx, c.now.Add(ehfRefusedKeyDelay), 0, 0, openErr.Error(), false)
}

// callContext bounds one provider call.
func callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ehfCallTimeout)
}

// submit is a queued row's claim.
func (c *ehfClaim) submit(ctx context.Context) error {
	previous := c.row.SubmitAttemptedAt

	// The cap by age (reading 11): never by attempts.
	if c.now.Sub(c.row.QueuedAt) > ehfAgeCap {
		if previous != nil {
			return c.unconfirm(ctx, reasonNoAnswer)
		}
		return c.fail(ctx, reasonNeverSubmitted)
	}

	// The receiver, asked again when the send's answer is a day old. A lookup
	// is not a provider call; the submit below still is this claim's one.
	if c.now.Sub(c.row.LookupAt) > ehfLookupMaxAge {
		res, err := c.w.srv.lookupReceiver(ctx, c.row.ReceiverParticipant)
		if err != nil {
			attempts := c.row.SubmitAttempts + 1
			return c.reschedule(ctx, c.now.Add(ehfBackoff(attempts)), 1, 0, "The Peppol network could not be asked about the receiver.", false)
		}
		receivable := res.CanReceiveInvoice
		if c.row.DocumentType == peppol.CreditNoteDocumentType {
			receivable = res.CanReceiveCreditNote
		}
		n, err := c.q().RefreshTransmissionLookup(ctx, store.RefreshTransmissionLookupParams{
			LookupRegistered: res.Registered, LookupCanReceive: receivable, Now: c.now, ID: c.row.ID, LeaseID: &c.lease,
		})
		if err != nil || n == 0 {
			return c.done(ctx, "refresh the lookup of", n, err)
		}
		if !res.Registered || !receivable {
			return c.fail(ctx, reasonReceiverNotReceivable)
		}
	}

	ap, ok, err := c.accessPoint(ctx)
	if !ok {
		return err
	}

	ubl, err := c.w.srv.loadStoredUBL(ctx, c.row)
	switch {
	case errors.Is(err, errUBLBroken):
		// Logged at error by loadStoredUBL. Never rendered again here: the
		// age cap ends the row by its marker.
		return c.reschedule(ctx, c.now.Add(ehfRefusedKeyDelay), 0, 0, "The stored UBL is missing or does not match its hash.", false)
	case err != nil:
		attempts := c.row.SubmitAttempts + 1
		return c.reschedule(ctx, c.now.Add(ehfBackoff(attempts)), 1, 0, "The document store could not be read.", false)
	}

	// The crash marker, committed on its own immediately before the call.
	n, err := c.q().MarkSubmitAttempted(ctx, store.MarkSubmitAttemptedParams{Now: c.now, ID: c.row.ID, LeaseID: &c.lease})
	if err != nil || n == 0 {
		return c.done(ctx, "stamp the marker of", n, err)
	}

	callCtx, cancel := callContext(ctx)
	ref, err := ap.Submit(callCtx, accesspoint.Submission{
		IdempotencyKey: c.row.IdempotencyKey, Sender: c.row.SenderParticipant, Receiver: c.row.ReceiverParticipant,
		DocumentType: c.row.DocumentType, ProcessID: c.row.ProcessID, UBL: ubl,
	})
	cancel()
	c.afterCall(ctx)

	var unprocessable *accesspoint.ErrUnprocessable
	var throttled *accesspoint.ErrThrottled
	switch {
	case err == nil:
		c.w.clearRejected(ctx)
		n, err := c.q().MarkSubmitted(ctx, store.MarkSubmittedParams{
			ProviderRef: ptr(string(ref)), Now: c.now, NextAttemptAt: c.now.Add(ehfProbeDelay(0)), ID: c.row.ID, LeaseID: &c.lease,
		})
		c.row.ProviderRef = ptr(string(ref))
		return c.done(ctx, "submit", n, err)
	case errors.As(err, &unprocessable):
		c.w.clearRejected(ctx)
		reason := strings.Join(unprocessable.Messages, "; ")
		if reason == "" {
			reason = "The access point refused the document."
		}
		if previous == nil {
			// The first attempt: a validation refusal.
			return c.fail(ctx, reason)
		}
		// A retry: possibly the duplicate key of a submission that went
		// through. The event drain resolves it by the key, or the seven days.
		n, err := c.q().MarkSubmittedWithoutRef(ctx, store.MarkSubmittedWithoutRefParams{
			Now: c.now, NextAttemptAt: c.now.Add(ehfUnreferencedDelay), LastError: reasonOrNil(reason), ID: c.row.ID, LeaseID: &c.lease,
		})
		return c.done(ctx, "submit without a reference", n, err)
	case errors.As(err, &throttled):
		wait := throttled.RetryAfter
		if wait <= 0 {
			wait = ehfDefaultThrottle
		}
		if err := c.restoreMarker(ctx, previous); err != nil {
			return err
		}
		return c.reschedule(ctx, c.now.Add(wait), 0, 0, err.Error(), false)
	case errors.Is(err, accesspoint.ErrUnauthorized):
		c.w.logger().ErrorContext(ctx, "invoices: the access point refused the key; the transmission waits an hour",
			"worker", ehfWorkerName, "transmission_id", c.row.ID)
		if err := c.restoreMarker(ctx, previous); err != nil {
			return err
		}
		c.w.flagRejected(ctx)
		return c.reschedule(ctx, c.now.Add(ehfRefusedKeyDelay), 0, 0, err.Error(), false)
	case errors.Is(err, accesspoint.ErrUnmappedScheme):
		if err := c.restoreMarker(ctx, previous); err != nil {
			return err
		}
		return c.fail(ctx, err.Error())
	default:
		// A transport failure or a 5xx: the outcome is unknown, the marker
		// stays, and the next claim retries under the same key.
		c.w.logger().WarnContext(ctx, "invoices: a submission failed; retrying", "worker", ehfWorkerName,
			"transmission_id", c.row.ID, "error", redactReason(err.Error()))
		attempts := c.row.SubmitAttempts + 1
		return c.reschedule(ctx, c.now.Add(ehfBackoff(attempts)), 1, 0, err.Error(), false)
	}
}

// evidence makes the claim's one Evidence call. handled is true when the
// outcome — a refused key, a throttle — was already completed here.
func (c *ehfClaim) evidence(ctx context.Context) (ev accesspoint.Evidence, handled bool, err error) {
	ap, ok, openErr := c.accessPoint(ctx)
	if !ok {
		return accesspoint.Evidence{}, true, openErr
	}
	callCtx, cancel := callContext(ctx)
	ev, err = ap.Evidence(callCtx, accesspoint.SubmissionRef(*c.row.ProviderRef))
	cancel()
	c.afterCall(ctx)
	var throttled *accesspoint.ErrThrottled
	switch {
	case err == nil, errors.Is(err, accesspoint.ErrNotYetAvailable):
		c.w.clearRejected(ctx)
		return ev, false, err
	case errors.Is(err, accesspoint.ErrUnauthorized):
		c.w.logger().ErrorContext(ctx, "invoices: the access point refused the key; the probe waits an hour",
			"worker", ehfWorkerName, "transmission_id", c.row.ID)
		c.w.flagRejected(ctx)
		return ev, true, c.reschedule(ctx, c.now.Add(ehfRefusedKeyDelay), 0, 0, err.Error(), false)
	case errors.As(err, &throttled):
		wait := throttled.RetryAfter
		if wait <= 0 {
			wait = ehfDefaultThrottle
		}
		return ev, true, c.reschedule(ctx, c.now.Add(wait), 0, 0, err.Error(), false)
	default:
		return ev, false, err
	}
}

// probe is a submitted row's claim: Evidence is the status proxy, 404 not
// delivered yet, 200 delivered even if the event was lost.
func (c *ehfClaim) probe(ctx context.Context) error {
	expired := c.row.SubmittedAt != nil && !c.now.Before(c.row.SubmittedAt.Add(ehfSubmittedLimit))
	if c.row.ProviderRef == nil {
		if expired {
			return c.unconfirm(ctx, reasonNoOutcome)
		}
		return c.reschedule(ctx, c.now.Add(ehfUnreferencedDelay), 0, 0, deref(c.row.LastError), false)
	}
	_, handled, err := c.evidence(ctx)
	switch {
	case handled:
		return err
	case err == nil:
		return c.delivered(ctx)
	case expired:
		return c.unconfirm(ctx, reasonNoOutcome)
	}
	reason := ""
	if !errors.Is(err, accesspoint.ErrNotYetAvailable) {
		reason = err.Error()
	}
	polls := c.row.PollAttempts + 1
	return c.reschedule(ctx, c.now.Add(ehfProbeDelay(polls)), 0, 1, reason, false)
}

// probeUnconfirmed is an unconfirmed row's claim: one probe a day for thirty
// days after the seven, then never again.
func (c *ehfClaim) probeUnconfirmed(ctx context.Context) error {
	if c.row.ProviderRef == nil || c.row.SubmittedAt == nil ||
		!c.now.Before(c.row.SubmittedAt.Add(ehfSubmittedLimit+ehfUnconfirmedProbing)) {
		return c.reschedule(ctx, c.now, 0, 0, deref(c.row.LastError), true)
	}
	_, handled, err := c.evidence(ctx)
	switch {
	case handled:
		return err
	case err == nil:
		return c.delivered(ctx)
	}
	return c.reschedule(ctx, c.now.Add(ehfUnconfirmedProbe), 0, 1, deref(c.row.LastError), false)
}

// delivered commits what a probe found; the evidence is the next claim's.
func (c *ehfClaim) delivered(ctx context.Context) error {
	n, err := c.q().MarkDeliveredLeased(ctx, store.MarkDeliveredLeasedParams{
		Now: c.now, MachineNote: ptr(ehfMachineNote), ID: c.row.ID, LeaseID: &c.lease, Status: c.row.Status,
	})
	return c.done(ctx, "deliver", n, err)
}

// storeEvidence is a delivered row's claim: the provider's receipt and the
// document it transmitted, stored once beside the document's PDF, the
// receipt's key and hash recorded.
func (c *ehfClaim) storeEvidence(ctx context.Context) error {
	if c.row.ProviderRef == nil {
		return c.reschedule(ctx, c.now, 0, 0, "", true)
	}
	retry := func(reason string) error {
		polls := c.row.PollAttempts + 1
		return c.reschedule(ctx, c.now.Add(ehfProbeDelay(polls)), 0, 1, reason, false)
	}
	inv, err := c.q().GetInvoice(ctx, c.row.InvoiceID)
	if err != nil {
		return fmt.Errorf("invoices: read transmission %d's document: %w", c.row.ID, err)
	}
	ev, handled, err := c.evidence(ctx)
	switch {
	case handled:
		return err
	case errors.Is(err, accesspoint.ErrNotYetAvailable):
		return retry("")
	case err != nil:
		return retry(err.Error())
	}
	number := int64(0)
	if inv.Number != nil {
		number = int64(*inv.Number)
	}
	base := fmt.Sprintf("documents/%d/%d-%d", inv.ID, number, c.row.ID)
	receiptKey := base + "-receipt.json"
	if err := c.w.storeOnce(ctx, receiptKey, ev.ReceiptJSON, "application/json"); err != nil {
		c.w.logger().WarnContext(ctx, "invoices: a transmission's evidence could not be stored", "worker", ehfWorkerName,
			"transmission_id", c.row.ID, "error", err.Error())
		return retry("The document store could not store the evidence.")
	}
	if len(ev.Delivered) > 0 {
		mime := ev.DeliveredMIME
		if mime == "" {
			mime = "application/xml"
		}
		if err := c.w.storeOnce(ctx, base+"-delivered.xml", ev.Delivered, mime); err != nil {
			c.w.logger().WarnContext(ctx, "invoices: a transmission's delivered copy could not be stored", "worker", ehfWorkerName,
				"transmission_id", c.row.ID, "error", err.Error())
			return retry("The document store could not store the evidence.")
		}
	}
	sum := sha256.Sum256(ev.ReceiptJSON)
	n, err := c.q().SetEvidenceLeased(ctx, store.SetEvidenceLeasedParams{
		EvidenceObjectKey: &receiptKey, EvidenceSha256: ptr(hex.EncodeToString(sum[:])), ID: c.row.ID, LeaseID: &c.lease,
	})
	return c.done(ctx, "store the evidence of", n, err)
}

// storeOnce puts body under key unless something is there already.
func (w *EhfWorker) storeOnce(ctx context.Context, key string, body []byte, contentType string) error {
	exists, err := w.srv.objectExists(ctx, key)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return w.srv.objectPut(ctx, key, bytes.NewReader(body), contentType)
}

// flagRejected sets rejected_at, which meta reports: the operator must look at
// the key.
func (w *EhfWorker) flagRejected(ctx context.Context) {
	if err := store.New(w.deps.Pool).MarkAccessPointRejected(ctx, w.now()); err != nil {
		w.logger().ErrorContext(ctx, "invoices: flag the access point key as rejected", "worker", ehfWorkerName, "error", err.Error())
	}
}

// clearRejected forgets a refusal once the provider accepted the key again.
func (w *EhfWorker) clearRejected(ctx context.Context) {
	if err := store.New(w.deps.Pool).ClearAccessPointRejected(ctx); err != nil {
		w.logger().WarnContext(ctx, "invoices: clear the access point key's refusal", "worker", ehfWorkerName, "error", err.Error())
	}
}

func (w *EhfWorker) now() time.Time { return w.deps.Clock().UTC() }

func (w *EhfWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

// reasonOrNil is a reason as last_error stores it: redacted, NULL when empty.
func reasonOrNil(reason string) *string {
	if r := redactReason(reason); r != "" {
		return &r
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
