package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-reminders worker (invoices payments and
// reminders design D10): every letter a run queued for e-mail is sent by
// it, one at a time, under a row lease — the shape of the invoices-ehf
// worker (ehf_worker.go). A claim takes one queued letter due by its
// next_attempt_at, then:
//
//  1. one transaction — the invoice FOR UPDATE, then the letter FOR NO KEY
//     UPDATE (D18), re-read: still queued under this claim's lease, else the
//     claim ends — judges it again on today, the claim's one clock read,
//     with the letter left out of the letters in flight
//     (judgeAndWriteFacts). Outdated rates or an unreviewed regime: the
//     letter waits an hour, its held_reason saying why, the attempt not
//     counted (plan readings 17, 46). Paid meanwhile, held, handed off, its
//     customer's policy none or the customer anonymised, or another action
//     or level: withdrawn with the reason. Otherwise its facts are written —
//     sent_on today, the deadline from it, the amounts: the facts are the
//     sending's, never the run's (amendment 12);
//  2. the PDF rendered from the row and stored once under a key carrying
//     its hash (plan reading 35), outside any lock, under its own timeout;
//  3. the lease left judged — a claim with less of it than the send may take
//     and a margin does not send — then the mail, through the installation's
//     SMTP seam under its own timeout, Reply-To the seller, the letter's
//     Message-ID made at its first claim (plan reading 52);
//  4. one transaction — the invoice, then the letter — marks it sent, and
//     when a hold or a lift that barred charges came while it was being
//     sent, waives its fee and compensation claimed_in_error (Task 11's
//     review).
//
// A failure after step 1 counts the attempt, clears the lease and the facts
// — so the letter is never "being sent" under no live claim (plan reading
// 45) — and backs off; a letter still unsent 48 hours after its first
// attempt is failed. Every call out of the module is made outside any
// transaction; every write of a claim names its lease.

const (
	// reminderWorkerName is what the runner logs this worker as.
	reminderWorkerName = "invoices-reminders"
	// reminderWorkerInterval is the poll cadence between cycles.
	reminderWorkerInterval = 5 * time.Second
	// reminderPerCycle and reminderPace: a cycle handles at most five
	// letters, one at a time and at most one a second (plan reading 16).
	reminderPerCycle = 5
	reminderPace     = time.Second
	// reminderDefaultLease is how long a claim keeps a letter from every
	// other worker. Nothing renews it.
	reminderDefaultLease = 60 * time.Second
	// reminderSendTimeout and reminderStoreTimeout bound the SMTP send and
	// the store, each under half the lease (ehf_worker.go's rule).
	reminderSendTimeout  = 20 * time.Second
	reminderStoreTimeout = 15 * time.Second
	// reminderSendMargin is what the lease must still hold beyond the send's
	// own timeout when the send starts: the letter is "being sent" — not
	// withdrawable — until it is marked sent, so the send and the mark must
	// both end inside the lease.
	reminderSendMargin = 10 * time.Second
	// reminderMarkTimeout bounds a completion's transaction, on a context
	// the worker's shutdown does not reach: a letter the mail server took
	// must still be marked sent.
	reminderMarkTimeout = 10 * time.Second
	// reminderHeldDelay is how long a letter waits on its rates or the
	// regime review before it is judged again.
	reminderHeldDelay = time.Hour
	// reminderFailAfter is how long after its first attempt a letter may
	// stay unsent before it is failed.
	reminderFailAfter = 48 * time.Hour

	// The reasons a failed attempt records (last_error).
	reasonLetterNotStored   = "The document store could not store the letter."
	reasonLetterNotRendered = "The letter could not be rendered."
	reasonLetterOutOfTime   = "The claim ran out of time before the send; it is retried."
	reasonLetterNotMailed   = "The mail server did not accept the letter."
	reasonMailUnavailable   = "This installation cannot send e-mail: MAIL_DRIVER is not smtp."
	reasonNoRecipient       = "The letter has no recipient."

	// waiverClaimedInError is the reason of the waivers a letter sent under
	// a hold or a barring lift gets (D9).
	waiverClaimedInError = "claimed_in_error"
)

// The reasons the dispatch's re-judge writes on the letters it withdraws
// (D10, plan reading 44), beside holds.go's on_hold and handed_off.
const (
	withdrawnSettled       = "settled"
	withdrawnPolicyNone    = "policy_none"
	withdrawnAnonymised    = "customer_anonymised"
	withdrawnActionChanged = "action_changed"
)

// reminderLease is the lease a claim takes. A test shortens it
// (export_test.go) to reach the lease-left check without waiting.
var reminderLease = reminderDefaultLease

// dispatchAfterLock is called inside the dispatch's first transaction right
// after it has locked the invoice and before it locks the letter, with the
// letter's id, so a race test can hold the dispatch there while a hold, a
// hand-off, a match or a withdrawal comes; an error rolls the step back and
// ends the claim. nil in production.
var dispatchAfterLock func(ctx context.Context, reminderID int64) error

// newReminderMessageID is a letter's Message-ID, bare as mail.Outbound takes
// it: made once per letter at its first claim and kept on every retry, so a
// mail sent twice is recognisably one, and never shared between
// installations (plan reading 52).
func newReminderMessageID() string {
	return "reminder-" + uuid.NewString() + "@" + messageIDDomain
}

// reminderBackoff is the wait after the n-th failed attempt: 2^n seconds, at
// most an hour (D10) — the EHF worker's formula.
func reminderBackoff(n int32) time.Duration { return ehfBackoff(n) }

// ReminderWorker sends the reminder letters queued for e-mail. It implements
// worker.Worker; Module.Workers registers it always — a letter is queued only
// while reminders are on and mail is available, and the claim judges again.
type ReminderWorker struct {
	srv *server
	// srvErr is newServer's failure — an object store the configuration
	// cannot build — kept so the worker still starts and every cycle says
	// why it does nothing.
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*ReminderWorker)(nil)

// NewReminderWorker builds the worker over worker mode's Deps: the pool, the
// clock, the configuration (its mail settings) and an object store built from
// it. It needs no directory: the recipient was read when the letter was made.
func NewReminderWorker(d module.Deps) *ReminderWorker {
	srv, err := newServer(d)
	return &ReminderWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *ReminderWorker) Name() string { return reminderWorkerName }

// Interval is the poll cadence between cycles.
func (w *ReminderWorker) Interval() time.Duration { return reminderWorkerInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done. A
// failing cycle is logged and the loop goes on.
func (w *ReminderWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(reminderWorkerInterval)
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: a reminder worker cycle failed", "worker", reminderWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle sends at most reminderPerCycle letters, one at a time, at most one
// a second, and stops early when none is due.
func (w *ReminderWorker) RunCycle(ctx context.Context) error {
	for i := range reminderPerCycle {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(reminderPace):
			}
		}
		processed, err := w.ProcessOne(ctx)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

// ProcessOne claims at most one due letter and carries it as far as it goes,
// answering whether a letter was claimed.
func (w *ReminderWorker) ProcessOne(ctx context.Context) (bool, error) {
	if w.srvErr != nil {
		return false, w.srvErr
	}
	now := w.now() // the claim's one clock read (D18)
	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	leaseUntil := now.Add(reminderLease)
	row, err := store.New(w.deps.Pool).ClaimReminder(ctx, store.ClaimReminderParams{
		LeaseID: lease, LeaseUntil: leaseUntil, MessageID: newReminderMessageID(), Now: now,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: claim a reminder letter: %w", err)
	}
	c := &reminderClaim{w: w, s: w.srv, row: row, lease: lease, leaseUntil: leaseUntil, now: now, today: businessDay(now)}
	return true, c.dispatch(ctx)
}

// reminderClaim is one claimed letter: the row as the claim saw it, its
// lease and the claim's time and day.
type reminderClaim struct {
	w          *ReminderWorker
	s          *server
	row        store.InvoicesReminder
	lease      string
	leaseUntil time.Time
	now, today time.Time
	// first is the letter's first attempt once step 1 stamped it.
	first *time.Time
}

// leaseLeft is how much of the claim's lease the module clock says remains.
func (c *reminderClaim) leaseLeft() time.Duration { return c.leaseUntil.Sub(c.w.now()) }

// dispatch is D10's four steps for the claimed letter.
func (c *reminderClaim) dispatch(ctx context.Context) error {
	j, inv, err := c.judge(ctx)
	if err != nil || j.outcome != judgedSend {
		return err
	}
	letter := j.letter
	c.first = letter.FirstAttemptAt

	// 2. The PDF, from the row, stored once — no transaction open.
	q := store.New(c.w.deps.Pool)
	m, body, err := c.s.renderLetter(ctx, q, letter, inv)
	if err != nil {
		if failErr := c.fail(ctx, reasonLetterNotRendered); failErr != nil {
			return failErr
		}
		return err
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	key := reminderKey(inv.ID, letter.ID, utcDay(letter.SentOn.Time), sha)
	if err := c.store(ctx, key, body); err != nil {
		c.w.logger().WarnContext(ctx, "invoices: a reminder letter could not be stored", "worker", reminderWorkerName,
			"reminder_id", letter.ID, "error", err.Error())
		return c.fail(ctx, reasonLetterNotStored)
	}
	n, err := q.SetReminderPDF(ctx, store.SetReminderPDFParams{PdfObjectKey: key, PdfSha256: sha, ID: letter.ID, LeaseID: c.lease})
	if err != nil || n == 0 {
		return c.done(ctx, "record the PDF of", n, err)
	}

	// 3. The mail: only while the lease still covers the send and its mark.
	switch {
	case !c.s.mailAvailable():
		return c.fail(ctx, reasonMailUnavailable)
	case letter.Recipient == "":
		return c.fail(ctx, reasonNoRecipient)
	case c.leaseLeft() < reminderSendTimeout+reminderSendMargin:
		return c.fail(ctx, reasonLetterOutOfTime)
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("invoices: read the settings: %w", err)
	}
	out := mail.Outbound{
		DisplayName: deref(inv.SellerLegalName), To: []string{letter.Recipient}, Subject: m.subject,
		ReplyTo: settings.Email, TextBody: m.cover, MessageID: deref(letter.MessageID),
		Attachments: []mail.Attachment{{FileName: m.fileName, ContentType: "application/pdf", Content: body}},
	}
	// A shutdown must not abort a transfer the mail server may already have
	// accepted: the send's own timeout bounds it.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reminderSendTimeout)
	err = c.s.smtpSend(sendCtx, c.s.deps.Config.Mail, out)
	cancel()
	if err != nil {
		// The address taken out: a mail server's refusal often quotes it.
		c.w.logger().WarnContext(ctx, "invoices: a reminder letter could not be mailed; retrying", "worker", reminderWorkerName,
			"reminder_id", letter.ID, "error", withoutRecipient(err.Error(), letter.Recipient))
		return c.fail(ctx, reasonLetterNotMailed)
	}

	// 4. Sent.
	return c.markSent(ctx)
}

// judge is step 1 under the invoice's lock, then the letter's: the letter
// re-read and judged again, then held, withdrawn or given its facts. It
// answers the judgement — judgedGone when the letter moved under the claim —
// and the invoice as locked.
func (c *reminderClaim) judge(ctx context.Context) (judgement, store.InvoicesInvoice, error) {
	var j judgement
	var inv store.InvoicesInvoice
	err := c.s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		if inv, err = lockInvoice(ctx, txq, c.row.InvoiceID); err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", c.row.InvoiceID, err)
		}
		if hook := dispatchAfterLock; hook != nil {
			if err := hook(ctx, c.row.ID); err != nil {
				return err
			}
		}
		letter, err := lockReminder(ctx, txq, c.row.ID)
		if err != nil {
			return fmt.Errorf("invoices: lock letter %d: %w", c.row.ID, err)
		}
		if letter.Status != reminderrules.StatusQueued || deref(letter.LeaseID) != c.lease {
			// Withdrawn meanwhile, or claimed again after this lease ran out.
			j.outcome = judgedGone
			return nil
		}
		if j, err = c.s.judgeAndWriteFacts(ctx, txq, inv, letter, c.today, c.now); err != nil {
			return err
		}
		var n int64
		switch j.outcome {
		case judgedHeld:
			n, err = txq.RescheduleReminderUncounted(ctx, store.RescheduleReminderUncountedParams{
				NextAttemptAt: c.now.Add(reminderHeldDelay), HeldReason: j.reason, ID: letter.ID, LeaseID: c.lease,
			})
		case judgedWithdraw:
			n, err = txq.WithdrawClaimedReminder(ctx, store.WithdrawClaimedReminderParams{
				WithdrawnAt: c.now, WithdrawalReason: j.reason, ID: letter.ID, LeaseID: c.lease,
			})
		default:
			return nil
		}
		if err != nil {
			return fmt.Errorf("invoices: settle letter %d: %w", letter.ID, err)
		}
		if n != 1 {
			return fmt.Errorf("invoices: letter %d moved under its own locks", letter.ID)
		}
		return nil
	})
	if err != nil {
		return judgement{}, inv, err
	}
	switch j.outcome {
	case judgedGone:
		c.w.logger().DebugContext(ctx, "invoices: a reminder letter moved under the claim; nothing sent",
			"worker", reminderWorkerName, "reminder_id", c.row.ID)
	case judgedHeld:
		c.w.logger().InfoContext(ctx, "invoices: a reminder letter waits on its rates or the regime review",
			"worker", reminderWorkerName, "reminder_id", c.row.ID, "held_reason", j.reason)
	case judgedWithdraw:
		c.w.logger().InfoContext(ctx, "invoices: a reminder letter was withdrawn at its sending",
			"worker", reminderWorkerName, "reminder_id", c.row.ID, "reason", j.reason)
	}
	return j, inv, nil
}

// judgementOutcome is what judgeAndWriteFacts decided about a letter.
type judgementOutcome int

const (
	// judgedSend: the letter goes; its facts are written.
	judgedSend judgementOutcome = iota
	// judgedHeld: a rate it needs is missing or the regime is unreviewed;
	// reason is the held_reason.
	judgedHeld
	// judgedWithdraw: the letter no longer goes; reason is the withdrawal's.
	judgedWithdraw
	// judgedGone: the dispatch's own — the letter moved under the claim.
	judgedGone
)

// judgement is judgeAndWriteFacts' answer: the outcome, its reason, the
// missing rate when held for one, and the letter — with its facts when it
// goes.
type judgement struct {
	outcome  judgementOutcome
	reason   string
	outdated *reminderrules.OutdatedRate
	letter   store.InvoicesReminder
}

// judgeAndWriteFacts is step 1's judgement, shared with the print batch: the
// letter judged again on day L under its invoice's lock and its own (the
// caller took both, D18), and its facts written when it still goes, at now.
// The customer anonymised, or the engine — the letter left out of the
// letters in flight — no longer giving this letter's level (paid, handed
// off, held, the policy none, another action) answers judgedWithdraw with
// D10's reason; a rate missing or the regime unreviewed answers judgedHeld.
// It never reschedules, withdraws or fails: the caller acts on the answer.
func (s *server) judgeAndWriteFacts(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice, letter store.InvoicesReminder, L, now time.Time) (judgement, error) {
	erased, err := txq.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return judgement{}, fmt.Errorf("invoices: read whether customer %d is anonymised: %w", inv.CustomerID, err)
	}
	if erased {
		return judgement{outcome: judgedWithdraw, reason: withdrawnAnonymised}, nil
	}
	in, err := s.ruleInputLocked(ctx, txq, inv, L, letter.ID)
	if err != nil {
		return judgement{}, err
	}
	out := reminderrules.Next(in)
	withdraw := func(reason string) (judgement, error) {
		return judgement{outcome: judgedWithdraw, reason: reason}, nil
	}
	switch {
	case out.Action == reminderrules.ActionNone && slices.Contains(out.Reasons, reminderrules.ReasonHandedOff):
		return withdraw(withdrawnHandedOff)
	case out.Action == reminderrules.ActionNone:
		return withdraw(withdrawnSettled)
	case slices.Contains(out.Reasons, reminderrules.ReasonOnHold):
		return withdraw(withdrawnOnHold)
	case slices.Contains(out.Reasons, reminderrules.ReasonPolicyNone):
		return withdraw(withdrawnPolicyNone)
	case out.Outdated != nil:
		return judgement{outcome: judgedHeld, reason: reminderrules.ReasonRatesOutdated, outdated: out.Outdated}, nil
	case slices.Contains(out.Reasons, reminderrules.ReasonRegimeUnreviewed):
		return judgement{outcome: judgedHeld, reason: reminderrules.ReasonRegimeUnreviewed}, nil
	case out.Letter == nil || string(out.Letter.Level) != letter.Level || out.Letter.AnnouncesCollection != letter.AnnouncesCollection:
		return withdraw(withdrawnActionChanged)
	}
	params, err := letterFactsParams(letter.ID, out.Letter, out.ChargeNotes, L, now)
	if err != nil {
		return judgement{}, err
	}
	written, err := txq.WriteLetterFacts(ctx, params)
	if err != nil {
		return judgement{}, fmt.Errorf("invoices: write letter %d's facts: %w", letter.ID, err)
	}
	return judgement{outcome: judgedSend, letter: written}, nil
}

// letterFactsParams is the engine's letter on day L as the row stores it:
// amounts as numeric(14,2), the fee and the compensation only when claimed,
// the segments as interestSegment JSON, the charge notes — none is an empty
// array.
func letterFactsParams(id int64, f *reminderrules.LetterFacts, notes []string, L, now time.Time) (store.WriteLetterFactsParams, error) {
	p := store.WriteLetterFactsParams{
		ID: id, SentOn: pgDate(L), Deadline: pgDate(f.Deadline), Regime: string(f.Regime), FeeKind: string(f.FeeKind),
		ChargeNotes: append([]string{}, notes...), Now: now,
	}
	for _, a := range []struct {
		dst *pgtype.Numeric
		v   *big.Rat
	}{
		{&p.PrincipalOpen, f.PrincipalOpen}, {&p.ChargesEarlier, f.ChargesEarlier}, {&p.Interest, f.Interest},
		{&p.InterestWaived, f.InterestWaived}, {&p.InterestPaid, f.InterestPaid}, {&p.Total, f.Total},
	} {
		n, err := numericFromRat(a.v, 2)
		if err != nil {
			return p, err
		}
		*a.dst = n
	}
	var err error
	if f.FeeKind == reminderrules.FeeReminder {
		if p.Fee, err = numericFromRat(f.Fee, 2); err != nil {
			return p, err
		}
	}
	if f.FeeKind == reminderrules.FeeCompensation {
		if p.Compensation, err = numericFromRat(f.Compensation, 2); err != nil {
			return p, err
		}
	}
	if f.Inkassosats != nil {
		if p.Inkassosats, err = numericFromRat(f.Inkassosats, 2); err != nil {
			return p, err
		}
	}
	if f.InterestFrom != nil {
		p.InterestFrom = pgDate(*f.InterestFrom)
	}
	segs := make([]interestSegment, 0, len(f.Segments))
	for _, s := range f.Segments {
		segs = append(segs, interestSegment{
			From: s.From.Format(time.DateOnly), To: s.To.Format(time.DateOnly),
			Rate: decimalText(s.Rate, 2), Base: decimalText(s.Base, 2),
		})
	}
	if p.InterestSegments, err = json.Marshal(segs); err != nil {
		return p, fmt.Errorf("invoices: letter %d's interest segments: %w", id, err)
	}
	return p, nil
}

// renderLetter is a letter's model and PDF, from its row and its invoice's
// snapshots and the credit notes issued by its day, read with q — on the
// pool, never under a lock.
func (s *server) renderLetter(ctx context.Context, q *store.Queries, letter store.InvoicesReminder, inv store.InvoicesInvoice) (reminderModel, []byte, error) {
	n, err := q.CreditedOn(ctx, store.CreditedOnParams{InvoiceID: inv.ID, Day: letter.SentOn})
	if err != nil {
		return reminderModel{}, nil, fmt.Errorf("invoices: read what was credited of document %d: %w", inv.ID, err)
	}
	credited, err := ratFromNumeric(n)
	if err != nil {
		return reminderModel{}, nil, err
	}
	m, err := reminderModelOf(letter, inv, credited)
	if err != nil {
		return reminderModel{}, nil, err
	}
	body, err := renderReminderPDF(m)
	if err != nil {
		return reminderModel{}, nil, err
	}
	return m, body, nil
}

// store puts body under key unless something is there already: the key
// carries the hash of the bytes, so what is there is these bytes. Both calls
// are bounded together by reminderStoreTimeout.
func (c *reminderClaim) store(ctx context.Context, key string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, reminderStoreTimeout)
	defer cancel()
	exists, err := c.s.objectExists(ctx, key)
	if err != nil || exists {
		return err
	}
	return c.s.objectPut(ctx, key, bytes.NewReader(body), "application/pdf")
}

