package calendar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the busy read (calendar Outlook sync design O9.1–O9.3, O9.9):
// after a sync's push worked, the worker reads the changes to the person's
// default calendar through the provider's Delta and keeps, in
// calendar.mailbox_busy, the times and show_as of every event that blocks
// their time and is not one of the calendar's own copies — and nothing else
// of it. The delta's link and the window it was started over are kept on
// calendar.mailbox_sync. Every call out is made on the sync's call context
// with no transaction open; each page is written in one short transaction
// after its calls. Nothing of an event — not even its id — is ever logged.

const (
	// outlookKind is the kind of an Outlook event's item (design O9.5).
	outlookKind = "calendar.outlook"

	// busyDaysBack and busyDaysAhead are the read's window around today
	// (design O9.1): wider than the push's, so a delta started once lasts
	// about a month.
	busyDaysBack  = 30
	busyDaysAhead = 210

	// busyRestartAhead: a delta whose window ends before today plus this
	// many days is started afresh.
	busyRestartAhead = 180
)

// mailboxBusyCap is the most busy rows one person holds (design O9.3); past
// it a new event is not stored. A variable so the tests can lower it.
var mailboxBusyCap = 5000

// busyShown are the show_as values a colleague's calendar and the
// CalendarBusy contract count as busy (design O9.7, O9.8).
var busyShown = []string{contracts.ShowAsBusy, contracts.ShowAsOOF}

// busyRead is one sync's read of a person's default calendar.
type busyRead struct {
	userID   uuid.UUID
	mailbox  string
	from, to time.Time // the window, civil dates
	fresh    bool      // the next page is a fresh delta's first
	link     string
	ours     map[string]bool // the person's copies: the map's ids and those Marked answered
	stored   map[string]bool // the busy rows' event ids
	capped   bool            // the cap was met and logged in this sync
}

// readBusy is the busy read (step 9): it continues the person's delta, or
// starts it afresh when there is no link, when its window ends before today
// plus busyRestartAhead days, or once when the provider no longer takes the
// link, and takes its pages while the delta is not done and call's deadline
// lasts. It answers the provider's refusal or failure as callErr, for the
// caller to record (refusedOrFailed), and the module's own database's
// failure as err.
func (w *MailboxSyncWorker) readBusy(ctx, call context.Context, r store.CalendarMailboxSync, mailbox string, today time.Time, loc *time.Location, now time.Time) (callErr, err error) {
	today = civil(today)
	b := busyRead{userID: r.UserID, mailbox: mailbox}
	busyTo, hasWindow := fromPgDate(r.BusyTo)
	busyFrom, _ := fromPgDate(r.BusyFrom)
	if r.BusyLink == nil || *r.BusyLink == "" || !hasWindow || busyTo.Before(addDays(today, busyRestartAhead)) {
		b.restart(today)
	} else {
		b.link, b.from, b.to = *r.BusyLink, busyFrom, busyTo
	}
	rows, err := w.srv.queries().ListMailboxEvents(ctx, r.UserID)
	if err != nil {
		return nil, fmt.Errorf("calendar: read the mailbox map: %w", err)
	}
	b.ours = make(map[string]bool, len(rows))
	for _, row := range rows {
		b.ours[row.EventID] = true
	}
	ids, err := w.srv.queries().ListMailboxBusyEventIDs(ctx, r.UserID)
	if err != nil {
		return nil, fmt.Errorf("calendar: read the busy rows: %w", err)
	}
	b.stored = make(map[string]bool, len(ids))
	for _, id := range ids {
		b.stored[id] = true
	}

	restarted := false
	for call.Err() == nil {
		page, deltaErr := w.srv.mailboxDelta(call, mailbox, wallClock(b.from, loc), wallClock(addDays(b.to, 1), loc), b.link)
		if errors.Is(deltaErr, contracts.ErrMailboxDeltaExpired) && !restarted {
			restarted = true
			if err := w.forgetBusy(ctx, r.UserID, now); err != nil {
				return nil, err
			}
			b.restart(today)
			b.stored = map[string]bool{}
			continue
		}
		if deltaErr != nil {
			return deltaErr, nil
		}
		marked, markedErr := w.marked(call, &b, page.Changes)
		if markedErr != nil {
			return markedErr, nil
		}
		for _, id := range marked {
			b.ours[id] = true
		}
		if err := w.writeBusyPage(ctx, &b, page, now); err != nil {
			return nil, err
		}
		if page.Done {
			return nil, nil
		}
	}
	return nil, nil
}

