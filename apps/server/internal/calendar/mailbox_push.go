package calendar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is what the Outlook sync pushes, and how it looks (calendar
// Outlook sync design O6): the desired set of one person's copies, each with
// its key, the event as the mailbox should hold it, the day it starts on and
// the hash the worker diffs against its map.
//
// The window is 30 days back to 180 ahead of today in the business zone. In
// it: the appointments the person owns or attends with any answer but
// declined, never a cancelled one — the feed's read — each occurrence its own
// event, keyed appointment:<id> for a single one and
// appointment:<id>:<YYYY-MM-DD> (the occurrence's date in the series) for a
// series; and the chosen provider kinds, read through the sources on the
// person's behalf (contracts.AsUser on a context with no request, the feed's
// way), keyed <kind>:<id>. Never a colleague's calendar, a subscription's
// events or a public holiday.
//
// The event: the title as subject — Busy / Opptatt in the person's language
// under detail busy, and for a private appointment whose owner the person is
// not; the body a text of the description (an appointment's) or the subtitle
// (an item's), then the "Kept by Vantigo" line and the record's link in the
// app — under busy, and for that private appointment, the link alone; the
// location under full only. showAs: a tentative answer is tentative, else the
// appointment's transparency; staff.absence oof, time.entry and
// expenses.claim busy, every other all-day item free and timed item busy. A
// private appointment is marked private. A point item (its end its start)
// lasts 30 minutes. The time zone is the business zone's name. At most 1500
// events: past it the farthest projected items are left out (the feed's
// order), never an appointment.

const (
	// mailboxDaysBack and mailboxDaysAhead are the window either side of
	// today, in days.
	mailboxDaysBack  = 30
	mailboxDaysAhead = 180
	// mailboxMaxEvents is the most copies one person has.
	mailboxMaxEvents = 1500
	// mailboxPointLength is how long a point item's copy lasts.
	mailboxPointLength = 30 * time.Minute
	// mailboxCalendarName is the name of the calendar target own writes to.
	mailboxCalendarName = "Vantigo"
)

// mailboxItem is one copy the person should have.
type mailboxItem struct {
	key   string
	event contracts.MailboxEvent
	// startsOn is the item's first civil day in the business zone, which
	// the map keeps for the window's edge.
	startsOn time.Time
	hash     string
}

// mailboxWindow is the push's window around today in loc.
func mailboxWindow(today time.Time, loc *time.Location) window {
	from, to := addDays(civil(today), -mailboxDaysBack), addDays(civil(today), mailboxDaysAhead)
	return window{from: from, to: to, loc: loc, fromAt: wallClock(from, loc), toAt: wallClock(addDays(to, 1), loc)}
}

// mailboxPush is what one desired set is built from.
type mailboxPush struct {
	userID uuid.UUID
	row    store.CalendarMailboxSync
	today  time.Time
	w      window
	words  words
}

// mailboxDesired is the person's desired set in p's window, appointments
// first, then the projected items nearest today first — cut to
// mailboxMaxEvents by leaving out the farthest projected items. It reads the
// module's pool and the sources, never inside a transaction.
func (s *server) mailboxDesired(ctx context.Context, p mailboxPush) ([]mailboxItem, error) {
	chosen := map[string]bool{}
	for _, k := range p.row.Kinds {
		chosen[k] = true
	}
	var out []mailboxItem
	if chosen[appointmentKind] {
		appts, err := s.mailboxAppointments(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, appts...)
	}
	delete(chosen, appointmentKind)
	delete(chosen, subscriptionKind)
	if len(chosen) == 0 {
		return out, nil
	}
	projected, err := s.sourceItemsIn(contracts.AsUser(withoutRequest{ctx}, p.userID), p.userID, p.w, chosen)
	if err != nil {
		return nil, err
	}
	byDistance(projected, p.today, p.w.loc)
	if room := mailboxMaxEvents - len(out); len(projected) > room {
		s.deps.Logger.InfoContext(ctx, "calendar: the Outlook sync was over its limit; the farthest calendar source items were left out",
			"user_id", p.userID.String(), "dropped", len(projected)-max(room, 0))
		projected = projected[:max(room, 0)]
	}
	for _, it := range projected {
		item, err := s.mailboxProjected(p, it)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// mailboxAppointments is every occurrence in the window of the person's
// appointments that are not declined — ListAppointmentsInWindow leaves the
// cancelled out — as its own copy.
func (s *server) mailboxAppointments(ctx context.Context, p mailboxPush) ([]mailboxItem, error) {
	q := s.queries()
	w := p.w
	rows, err := q.ListAppointmentsInWindow(ctx, store.ListAppointmentsInWindowParams{
		UserID: p.userID, FromAt: w.fromAt.Add(-24 * time.Hour), ToAt: w.toAt.Add(24 * time.Hour),
		FromDate: pgDate(addDays(w.from, -seriesReachDays)), ToDate: pgDate(addDays(w.to, 1)),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: read the Outlook sync's appointments: %w", err)
	}
	var recurring []int64
	for _, r := range rows {
		if r.CalendarAppointment.Rrule != nil {
			recurring = append(recurring, r.CalendarAppointment.ID)
		}
	}
	exceptions := map[int64][]store.CalendarAppointmentException{}
	if len(recurring) > 0 {
		excs, err := q.ListExceptions(ctx, recurring)
		if err != nil {
			return nil, fmt.Errorf("calendar: read the Outlook sync's exceptions: %w", err)
		}
		for _, e := range excs {
			exceptions[e.AppointmentID] = append(exceptions[e.AppointmentID], e)
		}
	}
	var out []mailboxItem
	for _, r := range rows {
		a := cleanText(r.CalendarAppointment)
		if r.MyStatus != nil && *r.MyStatus == "declined" {
			continue
		}
		occs, err := occurrencesIn(a, exceptions[a.ID], w)
		if err != nil {
			return nil, err
		}
		for _, o := range occs {
			item, err := s.mailboxAppointment(p, a, r.MyStatus, o)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// mailboxAppointment is one occurrence's copy.
func (s *server) mailboxAppointment(p mailboxPush, a store.CalendarAppointment, myStatus *string, o occurrence) (mailboxItem, error) {
	key := "appointment:" + strconv.FormatInt(a.ID, 10)
	if o.date != nil {
		key += ":" + o.date.Format(time.DateOnly)
	}
	startsOn := spanStartsOn(o.span, p.w.loc)
	hidden := p.row.Detail == mailboxDetailBusy || (a.Visibility == visibilityPrivate && a.OwnerUserID != p.userID)
	ev := contracts.MailboxEvent{
		Subject: a.Title,
		ShowAs:  contracts.ShowAsBusy,
		Private: a.Visibility == visibilityPrivate,
	}
	switch {
	case myStatus != nil && *myStatus == "tentative":
		ev.ShowAs = contracts.ShowAsTentative
	case a.Transparency == transparencyFree:
		ev.ShowAs = contracts.ShowAsFree
	}
	link := s.appointmentLinkOn(a, startsOn)
	if hidden {
		ev.Subject, ev.Body = p.words.mailboxBusy, link
	} else {
		var description string
		if a.Description != nil {
			description = *a.Description
		}
		ev.Body = mailboxBody(p.words, description, link)
		if a.Location != nil {
			ev.Location = *a.Location
		}
	}
	setMailboxWhen(&ev, o.span, p.w.loc)
	return newMailboxItem(key, ev, startsOn, p.row.Target)
}

// mailboxProjected is one provider item's copy.
func (s *server) mailboxProjected(p mailboxPush, it gen.CalendarItem) (mailboxItem, error) {
	sp := span{allDay: it.AllDay}
	switch {
	case it.AllDay && it.StartDate != nil && it.EndDate != nil:
		sp.startDate, sp.endDate = civil(it.StartDate.Time), civil(it.EndDate.Time)
	case it.StartsAt != nil:
		sp.startsAt = it.StartsAt.UTC()
		sp.endsAt = sp.startsAt.Add(mailboxPointLength)
		if it.EndsAt != nil && it.EndsAt.After(*it.StartsAt) {
			sp.endsAt = it.EndsAt.UTC()
		}
	}
	ev := contracts.MailboxEvent{ShowAs: projectedShowAs(it)}
	link := s.appPath(it.Path)
	if p.row.Detail == mailboxDetailBusy {
		ev.Subject, ev.Body = p.words.mailboxBusy, link
	} else {
		ev.Subject = oneLine(it.Title)
		if it.Done {
			ev.Subject = "✓ " + ev.Subject
		}
		var subtitle string
		if it.Subtitle != nil {
			subtitle = plainText(*it.Subtitle)
		}
		ev.Body = mailboxBody(p.words, subtitle, link)
	}
	setMailboxWhen(&ev, sp, p.w.loc)
	return newMailboxItem(it.Kind+":"+it.Id, ev, spanStartsOn(sp, p.w.loc), p.row.Target)
}

// projectedShowAs is how a provider item shows its time: an absence out of
// office, a time entry and an expense claim busy, every other all-day item
// free and every other timed one busy.
func projectedShowAs(it gen.CalendarItem) string {
	switch {
	case it.Kind == "staff.absence":
		return contracts.ShowAsOOF
	case it.Kind == "time.entry" || it.Kind == "expenses.claim":
		return contracts.ShowAsBusy
	case it.AllDay:
		return contracts.ShowAsFree
	}
	return contracts.ShowAsBusy
}

// mailboxBody is a full copy's body: the text, when there is one, then the
// line that says where to change it and the link, when the installation has
// an origin.
func mailboxBody(w words, text, link string) string {
	var parts []string
	if text = strings.TrimSpace(text); text != "" {
		parts = append(parts, text)
	}
	kept := w.mailboxKept
	if link != "" {
		kept += "\n" + link
	}
	parts = append(parts, kept)
	return strings.Join(parts, "\n\n")
}

// setMailboxWhen puts sp on the event: its civil dates, the end inclusive,
// when all-day; its instants in UTC when timed; and the business zone.
func setMailboxWhen(ev *contracts.MailboxEvent, sp span, loc *time.Location) {
	ev.TimeZone = loc.String()
	if sp.allDay {
		ev.AllDay, ev.StartDate, ev.EndDate = true, civil(sp.startDate), civil(sp.endDate)
		return
	}
	ev.StartsAt, ev.EndsAt = sp.startsAt.UTC(), sp.endsAt.UTC()
}

// spanStartsOn is the civil day sp starts on in loc.
func spanStartsOn(sp span, loc *time.Location) time.Time {
	if sp.allDay {
		return civil(sp.startDate)
	}
	return civil(floating(sp.startsAt, loc))
}

// appointmentLinkOn is the appointment in the app on day — an occurrence's
// own day, not the series' first — "" when the installation has no origin.
// It is the app's link (APP_URL and the base path), as the feed's and the
// notices' are: the public host serves only its allowlist, never the app.
func (s *server) appointmentLinkOn(a store.CalendarAppointment, day time.Time) string {
	cfg := s.deps.Config
	if cfg == nil || cfg.AppOrigin == "" {
		return ""
	}
	return cfg.AppOrigin + cfg.BasePath + "/calendar?date=" + day.Format(time.DateOnly) +
		"&appointment=" + strconv.FormatInt(a.ID, 10)
}

// newMailboxItem is the copy with its hash: the hex SHA-256 of the event's
// canonical encoding and the target — every field in a fixed order, the
// instants in UTC — so the same copy hashes the same from one sync to the
// next, and a move to the other target rewrites everything.
func newMailboxItem(key string, ev contracts.MailboxEvent, startsOn time.Time, target string) (mailboxItem, error) {
	canonical := struct {
		Target, Subject, Body, Location string
		AllDay                          bool
		StartDate, EndDate              string
		StartsAt, EndsAt                string
		TimeZone, ShowAs                string
		Private                         bool
	}{
		Target: target, Subject: ev.Subject, Body: ev.Body, Location: ev.Location, AllDay: ev.AllDay,
		TimeZone: ev.TimeZone, ShowAs: ev.ShowAs, Private: ev.Private,
	}
	if ev.AllDay {
		canonical.StartDate, canonical.EndDate = ev.StartDate.Format(time.DateOnly), ev.EndDate.Format(time.DateOnly)
	} else {
		canonical.StartsAt, canonical.EndsAt = ev.StartsAt.UTC().Format(time.RFC3339Nano), ev.EndsAt.UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return mailboxItem{}, fmt.Errorf("calendar: encode a mailbox copy: %w", err)
	}
	sum := sha256.Sum256(raw)
	return mailboxItem{key: key, event: ev, startsOn: startsOn, hash: hex.EncodeToString(sum[:])}, nil
}
