package purchasing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the module's two workers. purchasing.mail (P21, A19) retries
// the mailings a person's send left queued because the provider refused
// them: a cycle claims one queued row at a time under a lease (ClaimMailing:
// a SKIP LOCKED pick that pushes next_attempt_at out by the lease, so two
// workers never take one row and a worker that dies leaves it claimable
// again), then — outside any transaction — reads the stored PDF the row
// names, sends it through contracts.OutboundMail on the row's channel and
// marks it sent; a failure counts the attempt and backs off, and a row that
// failed mailMaxAttempts times, or can never be sent, is failed.
// purchasing.deletions (A8) deletes from the object store the stored PDFs the
// supplier's erase wrote to the deletion ledger inside Accounting's
// transaction, which never leaves the process.
//
// Every statement runs on its own pooled connection and returns it before the
// next is taken, and no call out of the module is made while a connection is
// held: both workers are safe on a pool of one.

const (
	mailWorkerName     = "purchasing.mail"
	mailWorkerInterval = 30 * time.Second
	mailPerCycle       = 10
	mailLease          = 2 * time.Minute
	mailMaxAttempts    = 8
	stepTimeout        = 10 * time.Second

	deletionsWorkerName     = "purchasing.deletions"
	deletionsWorkerInterval = time.Minute
	deletionsPerCycle       = 50
	deletionMaxAttempts     = 8
)

// mailBackoff is the wait after the n-th failed attempt: a minute doubled per
// attempt, at most an hour.
func mailBackoff(attempts int32) time.Duration {
	d := time.Minute
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
			d.Logger.Error("purchasing: the workers cannot start", "error", err.Error())
		}
		return nil
	}
	return []worker.Worker{&mailWorker{s: s}, &deletionsWorker{s: s}}
}

// runEvery runs cycle every interval until ctx is done.
func runEvery(ctx context.Context, interval time.Duration, cycle func(context.Context) int) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		cycle(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// mailWorker retries the queued mailings.
type mailWorker struct{ s *server }

func (w *mailWorker) Name() string            { return mailWorkerName }
func (w *mailWorker) Interval() time.Duration { return mailWorkerInterval }

func (w *mailWorker) Run(ctx context.Context) error {
	return runEvery(ctx, mailWorkerInterval, w.cycle)
}

// cycle sends up to mailPerCycle due rows, one at a time, and answers how
// many it claimed.
func (w *mailWorker) cycle(ctx context.Context) int {
	n := 0
	for ; n < mailPerCycle && ctx.Err() == nil; n++ {
		s := w.s
		now := s.deps.Clock()
		stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
		row, err := store.New(s.deps.Pool).ClaimMailing(stepCtx, store.ClaimMailingParams{Now: now, LeaseUntil: now.Add(mailLease), MessageID: newMessageID()})
		cancel()
		if noRows(err) {
			return n
		}
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "purchasing: the mail worker could not claim a mailing", "error", err.Error())
			return n
		}
		w.handle(ctx, row)
	}
	return n
}

// handle sends one claimed row and records the outcome under its lease.
func (w *mailWorker) handle(ctx context.Context, row store.PurchasingMailing) {
	s := w.s
	channel, reason, terminal := w.send(ctx, row)
	stepCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stepTimeout)
	defer cancel()
	q := store.New(s.deps.Pool)
	if reason == "" {
		if _, err := q.MarkMailingSent(stepCtx, store.MarkMailingSentParams{ID: row.ID, Now: ptr(s.deps.Clock()), ChannelID: channel, LeaseUntil: *row.NextAttemptAt}); err != nil {
			s.deps.Logger.ErrorContext(ctx, "purchasing: a mailing was sent but could not be marked", "mailing_id", row.ID, "error", err.Error())
		}
		return
	}
	attempts := row.Attempts + 1
	fail := terminal || attempts >= mailMaxAttempts
	if _, err := q.RetryMailing(stepCtx, store.RetryMailingParams{
		ID: row.ID, LastError: reason, Fail: fail, NextAttemptAt: s.deps.Clock().Add(mailBackoff(attempts)), LeaseUntil: *row.NextAttemptAt,
	}); err != nil {
		s.deps.Logger.ErrorContext(ctx, "purchasing: a mailing's failed attempt could not be recorded", "mailing_id", row.ID, "error", err.Error())
	}
	s.deps.Logger.WarnContext(ctx, "purchasing: a queued mailing was not sent", "mailing_id", row.ID, "attempts", attempts, "failed", fail, "reason", reason)
}