// restart makes the read a fresh delta over today's window.
func (b *busyRead) restart(today time.Time) {
	b.fresh, b.link = true, ""
	b.from, b.to = addDays(today, -busyDaysBack), addDays(today, busyDaysAhead)
}

// marked asks the provider which of the page's candidates are the
// calendar's own copies (design O9.2): the single events that would be
// stored and that the map does not know, in slices of
// contracts.MailboxMarkedMax. It answers their ids.
func (w *MailboxSyncWorker) marked(call context.Context, b *busyRead, changes []contracts.MailboxChange) ([]string, error) {
	var candidates []string
	seen := map[string]bool{}
	for _, c := range changes {
		if c.Series || !keeps(c) || b.ours[c.EventID] || seen[c.EventID] {
			continue
		}
		seen[c.EventID] = true
		candidates = append(candidates, c.EventID)
	}
	var out []string
	for start := 0; start < len(candidates); start += contracts.MailboxMarkedMax {
		refs, err := w.srv.mailboxMarked(call, b.mailbox, candidates[start:min(start+contracts.MailboxMarkedMax, len(candidates))])
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			out = append(out, ref.EventID)
		}
	}
	return out, nil
}

// keeps reports whether a change is one the busy rows hold, ours aside: not
// removed, not cancelled, not free.
func keeps(c contracts.MailboxChange) bool {
	return !c.Removed && !c.Cancelled && c.ShowAs != contracts.ShowAsFree
}

