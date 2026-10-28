package calendar

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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the calendar-mailbox-sync worker (calendar Outlook sync design
// O7, O8, O9): every minute, under an advisory lease of its own, it syncs at
// most fifty people whose sync is due — on, or owing a clean-up, next_sync_at
// null or past — one at a time, and reads their Outlook busy time back (step
// 9). The due list is read in a short transaction on the lease's connection
// and committed before the first call out, so no row lock is held while a
// mailbox is written; the lease is what keeps two replicas from syncing the
// same person. Everything else reads and writes the
// pool in short transactions, and no transaction is open while the provider,
// identity, Staff or a calendar source is asked (contractscalls.go refuses
// such a call). Each person's sync has three minutes (mailboxPersonDeadline)
// for everything it asks the neighbours, so one slow mailbox cannot hold the
// cycle; the person's own rows are written past it.
//
// For each person, in order:
//
//  1. The provider's Status: unavailable writes nothing and calls nothing —
//     the row is retried in an hour, a clean-up owed kept owed.
//  2. Access.UserHolds(calendar:access): a person who may no longer use
//     Calendar, a provider in cleanup (the admin switch off, the permission
//     missing) or a clean-up owed (turned off, the target changed) cleans up
//     (O8). Then: off when turned off; unavailable (error unavailable) when
//     the person or the provider cannot go on; otherwise — a target change —
//     the sync goes on at once, from a reconcile.
//  3. Workforce.DirectoryAccount under AsUser: none, a disabled one (a
//     leaver's) or another provider's, cleans up what was written with the
//     mailbox stored and is unavailable, error no_account. A different mailbox than the stored one cleans the
//     old one up first and starts over on the new from a reconcile.
//  4. Target own: EnsureCalendar named Vantigo; a changed id forgets the map.
//  5. Reconcile — the first sync, a create whose outcome is unknown, a
//     clean-up, and the first sync of each business day: Find from the
//     window's first instant (only events carrying this installation's
//     marker are answered), and an event — never a meeting —
//     carrying a key the desired set holds and the map does not is adopted
//     (mapped with an empty hash, so it is rewritten); a second event
//     carrying a key already mapped — the duplicate an unknown create
//     leaves — is deleted; a mapped event starting in the window that Find no
//     longer sees is forgotten, to be made anew. An event carrying a key the
//     set does not hold is left alone: such an event is most often a copy
//     that slid before the window and was forgotten on purpose (step 6) — the
//     past stays until a clean-up.
//  6. The write-back (O10, mailbox_writeback.go), after the desired set is
//     built and the map read, before the reconcile: the copies' versions,
//     the moved, missing and new ones read whole, the person's own edits
//     written to their appointments and the rest put back; the desired set
//     built again when an appointment was written. Its refusal is recorded
//     as step 8's; its failure lets the push and the busy read go on and is
//     recorded as a busy read's is, and the reconcile then forgets nothing.
//     The diff against the map (mailbox_push.go's desired set): creates,
//     updates where the hash changed, deletes of mapped keys the set no
//     longer holds that start inside the window; a mapped key that now
//     starts before it is forgotten, its copy left in the past.
//  7. Apply in slices of 100, the map written after each in a short
//     transaction; an update whose event is gone becomes a create in a later
//     slice. The first op that fails — throttled, or any failure to retry —
//     stops the person's sync after its slice: the rest wait for the next.
//  8. The result, written only if the PUT has not moved the revision on: a
//     sync that worked is active, the next due a poll later
//     (CALENDAR_MAILBOX_SYNC_POLL); a refusal (mailbox_denied, mailbox_gone)
//     is unavailable with its code, retried in six hours; a failure backs
//     off 1, 5, 15, 60 minutes, then hourly, and is failed from the fifth.
//  9. The busy read (O9.1–O9.3, mailbox_busy.go), before step 8's result,
//     once the push worked: Delta on the default calendar — afresh over
//     today−30..today+210 when there is no link, when the stored window ends
//     before today+180, or once when the provider no longer takes the link —
//     page by page while the deadline lasts; the events the map knows and
//     those Marked answers (single events that would be stored) are the
//     calendar's own copies and never stored, nor a free, cancelled or
//     removed one; each page written in one short transaction with its link,
//     a fresh delta's first emptying the person's rows; at most 5000 rows a
//     person. A refusal or failure of Delta or Marked is recorded as step
//     8's are.
//  10. Watch (O11.6), last, on a sync that pushed and left the person active
//     — one that worked, and one whose busy read or write-back failed beside
//     a push that stood: the mailbox's changes notified from now on. Its
//     error is logged by its code and changes nothing; a sync whose deadline
//     has passed does not ask, and the next one does.
//
// Change notifications (O11.6): each cycle, before it picks the due, asks
// Notified for the mailboxes the directory notified since the latest notice
// it took (held on the worker, the poll interval ago at its first cycle),
// less mailboxNoticesOverlap — a notice whose mark committed after a later
// one was read, or stamped by another replica's slightly slower clock, is
// asked for again rather than lost — and makes due at once, in one
// statement for the cycle's notices, each row of a notified mailbox,
// compared case-insensitively, that is on, active or pending, not backing
// off a failure, last synced before the notice and not due already. A
// notice asked for twice marks nothing once the row has synced since. A
// notice is a nudge: the reads stay the truth. A Notified that fails is
// logged and the cycle goes on; the poll runs either way.
//
// The clean-up (O8): target own deletes the Vantigo calendar — only one the
// provider made, and only when it holds no meeting (none Find answers as
// one, no released row); otherwise, and for a calendar not the provider's
// (ErrCalendarNotOurs), the copies in it are deleted one by one instead,
// never a meeting, and the calendar left — and the default calendar has every event
// deleted that carries this installation's marker, whatever its date (Find
// with no bound), and every mapped one (deletes are allowed in cleanup). Only
// a mailbox that is no longer there — gone, or an account the provider no
// longer manages — has its copies forgotten unremoved, the fact logged; a
// mailbox denied, a provider unusable or a failure keeps the map and the
// clean-up owed, retried on the backoff. Then the map is emptied, and the
// person's busy rows and their delta link forgotten — as they are by every
// result that leaves the sync off or unavailable (O9.9). Every clean-up but
// a target change's also Unwatches the mailbox it cleans (O11.6), once its
// copies are removed or forgotten: the subscription is the mailbox's, so a
// target change keeps it. Unwatch refused as gone, unknown or denied is
// logged by its code and the clean-up goes on — a subscription Graph will
// not let the module delete lapses within seven days, and its notifications
// name no sync; the provider unusable or a failure keeps the clean-up owed
// as a refused delete does. Nothing of
// a person's mailbox, their copies' titles or the provider's words (which
// carry the mailbox's paths) is ever logged: an error is logged by its code.

const (
	// mailboxWorkerName is what the runner logs this worker as.
	mailboxWorkerName = "calendar-mailbox-sync"

	// mailboxLeaseKey is the ASCII string "CALMBOX1" read as a big-endian
	// 64-bit value: this worker's own advisory lease.
	mailboxLeaseKey int64 = 0x43414C4D424F5831

	// mailboxTick is how often the worker wakes: a nudged person is synced
	// within a minute.
	mailboxTick = time.Minute

	// mailboxNoticesOverlap is how far before the latest notice taken the
	// next Notified asks from (design O11.6): a notice marked late — its
	// write committed after a later one was read, or stamped by a replica
	// whose clock runs a little behind — is still taken. Taking a notice
	// twice is harmless: the person has synced since.
	mailboxNoticesOverlap = 30 * time.Second

	// mailboxBatch bounds one cycle; a backlog drains over cycles.
	mailboxBatch = 50

	// mailboxSlice is the most ops one Apply is handed.
	mailboxSlice = 100

	// mailboxPersonDeadline bounds what one person's sync asks the
	// neighbours, so one slow mailbox cannot hold the cycle.
	mailboxPersonDeadline = 3 * time.Minute

	// mailboxFindBound is Find's bound: a list that long may be cut, so
	// nothing mapped is forgotten for being absent from it.
	mailboxFindBound = 5000

	// mailboxRetryUnavailable and mailboxRetryRefused are when a person is
	// tried again after the provider or their account could not be used,
	// and after the directory refused their mailbox.
	mailboxRetryUnavailable = time.Hour
	mailboxRetryRefused     = 6 * time.Hour

	// mailboxFailedAfter is the failures in a row that make the state failed.
	mailboxFailedAfter = 5

	// defaultMailboxPoll is the poll of a worker built from Deps without a
	// Config — config.go's own default for CALENDAR_MAILBOX_SYNC_POLL.
	defaultMailboxPoll = 15 * time.Minute
)

// mailboxBackoff is the wait after the nth failure in a row, the last for
// every one after.
var mailboxBackoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// The error codes calendar.mailbox_sync.error holds (openapi MailboxSync).
const (
	mailboxErrNoAccount   = "no_account"
	mailboxErrDenied      = "mailbox_denied"
	mailboxErrGone        = "mailbox_gone"
	mailboxErrUnavailable = "unavailable"
	mailboxErrFailed      = "failed"
)

// MailboxSyncWorker keeps people's Outlook copies. It implements
// worker.Worker.
type MailboxSyncWorker struct {
	deps module.Deps
	srv  *server

	// noticesMu guards noticesSince, the latest notice's At a cycle took
	// (design O11.6): zero until the first cycle sets it a poll ago.
	noticesMu    sync.Mutex
	noticesSince time.Time
	// noticesFull is whether the last Notified answered
	// contracts.MailboxNoticesMax: the next asks from noticesSince exactly,
	// the next page, not with the overlap that would answer the same page.
	noticesFull bool
}

var _ worker.Worker = (*MailboxSyncWorker)(nil)

// NewMailboxSyncWorker builds the worker over d.
func NewMailboxSyncWorker(d module.Deps) *MailboxSyncWorker {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = time.Now
	}
	return &MailboxSyncWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *MailboxSyncWorker) Name() string { return mailboxWorkerName }

