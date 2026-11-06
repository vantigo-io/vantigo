package communications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
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

// This file is the communications-messenger-sync worker (Messenger design N3,
// delivery A): it claims one Messenger channel's messenger_sync row under a
// lease, reads the events the channel's provider has queued for its Page after
// the row's cursor, imports each (messenger_import.go), and acknowledges them.
// It is a worker of its own, not a rename of the mailbox sync, whose name
// operators see in the installation guide: the mailbox sync reads a delta of
// mail, this one an inbox of events, and the two share nothing but the shape of
// their claim.
//
// The things about it that look like bugs and are deliberate, as the mailbox
// sync's:
//
//  1. There is no advisory lock. The conditional UPDATE of ClaimMessengerSync is
//     the lock, as the outbox's; every later write of the cycle is guarded by
//     the lease id and reports 0 rows once another holder took the row, and the
//     cycle then stops, writing nothing more. The cursor advance is inside the
//     import's transaction and guarded the same way, so an import by a holder
//     that lost its lease rolls back.
//  2. The cursor is the provider's insertion id and never a time: webhooks
//     arrive out of order, and the event's own time only orders the display.
//  3. A lease is two minutes, renewed before each event. An event that outlives
//     it loses the lease, and a second holder may import the same event at
//     once: the receipt's unique index lets one of them commit, and the other's
//     objects go to the cleanup ledger.
//  4. Ack is called at the end of every cycle with the cursor, even when no
//     event was read: it is idempotent, and it is what lets a cycle whose Ack
//     failed make the provider forget its copies the next time.
//  5. An event whose import Postgres refuses (class 22 or 23) is given up on at
//     once, with a failed import_failed receipt, and the cursor goes on. Any
//     other failure ends the cycle, backed off, and the event is read again.

const (
	// messengerSyncWorkerName is what the runner logs this worker as.
	messengerSyncWorkerName = "communications-messenger-sync"
	// messengerSyncPollInterval is how often the worker asks the providers which
	// Pages have new events (N3 step 1); each tick drains every channel due.
	messengerSyncPollInterval = 2 * time.Second
	// messengerSyncLease is how long a claimed channel stays invisible to other
	// workers; renewed before each event.
	messengerSyncLease = 2 * time.Minute
	// messengerSyncEvery is how long after a finished sync a channel is due
	// again without a notice (N3 step 1).
	messengerSyncEvery = 60 * time.Second
	// messengerPageSize is how many events one cycle reads (N3 step 3).
	messengerPageSize = 100
	// messengerNoticesOverlap is how far before the last tick the next Notified
	// asks from, so an event that arrived while the tick ran is not missed.
	messengerNoticesOverlap = 10 * time.Second
	// messengerProfileMaxAge is how old a participant's name is before it is
	// read from the platform again (N3 step 4).
	messengerProfileMaxAge = 30 * 24 * time.Hour
	// messengerEchoWait is how long an echo that matches none of the parts we
	// sent is held while a part of the channel's is in flight: its own echo may
	// arrive before the part's id is recorded (N12).
	messengerEchoWait = 2 * outboxSendTimeout
	// messengerEchoRetry is how soon a cycle that held an echo is due again.
	messengerEchoRetry = 5 * time.Second
	// messengerImportMaxBytes is the most one attachment's bytes the worker
	// reads when the module's config names no limit.
	messengerImportMaxBytes = 25 << 20
)

// errMessengerEchoPending is a cycle that stopped at an echo held for a part in
// flight: not a failure, the channel is due again in messengerEchoRetry.
var errMessengerEchoPending = errors.New("communications: an echo is held while a part is in flight")

// MessengerSyncWorker reads every Messenger channel's inbox into conversations.
// It implements worker.Worker.
type MessengerSyncWorker struct {
	deps module.Deps
	// store is this module's scoped object store, where each event's
	// attachments are kept. storeErr is deferred as the outbox's is: a cycle
	// that needs the store fails, the worker starts.
	store    storage.ObjectStore
	storeErr error
	metrics  *inboundMetrics

	// noticesSince is each provider's last tick, by its Key; heldUntil is, by
	// providerPageKey, the Pages whose cycle stopped at an echo held for a part in
	// flight, whose notices wait until the hold's retry is due: the event is
	// still in the provider's inbox, so its Page would be notified at once again.
	noticesMu    sync.Mutex
	noticesSince map[string]time.Time
	heldUntil    map[string]time.Time
}