// markSent is step 4 under the invoice's lock, then the letter's: the letter
// sent at the claim's time when it is still this claim's, and — when the
// invoice was put on hold, or a lift barred its charges, while it was being
// sent (Task 11's review; plan reading 45) — its fee and compensation waived
// claimed_in_error in the same transaction, after the status change, since
// a waiver names a sent letter. The waivers are the letter's author's: the
// run that made it, as the module has no user of its own.
func (c *reminderClaim) markSent(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reminderMarkTimeout)
	defer cancel()
	moved := false
	var waived []string
	err := c.s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		inv, err := lockInvoice(ctx, txq, c.row.InvoiceID)
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", c.row.InvoiceID, err)
		}
		letter, err := lockReminder(ctx, txq, c.row.ID)
		if err != nil {
			return fmt.Errorf("invoices: lock letter %d: %w", c.row.ID, err)
		}
		if letter.Status != reminderrules.StatusQueued || deref(letter.LeaseID) != c.lease {
			moved = true
			return nil
		}
		in, err := c.s.ruleInputLocked(ctx, txq, inv, c.today, letter.ID)
		if err != nil {
			return err
		}
		sent, err := txq.MarkReminderSent(ctx, store.MarkReminderSentParams{SentAt: c.now, ID: letter.ID, LeaseID: c.lease})
		if err != nil {
			return fmt.Errorf("invoices: mark letter %d sent: %w", letter.ID, err)
		}
		if !in.ChargesBarred && !in.OnHold {
			return nil
		}
		var ws []waiverRequest
		for _, charge := range []struct {
			kind string
			n    pgtype.Numeric
		}{{reminderrules.WaiverFee, sent.Fee}, {reminderrules.WaiverCompensation, sent.Compensation}} {
			v, err := ratOrNil(charge.n)
			if err != nil {
				return err
			}
			if v != nil && v.Sign() > 0 {
				ws = append(ws, waiverRequest{reminderID: sent.ID, kind: charge.kind})
				waived = append(waived, charge.kind)
			}
		}
		if len(ws) == 0 {
			return nil
		}
		note := "Sent while the invoice was on hold."
		if in.ChargesBarred {
			note = "Sent after a lift that barred charges on the invoice."
		}
		problem, err := waiveCharges(ctx, txq, inv.ID, ws, waiverClaimedInError, note, sent.CreatedByUserID, c.now)
		if err != nil {
			return err
		}
		if problem != nil {
			return fmt.Errorf("invoices: waive letter %d's charges: %s", sent.ID, deref(problem.Detail))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("invoices: complete letter %d: %w", c.row.ID, err)
	}
	if moved {
		return c.done(ctx, "mark sent", 0, nil)
	}
	c.w.logger().InfoContext(ctx, "invoices: a reminder letter was sent", "worker", reminderWorkerName,
		"reminder_id", c.row.ID, "waived", waived)
	return nil
}

