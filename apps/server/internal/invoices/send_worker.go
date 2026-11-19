package invoices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-send worker (invoices phase 6 design I36; plan
// E-1, E3, readings E3–E5): every send batch's jobs, one at a time under a row
// lease — the billing worker's shape (billing_worker.go). A claim takes the
// oldest job queued and due, or sending with its lease run out, commits, and
// carries it on a context bounded by the lease less a margin, under the
// batch's requester as its principal, so every send core records the
// requester as its sender.
//
// A job, in order: its document, issued and not delivered on any channel
// meanwhile (else skipped); the customer's billing profile and what the
// installation can send by, read on the pool; the channel rule
// (send_channel.go); then each channel it names, through the send's own core
// — e-mail, EHF, eFaktura — or, for print, the stored PDF, stored now when it
// never was, and the job left awaiting print. The outcome is written in a
// short transaction of its own, the document's lock first and then the job,
// with the event and the batch's end. A refusal is final; an unavailability
// (a 502 or a 503 but mail_failed) is tried again with backoff, five attempts
// in all.
//
// At most once for e-mail (reading E4): SMTP has no idempotency key and the
// delivery row follows the send, so a job reclaimed in sending that chose
// e-mail is sent when a delivery of its document was logged at or after the
// claim it lost, and uncertain otherwise — never mailed again by itself. An
// EHF or eFaktura reclaimed is handed over when its transmission exists, and
// tried again otherwise: the core's own check under the lock makes that safe.
// No core calls out of the process under a lock.

const (
	// sendWorkerName is the worker's name in the runner's logs.
	sendWorkerName = "invoices-send"
	// sendWorkerInterval is the poll cadence between cycles.
	sendWorkerInterval = 5 * time.Second
	// sendPerCycle is the most jobs one cycle carries. SMTP's pacing is the
	// mail driver's.
	sendPerCycle = 50
	// sendDefaultLease is the lease a claim takes.
	sendDefaultLease = 3 * time.Minute
	// sendMarkTimeout bounds each write of a claim's outcome, on a context
	// the worker's shutdown does not reach.
	sendMarkTimeout = 10 * time.Second
	// sendLeaseMargin is how much of the lease the job's context leaves
	// unused, so its outcome is written while the lease still holds: an
	// e-mail begun at the context's last moment runs uncancelled for its
	// SMTP send and its delivery row, then the outcome's write — all three
	// inside the margin, so a mail sent and logged never meets a reclaim
	// that would judge it uncertain.
	sendLeaseMargin = sendTimeout + deliveryRowTimeout + sendMarkTimeout + 15*time.Second
	// sendMaxAttempts is the attempts a job has against an unavailability.
	sendMaxAttempts = 5
)

// sendLease is the lease a claim takes. A test may shorten it.
var sendLease = sendDefaultLease

// errSendLeaseLost is a lease-checked write that found the job no longer
// under this claim.
var errSendLeaseLost = errors.New("invoices: the send job is no longer under this claim")

// SendWorker carries the send batches' jobs. It implements worker.Worker;
// Module.Workers registers it always — without mail it still fails its
// e-mail jobs honestly, and EHF, eFaktura and print do not need mail.
type SendWorker struct {
	srv    *server
	srvErr error
	deps   module.Deps
	// notified is when each batch's screen was last told of a job's outcome.
	notified runNotifier
}

var _ worker.Worker = (*SendWorker)(nil)

// NewSendWorker builds the worker over worker mode's Deps.
func NewSendWorker(d module.Deps) *SendWorker {
	srv, err := newServer(d)
	return &SendWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *SendWorker) Name() string { return sendWorkerName }

// Interval is the poll cadence between cycles.
func (w *SendWorker) Interval() time.Duration { return sendWorkerInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done.
func (w *SendWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(sendWorkerInterval)
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: a send worker cycle failed", "worker", sendWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle finishes the batches left unfinished, then carries at most
// sendPerCycle jobs, one at a time, and stops early when none is due.
func (w *SendWorker) RunCycle(ctx context.Context) error {
	if w.srvErr != nil {
		return w.srvErr
	}
	finished, err := store.New(w.deps.Pool).FinishIdleSendBatches(ctx, w.now())
	if err != nil {
		return fmt.Errorf("invoices: finish the send batches with nothing left: %w", err)
	}
	for _, id := range finished {
		w.notifyBatch(ctx, id)
	}
	for range sendPerCycle {
		if ctx.Err() != nil {
			return nil
		}
		processed, err := w.ProcessOne(ctx)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

// ProcessOne claims at most one due job and carries it to its outcome,
// answering whether a job was claimed.
func (w *SendWorker) ProcessOne(ctx context.Context) (bool, error) {
	if w.srvErr != nil {
		return false, w.srvErr
	}
	now := w.now()
	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	q := store.New(w.deps.Pool)
	row, err := q.ClaimSendJob(ctx, store.ClaimSendJobParams{LeaseID: lease, LeaseUntil: now.Add(sendLease), Now: now})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: claim a send job: %w", err)
	}
	c := &sendClaim{w: w, s: w.srv, row: row, lease: lease}
	batch, err := q.GetSendBatch(ctx, row.BatchID)
	if err != nil {
		return true, c.retry(ctx, fmt.Errorf("invoices: read send batch %d: %w", row.BatchID, err))
	}
	c.batch = batch
	w.notifyBatchProgress(ctx, row.BatchID)
	return true, c.carry(ctx)
}

// sendClaim is one claimed job: the row as the claim saw it, its batch and
// its lease.
type sendClaim struct {
	w     *SendWorker
	s     *server
	row   store.ClaimSendJobRow
	batch store.InvoicesSendBatch
	lease string
}

// jobOutcome is how a job ended, or that it waits: retry puts it back.
type jobOutcome struct {
	state          string
	channel        string
	code, detail   string
	transmissionID int64
	deliveryID     int64
	retry          bool
}

func jobEnd(state, code, detail string) jobOutcome {
	return jobOutcome{state: state, code: code, detail: detail}
}

// carry runs the job on a context bounded by the lease less its margin and
// carrying the batch's requester as the principal, and records its outcome
// on a context of its own.
func (c *sendClaim) carry(ctx context.Context) error {
	jobCtx, cancel := context.WithTimeout(ctx, max(sendLease-sendLeaseMargin, sendLeaseMargin))
	defer cancel()
	jobCtx = contracts.WithPrincipal(jobCtx, contracts.Principal{UserID: c.batch.RequestedBy})
	out, err := c.sendRecovered(jobCtx)
	switch {
	case errors.Is(err, errSendLeaseLost):
		c.w.logger().DebugContext(ctx, "invoices: a send job moved under the claim; nothing recorded",
			"worker", sendWorkerName, "job_id", c.row.ID)
		return nil
	case errors.Is(err, errMailedNotLogged):
		return c.complete(ctx, jobOutcome{state: jobUncertain, channel: channelEmail, code: codeSendUncertain,
			detail: "The e-mail was handed to the mail server, but its delivery could not be recorded. Check with the customer before sending it again."})
	case ctx.Err() != nil && err != nil:
		// The worker is stopping before anything left the process: the job
		// goes back uncounted.
		return c.release(ctx)
	case err != nil:
		return c.retry(ctx, err)
	case out.retry:
		return c.retryWith(ctx, out)
	}
	return c.complete(ctx, out)
}

// sendRecovered is send with a panic turned into an error, which counts an
// attempt as any error does.
func (c *sendClaim) sendRecovered(ctx context.Context) (out jobOutcome, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = jobOutcome{}, fmt.Errorf("invoices: sending document %d panicked: %v", c.row.InvoiceID, r)
		}
	}()
	return c.send(ctx)
}

// send is the job's steps up to its outcome.
func (c *sendClaim) send(ctx context.Context) (jobOutcome, error) {
	s, q, id := c.s, store.New(c.w.deps.Pool), c.row.InvoiceID

	// A job reclaimed mid-send is judged by what its lost attempt left.
	if c.row.PrevState == jobSending && c.row.Channel != nil {
		switch ch := *c.row.Channel; ch {
		case channelEmail:
			after := c.row.CreatedAt
			if c.row.PrevStartedAt != nil {
				after = *c.row.PrevStartedAt
			}
			delivery, err := q.EmailDeliveryAfter(ctx, store.EmailDeliveryAfterParams{InvoiceID: id, After: after})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return jobOutcome{state: jobUncertain, channel: ch, code: codeSendUncertain,
					detail: "The worker stopped while e-mailing this document, and no delivery was recorded. It may have gone: check with the customer before sending it again."}, nil
			case err != nil:
				return jobOutcome{}, fmt.Errorf("invoices: read document %d's e-mails: %w", id, err)
			}
			return jobOutcome{state: jobSent, channel: ch, deliveryID: delivery}, nil
		case channelEhf, channelEfaktura:
			t, err := q.LiveTransmissionOf(ctx, store.LiveTransmissionOfParams{InvoiceID: id, Channel: ch})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return jobOutcome{}, fmt.Errorf("invoices: read document %d's transmissions: %w", id, err)
			default:
				return jobOutcome{state: jobHandedOver, channel: ch, transmissionID: t}, nil
			}
		}
	}

	// The requester, still active and still allowed to send (the review's
	// finding 6): a batch outlives the session or the API key that made it.
	allowed, err := s.deps.Access.UserHolds(ctx, c.batch.RequestedBy, "invoices:access", "invoices:issue")
	if err != nil {
		return jobOutcome{}, fmt.Errorf("invoices: read whether send batch %d's requester may send: %w", c.batch.ID, err)
	}
	if !allowed {
		return jobEnd(jobSkipped, codeRequesterNotAllowed,
			"The person who asked for this Send all is no longer active or no longer may send documents."), nil
	}

	// The document, issued and not delivered since the batch was made.
	inv, err := q.GetInvoice(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return jobEnd(jobSkipped, codeSendDocumentGone, "The document no longer exists."), nil
	case err != nil:
		return jobOutcome{}, fmt.Errorf("invoices: read document %d: %w", id, err)
	case inv.Status != statusIssued:
		return jobEnd(jobSkipped, codeInvoiceDraft, "A draft is not a sales document: issue it before sending it."), nil
	}
	delivered, err := q.DeliveredDocuments(ctx, []int64{id})
	if err != nil {
		return jobOutcome{}, fmt.Errorf("invoices: read whether document %d is delivered: %w", id, err)
	}
	if len(delivered) > 0 {
		return jobEnd(jobSkipped, codeAlreadyDelivered, "The document was delivered meanwhile. Send it another way from the document itself."), nil
	}
	uncertain, err := q.UncertainDocuments(ctx, []int64{id})
	if err != nil {
		return jobOutcome{}, fmt.Errorf("invoices: read whether document %d may have been mailed: %w", id, err)
	}
	if len(uncertain) > 0 {
		return jobEnd(jobSkipped, codeSendUncertainElsewhere, uncertainElsewhereDetail), nil
	}

	// What the rule judges by, read on the pool.
	profile, err := s.customerProfile(ctx, inv.CustomerID)
	if err != nil {
		return jobOutcome{}, fmt.Errorf("invoices: read customer %d's billing profile: %w", inv.CustomerID, err)
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return jobOutcome{}, fmt.Errorf("invoices: read the settings: %w", err)
	}
	ehfOn, _, err := s.ehfAvailable(ctx, q, settings)
	if err != nil {
		return jobOutcome{}, err
	}
	efakturaOn, _, _, err := s.efakturaAvailable(ctx, q)
	if err != nil {
		return jobOutcome{}, err
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return jobOutcome{}, err
	}
	in := channelInput{
		creditNote:        inv.Kind == kindCreditNote,
		norwegianBusiness: inv.BuyerOrganisationNumber != nil && *inv.BuyerOrganisationNumber != "",
		today:             holidays.DayIn(s.deps.Clock(), loc),
		mailAvailable:     s.mailAvailable(ctx),
		ehfAvailable:      ehfOn,
		efakturaAvailable: efakturaOn,
	}
	if profile != nil {
		in.customerType, in.preference, in.hasInvoiceEmail = profile.Type, profile.InvoiceDelivery, profile.InvoiceEmail != ""
	}
	plan := chooseSendChannel(in)

	var last jobOutcome
	for _, ch := range plan.channels {
		if err := c.setChannel(ctx, ch); err != nil {
			return jobOutcome{}, err
		}
		out, refusal, err := c.sendBy(ctx, ch, inv)
		if err != nil {
			return jobOutcome{}, err
		}
		if refusal == nil {
			return out, nil
		}
		code := refusal.code()
		switch {
		case !plan.preferred && fallsThrough(ch, code):
			last = jobOutcome{state: jobFailed, channel: ch, code: code, detail: refusal.detail()}
			continue
		case code == codeAlreadyDelivered:
			return jobOutcome{state: jobSkipped, channel: ch, code: code, detail: refusal.detail()}, nil
		case code == codeEhfAlreadySent || code == codeEfakturaAlreadySent:
			if c.row.PrevState == jobSending {
				t, err := q.LiveTransmissionOf(ctx, store.LiveTransmissionOfParams{InvoiceID: id, Channel: ch})
				if err == nil {
					return jobOutcome{state: jobHandedOver, channel: ch, transmissionID: t}, nil
				}
			}
			return jobOutcome{state: jobSkipped, channel: ch, code: codeAlreadyDelivered,
				detail: "The document was sent meanwhile. Send it another way from the document itself."}, nil
		case retryable(ch, refusal):
			return jobOutcome{state: jobFailed, channel: ch, code: code, detail: refusal.detail(), retry: true}, nil
		}
		return jobOutcome{state: jobFailed, channel: ch, code: code, detail: refusal.detail()}, nil
	}
	if plan.endState != "" {
		return jobOutcome{state: plan.endState, channel: last.channel, code: plan.endCode, detail: endDetail(plan.endCode)}, nil
	}
	return last, nil
}

