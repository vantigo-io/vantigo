package communications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the communications-mailbox-sync worker (Outlook connection
// design M14; delivery D, reading 13): it claims one Microsoft 365 channel's
// channel_sync row under a lease, reads one page of its Inbox's delta, imports
// each message the channel has no receipt for (inbound_import.go), and keeps
// the page's link once every message of the page has a receipt.
//
// The things about it that look like bugs and are deliberate:
//
//  1. There is no advisory lock. The conditional UPDATE of ClaimChannelSync is
//     the lock, as the outbox's; every later write of the cycle is guarded by
//     the lease id and reports 0 rows once another holder took the row, and
//     the cycle then stops, writing nothing more.
//  2. A lease is two minutes, renewed before each message. A message that
//     outlives it loses the lease, and a second holder may import the same
//     message at once: the receipt's unique index lets one of them commit,
//     and the other's objects go to the cleanup ledger.
//  3. A page's link is stored only after every message of the page has a
//     receipt. A cycle that stops half-way reads the same page again next
//     time, and the receipts skip what it already imported.
//  4. Nothing received before the retention window is read: the receipts'
//     sweep deletes receipts by that same cutoff, so a message retention or
//     an erase deleted is never imported again when the delta answers it once
//     more (marked read, say).
//  5. A message whose import fails each time would hold its page for ever: a
//     value Postgres refuses (class 22 or 23) is given up on at once, any
//     other failure but the provider's or the store's on its third try, with
//     a failed import_failed receipt, and the page goes on.

const (
	// mailboxSyncWorkerName is what the runner logs this worker as.
	mailboxSyncWorkerName = "communications-mailbox-sync"
	// mailboxSyncPollInterval is how often the worker looks for a channel
	// due; each tick drains every channel due.
	mailboxSyncPollInterval = 30 * time.Second
	// mailboxSyncLease is how long a claimed channel stays invisible to other
	// workers; renewed before each message.
	mailboxSyncLease = 2 * time.Minute
	// mailboxSyncClaimAttempts is how many lost claim races one ProcessOne
	// tries past, as the outbox's.
	mailboxSyncClaimAttempts = 10
	// mailboxSyncEvery is how long after a finished sync a channel is due
	// again, and how long a held one waits for its provider.
	mailboxSyncEvery = 5 * time.Minute
	// mailboxSyncRefusedWait is how long a channel the provider refused
	// (denied, gone, unknown) waits: an admin may fix the mailbox's RBAC.
	mailboxSyncRefusedWait = time.Hour
	// mailboxSyncBackoffCap is the longest wait after any other failure.
	mailboxSyncBackoffCap = time.Hour
	// mailboxSyncFolder is the folder a channel receives from.
	mailboxSyncFolder = "inbox"
	// mailboxSyncRestartMargin is how far before the earlier of the last
	// finished sync and the newest receipt an expired delta restarts.
	mailboxSyncRestartMargin = time.Hour
	// mailboxImportTries is how many failed imports of one message the page
	// is held at before it is given up on.
	mailboxImportTries = 3
)

// errSyncLeaseLost is a cycle that found its lease taken: it stops and
// writes nothing more.
var errSyncLeaseLost = errors.New("communications: the mailbox sync lost its lease")

// errSyncProviderHeld is a cycle that found no available provider: the slot
// empty or under another key, or Status not available.
var errSyncProviderHeld = errors.New("communications: the mail channels provider is not available")

// errImportTransient marks an import's failure at the provider or the object
// store: never counted toward giving the message up.
var errImportTransient = errors.New("communications: the provider or the store failed")

// MailboxSyncWorker reads every Microsoft 365 channel's Inbox into
// conversations. It implements worker.Worker.
type MailboxSyncWorker struct {
	deps module.Deps
	// store is this module's scoped object store, where each message's raw
	// bytes and attachments are kept. storeErr is deferred as the outbox's
	// is: a cycle that needs the store fails, the worker starts.
	store    storage.ObjectStore
	storeErr error
	metrics  *inboundMetrics
}

var _ worker.Worker = (*MailboxSyncWorker)(nil)

// NewMailboxSyncWorker builds the worker over d, with the global meter
// provider (see NewOutboxWorker).
func NewMailboxSyncWorker(d module.Deps) *MailboxSyncWorker {
	return newMailboxSyncWorker(d, otel.GetMeterProvider())
}

func newMailboxSyncWorker(d module.Deps, mp metric.MeterProvider) *MailboxSyncWorker {
	s, err := moduleObjectStore(d)
	return &MailboxSyncWorker{deps: d, store: s, storeErr: err, metrics: newInboundMetrics(mp, d.Logger)}
}

// Name identifies this worker in the runner's logs.
func (w *MailboxSyncWorker) Name() string { return mailboxSyncWorkerName }

// Interval is the poll cadence between drains.
func (w *MailboxSyncWorker) Interval() time.Duration { return mailboxSyncPollInterval }

// Run drains every channel due, sleeps the poll interval, and repeats until
// ctx is done. A failing iteration is logged and the loop continues.
func (w *MailboxSyncWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(mailboxSyncPollInterval)
	defer ticker.Stop()
	for {
		w.drain(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// drain processes channels while one was due.
func (w *MailboxSyncWorker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := w.ProcessOne(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.logger().Error("mailbox sync iteration failed", "worker", mailboxSyncWorkerName, "error", err)
			}
			return
		}
		if !processed {
			return
		}
	}
}

// ProcessOne claims at most one channel due and runs one cycle on it,
// answering whether a channel was claimed — true for any outcome of the
// cycle, so a caller can drain.
func (w *MailboxSyncWorker) ProcessOne(ctx context.Context) (bool, error) {
	channelID, leaseID, ok, err := w.claim(ctx)
	if err != nil || !ok {
		return false, err
	}
	c := &syncCycle{channelID: channelID, leaseID: leaseID}
	err = w.cycle(ctx, c)
	switch {
	case err == nil:
		return true, nil
	case ctx.Err() != nil:
		return true, err
	case errors.Is(err, errSyncLeaseLost):
		w.logger().Info("mailbox sync stopped: another worker took the channel's lease",
			"worker", mailboxSyncWorkerName, "channel", channelID)
		return true, nil
	}
	return true, w.recordFailure(ctx, c, err)
}

// claim takes the first channel due by a conditional update. now and the
// lease's end are computed once, as the outbox's claim does.
func (w *MailboxSyncWorker) claim(ctx context.Context) (uuid.UUID, string, bool, error) {
	leaseID := hexN(uuid.New())
	now := w.now()
	leaseUntil := now.Add(mailboxSyncLease)
	q := store.New(w.deps.Pool)
	for range mailboxSyncClaimAttempts {
		channelID, err := q.SelectDueChannelSync(ctx, now)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", false, nil
		}
		if err != nil {
			return uuid.Nil, "", false, fmt.Errorf("communications: select a channel due for sync: %w", err)
		}
		rows, err := q.ClaimChannelSync(ctx, store.ClaimChannelSyncParams{
			LeaseID: &leaseID, LeaseUntil: &leaseUntil, Now: now, ChannelID: channelID,
		})
		if err != nil {
			return uuid.Nil, "", false, fmt.Errorf("communications: claim a channel for sync: %w", err)
		}
		if rows == 1 {
			return channelID, leaseID, true, nil
		}
	}
	return uuid.Nil, "", false, nil
}

// syncCycle is one claimed channel's cycle: the lease and what the cycle
// read of the row.
type syncCycle struct {
	channelID      uuid.UUID
	leaseID        string
	attempts       int32
	importFrom     time.Time
	lastSyncedAt   *time.Time
	restartFrom    *time.Time
	mailbox        string
	channelAddress string
	provider       contracts.ChannelMailbox
}

// cycle is reading 13's cycle: the provider, the page, each message, the
// link. Every error but errSyncLeaseLost is recorded by recordFailure.
func (w *MailboxSyncWorker) cycle(ctx context.Context, c *syncCycle) error {
	q := store.New(w.deps.Pool)
	row, err := q.GetChannelSyncForCycle(ctx, c.channelID)
	if err != nil {
		return fmt.Errorf("communications: read the claimed channel sync: %w", err)
	}
	if row.LeaseID == nil || *row.LeaseID != c.leaseID {
		return errSyncLeaseLost
	}
	c.attempts, c.importFrom, c.channelAddress = row.Attempts, row.ImportFrom, row.ChannelAddress
	c.lastSyncedAt, c.restartFrom = row.LastSyncedAt, row.RestartFrom

	provider, err := w.syncMailbox(ctx)
	if err != nil {
		return err
	}
	if provider == nil {
		return errSyncProviderHeld
	}
	status, err := syncMailboxStatus(ctx, provider)
	if err != nil || status.State != contracts.MailboxAvailable {
		return errSyncProviderHeld
	}
	c.provider = provider
	if c.mailbox, err = channelMailboxOf(row.ChannelSettings); err != nil {
		return err
	}
	if c.mailbox == "" {
		return contracts.ErrMailboxUnknown
	}

	link := ""
	if row.DeltaLink != nil {
		link = *row.DeltaLink
	}
	// A fresh read starts from import_from, or from the restart point an
	// expired link left.
	fresh := c.importFrom
	if c.restartFrom != nil {
		fresh = *c.restartFrom
	}
	page, err := mailboxDelta(ctx, provider, c.mailbox, mailboxSyncFolder, link, fresh)
	if errors.Is(err, contracts.ErrMailboxDeltaExpired) && link != "" {
		// The provider no longer takes the link: forget it, keep the restart
		// point, and read afresh from there. The receipts skip the overlap.
		since, rerr := w.restartPoint(ctx, q, c)
		if rerr != nil {
			return rerr
		}
		if err := w.guarded(q.ResetChannelSyncDelta(ctx, store.ResetChannelSyncDeltaParams{
			RestartFrom: &since, Now: w.now(), ChannelID: c.channelID, LeaseID: &c.leaseID,
		})); err != nil {
			return err
		}
		page, err = mailboxDelta(ctx, provider, c.mailbox, mailboxSyncFolder, "", since)
	}
	if err != nil {
		return err
	}

	floor := c.importFrom
	if cutoff := w.now().AddDate(0, 0, -retentionDaysOf(w.deps)); cutoff.After(floor) {
		floor = cutoff
	}
	for _, item := range pageItems(page.Items, floor) {
		now := w.now()
		if err := w.guarded(q.RenewChannelSyncLease(ctx, store.RenewChannelSyncLeaseParams{
			LeaseUntil: ptrTime(now.Add(mailboxSyncLease)), Now: now, ChannelID: c.channelID, LeaseID: &c.leaseID,
		})); err != nil {
			return err
		}
		seen, err := q.InboundReceiptExists(ctx, store.InboundReceiptExistsParams{
			ChannelID: c.channelID, ProviderMessageID: item.ID,
		})
		if err != nil {
			return fmt.Errorf("communications: look for a receipt: %w", err)
		}
		if seen {
			continue
		}
		if err := w.importItem(ctx, c, item); err != nil {
			if gaveUp, gerr := w.giveUp(ctx, q, c, item, err); gerr != nil || !gaveUp {
				return errors.Join(err, gerr)
			}
		}
	}

	now := w.now()
	next := now
	if page.Done {
		next = now.Add(mailboxSyncEvery)
	}
	return w.guarded(q.StoreChannelSyncPage(ctx, store.StoreChannelSyncPageParams{
		DeltaLink: nonEmpty(page.Link), Done: page.Done, Now: now, NextSyncAt: next,
		ChannelID: c.channelID, LeaseID: &c.leaseID,
	}))
}

// restartPoint is where an expired delta reads afresh from: import_from for
// a channel that never finished a pass — its pages come in no order, so a
// receipt says nothing of what is left —, else an hour before the earlier of
// the last finished sync and the newest receipt, never before import_from.
func (w *MailboxSyncWorker) restartPoint(ctx context.Context, q *store.Queries, c *syncCycle) (time.Time, error) {
	if c.lastSyncedAt == nil {
		return c.importFrom, nil
	}
	since := *c.lastSyncedAt
	newest, err := q.NewestInboundReceipt(ctx, c.channelID)
	switch {
	case err == nil:
		if newest.Before(since) {
			since = newest
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return time.Time{}, fmt.Errorf("communications: read the channel's newest receipt: %w", err)
	}
	since = since.Add(-mailboxSyncRestartMargin)
	if since.Before(c.importFrom) {
		since = c.importFrom
	}
	return since.UTC(), nil
}

// giveUp decides whether a failed import is given up on: a value Postgres
// refuses at once, another failure — not the provider's or the store's, not
// a lost lease or a cancelled cycle — on its mailboxImportTries'th try of
// the same message, counted on the row. A message given up on gets a failed
// import_failed receipt, and true lets the page go on.
func (w *MailboxSyncWorker) giveUp(ctx context.Context, q *store.Queries, c *syncCycle, item contracts.MailItem, cause error) (bool, error) {
	if ctx.Err() != nil || errors.Is(cause, errSyncLeaseLost) || errors.Is(cause, errImportTransient) {
		return false, nil
	}
	if !db.IsDataException(cause) && !db.IsIntegrityViolation(cause) {
		tries, err := q.CountChannelSyncItemFailure(ctx, store.CountChannelSyncItemFailureParams{
			Item: &item.ID, Now: w.now(), ChannelID: c.channelID, LeaseID: &c.leaseID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, errSyncLeaseLost
		}
		if err != nil {
			return false, fmt.Errorf("communications: count a failed import: %w", err)
		}
		if tries < mailboxImportTries {
			return false, nil
		}
	}
	w.logger().Warn("mailbox sync gave a message up: its import fails each time",
		"worker", mailboxSyncWorkerName, "channel", c.channelID, "code", reasonImportFailed, "error", cause)
	if err := w.writeReceipt(ctx, q, c, item, nil, nil, receiptFailed, reasonImportFailed); err != nil {
		return false, err
	}
	return true, nil
}

// pageItems are the items of a page the cycle imports: not removed — Vantigo
// keeps a message the mailbox no longer has (M17) — and not received before
// floor — the later of the channel's import_from and the retention window's
// start —, as the delta also answers changes to messages outside its filter;
// oldest first, ties by id. An item with no receivedDateTime is kept (it is
// received when it is read). None is dropped otherwise: the page's link is
// kept once these are done, so an item left out here would never be read
// again.
func pageItems(items []contracts.MailItem, floor time.Time) []contracts.MailItem {
	out := make([]contracts.MailItem, 0, len(items))
	for _, item := range items {
		if item.Removed || item.ID == "" || (!item.ReceivedAt.IsZero() && item.ReceivedAt.Before(floor)) {
			continue
		}
		out = append(out, item)
	}
	slices.SortStableFunc(out, func(a, b contracts.MailItem) int {
		if c := a.ReceivedAt.Compare(b.ReceivedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// guarded turns a lease-guarded write's rows into errSyncLeaseLost when it
// wrote nothing.
func (w *MailboxSyncWorker) guarded(rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("communications: write the channel sync: %w", err)
	}
	if rows == 0 {
		return errSyncLeaseLost
	}
	return nil
}

// syncFailureCode is the channel_sync.error of a failed cycle, and whether
// the failure is a refusal that leaves the channel 'failed' an hour.
func syncFailureCode(err error) (code string, refused bool) {
	switch {
	case errors.Is(err, contracts.ErrMailboxDenied):
		return "mailbox_denied", true
	case errors.Is(err, contracts.ErrMailboxGone):
		return "mailbox_gone", true
	case errors.Is(err, contracts.ErrMailboxUnknown):
		return "mailbox_unknown", true
	}
	return "sync_failed", false
}

// syncBackoff is the wait after a failure other than a refusal: one minute,
// doubled per attempt already spent, at most an hour.
func syncBackoff(attempts int32) time.Duration {
	wait := time.Minute
	for range max(attempts, 0) {
		wait *= 2
		if wait >= mailboxSyncBackoffCap {
			return mailboxSyncBackoffCap
		}
	}
	return wait
}

// recordFailure writes a cycle's failure on the row, under its lease: a held
// provider tries again in five minutes without spending an attempt; a
// refusal leaves the channel failed an hour; anything else backs off. Each
// is a warning with the channel and the code — never an address, a subject
// or the provider's words.
func (w *MailboxSyncWorker) recordFailure(ctx context.Context, c *syncCycle, cause error) error {
	q := store.New(w.deps.Pool)
	now := w.now()
	if errors.Is(cause, errSyncProviderHeld) || errors.Is(cause, contracts.ErrMailboxUnavailable) {
		w.logger().Warn("mailbox sync held: the mail channels provider is not available",
			"worker", mailboxSyncWorkerName, "channel", c.channelID, "code", "provider_unavailable")
		err := w.guarded(q.HoldChannelSync(ctx, store.HoldChannelSyncParams{
			NextSyncAt: now.Add(mailboxSyncEvery), Now: now, ChannelID: c.channelID, LeaseID: &c.leaseID,
		}))
		if errors.Is(err, errSyncLeaseLost) {
			return nil
		}
		return err
	}

	code, refused := syncFailureCode(cause)
	params := store.FailChannelSyncParams{
		Error: &code, NextSyncAt: now.Add(syncBackoff(c.attempts)), Now: now,
		ChannelID: c.channelID, LeaseID: &c.leaseID,
	}
	if refused {
		failed := "failed"
		params.State, params.NextSyncAt = &failed, now.Add(mailboxSyncRefusedWait)
	}
	w.logger().Warn("mailbox sync failed",
		"worker", mailboxSyncWorkerName, "channel", c.channelID, "code", code, "error", cause)
	err := w.guarded(q.FailChannelSync(ctx, params))
	if errors.Is(err, errSyncLeaseLost) {
		return nil
	}
	if err == nil {
		w.metrics.cyclesFailed.Add(ctx, 1)
	}
	return err
}

// now is the worker's clock, so tests control time as they do for the
// handlers.
func (w *MailboxSyncWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *MailboxSyncWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

func ptrTime(t time.Time) *time.Time { return &t }

// nonEmpty is s, or nil for "".
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
