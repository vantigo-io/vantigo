package calendar

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is the feed's iCalendar object (calendar feed design D4): one
// published VCALENDAR — PRODID, VERSION, no METHOD (a METHOD makes Outlook
// treat the file as an invitation), NAME and X-WR-CALNAME, the hour's
// REFRESH-INTERVAL and X-PUBLISHED-TTL — then one VTIMEZONE per TZID a timed
// series is written in, then the VEVENTs: the person's appointments through
// ics.go's writers, a series as its rule with its exceptions, and the
// providers' projected items. Neither variant writes ORGANIZER or ATTENDEE;
// the busy one writes spans and nothing a reader could tell one from another
// by (D3) — so a projected item's UID there is opaque (busyUID), and a
// point, which is no span, is left out.

// feedRefresh is how often the feed asks to be read, as an iCalendar
// DURATION.
const feedRefresh = "PT1H"

// feedNames is the calendar's name and the busy variant's one word, in the
// person's language.
func feedNames(lang string, busy bool) (calendar, busyWord string) {
	switch {
	case !busy:
		return "Vantigo", ""
	case lang == langNorwegian:
		return "Vantigo (opptatt)", "Opptatt"
	default:
		return "Vantigo (busy)", "Busy"
	}
}

// feedAppointment is one appointment the feed writes, with its exceptions.
type feedAppointment struct {
	appt store.CalendarAppointment
	excs []store.CalendarAppointmentException
}

// feedInput is what one feed is built from.
type feedInput struct {
	// userID is the person whose feed it is, which keys the busy variant's
	// UIDs.
	userID uuid.UUID
	busy   bool
	// lang is the person's language, which names the busy variant.
	lang      string
	appts     []feedAppointment
	projected []gen.CalendarItem
	// loc is the business zone, which orders the projected items by day.
	loc *time.Location
	now time.Time
}

// buildFeed is the VCALENDAR of in, CRLF line endings and all.
func (s *server) buildFeed(in feedInput) (string, error) {
	name, busyWord := feedNames(in.lang, in.busy)
	cal := ics.NewCalendar()
	cal.SetProductId(icsProductID)
	cal.SetName(name)
	cal.SetRefreshInterval(feedRefresh) // golang-ical writes it VALUE=DURATION
	cal.SetXPublishedTTL(feedRefresh)

	type prepared struct {
		feedAppointment
		sr  *series
		loc *time.Location
	}
	list := make([]prepared, 0, len(in.appts))
	zones := map[string]*time.Location{}
	spans := map[string][2]time.Time{}
	for _, fa := range in.appts {
		p := prepared{feedAppointment: feedAppointment{appt: cleanText(fa.appt), excs: fa.excs}}
		sr, err := seriesOf(p.appt)
		if err != nil {
			return "", err
		}
		p.sr = sr
		if sr != nil && !p.appt.AllDay {
			p.loc = sr.loc
			from, to := zoneSpan(p.appt, p.excs, sr)
			zone := sr.loc.String()
			if have, ok := spans[zone]; ok {
				if have[0].Before(from) {
					from = have[0]
				}
				if have[1].After(to) {
					to = have[1]
				}
			}
			zones[zone], spans[zone] = sr.loc, [2]time.Time{from, to}
		}
		list = append(list, p)
	}
	tzids := make([]string, 0, len(zones))
	for zone := range zones {
		tzids = append(tzids, zone)
	}
	slices.Sort(tzids)
	for _, zone := range tzids {
		cal.AddVTimezone(vtimezone(zones[zone], spans[zone][0], spans[zone][1]))
	}

	for _, p := range list {
		a := p.appt
		body := func(ev *ics.VEvent) { describeAppointment(ev, a, s.appointmentLink(a), false) }
		if in.busy {
			body = func(ev *ics.VEvent) {
				ev.SetSummary(busyWord)
				setTransp(ev, true)
			}
		}
		writeAppointment(cal, a, p.excs, p.sr, p.loc, in.now, body)
	}

	projected := slices.Clone(in.projected)
	sortItems(projected, in.loc)
	for _, it := range projected {
		if in.busy && (it.AllDay || isPoint(it)) {
			continue
		}
		uid := it.Kind + ":" + it.Id + "@vantigo"
		if in.busy {
			var err error
			if uid, err = s.busyUID(in.userID, it); err != nil {
				return "", err
			}
		}
		ev := cal.AddEvent(uid)
		stamp(ev, in.now, 0)
		setItemWhen(ev, it)
		if in.busy {
			ev.SetSummary(busyWord)
			setTransp(ev, true)
			continue
		}
		s.describeProjected(ev, it)
	}
	return cal.Serialize(ics.WithNewLineWindows), nil
}