// sendBy sends the document by one channel through its core, or — print —
// stores its PDF and leaves it for printing.
func (c *sendClaim) sendBy(ctx context.Context, channel string, inv store.InvoicesInvoice) (jobOutcome, *sendRefusal, error) {
	ctx = withSendAll(ctx)
	switch channel {
	case channelEmail:
		sent, err := c.s.sendEmailCore(ctx, inv.ID, sendInput{})
		if err != nil || sent.refusal != nil {
			return jobOutcome{}, sent.refusal, err
		}
		return jobOutcome{state: jobSent, channel: channel, deliveryID: sent.deliveryID}, nil, nil
	case channelEhf:
		sent, err := c.s.sendEhfCore(ctx, inv.ID)
		if err != nil || sent.refusal != nil {
			return jobOutcome{}, sent.refusal, err
		}
		return jobOutcome{state: jobHandedOver, channel: channel, transmissionID: sent.transmissionID}, nil, nil
	case channelEfaktura:
		sent, err := c.s.sendEfakturaCore(ctx, inv.ID)
		if err != nil || sent.refusal != nil {
			return jobOutcome{}, sent.refusal, err
		}
		return jobOutcome{state: jobHandedOver, channel: channel, transmissionID: sent.transmissionID}, nil, nil
	}
	// Print: a person this module has anonymised is not written to again,
	// and the PDF is stored now, so the ZIP serves stored bytes only.
	q := store.New(c.w.deps.Pool)
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return jobOutcome{}, nil, fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	if erased {
		return jobOutcome{}, refusedWith(http.StatusConflict, conflict(codeCustomerAnonymised, cannotSendTitle,
			"This document's customer has been anonymised, and is not written to again.")), nil
	}
	if _, problem := c.s.loadStoredPDF(ctx, q, inv); problem != nil {
		r, err := pdfRefusal(problem)
		return jobOutcome{}, r, err
	}
	return jobOutcome{state: jobAwaitingPrint, channel: channel,
		detail: "Download the document for printing, post it, and mark it posted."}, nil, nil
}