// writeBusyPage writes one page in one short transaction: a fresh delta's
// first page empties the person's rows first; then each change is a delete
// (removed, cancelled, free, or ours) or an upsert — but a new event past
// the cap is not stored, the fact logged once a sync by the person's id —
// and the link, with the delta's window, and the read's time at its end.
func (w *MailboxSyncWorker) writeBusyPage(ctx context.Context, b *busyRead, page contracts.MailboxDelta, now time.Time) error {
	fresh := b.fresh
	stored := make(map[string]bool, len(b.stored))
	for id := range b.stored {
		stored[id] = true
	}
	if fresh {
		stored = map[string]bool{}
	}
	capped := false
	err := w.srv.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if fresh {
			if _, err := txq.DeleteMailboxBusyForUser(ctx, b.userID); err != nil {
				return fmt.Errorf("calendar: empty the busy rows: %w", err)
			}
		}
		for _, c := range page.Changes {
			if !keeps(c) || b.ours[c.EventID] {
				if err := txq.DeleteMailboxBusy(ctx, store.DeleteMailboxBusyParams{UserID: b.userID, EventID: c.EventID}); err != nil {
					return fmt.Errorf("calendar: forget a busy row: %w", err)
				}
				delete(stored, c.EventID)
				continue
			}
			if !stored[c.EventID] && len(stored) >= mailboxBusyCap {
				capped = true
				continue
			}
			if err := txq.UpsertMailboxBusy(ctx, busyRow(b.userID, c, now)); err != nil {
				return fmt.Errorf("calendar: write a busy row: %w", err)
			}
			stored[c.EventID] = true
		}
		if err := txq.SetMailboxBusyLink(ctx, store.SetMailboxBusyLinkParams{
			BusyLink: page.Link, BusyFrom: pgDate(b.from), BusyTo: pgDate(b.to), Now: now, UserID: b.userID,
		}); err != nil {
			return fmt.Errorf("calendar: record the busy link: %w", err)
		}
		if page.Done {
			if err := txq.SetMailboxBusySynced(ctx, store.SetMailboxBusySyncedParams{Now: now, UserID: b.userID}); err != nil {
				return fmt.Errorf("calendar: record the busy read: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	b.stored, b.fresh, b.link = stored, false, page.Link
	if capped && !b.capped {
		b.capped = true
		w.deps.Logger.WarnContext(ctx, "calendar: a person's Outlook busy time is past its cap; new events are not stored",
			"worker", mailboxWorkerName, "user_id", b.userID.String(), "cap", mailboxBusyCap)
	}
	return nil
}

// busyRow is a change as its row: an all-day event's dates (end inclusive),
// a timed one's instants, and its show_as — busy when the provider answered
// a value the contract does not name.
func busyRow(userID uuid.UUID, c contracts.MailboxChange, now time.Time) store.UpsertMailboxBusyParams {
	showAs := c.ShowAs
	if showAs == "" {
		showAs = contracts.ShowAsBusy
	}
	p := store.UpsertMailboxBusyParams{UserID: userID, EventID: c.EventID, AllDay: c.AllDay, ShowAs: showAs, Now: now}
	if c.AllDay {
		end := civil(c.EndDate)
		if end.Before(civil(c.StartDate)) {
			end = civil(c.StartDate)
		}
		p.StartDate, p.EndDate = pgDate(c.StartDate), pgDate(end)
		return p
	}
	sa, ea := c.StartsAt.UTC(), c.EndsAt.UTC()
	p.StartsAt, p.EndsAt = &sa, &ea
	return p
}

// forgetBusy forgets the person's busy rows and their delta — its link, its
// window and the last read — in one short transaction (design O9.4, O9.9).
func (w *MailboxSyncWorker) forgetBusy(ctx context.Context, userID uuid.UUID, now time.Time) error {
	return w.srv.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if _, err := txq.DeleteMailboxBusyForUser(ctx, userID); err != nil {
			return fmt.Errorf("calendar: forget the busy rows: %w", err)
		}
		if err := txq.ClearMailboxBusyLink(ctx, store.ClearMailboxBusyLinkParams{Now: now, UserID: userID}); err != nil {
			return fmt.Errorf("calendar: forget the busy link: %w", err)
		}
		return nil
	})
}

// outlookItemID is an Outlook event's item id (design O9.5): the first 16
// hex digits of the SHA-256 of its id in the mailbox, which never reaches
// the browser.
func outlookItemID(eventID string) string {
	sum := sha256.Sum256([]byte(eventID))
	return hex.EncodeToString(sum[:])[:16]
}

// outlookItem is one busy row on the wire: no title, its show_as, an
// all-day row's dates (end inclusive) or a timed one's instants.
func outlookItem(row store.CalendarMailboxBusy) gen.CalendarItem {
	it := gen.CalendarItem{
		Kind: outlookKind, Id: outlookItemID(row.EventID), AllDay: row.AllDay,
		Outlook: &gen.CalendarItemOutlook{ShowAs: gen.CalendarItemOutlookShowAs(row.ShowAs)},
	}
	sp := span{allDay: row.AllDay}
	if row.AllDay {
		sp.startDate, _ = fromPgDate(row.StartDate)
		sp.endDate, _ = fromPgDate(row.EndDate)
	} else if row.StartsAt != nil && row.EndsAt != nil {
		sp.startsAt, sp.endsAt = *row.StartsAt, *row.EndsAt
	}
	setSpan(&it, sp)
	return it
}

// outlookItems is userID's busy rows in the window as items (design O9.5),
// when kinds is nil or names calendar.outlook — only while their sync is
// on, which the query holds.
func (s *server) outlookItems(ctx context.Context, userID uuid.UUID, w window, kinds map[string]bool) ([]gen.CalendarItem, error) {
	if kinds != nil && !kinds[outlookKind] {
		return nil, nil
	}
	rows, err := s.queries().ListMailboxBusyInWindow(ctx, store.ListMailboxBusyInWindowParams{
		UserID: userID, FromAt: w.fromAt, ToAt: w.toAt, FromDate: pgDate(w.from), ToDate: pgDate(w.to),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: read the Outlook busy time: %w", err)
	}
	out := make([]gen.CalendarItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, outlookItem(row))
	}
	return out, nil
}

// colleagueOutlookItems is a colleague's busy and out-of-office rows in the
// window as items with no title (design O9.7), while their sync is on.
func (s *server) colleagueOutlookItems(ctx context.Context, userID uuid.UUID, w window) ([]gen.CalendarItem, error) {
	rows, err := s.queries().ListMailboxBusyForUsers(ctx, store.ListMailboxBusyForUsersParams{
		UserIds: []uuid.UUID{userID}, ShowAs: busyShown,
		FromAt: w.fromAt, ToAt: w.toAt, FromDate: pgDate(w.from), ToDate: pgDate(w.to),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: read a colleague's Outlook busy time: %w", err)
	}
	out := make([]gen.CalendarItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, outlookItem(row))
	}
	return out, nil
}
