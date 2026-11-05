package orders

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the orders.mail worker (O25): every automatic send — the
// order confirmation the orders.queue_confirmation handler queues when the
// settings say so, and later a booking's — is a queued row of orders.mailings,
// written on its producer's own transaction and sent here. A cycle claims one
// queued row at a time under a lease (ClaimMailing: a SKIP LOCKED pick that
// pushes next_attempt_at out by the lease, so two workers never take one row
// and a worker that dies leaves it claimable again), then — outside any
// transaction — reads the document's stored PDF, sends it through
// contracts.OutboundMail on the settings' channel, and marks the row sent; a
// failure counts the attempt and backs off, and a row that failed
// mailMaxAttempts times, or that can never be sent, is failed.
//
// Every statement runs on its own pooled connection and returns it before the
// next is taken, and no call out of the module is made while a connection is
// held: the worker is safe on a pool of one.

const (
	// mailWorkerName is what the runner logs this worker as.
	mailWorkerName = "orders.mail"
	// mailWorkerInterval is the poll cadence between cycles.
	mailWorkerInterval = 10 * time.Second
	// mailPerCycle bounds one cycle.
	mailPerCycle = 10
	// mailLease is how long a claim keeps a row from every other worker; the
	// send's timeout is well inside it.
	mailLease = 2 * time.Minute
	// mailMaxAttempts is how many failed attempts fail a row for good.
	mailMaxAttempts = 8
	// mailStepTimeout bounds each database step of a claim.
	mailStepTimeout = 10 * time.Second
)

// The reasons a failed attempt records (last_error).
const (
	reasonSendingDisabled = "Sending is disabled: Communications is off, or Orders settings choose no channel that can send."
	reasonNoDocument      = "The document's PDF is not stored yet."
	reasonNotStored       = "The stored PDF could not be read."
	reasonNotMailed       = "The mail provider did not take the e-mail."
	reasonGone            = "The document is gone."
)

// mailBackoff is the wait after the n-th failed attempt: 30 seconds doubled
// per attempt, at most an hour.
func mailBackoff(attempts int32) time.Duration {
	d := 30 * time.Second
	for i := int32(1); i < attempts && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}

// workers are the module's background workers (module.Module.Workers).
func workers(d module.Deps) []worker.Worker {
	s, err := newServer(d)
	if err != nil {
		if d.Logger != nil {
			d.Logger.Error("orders: the mail worker cannot start", "error", err.Error())
		}
		return nil
	}
	return []worker.Worker{&mailWorker{s: s}}
}

// mailWorker sends the queued mailings.
type mailWorker struct{ s *server }

func (w *mailWorker) Name() string            { return mailWorkerName }
func (w *mailWorker) Interval() time.Duration { return mailWorkerInterval }