// retryable is whether a refusal is an unavailability worth another attempt:
// a 502 or a 503 — the network, the provider, the store, mail switched off
// meanwhile — but mail_failed, which may have reached the receiver.
func retryable(channel string, r *sendRefusal) bool {
	if channel == channelEmail && problemCode(r.problem) == codeMailFailed {
		return false
	}
	return r.status == http.StatusBadGateway || r.status == http.StatusServiceUnavailable
}

// endDetail is the sentence of a job the channel rule ended.
func endDetail(code string) string {
	switch code {
	case warningBuyerNorwegianBusinessRequired:
		return "From 1 January 2027 an e-mailed PDF is not a lawful invoice to a Norwegian business, and it cannot receive EHF. Send it another way from the document itself."
	case codeEhfUnavailable:
		return "The customer prefers EHF, and this installation cannot send as EHF."
	case codeEfakturaUnavailable:
		return "The customer prefers eFaktura, and this installation cannot send eFaktura."
	case codeMailUnavailable:
		return sendingDisabled
	case codeNoInvoiceEmail:
		return "The customer has no invoice e-mail. Add one to the customer, or send it from the document."
	}
	return ""
}

// setChannel records the channel the job is about to go by.
func (c *sendClaim) setChannel(ctx context.Context, channel string) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendMarkTimeout)
	defer cancel()
	n, err := store.New(c.w.deps.Pool).SetSendJobChannel(wctx, store.SetSendJobChannelParams{
		Channel: channel, ID: c.row.ID, LeaseID: c.lease,
	})
	if err != nil {
		return fmt.Errorf("invoices: record send job %d's channel: %w", c.row.ID, err)
	}
	if n == 0 {
		return errSendLeaseLost
	}
	c.row.Channel = &channel
	return nil
}