// Interval is the tick, a minute.
func (w *MailboxSyncWorker) Interval() time.Duration { return mailboxTick }

// Run is the worker loop: a cycle, then the tick. A failure is logged and
// the loop goes on — the runner never restarts a worker.
func (w *MailboxSyncWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.Error("calendar: the mailbox sync cycle failed", "worker", mailboxWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the lease: it reports whether it ran — false
// when another holder has the lease — and how many people it synced,
// whatever each sync answered. A cancelled context stops it between two
// people. The unlock runs on a context stripped of cancellation, so a
// cancelled cycle still gives the lease back; an unlock that fails closes
// the connection, which ends the session and the lock with it.
func (w *MailboxSyncWorker) RunCycle(ctx context.Context) (bool, int, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("calendar: acquire a connection for the mailbox sync lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, mailboxLeaseKey).Scan(&acquired); err != nil {
		return false, 0, fmt.Errorf("calendar: take the mailbox sync lease: %w", err)
	}
	if !acquired {
		w.deps.Logger.DebugContext(ctx, "calendar: the mailbox sync lease is held elsewhere; skipping this cycle", "worker", mailboxWorkerName)
		return false, 0, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, mailboxLeaseKey); err != nil {
			w.deps.Logger.ErrorContext(release, "calendar: releasing the mailbox sync lease failed; discarding the connection",
				"worker", mailboxWorkerName, "error", err.Error())
			_ = conn.Conn().Close(release)
		}
	}()

	w.takeNotices(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return true, 0, fmt.Errorf("calendar: begin the mailbox sync due list: %w", err)
	}
	due, err := store.New(tx).ClaimDueMailboxSyncs(ctx, store.ClaimDueMailboxSyncsParams{
		Now: w.deps.Clock(), RowLimit: mailboxBatch,
	})
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return true, 0, fmt.Errorf("calendar: list the mailbox syncs due: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, 0, fmt.Errorf("calendar: commit the mailbox sync due list: %w", err)
	}
	if len(due) == 0 {
		return true, 0, nil
	}

	loc, err := w.srv.calendarZone(ctx)
	if err != nil {
		return true, 0, fmt.Errorf("calendar: read the business time zone: %w", err)
	}
	synced := 0
	for _, r := range due {
		if ctx.Err() != nil {
			break
		}
		if err := w.syncPerson(ctx, r, loc); err != nil {
			if ctx.Err() != nil {
				break
			}
			return true, synced, err
		}
		synced++
	}
	if synced > 0 {
		w.deps.Logger.InfoContext(ctx, "calendar: mailbox syncs run", "worker", mailboxWorkerName, "synced", synced)
	}
	return true, synced, nil
}

// takeNotices makes the notified people's syncs due (design O11.6): it asks
// the provider for the mailboxes notified since mailboxNoticesOverlap
// before the latest notice taken — since that notice itself after a full
// page (noticesFull), so a page of contracts.MailboxNoticesMax is followed
// by the next rather than asked again — and marks their rows due at now in
// one statement, holding no transaction across the call. noticesSince
// moves on to the latest At only when that statement worked; one that
// fails is logged, and the same notices are asked for again next cycle.
// Without a provider nothing is asked; a failing Notified is logged by its
// code and the cycle goes on.
func (w *MailboxSyncWorker) takeNotices(ctx context.Context) {
	if w.srv.mailboxCalendar() == nil {
		return
	}
	w.noticesMu.Lock()
	defer w.noticesMu.Unlock()
	now := w.deps.Clock()
	if w.noticesSince.IsZero() {
		w.noticesSince = now.Add(-w.poll())
	}
	since := w.noticesSince.Add(-mailboxNoticesOverlap)
	if w.noticesFull {
		since = w.noticesSince
	}
	notices, err := w.srv.mailboxNotified(ctx, since)
	if err != nil {
		w.deps.Logger.WarnContext(ctx, "calendar: the mailboxes notified could not be read; the poll goes on",
			"worker", mailboxWorkerName, "error", mailboxErrorCode(err))
		return
	}
	if len(notices) == 0 {
		w.noticesFull = false
		return
	}
	mailboxes, ats := make([]string, len(notices)), make([]time.Time, len(notices))
	latest := w.noticesSince
	for i, n := range notices {
		mailboxes[i], ats[i] = n.Mailbox, n.At
		if n.At.After(latest) {
			latest = n.At
		}
	}
	marked, err := w.srv.queries().MarkMailboxSyncNotified(ctx, store.MarkMailboxSyncNotifiedParams{
		Now: now, Mailboxes: mailboxes, Ats: ats,
	})
	if err != nil {
		w.deps.Logger.WarnContext(ctx, "calendar: the notified mailboxes' syncs could not be made due; they are asked for again",
			"worker", mailboxWorkerName, "error", mailboxErrorCode(err))
		return
	}
	w.noticesSince, w.noticesFull = latest, len(notices) >= contracts.MailboxNoticesMax
	w.deps.Logger.DebugContext(ctx, "calendar: mailbox change notices taken",
		"worker", mailboxWorkerName, "notices", len(notices), "due", marked)
}

// mailboxResult is what one person's sync writes to their row.
type mailboxResult struct {
	state     string
	code      string // "" for none
	next      *time.Time
	synced    bool
	attempts  int32
	reconcile bool
}

// syncPerson is one person's sync, from the row as the cycle claimed it. It
// answers an error only when the module's own database fails it; whatever a
// neighbour answered is recorded on the row.
//
// call, ctx bounded by mailboxPersonDeadline, is what every call out of the
// module is made on; the person's rows are written on ctx, so a sync cut
// short is still recorded.
func (w *MailboxSyncWorker) syncPerson(ctx context.Context, r store.CalendarMailboxSync, loc *time.Location) error {
	call, cancel := context.WithTimeout(ctx, mailboxPersonDeadline)
	defer cancel()
	now := w.deps.Clock()
	st, err := w.srv.mailboxStatus(call)
	if err != nil {
		return w.failed(ctx, r, now, "read the provider's status", err)
	}
	if st.State != contracts.MailboxAvailable && st.State != contracts.MailboxCleanup {
		state := mailboxStateUnavailable
		if r.State == mailboxStateStopping {
			state = mailboxStateStopping
		}
		return w.write(ctx, r, now, mailboxResult{
			state: state, code: mailboxErrUnavailable, next: timePtr(now.Add(mailboxRetryUnavailable)),
			attempts: r.Attempts, reconcile: true,
		})
	}
	holds, err := w.srv.accessUserHolds(call, r.UserID, "calendar:access")
	if err != nil {
		return w.failed(ctx, r, now, "check the person's access", err)
	}
	cannotGoOn := !holds || st.State == contracts.MailboxCleanup
	if cannotGoOn || r.State == mailboxStateStopping {
		// A target change — still on, and able to go on — keeps the
		// mailbox's subscription; every other clean-up removes it.
		if err := w.cleanup(ctx, call, &r, now, !r.Enabled || cannotGoOn); err != nil {
			return w.failed(ctx, r, now, "clean up", err)
		}
		switch {
		case !r.Enabled:
			return w.write(ctx, r, now, mailboxResult{state: mailboxStateOff, reconcile: true})
		case cannotGoOn:
			return w.write(ctx, r, now, mailboxResult{
				state: mailboxStateUnavailable, code: mailboxErrUnavailable,
				next: timePtr(now.Add(mailboxRetryUnavailable)), reconcile: true,
			})
		}
		// A target change, cleaned up: the sync goes on to the new one.
		r.State = mailboxStatePending
	}

	acct, err := w.srv.workforceDirectoryAccount(contracts.AsUser(withoutRequest{call}, r.UserID))
	if err != nil {
		return w.failed(ctx, r, now, "read the person's directory account", err)
	}
	key := ""
	if p := w.srv.mailboxCalendar(); p != nil {
		key = p.Key()
	}
	if !usableAccount(acct, key) {
		if err := w.cleanup(ctx, call, &r, now, true); err != nil {
			return w.failed(ctx, r, now, "clean up", err)
		}
		return w.write(ctx, r, now, mailboxResult{
			state: mailboxStateUnavailable, code: mailboxErrNoAccount,
			next: timePtr(now.Add(mailboxRetryUnavailable)), reconcile: true,
		})
	}
	mailbox := acct.ExternalID
	if r.Mailbox != nil && *r.Mailbox != mailbox {
		if err := w.cleanup(ctx, call, &r, now, true); err != nil {
			return w.failed(ctx, r, now, "clean up the previous mailbox", err)
		}
	}
	if r.Mailbox == nil || *r.Mailbox != mailbox {
		r.Mailbox = &mailbox
		if err := w.setMailbox(ctx, r, now); err != nil {
			return err
		}
	}

	calendarID := ""
	if r.Target == mailboxTargetOwn {
		known := ""
		if r.CalendarID != nil {
			known = *r.CalendarID
		}
		id, err := w.srv.mailboxEnsure(call, mailbox, known, mailboxCalendarName)
		if err != nil {
			return w.refusedOrFailed(ctx, r, now, "ensure the Vantigo calendar", err)
		}
		if id != known {
			if err := w.forgetAll(ctx, r.UserID); err != nil {
				return err
			}
			r.CalendarID, r.Reconcile = &id, true
			if err := w.setMailbox(ctx, r, now); err != nil {
				return err
			}
		}
		calendarID = id
	}

	today, err := w.srv.calendarToday(ctx, now)
	if err != nil {
		return w.failed(ctx, r, now, "read today", err)
	}
	lang := langEnglish
	u, err := w.srv.usersUser(call, r.UserID)
	if err != nil {
		return w.failed(ctx, r, now, "read the person", err)
	}
	if u != nil {
		lang = languageOf(u.PreferredLanguage)
	}
	p := mailboxPush{userID: r.UserID, row: r, today: civil(today), w: mailboxWindow(today, loc), words: wordsFor(lang)}
	desired, err := w.srv.mailboxDesired(call, p)
	if err != nil {
		return w.failed(ctx, r, now, "build the desired set", err)
	}
	reconcile := r.Reconcile || r.LastSyncedAt == nil || civil(r.LastSyncedAt.In(loc)).Before(civil(today))

	rows, err := w.srv.queries().ListMailboxEvents(ctx, r.UserID)
	if err != nil {
		return fmt.Errorf("calendar: read the mailbox map: %w", err)
	}
	wb, writeBackErr, err := w.writeBack(ctx, call, r, mailbox, calendarID, p, desired, rows, now)
	if err != nil {
		return err
	}
	if writeBackErr != nil && mailboxRefusal(writeBackErr) {
		return w.refusedOrFailed(ctx, r, now, "read the copies' versions", writeBackErr)
	}
	if wb.wrote > 0 {
		if desired, err = w.srv.mailboxDesired(call, p); err != nil {
			return w.failed(ctx, r, now, "build the desired set", err)
		}
	}
	if wb.mapChanged {
		if rows, err = w.srv.queries().ListMailboxEvents(ctx, r.UserID); err != nil {
			return fmt.Errorf("calendar: read the mailbox map: %w", err)
		}
	}
	d := newMailboxDiff(r.UserID, desired)
	d.load(rows)
	if reconcile {
		refs, err := w.srv.mailboxFind(call, mailbox, calendarID, p.w.fromAt)
		if err != nil {
			return w.refusedOrFailed(ctx, r, now, "list the mailbox's copies", err)
		}
		d.reconcile(refs, len(refs) < mailboxFindBound && writeBackErr == nil && wb.complete, p.w.from, wb.present)
	}
	ops := d.ops(p.w.from)
	if err := w.writeMap(ctx, now, d.takeWrites()); err != nil {
		return err
	}

	stop, opErr, createUnknown, err := w.apply(ctx, call, r.UserID, mailbox, calendarID, ops, now)
	if err != nil {
		return err
	}
	switch {
	case stop != nil:
		return w.refusedOrFailed(ctx, r, now, "write the copies", stop)
	case opErr != nil:
		r.Reconcile = r.Reconcile || createUnknown
		return w.failed(ctx, r, now, "write a copy", opErr)
	}
	callErr, err := w.readBusy(ctx, call, r, mailbox, today, loc, now)
	if err != nil {
		return err
	}
	if callErr != nil && mailboxRefused(callErr) {
		return w.refusedOrFailed(ctx, r, now, "read the busy time", callErr)
	}
	// The push stood: the person is active whatever else failed beside it.
	switch {
	case callErr != nil:
		err = w.pushedButFailed(ctx, r, now, "read the busy time", callErr)
	case writeBackErr != nil:
		err = w.pushedButFailed(ctx, r, now, "take the changes made in Outlook", writeBackErr)
	default:
		err = w.write(ctx, r, now, mailboxResult{
			state: mailboxStateActive, next: timePtr(now.Add(w.poll())), synced: true,
		})
	}
	if err != nil {
		return err
	}
	w.watch(ctx, call, r.UserID, mailbox)
	return nil
}

// watch asks the provider to keep the mailbox's changes notified (design
// O11.6), last in a sync that pushed and left the person active, on call —
// not at all once its deadline has passed: the next sync asks. Its error is
// logged by its code and changes nothing: the row is already written, and
// the poll runs either way.
func (w *MailboxSyncWorker) watch(ctx, call context.Context, userID uuid.UUID, mailbox string) {
	if call.Err() != nil {
		return
	}
	if err := w.srv.mailboxWatch(call, mailbox); err != nil {
		w.deps.Logger.WarnContext(ctx, "calendar: a person's mailbox could not be watched for changes; the poll goes on",
			"worker", mailboxWorkerName, "user_id", userID.String(), "error", mailboxErrorCode(err))
	}
}

// pendingOp is one op of a sync with the copy it writes (nil for a delete)
// and the map key it changes ("" for a foreign event's delete).
type pendingOp struct {
	op   contracts.MailboxOp
	key  string
	item *mailboxItem
}

// mapWrite is one change to the person's map: an upsert, or a forget when
// eventID is "". changeKey is the event's version the write answered (design
// O10.3); nil keeps the one stored.
type mapWrite struct {
	userID    uuid.UUID
	key       string
	eventID   string
	hash      string
	startsOn  time.Time
	changeKey *string
}

// mapEntry is one row of the person's map as the diff holds it.
type mapEntry struct {
	eventID, hash string
	startsOn      time.Time
	// released: the person turned the copy into a meeting (design O10.8) —
	// never written or deleted again, forgotten when no longer wanted.
	released bool
}

// mailboxDiff is one sync's desired set against the person's map.
type mailboxDiff struct {
	userID  uuid.UUID
	desired []mailboxItem
	want    map[string]*mailboxItem
	mapped  map[string]mapEntry
	writes  []mapWrite
	foreign []string // the duplicates' event ids, to delete
}

func newMailboxDiff(userID uuid.UUID, desired []mailboxItem) *mailboxDiff {
	d := &mailboxDiff{userID: userID, desired: desired, want: map[string]*mailboxItem{}, mapped: map[string]mapEntry{}}
	for i := range desired {
		d.want[desired[i].key] = &desired[i]
	}
	return d
}

// load is the map as the database holds it.
func (d *mailboxDiff) load(rows []store.CalendarMailboxEvent) {
	for _, r := range rows {
		startsOn, _ := fromPgDate(r.StartsOn)
		d.mapped[r.ItemKey] = mapEntry{eventID: r.EventID, hash: r.ContentHash, startsOn: startsOn, released: r.ReleasedAt != nil}
	}
}

// reconcile holds the map against what the mailbox holds (step 5): refs, the
// events Find answered from the window's first instant. complete is whether
// refs is the whole list, so an absent mapped event is truly gone — judged
// only for one starting on or after from, the window's first day, as one
// that starts before it Find was never asked about — and false too when the
// write-back did not complete, as an absence may then be a delete still
// unread (design O10.8). present is the events the write-back saw there,
// never forgotten: an event taken in from the Vantigo calendar carries no
// marker until the push tags it. A meeting Find answers (design O10.8) is
// never adopted nor deleted as a duplicate — the person turned it into
// theirs — and a mapped one is held as released for this sync, never
// written nor deleted by its push, until the write-back releases its row.
func (d *mailboxDiff) reconcile(refs []contracts.MailboxEventRef, complete bool, from time.Time, present map[string]bool) {
	found := map[string]bool{}
	for id := range present {
		found[id] = true
	}
	meeting := map[string]bool{}
	for _, ref := range refs {
		found[ref.EventID] = true
		if ref.Meeting {
			meeting[ref.EventID] = true
		}
	}
	mine := map[string]bool{}
	for key, e := range d.mapped {
		if !found[e.eventID] && complete && !e.startsOn.Before(from) {
			delete(d.mapped, key)
			d.writes = append(d.writes, mapWrite{userID: d.userID, key: key})
			continue
		}
		if meeting[e.eventID] && !e.released {
			e.released = true
			d.mapped[key] = e
		}
		mine[e.eventID] = true
	}
	for _, ref := range refs {
		if ref.Key == "" || ref.Meeting || mine[ref.EventID] {
			continue
		}
		it, wanted := d.want[ref.Key]
		if !wanted {
			continue
		}
		if _, mapped := d.mapped[ref.Key]; !mapped {
			d.mapped[ref.Key] = mapEntry{eventID: ref.EventID, startsOn: it.startsOn}
			d.writes = append(d.writes, mapWrite{userID: d.userID, key: ref.Key, eventID: ref.EventID, startsOn: it.startsOn})
			mine[ref.EventID] = true
			continue
		}
		d.foreign = append(d.foreign, ref.EventID)
	}
}

// ops is the diff (step 6): the duplicates' deletes, then the creates and
// updates in the desired set's order, then the deletes of mapped keys no
// longer wanted that start on or after from — those before it are forgotten.
// A released copy (design O10.8) is never updated nor deleted: forgotten
// once no longer wanted. An update writes the key as the marker, so an event
// taken in from the Vantigo calendar is tagged (O10.2).
func (d *mailboxDiff) ops(from time.Time) []pendingOp {
	var out []pendingOp
	for _, id := range d.foreign {
		out = append(out, pendingOp{op: contracts.MailboxOp{Kind: contracts.MailboxOpDelete, EventID: id}})
	}
	for i := range d.desired {
		it := &d.desired[i]
		e, ok := d.mapped[it.key]
		switch {
		case !ok:
			out = append(out, createOp(it))
		case e.released:
		case e.hash != it.hash:
			ev := it.event
			out = append(out, pendingOp{op: contracts.MailboxOp{Kind: contracts.MailboxOpUpdate, EventID: e.eventID, Key: it.key, Event: &ev}, key: it.key, item: it})
		}
	}
	keys := make([]string, 0, len(d.mapped))
	for key := range d.mapped {
		if d.want[key] == nil {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		e := d.mapped[key]
		if e.released || e.startsOn.Before(from) {
			d.writes = append(d.writes, mapWrite{userID: d.userID, key: key})
			continue
		}
		out = append(out, pendingOp{op: contracts.MailboxOp{Kind: contracts.MailboxOpDelete, EventID: e.eventID}, key: key})
	}
	return out
}

// takeWrites is the map changes collected so far, and forgets them.
func (d *mailboxDiff) takeWrites() []mapWrite {
	out := d.writes
	d.writes = nil
	return out
}

// createOp is the create of a copy.
func createOp(it *mailboxItem) pendingOp {
	ev := it.event
	return pendingOp{op: contracts.MailboxOp{Kind: contracts.MailboxOpCreate, Key: it.key, Event: &ev}, key: it.key, item: it}
}

// apply runs ops in slices (step 7) on call, writing the map after each on
// ctx. It answers the refusal that stopped the person (stop), or the first
// failure among a slice's ops (opErr) — throttled or any other, after which
// no further slice is sent — and whether a create's outcome is unknown, or
// the database's error.
func (w *MailboxSyncWorker) apply(ctx, call context.Context, userID uuid.UUID, mailbox, calendarID string, queue []pendingOp, now time.Time) (stop, opErr error, createUnknown bool, err error) {
	for len(queue) > 0 {
		if call.Err() != nil {
			return nil, call.Err(), false, nil
		}
		n := min(len(queue), mailboxSlice)
		slice := queue[:n]
		queue = queue[n:]
		ops := make([]contracts.MailboxOp, n)
		for i, p := range slice {
			ops[i] = p.op
		}
		results, callErr := w.srv.mailboxApply(call, mailbox, calendarID, ops)
		if callErr != nil {
			if mailboxRefusal(callErr) {
				return callErr, nil, false, nil
			}
			return nil, callErr, false, nil
		}
		if len(results) != n {
			return nil, fmt.Errorf("calendar: the provider answered %d results for %d ops", len(results), n), false, nil
		}
		var writes []mapWrite
		for i, res := range results {
			p := slice[i]
			switch {
			case res.Err != nil:
				switch {
				case mailboxRefusal(res.Err):
					stop = res.Err
				case opErr == nil:
					opErr = res.Err
				}
				createUnknown = createUnknown || (!mailboxRefusal(res.Err) && p.op.Kind == contracts.MailboxOpCreate)
			case p.op.Kind == contracts.MailboxOpDelete:
				if p.key != "" {
					writes = append(writes, mapWrite{userID: userID, key: p.key})
				}
			case p.op.Kind == contracts.MailboxOpUpdate && res.Gone:
				queue = append(queue, createOp(p.item))
			default:
				id := res.EventID
				if id == "" {
					id = p.op.EventID
				}
				var changeKey *string
				if res.ChangeKey != "" {
					changeKey = &res.ChangeKey
				}
				writes = append(writes, mapWrite{userID: userID, key: p.key, eventID: id, hash: p.item.hash, startsOn: p.item.startsOn, changeKey: changeKey})
			}
		}
		if err := w.writeMap(ctx, now, writes); err != nil {
			return nil, nil, false, err
		}
		if stop != nil {
			return stop, nil, false, nil
		}
		if opErr != nil {
			return nil, opErr, createUnknown, nil
		}
	}
	return nil, nil, false, nil
}

// cleanup removes the person's copies (O8) from the mailbox stored on r,
// asking the provider on call: the Vantigo calendar when one was made — or,
// when the provider says it is not one it made (ErrCalendarNotOurs), the
// copies in it — otherwise every copy of the default calendar. A mailbox no
// longer there (gone, or an account the provider no longer manages) has its
// copies forgotten and the fact logged. Then the map is emptied, the busy
// rows and their delta forgotten (design O9.9), the calendar forgotten and
// the next sync made a reconcile; r is changed to match. Anything else — the mailbox denied, the provider unusable, a
// failure — is answered as the error, with the map as the deletes that
// worked left it and the clean-up still owed.
//
// unwatch (every clean-up but a target change's) also deletes the mailbox's
// subscription (design O11.6), after the copies — they are what the person
// sees, so an Unwatch refused does not hold them back — and before anything
// is forgotten, so an Unwatch the provider could not make (unusable) or that
// failed keeps the clean-up owed whole, retried as a refused delete is (the
// deletes it repeats are done or gone by then). Gone, unknown or denied, the
// subscription is left to the provider — a denied delete stays owed there,
// and Graph's subscription lapses within seven days — and the clean-up,
// a mailbox move's among them, goes on.
func (w *MailboxSyncWorker) cleanup(ctx, call context.Context, r *store.CalendarMailboxSync, now time.Time, unwatch bool) error {
	if r.Mailbox != nil {
		mailbox := *r.Mailbox
		var err error
		if r.CalendarID != nil {
			var meetings, released int
			meetings, released, err = w.meetingsIn(ctx, call, r.UserID, mailbox, *r.CalendarID)
			switch {
			case err != nil:
			case meetings > 0 || released > 0:
				w.deps.Logger.InfoContext(ctx, "calendar: a person's Vantigo calendar holds meetings; only the copies in it are deleted, the calendar left",
					"worker", mailboxWorkerName, "user_id", r.UserID.String(), "meetings", meetings, "released", released)
				err = w.cleanupEvents(ctx, call, r.UserID, mailbox, *r.CalendarID, now)
			default:
				err = w.srv.mailboxDeleteCalendar(call, mailbox, *r.CalendarID)
				if errors.Is(err, contracts.ErrCalendarNotOurs) {
					w.deps.Logger.WarnContext(ctx, "calendar: a person's Vantigo calendar is not one the provider made; only the copies in it are deleted",
						"worker", mailboxWorkerName, "user_id", r.UserID.String())
					err = w.cleanupEvents(ctx, call, r.UserID, mailbox, *r.CalendarID, now)
				}
			}
		} else {
			err = w.cleanupEvents(ctx, call, r.UserID, mailbox, "", now)
		}
		switch {
		case err == nil:
		case errors.Is(err, contracts.ErrMailboxGone) || errors.Is(err, contracts.ErrMailboxUnknown):
			w.deps.Logger.WarnContext(ctx, "calendar: a person's mailbox is no longer there for the clean-up; their Outlook copies are forgotten",
				"worker", mailboxWorkerName, "user_id", r.UserID.String(), "error", mailboxErrorCode(err))
		default:
			return err
		}
		if unwatch {
			err := w.srv.mailboxUnwatch(call, mailbox)
			switch {
			case err == nil:
			case errors.Is(err, contracts.ErrMailboxGone) || errors.Is(err, contracts.ErrMailboxUnknown) ||
				errors.Is(err, contracts.ErrMailboxDenied):
				w.deps.Logger.InfoContext(ctx, "calendar: a person's mailbox could not be unwatched; its subscription is left to lapse and the clean-up goes on",
					"worker", mailboxWorkerName, "user_id", r.UserID.String(), "error", mailboxErrorCode(err))
			default:
				return err
			}
		}
	}
	if err := w.forgetAll(ctx, r.UserID); err != nil {
		return err
	}
	if err := w.forgetBusy(ctx, r.UserID, now); err != nil {
		return err
	}
	if err := w.srv.queries().ClearMailboxPutBack(ctx, r.UserID); err != nil {
		return fmt.Errorf("calendar: forget the change put back: %w", err)
	}
	r.CalendarID, r.Reconcile = nil, true
	r.BusyLink, r.BusyFrom, r.BusyTo, r.BusySyncedAt = nil, pgtype.Date{}, pgtype.Date{}, nil
	return w.setMailbox(ctx, *r, now)
}

// meetingsIn counts what keeps the Vantigo calendar calendarID from being
// deleted whole (design O8, O10.8): the events Find answers there that are
// meetings, and the person's released rows — a copy they turned into a
// meeting, whatever Find now says of it.
func (w *MailboxSyncWorker) meetingsIn(ctx, call context.Context, userID uuid.UUID, mailbox, calendarID string) (int, int, error) {
	refs, err := w.srv.mailboxFind(call, mailbox, calendarID, time.Time{})
	if err != nil {
		return 0, 0, err
	}
	rows, err := w.srv.queries().ListMailboxEvents(ctx, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("calendar: read the mailbox map: %w", err)
	}
	meetings, released := 0, 0
	for _, ref := range refs {
		if ref.Meeting {
			meetings++
		}
	}
	for _, row := range rows {
		if row.ReleasedAt != nil {
			released++
		}
	}
	return meetings, released, nil
}

// cleanupEvents deletes, from calendarID ("" the default calendar), every
// event Find answers with no bound — every copy of this installation's,
// whatever its date — and every mapped one besides, in slices, forgetting
// each one done or gone. A meeting — one Find answers as such, or a
// released row's event — is never deleted, only forgotten. A refusal is
// answered as is; a list as long as Find's bound is answered as a failure
// once its deletes are done, so the rest are deleted on the retry.
func (w *MailboxSyncWorker) cleanupEvents(ctx, call context.Context, userID uuid.UUID, mailbox, calendarID string, now time.Time) error {
	refs, err := w.srv.mailboxFind(call, mailbox, calendarID, time.Time{})
	if err != nil {
		return err
	}
	rows, err := w.srv.queries().ListMailboxEvents(ctx, userID)
	if err != nil {
		return fmt.Errorf("calendar: read the mailbox map: %w", err)
	}
	keyOf := map[string]string{}
	released := map[string]bool{}
	for _, ref := range refs {
		if ref.Meeting {
			released[ref.EventID] = true
		}
	}
	var ids []string
	var forget []mapWrite
	for _, row := range rows {
		keyOf[row.EventID] = row.ItemKey
		if row.ReleasedAt != nil {
			released[row.EventID] = true
		}
		if released[row.EventID] {
			forget = append(forget, mapWrite{userID: userID, key: row.ItemKey})
		}
	}
	// A released copy, or any meeting, is forgotten, never deleted.
	if err := w.writeMap(ctx, now, forget); err != nil {
		return err
	}
	for _, ref := range refs {
		if !released[ref.EventID] && !slices.Contains(ids, ref.EventID) {
			ids = append(ids, ref.EventID)
		}
	}
	for _, row := range rows {
		if !released[row.EventID] && !slices.Contains(ids, row.EventID) {
			ids = append(ids, row.EventID)
		}
	}
	for start := 0; start < len(ids); start += mailboxSlice {
		slice := ids[start:min(start+mailboxSlice, len(ids))]
		ops := make([]contracts.MailboxOp, len(slice))
		for i, id := range slice {
			ops[i] = contracts.MailboxOp{Kind: contracts.MailboxOpDelete, EventID: id}
		}
		results, err := w.srv.mailboxApply(call, mailbox, calendarID, ops)
		if err != nil {
			return err
		}
		if len(results) != len(slice) {
			return fmt.Errorf("calendar: the provider answered %d results for %d deletes", len(results), len(slice))
		}
		var writes []mapWrite
		var failure, refused error
		for i, res := range results {
			switch {
			case res.Err == nil:
				if key, ok := keyOf[slice[i]]; ok {
					writes = append(writes, mapWrite{userID: userID, key: key})
				}
			case mailboxRefusal(res.Err):
				refused = res.Err
			default:
				failure = res.Err
			}
		}
		if err := w.writeMap(ctx, now, writes); err != nil {
			return err
		}
		if refused != nil {
			return refused
		}
		if failure != nil {
			return failure
		}
	}
	if len(refs) >= mailboxFindBound {
		return errors.New("calendar: more copies remain than one list holds")
	}
	return nil
}

// mailboxRefusal reports whether err is one of the provider's answers that no
// retry soon will change: the mailbox gone, unknown, denied, or the provider
// unusable.
func mailboxRefusal(err error) bool {
	return errors.Is(err, contracts.ErrMailboxGone) || errors.Is(err, contracts.ErrMailboxUnknown) ||
		errors.Is(err, contracts.ErrMailboxDenied) || errors.Is(err, contracts.ErrMailboxUnavailable)
}

// refusedOrFailed records a provider's answer: a refusal sets the state
// unavailable with its code — mailbox_denied and mailbox_gone retried in six
// hours, an unknown account (no_account) and an unusable provider
// (unavailable) in one — and anything else is a failure.
func (w *MailboxSyncWorker) refusedOrFailed(ctx context.Context, r store.CalendarMailboxSync, now time.Time, what string, err error) error {
	code, retry := "", mailboxRetryUnavailable
	switch {
	case errors.Is(err, contracts.ErrMailboxDenied):
		code, retry = mailboxErrDenied, mailboxRetryRefused
	case errors.Is(err, contracts.ErrMailboxGone):
		code, retry = mailboxErrGone, mailboxRetryRefused
	case errors.Is(err, contracts.ErrMailboxUnknown):
		code = mailboxErrNoAccount
	case errors.Is(err, contracts.ErrMailboxUnavailable):
		code = mailboxErrUnavailable
	default:
		return w.failed(ctx, r, now, what, err)
	}
	w.deps.Logger.InfoContext(ctx, "calendar: a person's mailbox refused the sync",
		"worker", mailboxWorkerName, "user_id", r.UserID.String(), "code", code)
	return w.write(ctx, r, now, mailboxResult{
		state: mailboxStateUnavailable, code: code, next: timePtr(now.Add(retry)), reconcile: true,
	})
}

// mailboxRefused reports whether err is the provider refusing the person's
// mailbox, which refusedOrFailed records as unavailable.
func mailboxRefused(err error) bool {
	return errors.Is(err, contracts.ErrMailboxDenied) || errors.Is(err, contracts.ErrMailboxGone) ||
		errors.Is(err, contracts.ErrMailboxUnknown) || errors.Is(err, contracts.ErrMailboxUnavailable)
}

// pushedButFailed records a step that failed beside a push that worked — the
// busy read (design O9.1) or the write-back (O10.5) — what: the sync active
// and synced at now, as the push left it, with the error failed, the
// attempts counted and the next sync on the failure's backoff, so the
// failure is retried soon and does not hide that the push reached the
// mailbox. One that keeps failing never turns the row failed: the push it
// rides on works. When both fail, the busy read's is the one recorded.
func (w *MailboxSyncWorker) pushedButFailed(ctx context.Context, r store.CalendarMailboxSync, now time.Time, what string, err error) error {
	w.deps.Logger.WarnContext(ctx, "calendar: a person's mailbox sync failed; it is retried",
		"worker", mailboxWorkerName, "user_id", r.UserID.String(), "step", what, "error", mailboxErrorCode(err))
	attempts := r.Attempts + 1
	return w.write(ctx, r, now, mailboxResult{
		state: mailboxStateActive, code: mailboxErrFailed, synced: true, attempts: attempts,
		next: timePtr(now.Add(mailboxBackoff[min(int(attempts), len(mailboxBackoff))-1])),
	})
}

// failed records a failure to retry: the attempts counted, the backoff, the
// state failed from the fifth in a row — a clean-up owed stays owed, and
// stays due. The error code is the refusal's when a clean-up met one
// (mailbox_denied, unavailable), failed otherwise; the log names the step and
// the code, never the error's words.
func (w *MailboxSyncWorker) failed(ctx context.Context, r store.CalendarMailboxSync, now time.Time, what string, err error) error {
	w.deps.Logger.WarnContext(ctx, "calendar: a person's mailbox sync failed; it is retried",
		"worker", mailboxWorkerName, "user_id", r.UserID.String(), "step", what, "error", mailboxErrorCode(err))
	code := mailboxErrFailed
	switch {
	case errors.Is(err, contracts.ErrMailboxDenied):
		code = mailboxErrDenied
	case errors.Is(err, contracts.ErrMailboxUnavailable):
		code = mailboxErrUnavailable
	}
	attempts := r.Attempts + 1
	wait := mailboxBackoff[min(int(attempts), len(mailboxBackoff))-1]
	state := mailboxStatePending
	switch {
	case r.State == mailboxStateStopping:
		state = mailboxStateStopping
	case attempts >= mailboxFailedAfter:
		state = mailboxStateFailed
	case r.State == mailboxStateActive:
		state = mailboxStateActive
	}
	return w.write(ctx, r, now, mailboxResult{
		state: state, code: code, next: timePtr(now.Add(wait)), attempts: attempts, reconcile: r.Reconcile,
	})
}

// mailboxErrorCode is err as the log names it: the provider's sentinel, or
// the context's, by a code — never the error's words, which may carry the
// mailbox's paths.
func mailboxErrorCode(err error) string {
	switch {
	case errors.Is(err, contracts.ErrMailboxDenied):
		return mailboxErrDenied
	case errors.Is(err, contracts.ErrMailboxGone):
		return mailboxErrGone
	case errors.Is(err, contracts.ErrMailboxUnknown):
		return "mailbox_unknown"
	case errors.Is(err, contracts.ErrMailboxUnavailable):
		return mailboxErrUnavailable
	case errors.Is(err, contracts.ErrCalendarNotOurs):
		return "calendar_not_ours"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return mailboxErrFailed
}

// write records res on r's row, unless a PUT has moved the revision on — the
// sync was then of a setting the row no longer holds, and the PUT has made
// it due again. A result that leaves the sync off or unavailable forgets the
// person's busy rows and their delta (design O9.9); a failure on the backoff
// keeps them.
func (w *MailboxSyncWorker) write(ctx context.Context, r store.CalendarMailboxSync, now time.Time, res mailboxResult) error {
	params := store.WriteMailboxSyncResultParams{
		State: res.state, NextSyncAt: res.next, Attempts: res.attempts, Reconcile: res.reconcile,
		Now: now, UserID: r.UserID, Revision: r.Revision,
	}
	if res.code != "" {
		params.Error = &res.code
	}
	if res.synced {
		params.LastSyncedAt = &now
	}
	_, err := w.srv.queries().WriteMailboxSyncResult(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		w.deps.Logger.DebugContext(ctx, "calendar: a mailbox sync was overtaken by a change of the setting",
			"worker", mailboxWorkerName, "user_id", r.UserID.String())
		return nil
	}
	if err != nil {
		return fmt.Errorf("calendar: record a mailbox sync: %w", err)
	}
	if res.state == mailboxStateOff || res.state == mailboxStateUnavailable {
		return w.forgetBusy(ctx, r.UserID, now)
	}
	return nil
}

// setMailbox records r's mailbox and calendar as soon as they are known.
func (w *MailboxSyncWorker) setMailbox(ctx context.Context, r store.CalendarMailboxSync, now time.Time) error {
	if err := w.srv.queries().SetMailboxSyncMailbox(ctx, store.SetMailboxSyncMailboxParams{
		Mailbox: r.Mailbox, CalendarID: r.CalendarID, Now: now, UserID: r.UserID,
	}); err != nil {
		return fmt.Errorf("calendar: record the person's mailbox: %w", err)
	}
	return nil
}

// forgetAll empties the person's map.
func (w *MailboxSyncWorker) forgetAll(ctx context.Context, userID uuid.UUID) error {
	if _, err := w.srv.queries().DeleteMailboxEvents(ctx, userID); err != nil {
		return fmt.Errorf("calendar: empty the mailbox map: %w", err)
	}
	return nil
}

// writeMap writes the map changes in one short transaction.
func (w *MailboxSyncWorker) writeMap(ctx context.Context, now time.Time, writes []mapWrite) error {
	if len(writes) == 0 {
		return nil
	}
	return w.srv.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		for _, m := range writes {
			var err error
			if m.eventID == "" {
				err = txq.DeleteMailboxEvent(ctx, store.DeleteMailboxEventParams{UserID: m.userID, ItemKey: m.key})
			} else {
				err = txq.UpsertMailboxEvent(ctx, store.UpsertMailboxEventParams{
					UserID: m.userID, ItemKey: m.key, EventID: m.eventID, ContentHash: m.hash,
					StartsOn: pgDate(m.startsOn), ChangeKey: m.changeKey, Now: now,
				})
			}
			if err != nil {
				return fmt.Errorf("calendar: write the mailbox map: %w", err)
			}
		}
		return nil
	})
}

// poll is CALENDAR_MAILBOX_SYNC_POLL, fifteen minutes when a bare Deps has
// no Config.
func (w *MailboxSyncWorker) poll() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CalendarMailboxSyncPoll > 0 {
		return w.deps.Config.CalendarMailboxSyncPoll
	}
	return defaultMailboxPoll
}

// timePtr is t's address, for a nullable column.
func timePtr(t time.Time) *time.Time { return &t }