var _ worker.Worker = (*MessengerSyncWorker)(nil)

// NewMessengerSyncWorker builds the worker over d, with the global meter
// provider (see NewOutboxWorker).
func NewMessengerSyncWorker(d module.Deps) *MessengerSyncWorker {
	return newMessengerSyncWorker(d, otel.GetMeterProvider())
}

func newMessengerSyncWorker(d module.Deps, mp metric.MeterProvider) *MessengerSyncWorker {
	s, err := moduleObjectStore(d)
	return &MessengerSyncWorker{deps: d, store: s, storeErr: err, metrics: newInboundMetrics(mp, d.Logger)}
}

// Name identifies this worker in the runner's logs.
func (w *MessengerSyncWorker) Name() string { return messengerSyncWorkerName }

// Interval is the poll cadence between drains.
func (w *MessengerSyncWorker) Interval() time.Duration { return messengerSyncPollInterval }

// Run drains every channel due, sleeps the poll interval, and repeats until ctx
// is done. A failing iteration is logged and the loop continues.
func (w *MessengerSyncWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(messengerSyncPollInterval)
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
func (w *MessengerSyncWorker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := w.ProcessOne(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.logger().Error("messenger sync iteration failed", "worker", messengerSyncWorkerName, "error", err)
			}
			return
		}
		if !processed {
			return
		}
	}
}

// ProcessOne takes any pending notices, claims at most one channel due and runs
// one cycle on it, answering whether work was claimed — true for any outcome of
// the cycle, so a caller can drain.
func (w *MessengerSyncWorker) ProcessOne(ctx context.Context) (bool, error) {
	w.takeNotices(ctx)
	channelID, leaseID, ok, err := w.claim(ctx)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	c := &messengerCycle{channelID: channelID, leaseID: leaseID}
	err = w.cycle(ctx, c)
	switch {
	case err == nil:
		return true, nil
	case ctx.Err() != nil:
		return true, err
	case errors.Is(err, errSyncLeaseLost):
		w.logger().Info("messenger sync stopped: another worker took the channel's lease",
			"worker", messengerSyncWorkerName, "channel", channelID)
		return true, nil
	}
	return true, w.recordFailure(ctx, c, err)
}

// takeNotices asks every messenger provider for the Pages notified since that
// provider's last tick (with a short overlap) and marks the active channels on
// them notified and due at now (N3 step 1). Each provider keeps its own tick.
func (w *MessengerSyncWorker) takeNotices(ctx context.Context) {
	w.noticesMu.Lock()
	defer w.noticesMu.Unlock()
	for _, m := range w.deps.ChannelMessengers {
		if m == nil {
			continue
		}
		provider, err := w.syncMessenger(ctx, m.Key())
		if err != nil {
			return
		}
		if provider != nil {
			w.takeProviderNotices(ctx, provider)
		}
	}
}

// takeProviderNotices is takeNotices for one provider. noticesMu is held.
func (w *MessengerSyncWorker) takeProviderNotices(ctx context.Context, provider contracts.ChannelMessenger) {
	key := provider.Key()
	if w.noticesSince == nil {
		w.noticesSince = map[string]time.Time{}
	}
	now := w.now()
	since, ok := w.noticesSince[key]
	if !ok {
		since = now.Add(-messengerSyncEvery)
	}
	addresses, err := messengerNotified(ctx, provider, since.Add(-messengerNoticesOverlap))
	if err != nil {
		w.logger().Warn("messenger sync: the Pages notified could not be read; the poll goes on",
			"worker", messengerSyncWorkerName, "provider", key, "error", err)
		return
	}
	w.noticesSince[key] = now
	addresses = slices.DeleteFunc(addresses, func(address string) bool {
		return w.heldUntil[providerPageKey(key, address)].After(now)
	})
	if len(addresses) == 0 {
		return
	}
	if _, err := store.New(w.deps.Pool).MarkMessengerSyncNotified(ctx, store.MarkMessengerSyncNotifiedParams{
		Now: now, Provider: key, Addresses: addresses,
	}); err != nil {
		// The notices are asked for again: the tick did not move.
		delete(w.noticesSince, key)
		w.logger().Warn("messenger sync: the notified channels could not be made due; they are asked for again",
			"worker", messengerSyncWorkerName, "provider", key, "error", err)
	}
}