// send makes one attempt at a claimed row, outside any transaction: the
// stored PDF it names, the cover mail from the order's row, the send. reason
// is "" when it went out; terminal says the row can never be sent.
func (w *mailWorker) send(ctx context.Context, row store.PurchasingMailing) (*uuid.UUID, string, bool) {
	s := w.s
	q := store.New(s.deps.Pool)
	channel := row.ChannelID
	if channel == nil {
		settings, err := readSettings(ctx, q)
		if err != nil {
			return nil, "The settings could not be read.", false
		}
		channel = settings.sendChannel
	}
	if channel == nil || s.deps.OutboundMail == nil {
		return nil, "Sending is disabled: Communications is off, or no channel to send from is chosen.", true
	}
	if strings.TrimSpace(row.Recipient) == "" {
		return channel, "The mailing has no recipient.", true
	}
	o, err := q.GetOrder(ctx, row.OrderID)
	if err != nil {
		return channel, "The order could not be read.", noRows(err)
	}
	if row.Kind == docOrder && o.Status == statusCancelled {
		return channel, "The order was cancelled; its PDF is not sent.", true
	}
	if row.DocumentID == nil {
		return channel, "The mailing names no stored PDF.", true
	}
	doc, err := q.GetDocument(ctx, store.GetDocumentParams{ID: *row.DocumentID, OrderID: o.ID})
	if err != nil {
		return channel, "The stored PDF could not be read.", noRows(err)
	}
	if doc.ErasedAt != nil {
		return channel, "The stored PDF was erased.", true
	}
	pdf, err := s.loadDocument(ctx, doc)
	if err != nil {
		return channel, "The stored PDF could not be read.", false
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return channel, "The seller could not be read.", false
	}
	m := mailRequest{recipient: row.Recipient, subject: row.Subject, message: row.Message}
	for _, c := range strings.Split(row.Cc, ",") {
		if c = strings.TrimSpace(c); c != "" {
			m.cc = append(m.cc, c)
		}
	}
	messageID := newMessageID()
	if row.ProviderMessageID != nil {
		messageID = *row.ProviderMessageID
	}
	out := envelope(o, doc.Kind, doc.Version, m, seller, pdf, messageID)
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.outboundSend(sendCtx, *channel, out); err != nil {
		return channel, truncate(fmt.Sprintf("The mail provider did not take the e-mail. %s", withoutAddress(err.Error(), row.Recipient)), 500), false
	}
	return channel, "", false
}

// deletionsWorker deletes the erased stored PDFs from the object store.
type deletionsWorker struct{ s *server }

func (w *deletionsWorker) Name() string            { return deletionsWorkerName }
func (w *deletionsWorker) Interval() time.Duration { return deletionsWorkerInterval }

func (w *deletionsWorker) Run(ctx context.Context) error {
	return runEvery(ctx, deletionsWorkerInterval, w.cycle)
}

// cycle deletes up to deletionsPerCycle objects of the ledger, oldest first,
// and answers how many it deleted. A delete is idempotent, so two workers
// that take the same row only delete one object twice.
func (w *deletionsWorker) cycle(ctx context.Context) int {
	s := w.s
	q := store.New(s.deps.Pool)
	done, after := 0, int64(0)
	for i := 0; i < deletionsPerCycle && ctx.Err() == nil; i++ {
		stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
		row, err := q.NextDeletion(stepCtx, store.NextDeletionParams{MaxAttempts: deletionMaxAttempts, AfterID: after})
		cancel()
		if noRows(err) {
			return done
		}
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "purchasing: the deletions worker could not read the ledger", "error", err.Error())
			return done
		}
		after = row.ID
		delErr := s.objectDelete(ctx, row.StorageKey)
		if errors.Is(delErr, storage.ErrNotExist) {
			delErr = nil
		}
		stepCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), stepTimeout)
		if delErr != nil {
			err = q.FailDeletion(stepCtx, store.FailDeletionParams{ID: row.ID, LastError: ptr(truncate(delErr.Error(), 500))})
		} else {
			err = q.CompleteDeletion(stepCtx, store.CompleteDeletionParams{ID: row.ID, Now: ptr(s.deps.Clock())})
			done++
		}
		cancel()
		if err != nil {
			s.deps.Logger.ErrorContext(ctx, "purchasing: a deletion could not be recorded", "deletion_id", row.ID, "error", err.Error())
		}
	}
	return done
}
