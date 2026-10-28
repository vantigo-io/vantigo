package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the write-back from Outlook (calendar Outlook sync design
// O10): the step of the calendar-mailbox-sync worker between the desired set
// and the push (O10.5) that takes what the person changed in a copy of their
// own appointment back into the appointment, through the module's own write
// paths — the screen's PUT, occurrence and DELETE, and under target own its
// create for an event made in the Vantigo calendar — and puts back whatever
// else they changed.
//
// In order: the provider's Versions of the copies (ids and change keys);
// each mapped row not released compared by its change key — none stored is a
// baseline, taken unread; the same is as Vantigo left it; a moved one, one
// missing from a complete list that starts in the window, and under target
// own (while the person's copies include their appointments) an event
// neither mapped nor marked are read whole with Events, at most
// mailboxReadMax ids a sync — a row is deleted only when the directory
// answered it not found (Gone), and one neither answered nor Gone is left
// for the next sync; then the decisions of O10.6–O10.9, at most
// mailboxWriteBackMax writes to appointments a sync and none of the
// deletes when more than mailboxMassDeleteMax would be taken, each on the person's
// behalf (contracts.AsUser) through the same code the screen runs, with no
// transaction open across any call out; then the map's change keys, stale
// hashes, releases and forgets, and the put-back, in one short transaction.
// Whatever is left over keeps its old change key and is seen again by the
// next sync.
//
// Nothing an event says — its subject, body or location — and nothing the
// provider answers in words is ever logged: codes, counts and the user's id
// alone.

const (
	// mailboxReadMax is the most events one sync reads whole.
	mailboxReadMax = contracts.MailboxMarkedMax
	// mailboxWriteBackMax is the most writes to appointments one sync makes.
	mailboxWriteBackMax = 20
	// mailboxMassDeleteMax is the most deletes of the person's own
	// appointments one sync takes from Outlook; past it none is taken.
	mailboxMassDeleteMax = 10
)

// The reasons a change made in Outlook was put back (design O10.3), as
// calendar.mailbox_sync.put_back_reason holds them.
const (
	putBackConflict    = "conflict"
	putBackRefused     = "refused"
	putBackNotOwner    = "not_owner"
	putBackNotEditable = "not_editable"
	// putBackManyDeleted: more than mailboxMassDeleteMax of the person's
	// own appointments were deleted in Outlook in one sync, and none was
	// taken; recorded with no key.
	putBackManyDeleted = "many_deleted"
)

// writeBackResult is what a write-back did, for the rest of the sync.
type writeBackResult struct {
	// wrote is the appointments written: the desired set is built again.
	wrote int
	// mapChanged: the map was written, so it is read again.
	mapChanged bool
	// present is every event id the provider answered as there, which the
	// reconcile never forgets.
	present map[string]bool
	// complete: every version was looked at and every decision made, so
	// the reconcile may forget a mapped event the provider no longer has.
	complete bool
}

// wbMapWrite is one change the write-back makes to a map row.
type wbMapWrite struct {
	kind      string // "change_key", "stale", "taken", "release", "forget"
	key       string
	changeKey string
	// eventID and startsOn are a taken change's: the copy now starts there.
	eventID  string
	startsOn time.Time
}

// writeBackRun is one person's write-back in progress.
type writeBackRun struct {
	w          *MailboxSyncWorker
	ctx        context.Context // the module's own rows
	as         context.Context // the person's behalf, on the sync's deadline
	r          store.CalendarMailboxSync
	p          mailboxPush
	want       map[string]*mailboxItem
	now        time.Time
	out        writeBackResult
	writes     []wbMapWrite
	putBack    *wbMapWrite // the last change put back: its reason in kind
	putBacks   int
	attempts   int // the writes to appointments tried
	creates    int
	refusedNew int
	callErr    error
}

// writeBack is the write-back (design O10.5's steps 1 to 4) for the person
// of r, before the reconcile and the push: desired is the set built, rows
// the map as loaded. It answers what it did, the provider's refusal or
// failure as callErr — with what it did up to it — and the module's own
// database's failure as err.
func (w *MailboxSyncWorker) writeBack(ctx, call context.Context, r store.CalendarMailboxSync, mailbox, calendarID string, p mailboxPush, desired []mailboxItem, rows []store.CalendarMailboxEvent, now time.Time) (writeBackResult, error, error) {
	run := &writeBackRun{
		w: w, ctx: ctx, as: contracts.AsUser(withoutRequest{call}, r.UserID), r: r, p: p, now: now,
		want: make(map[string]*mailboxItem, len(desired)),
		out:  writeBackResult{present: map[string]bool{}},
	}
	for i := range desired {
		run.want[desired[i].key] = &desired[i]
	}
	versions, callErr := w.srv.mailboxVersions(call, mailbox, calendarID, p.w.fromAt, p.w.toAt)
	if callErr != nil {
		return run.out, callErr, nil
	}
	run.out.complete = versions.Complete
	byEvent := make(map[string]contracts.MailboxEventVersion, len(versions.Events))
	for _, v := range versions.Events {
		byEvent[v.EventID] = v
		run.out.present[v.EventID] = true
	}

	// The rows: a baseline, nothing, or a read.
	known := make(map[string]bool, len(rows))
	var reads []*store.CalendarMailboxEvent
	var missing []*store.CalendarMailboxEvent
	for i := range rows {
		row := &rows[i]
		known[row.EventID] = true
		if row.ReleasedAt != nil {
			continue
		}
		v, ok := byEvent[row.EventID]
		startsOn, _ := fromPgDate(row.StartsOn)
		switch {
		case ok && row.ChangeKey == nil:
			if v.ChangeKey != "" {
				run.writes = append(run.writes, wbMapWrite{kind: "change_key", key: row.ItemKey, changeKey: v.ChangeKey})
			}
		case ok && *row.ChangeKey == v.ChangeKey:
		case ok:
			reads = append(reads, row)
		case versions.Complete && !startsOn.Before(p.w.from):
			missing = append(missing, row)
		}
	}
	// Under target own, an event of the Vantigo calendar neither mapped nor
	// marked may be one the person made there (O10.9) — taken only while
	// the person's copies include their appointments.
	var fresh []string
	if calendarID != "" && slices.Contains(r.Kinds, appointmentKind) {
		for _, v := range versions.Events {
			if !known[v.EventID] && v.Key == "" && !v.Series {
				fresh = append(fresh, v.EventID)
			}
		}
	}

	// At most mailboxReadMax read: the moved first, then the missing, then
	// the new. A missing row left unread makes the list incomplete, so the
	// reconcile infers nothing from it.
	if len(reads) > mailboxReadMax {
		reads = reads[:mailboxReadMax]
	}
	if room := mailboxReadMax - len(reads); len(missing) > room {
		missing = missing[:room]
		run.out.complete = false
	}
	if room := mailboxReadMax - len(reads) - len(missing); len(fresh) > room {
		fresh = fresh[:room]
	}
	ids := make([]string, 0, len(reads)+len(missing)+len(fresh))
	for _, row := range reads {
		ids = append(ids, row.EventID)
	}
	for _, row := range missing {
		ids = append(ids, row.EventID)
	}
	ids = append(ids, fresh...)

	if len(ids) > 0 {
		read, callErr := w.srv.mailboxEvents(call, mailbox, calendarID, ids)
		if callErr != nil {
			if err := run.commit(); err != nil {
				return run.out, nil, err
			}
			return run.out, callErr, nil
		}
		got := make(map[string]contracts.MailboxEventState, len(read.Events))
		for _, st := range read.Events {
			got[st.EventID] = st
		}
		gone := make(map[string]bool, len(read.Gone))
		for _, id := range read.Gone {
			gone[id] = true
		}
		// An answered row is compared; a Gone one — the directory's own "not
		// found", whether it was missing from the list or its version moved
		// moments ago — is deleted; a row neither answered nor Gone was not
		// read this time, and keeps its version for the next sync: a missing
		// one so unread leaves the reconcile inferring nothing.
		var deletes []*store.CalendarMailboxEvent
		for _, row := range append(reads, missing...) {
			if run.callErr != nil {
				break
			}
			st, answered := got[row.EventID]
			switch {
			case answered:
				run.out.present[row.EventID] = true
				if err := run.change(*row, st); err != nil {
					return run.out, nil, err
				}
			case gone[row.EventID]:
				delete(run.out.present, row.EventID)
				deletes = append(deletes, row)
			default:
				run.out.complete = false
			}
		}
		if err := run.deletedAll(deletes); err != nil {
			return run.out, nil, err
		}
		for _, id := range fresh {
			if run.callErr != nil {
				break
			}
			if st, answered := got[id]; answered {
				if err := run.take(st); err != nil {
					return run.out, nil, err
				}
			}
		}
	}
	if err := run.commit(); err != nil {
		return run.out, nil, err
	}
	if run.out.wrote > 0 || run.putBacks > 0 || run.refusedNew > 0 {
		w.deps.Logger.InfoContext(ctx, "calendar: the Outlook write-back ran",
			"worker", mailboxWorkerName, "user_id", r.UserID.String(),
			"written", run.out.wrote, "created", run.creates, "put_back", run.putBacks, "refused_new", run.refusedNew)
	}
	if run.callErr != nil {
		run.out.complete = false
	}
	return run.out, run.callErr, nil
}

// commit writes the map's changes and the put-back in one short
// transaction.
func (run *writeBackRun) commit() error {
	if len(run.writes) == 0 && run.putBack == nil {
		return nil
	}
	userID, now := run.r.UserID, run.now
	err := run.w.srv.withLockedTx(run.ctx, func(ctx context.Context, txq *store.Queries) error {
		for _, m := range run.writes {
			var err error
			switch m.kind {
			case "change_key":
				err = txq.SetMailboxEventChangeKey(ctx, store.SetMailboxEventChangeKeyParams{ChangeKey: m.changeKey, Now: now, UserID: userID, ItemKey: m.key})
			case "stale":
				err = txq.StaleMailboxEvent(ctx, store.StaleMailboxEventParams{ChangeKey: optional(m.changeKey), Now: now, UserID: userID, ItemKey: m.key})
			case "taken":
				err = txq.UpsertMailboxEvent(ctx, store.UpsertMailboxEventParams{
					UserID: userID, ItemKey: m.key, EventID: m.eventID, ContentHash: "",
					StartsOn: pgDate(m.startsOn), ChangeKey: optional(m.changeKey), Now: now,
				})
			case "release":
				err = txq.ReleaseMailboxEvent(ctx, store.ReleaseMailboxEventParams{Now: now, UserID: userID, ItemKey: m.key})
			case "forget":
				err = txq.DeleteMailboxEvent(ctx, store.DeleteMailboxEventParams{UserID: userID, ItemKey: m.key})
			}
			if err != nil {
				return fmt.Errorf("calendar: write the mailbox map after the write-back: %w", err)
			}
		}
		if run.putBack != nil {
			if err := txq.SetMailboxPutBack(ctx, store.SetMailboxPutBackParams{Reason: run.putBack.kind, Key: run.putBack.key, At: now, UserID: userID}); err != nil {
				return fmt.Errorf("calendar: record the change put back: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	run.out.mapChanged = true
	run.writes, run.putBack = nil, nil
	return nil
}

// optional is s as a nullable column, nil for "".
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// stale clears the row's hash, so the push rewrites the copy whole, and
// keeps the version read.
func (run *writeBackRun) stale(key, changeKey string) {
	run.writes = append(run.writes, wbMapWrite{kind: "stale", key: key, changeKey: changeKey})
}

// putBackAs puts the copy back for reason: stale, and the change recorded.
func (run *writeBackRun) putBackAs(key, changeKey, reason string) {
	run.stale(key, changeKey)
	run.putBack = &wbMapWrite{kind: reason, key: key}
	run.putBacks++
}

// mayWrite reports whether a write to an appointment is still within the
// sync's cap; past it the copy is left as it is, its version unkept, and the
// write-back is incomplete.
func (run *writeBackRun) mayWrite() bool {
	if run.attempts < mailboxWriteBackMax {
		run.attempts++
		return true
	}
	run.out.complete = false
	return false
}

// change is a mapped copy whose version moved, read whole (O10.6, O10.7).
func (run *writeBackRun) change(row store.CalendarMailboxEvent, st contracts.MailboxEventState) error {
	key := row.ItemKey
	if st.Meeting {
		run.writes = append(run.writes, wbMapWrite{kind: "release", key: key})
		return nil
	}
	it := run.want[key]
	if it == nil {
		// The push deletes or forgets it.
		return nil
	}
	if st.Series || st.Foreign || st.Cancelled {
		run.writes = append(run.writes, wbMapWrite{kind: "change_key", key: key, changeKey: st.ChangeKey})
		return nil
	}
	if sameCopy(st, it.event) {
		run.writes = append(run.writes, wbMapWrite{kind: "change_key", key: key, changeKey: st.ChangeKey})
		return nil
	}
	if row.ContentHash == "" {
		// A copy whose last write is unknown — a put-back, a take or an
		// adoption whose push then failed — is never taken: put back, with
		// no record, as the push rewrites it whole.
		run.stale(key, st.ChangeKey)
		return nil
	}
	id, date, ok := appointmentKey(key)
	if !ok {
		run.putBackAs(key, st.ChangeKey, putBackNotEditable)
		return nil
	}
	a, atts, found, err := run.appointment(id)
	if err != nil || !found {
		return err
	}
	switch {
	case a.OwnerUserID != run.r.UserID:
		run.putBackAs(key, st.ChangeKey, putBackNotOwner)
		return nil
	case a.CancelledAt != nil:
		return nil
	case row.ContentHash != it.hash:
		if run.asWritten(row, st, it) {
			// The event is exactly what the push last wrote — Exchange
			// moved its version alone — so it is no edit: the version is
			// kept, and the push rewrites it, the hash being the old one.
			run.writes = append(run.writes, wbMapWrite{kind: "change_key", key: key, changeKey: st.ChangeKey})
			return nil
		}
		run.putBackAs(key, st.ChangeKey, putBackConflict)
		return nil
	}
	if date == nil {
		return run.takeSingle(key, a, atts, st, it.event)
	}
	return run.takeOccurrence(key, a, *date, st, it.event)
}

// takeSingle writes the fields the person changed in the copy of their own
// single appointment a (O10.7) through the PUT's path, the appointment's
// revision as read as its If-Match. Under detail busy only the times, the
// showAs and the sensitivity are taken: the rest were placeholders.
func (run *writeBackRun) takeSingle(key string, a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee, st contracts.MailboxEventState, was contracts.MailboxEvent) error {
	body := writeOf(a, atts)
	ev := st.Event
	changed := false
	if run.r.Detail != mailboxDetailBusy {
		if title := normalText(ev.Subject); title != normalText(was.Subject) {
			if title == "" {
				run.putBackAs(key, st.ChangeKey, putBackRefused)
				return nil
			}
			body.Title, changed = title, true
		}
		if st.TextBody {
			if d := descriptionOf(ev.Body); d != descriptionOf(was.Body) {
				body.Description, changed = optional(d), true
			}
		}
		if l := normalText(ev.Location); l != normalText(was.Location) {
			body.Location, changed = optional(l), true
		}
	}
	if !sameTimes(ev, was) {
		setBodyWhen(&body, ev)
		changed = true
	}
	if t := transparencyOf(ev.ShowAs); t != a.Transparency {
		v := gen.AppointmentTransparency(t)
		body.Transparency, changed = &v, true
	}
	if v := visibilityOf(ev.Private); v != a.Visibility {
		vis := gen.AppointmentVisibility(v)
		body.Visibility, changed = &vis, true
	}
	if !changed {
		// Nothing the appointment holds changed: the copy is written back.
		run.stale(key, st.ChangeKey)
		return nil
	}
	if !run.mayWrite() {
		return nil
	}
	_, err := run.w.srv.replaceAppointment(run.as, run.r.UserID, a.ID, a.Revision, body, nil)
	return run.written(key, st, err)
}

// takeOccurrence moves the occurrence on date of the person's own series a
// when the copy's times changed, held to the series' kind; any other change
// is one an occurrence cannot carry, and is put back not_editable — after
// the move, when both changed.
func (run *writeBackRun) takeOccurrence(key string, a store.CalendarAppointment, date time.Time, st contracts.MailboxEventState, was contracts.MailboxEvent) error {
	if a.Rrule == nil {
		return nil
	}
	ev := st.Event
	other := ev.ShowAs != was.ShowAs || ev.Private != was.Private ||
		(run.r.Detail != mailboxDetailBusy && (normalText(ev.Subject) != normalText(was.Subject) ||
			normalText(ev.Location) != normalText(was.Location) ||
			(st.TextBody && descriptionOf(ev.Body) != descriptionOf(was.Body))))
	if !sameTimes(ev, was) {
		if ev.AllDay != a.AllDay {
			run.putBackAs(key, st.ChangeKey, putBackNotEditable)
			return nil
		}
		if !run.mayWrite() {
			return nil
		}
		var body gen.OccurrenceWrite
		if ev.AllDay {
			body.StartDate, body.EndDate = &openapi_types.Date{Time: civil(ev.StartDate)}, &openapi_types.Date{Time: civil(ev.EndDate)}
		} else {
			starts, ends := ev.StartsAt.UTC(), ev.EndsAt.UTC()
			body.StartsAt, body.EndsAt = &starts, &ends
		}
		_, err := run.w.srv.writeOccurrence(run.as, run.r.UserID, a.ID, date, body)
		if err := run.written(key, st, err); err != nil || run.callErr != nil {
			return err
		}
	}
	if other {
		run.putBackAs(key, st.ChangeKey, putBackNotEditable)
		return nil
	}
	run.stale(key, st.ChangeKey)
	return nil
}

// written is what a write to an appointment answered: done, the copy made
// stale so the push writes the appointment's own form back, starting where
// the person moved it — so one moved before the window is forgotten, not
// deleted; a field the
// rules refuse, put back refused; a revision moved on, put back conflict; an
// appointment gone, cancelled or no longer a series' day, left to the push.
// Any other failure stops the write-back.
func (run *writeBackRun) written(key string, st contracts.MailboxEventState, err error) error {
	changeKey := st.ChangeKey
	if err == nil {
		run.out.wrote++
		ev := st.Event
		sp := span{allDay: ev.AllDay, startDate: civil(ev.StartDate), endDate: civil(ev.EndDate), startsAt: ev.StartsAt, endsAt: ev.EndsAt}
		run.writes = append(run.writes, wbMapWrite{kind: "taken", key: key, changeKey: changeKey, eventID: st.EventID, startsOn: spanStartsOn(sp, run.p.w.loc)})
		return nil
	}
	r, ok := asRefusal(err)
	if !ok {
		run.callErr = err
		return nil
	}
	switch {
	case r.status == http.StatusBadRequest:
		run.putBackAs(key, changeKey, putBackRefused)
	case r.status == http.StatusConflict && r.problem.Code != nil && *r.problem.Code == codeRevisionMismatch:
		run.putBackAs(key, changeKey, putBackConflict)
	default:
		run.stale(key, changeKey)
	}
	return nil
}

// The fates of a mapped copy no longer in its calendar (deletion).
const (
	delNone     = iota // left to the push: no longer wanted
	delForget          // forgotten, made again by the push while wanted
	delSilent          // put back with no record: its last write is unknown
	delConflict        // put back: the appointment changed in Vantigo too
	delTake            // the person's own appointment deleted, or occurrence cancelled
)

// deletion is what becomes of a mapped copy no longer in its calendar
// (O10.8), with the appointment and the occurrence's date for a take: the
// person's own single appointment is deleted, their own occurrence
// cancelled — unless the row's hash is "" (its last write unknown), put
// back with no record, or is not the hash wanted now (the appointment
// changed in Vantigo since), put back as a conflict; any other copy is
// forgotten, and made again by the push while it is wanted.
func (run *writeBackRun) deletion(row store.CalendarMailboxEvent) (int, store.CalendarAppointment, *time.Time, error) {
	var none store.CalendarAppointment
	it := run.want[row.ItemKey]
	if it == nil {
		// The push deletes or forgets it.
		return delNone, none, nil, nil
	}
	id, date, ok := appointmentKey(row.ItemKey)
	if !ok {
		return delForget, none, nil, nil
	}
	a, _, found, err := run.appointment(id)
	if err != nil {
		return delNone, none, nil, err
	}
	switch {
	case !found || a.OwnerUserID != run.r.UserID || a.CancelledAt != nil || (date != nil && a.Rrule == nil):
		return delForget, none, nil, nil
	case row.ContentHash == "":
		return delSilent, none, nil, nil
	case row.ContentHash != it.hash:
		return delConflict, none, nil, nil
	}
	return delTake, a, date, nil
}

// deletedAll is the mapped copies the directory answered no longer in
// their calendar (O10.8), each to its fate (deletion). When more than
// mailboxMassDeleteMax of them would delete or cancel the person's own
// appointments in this one sync — a calendar emptied, a client gone wrong,
// more likely than a person's intent — none is taken: every one of them is
// put back (made stale, so the push re-creates it), the put-back recorded
// many_deleted with no key, and the counts logged.
func (run *writeBackRun) deletedAll(rows []*store.CalendarMailboxEvent) error {
	type taking struct {
		row  store.CalendarMailboxEvent
		a    store.CalendarAppointment
		date *time.Time
	}
	var takes []taking
	for _, row := range rows {
		fate, a, date, err := run.deletion(*row)
		if err != nil {
			return err
		}
		switch fate {
		case delForget:
			run.writes = append(run.writes, wbMapWrite{kind: "forget", key: row.ItemKey})
		case delSilent:
			run.stale(row.ItemKey, "")
		case delConflict:
			run.putBackAs(row.ItemKey, "", putBackConflict)
		case delTake:
			takes = append(takes, taking{row: *row, a: a, date: date})
		}
	}
	if len(takes) > mailboxMassDeleteMax {
		for _, t := range takes {
			run.stale(t.row.ItemKey, "")
		}
		run.putBack = &wbMapWrite{kind: putBackManyDeleted}
		run.putBacks += len(takes)
		run.w.deps.Logger.WarnContext(run.ctx, "calendar: many copies were deleted in Outlook at once; none was taken, every one put back",
			"worker", mailboxWorkerName, "user_id", run.r.UserID.String(), "deleted", len(takes), "limit", mailboxMassDeleteMax)
		return nil
	}
	for _, t := range takes {
		if run.callErr != nil {
			return nil
		}
		run.deleted(t.row.ItemKey, t.a, t.date)
	}
	return nil
}

// deleted deletes the person's own single appointment a through the
// DELETE's path, or cancels its occurrence on date, for its copy deleted
// in Outlook, and forgets the copy; a refusal forgets it too, the push
// making it again while it is wanted.
func (run *writeBackRun) deleted(key string, a store.CalendarAppointment, date *time.Time) {
	if !run.mayWrite() {
		return
	}
	var err error
	if date == nil {
		err = run.w.srv.deleteAppointment(run.as, run.r.UserID, a.ID)
	} else {
		cancelled := true
		_, err = run.w.srv.writeOccurrence(run.as, run.r.UserID, a.ID, *date, gen.OccurrenceWrite{Cancelled: &cancelled})
	}
	if _, refused := asRefusal(err); err != nil && !refused {
		run.callErr = err
		return
	}
	if err == nil {
		run.out.wrote++
	}
	run.writes = append(run.writes, wbMapWrite{kind: "forget", key: key})
}

// asWritten reports whether the event read is exactly the copy the push
// last wrote: turned into a MailboxEvent with the row's target — the zone
// the copy was written in, the read carrying none, and its line endings
// made \n — and hashed the way newMailboxItem hashes, it is row's hash.
func (run *writeBackRun) asWritten(row store.CalendarMailboxEvent, st contracts.MailboxEventState, it *mailboxItem) bool {
	ev := st.Event
	ev.TimeZone = it.event.TimeZone
	ev.Body = strings.ReplaceAll(ev.Body, "\r\n", "\n")
	read, err := newMailboxItem(row.ItemKey, ev, it.startsOn, run.r.Target)
	return err == nil && read.hash == row.ContentHash
}

// take makes the person's own new event of the Vantigo calendar an
// appointment they own (O10.9), its map row written in the create's own
// transaction so it is never taken twice. A series, a meeting, a cancelled
// one, another installation's and a copy (one carrying a key, left to the
// reconcile) are left alone; a refusal is logged by its code and tried again
// at the next sync.
func (run *writeBackRun) take(st contracts.MailboxEventState) error {
	if st.Key != "" || st.Foreign || st.Series || st.Cancelled || st.Meeting {
		return nil
	}
	if !run.mayWrite() {
		return nil
	}
	body, startsOn := newAppointmentOf(st, run.p)
	_, err := run.w.srv.createAppointment(run.as, run.r.UserID, body, nil, func(ctx context.Context, txq *store.Queries, row store.CalendarAppointment) error {
		changeKey := optional(st.ChangeKey)
		return txq.UpsertMailboxEvent(ctx, store.UpsertMailboxEventParams{
			UserID: run.r.UserID, ItemKey: "appointment:" + strconv.FormatInt(row.ID, 10), EventID: st.EventID,
			ContentHash: "", StartsOn: pgDate(startsOn), ChangeKey: changeKey, Now: run.now,
		})
	})
	if r, refused := asRefusal(err); refused {
		run.refusedNew++
		code := "invalid"
		if r.problem.Code != nil {
			code = *r.problem.Code
		}
		run.w.deps.Logger.InfoContext(run.ctx, "calendar: an event of the Vantigo calendar was not taken in",
			"worker", mailboxWorkerName, "user_id", run.r.UserID.String(), "code", code)
		return nil
	}
	if err != nil {
		run.callErr = err
		return nil
	}
	run.out.wrote++
	run.out.mapChanged = true
	run.creates++
	return nil
}

// appointment is the appointment id and its attendees, found false when it
// is gone.
func (run *writeBackRun) appointment(id int64) (store.CalendarAppointment, []store.CalendarAppointmentAttendee, bool, error) {
	q := run.w.srv.queries()
	a, err := q.GetAppointment(run.ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, nil, false, nil
	}
	if err != nil {
		return a, nil, false, fmt.Errorf("calendar: read appointment %d for the write-back: %w", id, err)
	}
	atts, err := q.ListAttendees(run.ctx, id)
	if err != nil {
		return a, nil, false, fmt.Errorf("calendar: read appointment %d's attendees for the write-back: %w", id, err)
	}
	return a, atts, true, nil
}

// appointmentKey reads an appointment's item key — appointment:<id>, or
// appointment:<id>:<YYYY-MM-DD> for an occurrence — ok false for any other.
func appointmentKey(key string) (int64, *time.Time, bool) {
	rest, ok := strings.CutPrefix(key, "appointment:")
	if !ok {
		return 0, nil, false
	}
	idPart, datePart, hasDate := strings.Cut(rest, ":")
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return 0, nil, false
	}
	if !hasDate {
		return id, nil, true
	}
	d, err := time.Parse(time.DateOnly, datePart)
	if err != nil {
		return 0, nil, false
	}
	return id, &d, true
}

// newAppointmentOf is the appointment a new event of the Vantigo calendar
// becomes (O10.9) and the day it starts on in the business zone: the subject
// its title (the person's word for untitled when empty), the body its
// description and the location its location, each cut to its limit, the
// times in the business zone, showAs its transparency and the sensitivity
// its visibility.
func newAppointmentOf(st contracts.MailboxEventState, p mailboxPush) (gen.AppointmentWrite, time.Time) {
	ev := st.Event
	title := cutRunes(normalText(ev.Subject), maxTitle)
	if title == "" {
		title = p.words.mailboxUntitled
	}
	transparency := gen.AppointmentTransparency(transparencyOf(ev.ShowAs))
	visibility := gen.AppointmentVisibility(visibilityOf(ev.Private))
	body := gen.AppointmentWrite{
		Title: title, TimeZone: p.w.loc.String(), Transparency: &transparency, Visibility: &visibility,
		Location: optional(cutRunes(normalText(ev.Location), maxLocation)),
	}
	if st.TextBody {
		body.Description = optional(cutRunes(descriptionOf(ev.Body), maxDescription))
	}
	setBodyWhen(&body, ev)
	sp := span{allDay: ev.AllDay, startDate: civil(ev.StartDate), endDate: civil(ev.EndDate), startsAt: ev.StartsAt, endsAt: ev.EndsAt}
	return body, spanStartsOn(sp, p.w.loc)
}

// setBodyWhen puts the event's times on body: its dates when all-day, its
// instants to the second when timed.
func setBodyWhen(body *gen.AppointmentWrite, ev contracts.MailboxEvent) {
	body.AllDay = ev.AllDay
	body.StartDate, body.EndDate, body.StartsAt, body.EndsAt = nil, nil, nil, nil
	if ev.AllDay {
		body.StartDate, body.EndDate = &openapi_types.Date{Time: civil(ev.StartDate)}, &openapi_types.Date{Time: civil(ev.EndDate)}
		return
	}
	starts, ends := ev.StartsAt.UTC().Truncate(time.Second), ev.EndsAt.UTC().Truncate(time.Second)
	body.StartsAt, body.EndsAt = &starts, &ends
}

// transparencyOf is a showAs as an appointment's transparency: free is free,
// anything else busy.
func transparencyOf(showAs string) string {
	if showAs == contracts.ShowAsFree {
		return transparencyFree
	}
	return transparencyBusy
}

// visibilityOf is a sensitivity as an appointment's visibility.
func visibilityOf(private bool) string {
	if private {
		return visibilityPrivate
	}
	return visibilityShared
}

// sameCopy reports whether the event read is the copy as written, after
// normalising (O10.4): the subject, the body above the kept line (only when
// it came as text), the location, the times to the second, showAs and the
// sensitivity.
func sameCopy(st contracts.MailboxEventState, was contracts.MailboxEvent) bool {
	ev := st.Event
	return normalText(ev.Subject) == normalText(was.Subject) &&
		(!st.TextBody || descriptionOf(ev.Body) == descriptionOf(was.Body)) &&
		normalText(ev.Location) == normalText(was.Location) &&
		sameTimes(ev, was) && ev.ShowAs == was.ShowAs && ev.Private == was.Private
}

// sameTimes reports whether two events take the same time: the same kind,
// and the same dates or the same instants to the second.
func sameTimes(a, b contracts.MailboxEvent) bool {
	if a.AllDay != b.AllDay {
		return false
	}
	if a.AllDay {
		return civil(a.StartDate).Equal(civil(b.StartDate)) && civil(a.EndDate).Equal(civil(b.EndDate))
	}
	return a.StartsAt.Truncate(time.Second).Equal(b.StartsAt.Truncate(time.Second)) &&
		a.EndsAt.Truncate(time.Second).Equal(b.EndsAt.Truncate(time.Second))
}

// normalText is a text with its line endings made \n, each line's trailing
// space dropped, and the whole trimmed.
func normalText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// descriptionOf is a copy's body as a description: normalised and cut at the
// first line that starts with the kept line in either language — trimmed,
// a no-break space read as a space — with the link after it.
func descriptionOf(body string) string {
	lines := strings.Split(normalText(body), "\n")
	for i, l := range lines {
		l = strings.TrimSpace(strings.ReplaceAll(l, "\u00a0", " "))
		if strings.HasPrefix(l, english.mailboxKept) || strings.HasPrefix(l, norwegian.mailboxKept) {
			lines = lines[:i]
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// cutRunes is s cut to at most n characters, trimmed again.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}