// claim takes the first channel due by a conditional update. now and the
// lease's end are computed once, as the outbox's claim does.
func (w *MessengerSyncWorker) claim(ctx context.Context) (uuid.UUID, string, bool, error) {
	leaseID := hexN(uuid.New())
	now := w.now()
	leaseUntil := now.Add(messengerSyncLease)
	q := store.New(w.deps.Pool)
	for range mailboxSyncClaimAttempts {
		channelID, err := q.SelectDueMessengerSync(ctx, now)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", false, nil
		}
		if err != nil {
			return uuid.Nil, "", false, fmt.Errorf("communications: select a messenger channel due for sync: %w", err)
		}
		rows, err := q.ClaimMessengerSync(ctx, store.ClaimMessengerSyncParams{
			LeaseID: &leaseID, LeaseUntil: &leaseUntil, Now: now, ChannelID: channelID,
		})
		if err != nil {
			return uuid.Nil, "", false, fmt.Errorf("communications: claim a messenger channel for sync: %w", err)
		}
		if rows == 1 {
			return channelID, leaseID, true, nil
		}
	}
	return uuid.Nil, "", false, nil
}

// messengerCycle is one claimed channel's cycle: the lease and what the cycle
// read of the row.
type messengerCycle struct {
	channelID uuid.UUID
	leaseID   string
	attempts  int32
	cursor    int64
	address   string
	provider  contracts.ChannelMessenger
	// held is an echo the cycle stopped at, for a part in flight.
	held bool
}

// cycle is design N3's cycle: the provider, the page of events, each imported,
// the Ack. Every error but errSyncLeaseLost is recorded by recordFailure.
func (w *MessengerSyncWorker) cycle(ctx context.Context, c *messengerCycle) error {
	q := store.New(w.deps.Pool)
	row, err := q.GetMessengerSyncForCycle(ctx, c.channelID)
	if err != nil {
		return fmt.Errorf("communications: read the claimed messenger sync: %w", err)
	}
	if row.LeaseID == nil || *row.LeaseID != c.leaseID {
		return errSyncLeaseLost
	}
	c.attempts, c.cursor, c.address = row.Attempts, row.Cursor, row.ChannelAddress

	provider, err := w.syncMessenger(ctx, row.ChannelProvider)
	if err != nil {
		return err
	}
	if provider == nil {
		return errSyncProviderHeld
	}
	status, err := messengerStatus(ctx, provider)
	if err != nil || !status.Available {
		return errSyncProviderHeld
	}
	c.provider = provider

	events, err := messengerPending(ctx, provider, c.address, c.cursor, messengerPageSize)
	if err != nil {
		return err
	}
	for _, event := range events {
		now := w.now()
		if err := w.guarded(q.RenewMessengerSyncLease(ctx, store.RenewMessengerSyncLeaseParams{
			LeaseUntil: ptrTime(now.Add(messengerSyncLease)), Now: now, ChannelID: c.channelID, LeaseID: &c.leaseID,
		})); err != nil {
			return err
		}
		if err := w.importEvent(ctx, c, event); err != nil {
			if errors.Is(err, errMessengerEchoPending) {
				c.held = true
				break
			}
			if gaveUp, gerr := w.giveUp(ctx, c, event, err); gerr != nil || !gaveUp {
				return errors.Join(err, gerr)
			}
		}
		c.cursor = max(c.cursor, event.Cursor)
	}

	if c.cursor > 0 {
		if err := messengerAck(ctx, provider, c.address, c.cursor); err != nil {
			return err
		}
	}
	now := w.now()
	next := now.Add(messengerSyncEvery)
	if c.held {
		next = now.Add(messengerEchoRetry)
		w.hold(row.ChannelProvider, c.address, next)
	}
	return w.guarded(q.FinishMessengerSync(ctx, store.FinishMessengerSyncParams{
		Now: now, More: len(events) == messengerPageSize && !c.held, NextSyncAt: next,
		ChannelID: c.channelID, LeaseID: &c.leaseID,
	}))
}

// hold remembers that the Page's cycle stopped at a held echo until until.
func (w *MessengerSyncWorker) hold(provider, address string, until time.Time) {
	w.noticesMu.Lock()
	defer w.noticesMu.Unlock()
	if w.heldUntil == nil {
		w.heldUntil = map[string]time.Time{}
	}
	w.heldUntil[providerPageKey(provider, address)] = until
}