// fail counts a failed attempt — or fails the letter once 48 hours have
// passed since its first — and clears the lease and the facts, on a context
// the worker's shutdown does not reach.
func (c *reminderClaim) fail(ctx context.Context, reason string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reminderMarkTimeout)
	defer cancel()
	q := store.New(c.w.deps.Pool)
	first := c.now
	if c.first != nil {
		first = *c.first
	}
	if !c.now.Before(first.Add(reminderFailAfter)) {
		n, err := q.MarkReminderFailed(ctx, store.MarkReminderFailedParams{
			FailedAt: c.now, LastError: reasonOrNil(reason), ID: c.row.ID, LeaseID: c.lease,
		})
		if err == nil && n == 1 {
			c.w.logger().WarnContext(ctx, "invoices: a reminder letter failed: unsent 48 hours after its first attempt",
				"worker", reminderWorkerName, "reminder_id", c.row.ID, "reason", reason)
		}
		return c.done(ctx, "fail", n, err)
	}
	n, err := q.FailReminderAttempt(ctx, store.FailReminderAttemptParams{
		NextAttemptAt: c.now.Add(reminderBackoff(c.row.Attempts + 1)), LastError: reasonOrNil(reason), Now: c.now,
		ID: c.row.ID, LeaseID: c.lease,
	})
	return c.done(ctx, "reschedule", n, err)
}

// done reports a lease-checked write: 0 rows is another actor's move — a
// withdrawal after the lease ran out, a newer claim — and only worth a debug
// line.
func (c *reminderClaim) done(ctx context.Context, what string, n int64, err error) error {
	if err != nil {
		return fmt.Errorf("invoices: %s letter %d: %w", what, c.row.ID, err)
	}
	if n == 0 {
		c.w.logger().DebugContext(ctx, "invoices: a reminder letter moved under the claim; nothing recorded",
			"worker", reminderWorkerName, "reminder_id", c.row.ID, "step", what)
	}
	return nil
}

func (w *ReminderWorker) now() time.Time { return w.deps.Clock().UTC() }

func (w *ReminderWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