// Run runs a cycle every interval until ctx is done.
func (w *mailWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(mailWorkerInterval)
	defer ticker.Stop()
	for {
		w.cycle(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// cycle sends up to mailPerCycle queued rows, one at a time, and answers how
// many it claimed.
func (w *mailWorker) cycle(ctx context.Context) int {
	n := 0
	for ; n < mailPerCycle && ctx.Err() == nil; n++ {
		claimed, err := w.claimOne(ctx)
		if err != nil {
			w.s.deps.Logger.WarnContext(ctx, "orders: the mail worker could not claim a mailing", "error", err.Error())
			return n
		}
		if !claimed {
			return n
		}
	}
	return n
}

// claimOne claims the next due row and handles it; false when none is due.
func (w *mailWorker) claimOne(ctx context.Context) (bool, error) {
	s := w.s
	now := s.deps.Clock()
	stepCtx, cancel := context.WithTimeout(ctx, mailStepTimeout)
	row, err := store.New(s.deps.Pool).ClaimMailing(stepCtx, store.ClaimMailingParams{
		Now: now, LeaseUntil: now.Add(mailLease), MessageID: newMessageID(),
	})
	cancel()
	if noRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	w.handle(ctx, row)
	return true, nil
}

// handle sends one claimed row and records the outcome under its lease.
func (w *mailWorker) handle(ctx context.Context, row store.OrdersMailing) {
	s := w.s
	documentID, channel, subject, reason, terminal := w.send(ctx, row)
	done := context.WithoutCancel(ctx)
	stepCtx, cancel := context.WithTimeout(done, mailStepTimeout)
	defer cancel()
	q := store.New(s.deps.Pool)
	if reason == "" {
		if _, err := q.MarkMailingSent(stepCtx, store.MarkMailingSentParams{
			ID: row.ID, Now: ptr(s.deps.Clock()), LeaseUntil: *row.NextAttemptAt, DocumentID: documentID, ChannelID: channel, Subject: subject,
		}); err != nil {
			s.deps.Logger.ErrorContext(ctx, "orders: a mailing was sent but could not be marked", "mailing_id", row.ID, "error", err.Error())
		}
		return
	}
	attempts := row.Attempts + 1
	fail := terminal || attempts >= mailMaxAttempts
	if _, err := q.RetryMailing(stepCtx, store.RetryMailingParams{
		ID: row.ID, LastError: reason, Fail: fail, NextAttemptAt: s.deps.Clock().Add(mailBackoff(attempts)), LeaseUntil: *row.NextAttemptAt,
	}); err != nil {
		s.deps.Logger.ErrorContext(ctx, "orders: a mailing's failed attempt could not be recorded", "mailing_id", row.ID, "error", err.Error())
	}
	s.deps.Logger.WarnContext(ctx, "orders: a queued mailing was not sent", "mailing_id", row.ID, "attempts", attempts,
		"failed", fail, "reason", reason)
}

// send makes one attempt at a claimed row, outside any transaction: the
// document's stored PDF, the cover mail from the document's own row, the
// send. reason is "" when it went out; terminal says the row can never be
// sent.
func (w *mailWorker) send(ctx context.Context, row store.OrdersMailing) (documentID *int64, channel *uuid.UUID, subject, reason string, terminal bool) {
	s := w.s
	q := store.New(s.deps.Pool)
	settings, err := readSettings(ctx, q)
	if err != nil {
		return nil, nil, row.Subject, "The settings could not be read.", false
	}
	channel = row.ChannelID
	if channel == nil {
		channel = settings.sendChannel
	}
	if channel == nil || s.deps.OutboundMail == nil {
		return nil, nil, row.Subject, reasonSendingDisabled, true
	}

	// The document: its kind, number, language and totals, and its PDF.
	var facts mailFacts
	var fileName string
	kind := docQuote
	switch row.OwnerKind {
	case ownerQuote:
		quote, err := q.GetQuote(ctx, row.OwnerID)
		if noRows(err) {
			return nil, channel, row.Subject, reasonGone, true
		}
		if err != nil {
			return nil, channel, row.Subject, "The quote could not be read.", false
		}
		facts = mailFacts{kind: docQuote, language: quote.Language, number: quote.Number, version: row.Version,
			currency: quote.Currency, gross: ratOrZero(quote.GrossTotal)}
		if quote.ValidUntil.Valid {
			facts.validUntil = &quote.ValidUntil.Time
		}
		fileName = quoteFileName(quote, row.Version, false)
	case ownerOrder:
		kind = docConfirmation
		order, err := q.OrderByID(ctx, row.OwnerID)
		if noRows(err) {
			return nil, channel, row.Subject, reasonGone, true
		}
		if err != nil {
			return nil, channel, row.Subject, "The order could not be read.", false
		}
		facts = mailFacts{kind: docConfirmation, language: order.Language, number: order.Number, version: row.Version,
			revised: row.Version > 1, currency: order.Currency, gross: ratOrZero(order.GrossTotal)}
		fileName = orderFileName(order, false)
	default:
		return nil, channel, row.Subject, reasonGone, true
	}
	var doc store.OrdersDocument
	if row.DocumentID != nil {
		doc, err = q.GetDocument(ctx, *row.DocumentID)
	} else {
		doc, err = q.DocumentOf(ctx, store.DocumentOfParams{OwnerKind: row.OwnerKind, OwnerID: row.OwnerID, Kind: kind, Version: row.Version})
	}
	if noRows(err) {
		return nil, channel, row.Subject, reasonNoDocument, false
	}
	if err != nil {
		return nil, channel, row.Subject, reasonNotStored, false
	}
	pdf, err := s.loadDocument(ctx, doc)
	if err != nil {
		return &doc.ID, channel, row.Subject, reasonNotStored, false
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return &doc.ID, channel, row.Subject, "The seller could not be read.", false
	}
	facts.seller = seller.LegalName
	req := sendRequest{recipient: row.Recipient, subject: row.Subject}
	for _, c := range strings.Split(row.Cc, ",") {
		if c = strings.TrimSpace(c); c != "" {
			req.cc = append(req.cc, c)
		}
	}
	messageID := newMessageID()
	if row.ProviderMessageID != nil {
		messageID = *row.ProviderMessageID
	}
	out := envelope(req, facts, settings, seller, pdf, fileName, messageID)
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.outboundSend(sendCtx, *channel, out); err != nil {
		return &doc.ID, channel, out.Subject, truncate(fmt.Sprintf("%s %s", reasonNotMailed, withoutAddress(err.Error(), row.Recipient)), 500), false
	}
	return &doc.ID, channel, truncate(out.Subject, 300), "", false
}

// withoutAddress is msg with the recipient's address taken out: a provider's
// refusal often quotes it, and it may be a person's an erase is about to take
// out.
func withoutAddress(msg, address string) string {
	if address == "" {
		return msg
	}
	return strings.ReplaceAll(msg, address, "<recipient>")
}

// queueMailing queues an automatic send of a document's PDF of version on
// the caller's own transaction (O25) — the orders.queue_confirmation
// handler's, on its delivery's transaction — once per triggering event: a
// repeat is a no-op, answered false. The orders.mail worker sends it with the
// settings' channel and the built-in mail text, the document's stored PDF of
// that version attached (found by kind and version when documentID is nil).
func queueMailing(ctx context.Context, txq *store.Queries, ownerKind string, ownerID int64, version int32, documentID *int64,
	recipient string, triggerEventID uuid.UUID, at time.Time) (bool, error) {
	n, err := txq.QueueMailing(ctx, store.QueueMailingParams{
		OwnerKind: ownerKind, OwnerID: ownerID, Version: version, DocumentID: documentID, Recipient: recipient,
		TriggerEventID: &triggerEventID, CreatedAt: &at,
	})
	if err != nil {
		return false, fmt.Errorf("orders: queue the send of %s %d: %w", ownerKind, ownerID, err)
	}
	return n == 1, nil
}