// complete records the job's outcome, under its document's lock and then
// the job's, with its event and, when it was the last, the batch's end.
func (c *sendClaim) complete(ctx context.Context, out jobOutcome) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendMarkTimeout)
	defer cancel()
	finished := false
	err := c.s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := lockInvoice(ctx, txq, c.row.InvoiceID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("invoices: lock document %d: %w", c.row.InvoiceID, err)
		}
		if err := txq.LockSendBatch(ctx, c.row.BatchID); err != nil {
			return fmt.Errorf("invoices: lock send batch %d: %w", c.row.BatchID, err)
		}
		now := c.w.now()
		n, err := txq.CompleteSendJob(ctx, store.CompleteSendJobParams{
			State: out.state, Channel: nonEmpty(out.channel), Code: nonEmpty(out.code), Detail: out.detail,
			TransmissionID: nonZero(out.transmissionID), DeliveryID: nonZero(out.deliveryID),
			Now: now, ID: c.row.ID, LeaseID: c.lease,
		})
		if err != nil {
			return fmt.Errorf("invoices: record send job %d's outcome: %w", c.row.ID, err)
		}
		if n == 0 {
			return errSendLeaseLost
		}
		if err := events.Publish(ctx, tx, "invoices", contracts.SendJobFinished{
			JobID: c.row.ID, BatchID: c.row.BatchID, InvoiceID: c.row.InvoiceID, Channel: out.channel, State: out.state, Code: out.code,
		}, now); err != nil {
			return err
		}
		ended, err := txq.FinishSendBatch(ctx, store.FinishSendBatchParams{Now: now, ID: c.row.BatchID})
		if err != nil {
			return fmt.Errorf("invoices: finish send batch %d: %w", c.row.BatchID, err)
		}
		finished = ended > 0
		return nil
	})
	if errors.Is(err, errSendLeaseLost) {
		c.w.logger().DebugContext(ctx, "invoices: a send job moved under the claim; nothing recorded",
			"worker", sendWorkerName, "job_id", c.row.ID)
		return nil
	}
	if err != nil {
		return err
	}
	if finished {
		c.w.notified.forget(c.row.BatchID)
		c.w.notifyBatch(ctx, c.row.BatchID)
	} else {
		c.w.notifyBatchProgress(ctx, c.row.BatchID)
	}
	return nil
}