// isPoint reports whether a timed projected item is a point — its end its
// start, a follow-up at a time — which the busy variant leaves out: a point
// is not a span of busy time.
func isPoint(it gen.CalendarItem) bool {
	return !it.AllDay && it.StartsAt != nil && (it.EndsAt == nil || !it.EndsAt.After(*it.StartsAt))
}

// busyUID is a projected item's UID in the busy variant: the first 32 hex
// digits of a MAC, under feedBusyUIDPurpose, of the person's id and the
// item's kind and id. The full variant's kind:id would name the module and
// the row, which is what a busy feed must not tell; a MAC is opaque, the same
// for one item from one request to the next — so a client updates the event
// rather than duplicating it — and different for two people, so two busy
// feeds cannot be joined on it. An appointment keeps its ical_uid, which is
// random already.
func (s *server) busyUID(userID uuid.UUID, it gen.CalendarItem) (string, error) {
	mac, err := s.deps.Secrets.MAC(feedBusyUIDPurpose, []byte(userID.String()+"\x00"+it.Kind+":"+it.Id))
	if err != nil {
		return "", fmt.Errorf("calendar: key a busy feed's UID: %w", err)
	}
	return hex.EncodeToString(mac)[:32] + "@vantigo", nil
}

// setItemWhen writes a projected item's DTSTART and DTEND: dates with the
// exclusive end for an all-day one, UTC instants for a timed one, and no
// DTEND for a point (its end its start).
func setItemWhen(ev *ics.VEvent, it gen.CalendarItem) {
	switch {
	case it.AllDay && it.StartDate != nil && it.EndDate != nil:
		setWhen(ev, span{allDay: true, startDate: civil(it.StartDate.Time), endDate: civil(it.EndDate.Time)}, nil)
	case it.StartsAt != nil && it.EndsAt != nil && it.EndsAt.After(*it.StartsAt):
		setWhen(ev, span{startsAt: *it.StartsAt, endsAt: *it.EndsAt}, nil)
	case it.StartsAt != nil:
		ev.SetStartAt(*it.StartsAt)
	}
}

// describeProjected writes a projected item's body in the full variant:
// SUMMARY (with ✓ before a done one's, since no client draws a strike),
// DESCRIPTION the subtitle, CATEGORIES the module, URL the app's page for
// it, STATUS CONFIRMED.
func (s *server) describeProjected(ev *ics.VEvent, it gen.CalendarItem) {
	title := oneLine(it.Title)
	if it.Done {
		title = "✓ " + title
	}
	ev.SetSummary(title)
	if it.Subtitle != nil && strings.TrimSpace(*it.Subtitle) != "" {
		ev.SetDescription(plainText(*it.Subtitle))
	}
	if module, _, ok := strings.Cut(it.Kind, "."); ok && module != "" {
		ev.AddCategory(oneLine(module))
	}
	if link := s.appPath(it.Path); link != "" {
		ev.SetURL(link)
	}
	ev.SetStatus(ics.ObjectStatusConfirmed)
}

// appPath is path in the app — APP_URL with the base path before it — ""
// when there is no path or the installation has no origin.
func (s *server) appPath(path *string) string {
	cfg := s.deps.Config
	if path == nil || *path == "" || cfg == nil || cfg.AppOrigin == "" {
		return ""
	}
	return cfg.AppOrigin + cfg.BasePath + *path
}