// giveUp decides whether a failed import is given up on: a value Postgres
// refuses, at once — not the provider's or the store's, not a lost lease or a
// cancelled cycle. An event given up on gets a failed import_failed receipt, its
// cursor advanced past, and true lets the page go on.
func (w *MessengerSyncWorker) giveUp(ctx context.Context, c *messengerCycle, event contracts.MessengerEvent, cause error) (bool, error) {
	if ctx.Err() != nil || errors.Is(cause, errSyncLeaseLost) || errors.Is(cause, errImportTransient) {
		return false, nil
	}
	if !db.IsDataException(cause) && !db.IsIntegrityViolation(cause) {
		return false, nil
	}
	w.logger().Warn("messenger sync gave an event up: its import fails each time",
		"worker", messengerSyncWorkerName, "channel", c.channelID, "code", reasonImportFailed, "error", cause)
	now := w.now()
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	err := db.WithTx(locked, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := locked
		txq := store.New(tx)
		if event.Kind == contracts.MessengerEventMessage {
			if err := txq.InsertInboundReceipt(ctx, store.InsertInboundReceiptParams{
				ID: uuid.New(), ChannelID: c.channelID, ProviderMessageID: receiptKeyOf(event), Outcome: receiptFailed,
				Reason: nonEmpty(reasonImportFailed), ReceivedAt: eventTime(event, now), CreatedAt: now,
			}); err != nil && !db.IsUniqueViolation(err, "ux_inbound_receipts_provider") {
				return fmt.Errorf("communications: write a receipt: %w", err)
			}
		}
		return w.advanceCursor(ctx, txq, c, event.Cursor, now)
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// advanceCursor moves the channel's cursor to cursor under the lease:
// errSyncLeaseLost when the lease is no longer this cycle's.
func (w *MessengerSyncWorker) advanceCursor(ctx context.Context, txq *store.Queries, c *messengerCycle, cursor int64, now time.Time) error {
	return w.guarded(txq.AdvanceMessengerSyncCursor(ctx, store.AdvanceMessengerSyncCursorParams{
		Cursor: cursor, Now: now, ChannelID: c.channelID, LeaseID: &c.leaseID,
	}))
}

// guarded turns a lease-guarded write's rows into errSyncLeaseLost when it wrote
// nothing.
func (w *MessengerSyncWorker) guarded(rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("communications: write the messenger sync: %w", err)
	}
	if rows == 0 {
		return errSyncLeaseLost
	}
	return nil
}

// recordFailure writes a cycle's failure on the row, under its lease: a held
// provider tries again in five minutes without spending an attempt; anything
// else backs off, as the mailbox sync's. Each is a warning with the channel and
// the code — never an address, a text or the provider's words.
func (w *MessengerSyncWorker) recordFailure(ctx context.Context, c *messengerCycle, cause error) error {
	q := store.New(w.deps.Pool)
	now := w.now()
	if errors.Is(cause, errSyncProviderHeld) || errors.Is(cause, contracts.ErrMessengerUnavailable) {
		w.logger().Warn("messenger sync held: the messenger provider is not available",
			"worker", messengerSyncWorkerName, "channel", c.channelID, "code", "provider_unavailable")
		err := w.guarded(q.HoldMessengerSync(ctx, store.HoldMessengerSyncParams{
			NextSyncAt: now.Add(mailboxSyncEvery), Now: now, ChannelID: c.channelID, LeaseID: &c.leaseID,
		}))
		if errors.Is(err, errSyncLeaseLost) {
			return nil
		}
		return err
	}

	code := "sync_failed"
	w.logger().Warn("messenger sync failed",
		"worker", messengerSyncWorkerName, "channel", c.channelID, "code", code, "error", cause)
	err := w.guarded(q.FailMessengerSync(ctx, store.FailMessengerSyncParams{
		Error: &code, NextSyncAt: now.Add(syncBackoff(c.attempts)), Now: now,
		ChannelID: c.channelID, LeaseID: &c.leaseID,
	}))
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
func (w *MessengerSyncWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *MessengerSyncWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

// syncMessenger is the messenger provider the sync reads a channel on provider
// through: the one filled under that very key, else nil — a channel is never
// read through a provider under another key. Key makes no network call.
func (w *MessengerSyncWorker) syncMessenger(ctx context.Context, provider string) (contracts.ChannelMessenger, error) {
	if err := noteContractCall(ctx, "ChannelMessenger.Key"); err != nil {
		return nil, err
	}
	return channelMessengerFor(w.deps, provider), nil
}