// retryWith puts the job back after an unavailability, due after a minute
// per attempt so far — or, on its last attempt, fails it with the refusal.
func (c *sendClaim) retryWith(ctx context.Context, out jobOutcome) error {
	if c.row.Attempts >= sendMaxAttempts {
		out.retry = false
		return c.complete(ctx, out)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendMarkTimeout)
	defer cancel()
	n, err := store.New(c.w.deps.Pool).RetrySendJob(ctx, store.RetrySendJobParams{
		Code: out.code, Detail: out.detail, NotBefore: c.w.now().Add(time.Duration(c.row.Attempts) * time.Minute),
		ID: c.row.ID, LeaseID: c.lease,
	})
	if err == nil && n > 0 {
		c.w.notifyBatchProgress(ctx, c.row.BatchID)
	}
	return c.done(ctx, "reschedule", n, err)
}

// retry is retryWith for an error: logged, never recorded.
func (c *sendClaim) retry(ctx context.Context, cause error) error {
	c.w.logger().WarnContext(ctx, "invoices: a send job failed; it is tried again",
		"worker", sendWorkerName, "job_id", c.row.ID, "batch_id", c.row.BatchID, "attempt", c.row.Attempts, "error", cause.Error())
	return c.retryWith(ctx, jobOutcome{state: jobFailed, channel: deref(c.row.Channel), code: codeSendError,
		detail: "The document could not be sent after five attempts; the server log names the error."})
}

// release puts the claimed job back, due at once and uncounted.
func (c *sendClaim) release(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendMarkTimeout)
	defer cancel()
	n, err := store.New(c.w.deps.Pool).ReleaseSendJob(ctx, store.ReleaseSendJobParams{ID: c.row.ID, LeaseID: c.lease})
	return c.done(ctx, "release", n, err)
}

// done reports a lease-checked write: 0 rows is another actor's move.
func (c *sendClaim) done(ctx context.Context, what string, n int64, err error) error {
	if err != nil {
		return fmt.Errorf("invoices: %s send job %d: %w", what, c.row.ID, err)
	}
	if n == 0 {
		c.w.logger().DebugContext(ctx, "invoices: a send job moved under the claim; nothing recorded",
			"worker", sendWorkerName, "job_id", c.row.ID, "step", what)
	}
	return nil
}

// nonZero is an id as a nullable column: nil for 0.
func nonZero(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// notifyBatch is a batch's state change: always sent, on the pool, a failure
// logged — the screens' fallback poll repairs a lost event.
func (w *SendWorker) notifyBatch(ctx context.Context, id int64) {
	if err := notifySendBatch(ctx, w.deps.Pool, id); err != nil {
		w.logger().WarnContext(ctx, "invoices: notify a send batch", "worker", sendWorkerName, "batch_id", id, "error", err)
	}
}

// notifyBatchProgress is notifyBatch for a job's outcome, at most once a
// second per batch (the billing runs' throttle).
func (w *SendWorker) notifyBatchProgress(ctx context.Context, id int64) {
	ctx = context.WithoutCancel(ctx)
	w.notified.throttle(id, func() { w.notifyBatch(ctx, id) })
}

func (w *SendWorker) now() time.Time { return w.deps.Clock().UTC() }

func (w *SendWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
