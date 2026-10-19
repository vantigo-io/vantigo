package calendar

import (
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is the iCalendar object an invitation carries (calendar
// invitations design D3), built with golang-ical: one VCALENDAR with METHOD
// REQUEST or CANCEL, the appointment as a VEVENT and, for a series, one more
// VEVENT per moved occurrence. Nothing in it is read from another module: the
// caller resolves the organiser's name, the attendees' addresses and the
// link first. Its writers — writeAppointment, setWhen, describeAppointment,
// icsRule, originalStart, zoneSpan, vtimezone — are the feed's too
// (feed_ics.go, calendar feed design D4).
//
// A single timed appointment's instants are written in UTC, which is exact. A
// timed series is not: with an RRULE, a UTC DTSTART repeats at a fixed UTC
// time, and a weekly 09:00 Oslo series would land at 08:00 or 10:00 across
// the clock change. So a timed series writes its DTSTART, DTEND, EXDATEs and
// RECURRENCE-IDs as wall-clock with TZID=<its zone>, and the calendar carries
// a VTIMEZONE for that zone built from Go's tzdata (vtimezone). An all-day
// appointment is dates, VALUE=DATE, its end exclusive.

// The two methods an invitation is sent with.
const (
	methodRequest = "REQUEST"
	methodCancel  = "CANCEL"
)

// icsProductID is the PRODID every object carries.
const icsProductID = "-//Vantigo//Calendar//EN"

// The iCalendar date-time forms: an instant in UTC, a wall-clock read with a
// TZID, and a date.
const (
	icsUTC   = "20060102T150405Z"
	icsLocal = "20060102T150405"
	icsDate  = "20060102"
)

// vtimezoneYears is how far past its start the VTIMEZONE of an unbounded
// series reaches; past it, the last offset holds.
const vtimezoneYears = 3

// icsAttendee is one outside attendee as an ATTENDEE line names them.
type icsAttendee struct {
	name, address, status string
}

// icsInput is what one invitation's object is built from.
type icsInput struct {
	method    string
	appt      store.CalendarAppointment
	excs      []store.CalendarAppointmentException
	attendees []icsAttendee
	// link is the recipient's RSVP link: the URL, and the end of the
	// DESCRIPTION of a REQUEST after rsvpSentence.
	link string
	// rsvpSentence is the line, in the organiser's language, that asks for
	// the answer by the link.
	rsvpSentence string
	// organiser is the owner's display name, the ORGANIZER's CN; from is the
	// installation's SMTP_FROM, its address.
	organiser, from string
	now             time.Time
}

// buildICS is the VCALENDAR of in, CRLF line endings and all.
func buildICS(in icsInput) (string, error) {
	in.appt = cleanText(in.appt)
	in.organiser = oneLine(in.organiser)
	in.attendees = slices.Clone(in.attendees)
	for i := range in.attendees {
		in.attendees[i].name = oneLine(in.attendees[i].name)
	}
	sr, err := seriesOf(in.appt)
	if err != nil {
		return "", err
	}
	var loc *time.Location
	zoned := sr != nil && !in.appt.AllDay
	if zoned {
		loc = sr.loc
	}

	cal := ics.NewCalendar()
	cal.SetProductId(icsProductID)
	cal.SetMethod(ics.Method(in.method))
	if zoned {
		from, to := zoneSpan(in.appt, in.excs, sr)
		cal.AddVTimezone(vtimezone(loc, from, to))
	}

	writeAppointment(cal, in.appt, in.excs, sr, loc, in.now, func(ev *ics.VEvent) { describe(ev, in) })
	return cal.Serialize(ics.WithNewLineWindows), nil
}

// writeAppointment adds a's VEVENTs to cal — the invitation's and the
// feed's alike (calendar feed design D4): the master with its UID, DTSTAMP,
// SEQUENCE and time, then body, then for a series its RRULE and one EXDATE per
// cancelled occurrence; and one VEVENT per moved occurrence with the same UID,
// its RECURRENCE-ID, its new time and body. sr is a's series (nil when single)
// and loc its zone when the series is timed, nil otherwise. The caller has
// added the VTIMEZONE loc needs.
func writeAppointment(cal *ics.Calendar, a store.CalendarAppointment, excs []store.CalendarAppointmentException, sr *series, loc *time.Location, now time.Time, body func(*ics.VEvent)) {
	master := cal.AddEvent(a.IcalUid)
	stamp(master, now, a.Sequence)
	setWhen(master, spanOf(a), loc)
	body(master)
	if a.Rrule != nil {
		master.AddRrule(icsRule(*a.Rrule, a.AllDay, loc))
	}
	excs = slices.Clone(excs)
	slices.SortFunc(excs, func(x, y store.CalendarAppointmentException) int {
		return x.OccurrenceDate.Time.Compare(y.OccurrenceDate.Time)
	})
	for _, e := range excs {
		if !e.Cancelled {
			continue
		}
		value, params := originalStart(e, sr, a.AllDay)
		master.AddExdate(value, params...)
	}
	for _, e := range excs {
		if e.Cancelled {
			continue
		}
		ev := cal.AddEvent(a.IcalUid)
		stamp(ev, now, a.Sequence)
		value, params := originalStart(e, sr, a.AllDay)
		ev.AddProperty(ics.ComponentPropertyRecurrenceId, value, params...)
		setWhen(ev, exceptionSpan(e, a.AllDay), loc)
		body(ev)
	}
}

// stamp writes DTSTAMP and SEQUENCE, after the UID AddEvent wrote.
func stamp(ev *ics.VEvent, now time.Time, sequence int32) {
	ev.SetDtStampTime(now)
	ev.SetSequence(int(sequence))
}

// setWhen writes DTSTART and DTEND for s: dates with the exclusive end for an
// all-day one, wall-clock with the TZID when loc is the series' zone, UTC
// instants otherwise.
func setWhen(ev *ics.VEvent, s span, loc *time.Location) {
	switch {
	case s.allDay:
		ev.SetAllDayStartAt(s.startDate)
		ev.SetAllDayEndAt(addDays(s.endDate, 1))
	case loc != nil:
		ev.SetProperty(ics.ComponentPropertyDtStart, s.startsAt.In(loc).Format(icsLocal), ics.WithTZID(loc.String()))
		ev.SetProperty(ics.ComponentPropertyDtEnd, s.endsAt.In(loc).Format(icsLocal), ics.WithTZID(loc.String()))
	default:
		ev.SetStartAt(s.startsAt)
		ev.SetEndAt(s.endsAt)
	}
}

// describe writes everything of an invitation's VEVENT but its time: the
// appointment's own body (describeAppointment) — its DESCRIPTION followed, on
// a REQUEST, by the RSVP sentence and the link, its URL the link, its STATUS
// CANCELLED on a CANCEL — and then its participants.
func describe(ev *ics.VEvent, in icsInput) {
	var more []string
	if in.method == methodRequest && in.link != "" {
		more = append(more, in.rsvpSentence+"\n"+in.link)
	}
	describeAppointment(ev, in.appt, in.link, in.method == methodCancel, more...)
	describeParticipants(ev, in)
}

// describeAppointment writes an appointment's VEVENT body but its time and
// its participants: SUMMARY, LOCATION, DESCRIPTION (the description, then
// each of more after a blank line), URL when link is set, STATUS (CANCELLED
// when cancelled, CONFIRMED otherwise), CLASS and TRANSP. a's text is the
// caller's to have cleaned (cleanText).
func describeAppointment(ev *ics.VEvent, a store.CalendarAppointment, link string, cancelled bool, more ...string) {
	ev.SetSummary(a.Title)
	if a.Location != nil {
		ev.SetLocation(*a.Location)
	}
	var parts []string
	if a.Description != nil && strings.TrimSpace(*a.Description) != "" {
		parts = append(parts, *a.Description)
	}
	parts = append(parts, more...)
	if len(parts) > 0 {
		ev.SetDescription(strings.Join(parts, "\n\n"))
	}
	if link != "" {
		ev.SetURL(link)
	}
	if cancelled {
		ev.SetStatus(ics.ObjectStatusCancelled)
	} else {
		ev.SetStatus(ics.ObjectStatusConfirmed)
	}
	if a.Visibility == visibilityPrivate {
		ev.SetClass(ics.ClassificationPrivate)
	} else {
		ev.SetClass(ics.ClassificationPublic)
	}
	setTransp(ev, a.Transparency != transparencyFree)
}

// setTransp writes TRANSP: OPAQUE for a span that blocks the time,
// TRANSPARENT for one that does not.
func setTransp(ev *ics.VEvent, opaque bool) {
	if opaque {
		ev.SetProperty(ics.ComponentPropertyTransp, "OPAQUE")
	} else {
		ev.SetProperty(ics.ComponentPropertyTransp, "TRANSPARENT")
	}
}

// describeParticipants writes an invitation's ORGANIZER and one ATTENDEE per
// outside attendee. The feed writes neither (calendar feed design D3).
func describeParticipants(ev *ics.VEvent, in icsInput) {
	var organiser []ics.PropertyParameter
	if cn := safeCN(in.organiser); cn != "" {
		organiser = append(organiser, ics.WithCN(cn))
	}
	ev.SetOrganizer("mailto:"+bareAddress(in.from), organiser...)
	for _, at := range in.attendees {
		params := []ics.PropertyParameter{
			ics.ParticipationRoleReqParticipant,
			partstat(at.status),
			&ics.KeyValues{Key: string(ics.ParameterRsvp), Value: []string{"TRUE"}},
		}
		if cn := safeCN(at.name); cn != "" {
			params = append(params, ics.WithCN(cn))
		}
		ev.AddAttendee("mailto:"+at.address, params...)
	}
}

// partstat is an attendee's answer as iCalendar's PARTSTAT.
func partstat(status string) ics.ParticipationStatus {
	switch status {
	case "accepted":
		return ics.ParticipationStatusAccepted
	case "declined":
		return ics.ParticipationStatusDeclined
	case "tentative":
		return ics.ParticipationStatusTentative
	}
	return ics.ParticipationStatusNeedsAction
}

// safeCN is a name fit for a CN parameter. golang-ical writes a parameter
// value unquoted with a backslash before , ; : ' and ", which is not RFC
// 5545's form, so the name is written without them: an apostrophe as the
// typographic one, the separators as spaces.
func safeCN(name string) string {
	r := strings.NewReplacer("'", "’", `"`, "", `\`, "", ",", " ", ";", " ", ":", " ", "\r", " ", "\n", " ")
	return strings.Join(strings.Fields(r.Replace(name)), " ")
}

// bareAddress is SMTP_FROM's address, without a display name it may carry.
func bareAddress(from string) string {
	if a, err := mail.ParseAddress(from); err == nil {
		return a.Address
	}
	return strings.TrimSpace(from)
}

// icsRule is the stored rule as an RRULE: as stored for an all-day series,
// whose DTSTART is a date; for a timed one, whose DTSTART is wall-clock with
// a TZID, an UNTIL date becomes the last second of that day in the zone, in
// UTC, the form RFC 5545 requires beside such a DTSTART.
func icsRule(rule string, allDay bool, loc *time.Location) string {
	if allDay || loc == nil {
		return rule
	}
	parts := strings.Split(rule, ";")
	for i, p := range parts {
		value, ok := strings.CutPrefix(p, "UNTIL=")
		if !ok {
			continue
		}
		d, err := time.Parse(icsDate, value)
		if err != nil {
			continue
		}
		last := time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, loc)
		parts[i] = "UNTIL=" + last.UTC().Format(icsUTC)
	}
	return strings.Join(parts, ";")
}

// originalStart is where the series put an exception's occurrence, as
// EXDATE and RECURRENCE-ID write it: its date for an all-day series, the
// series' start wall-clock on that day with the zone's TZID for a timed one.
func originalStart(e store.CalendarAppointmentException, sr *series, allDay bool) (string, []ics.PropertyParameter) {
	d := civil(e.OccurrenceDate.Time)
	if allDay || sr == nil {
		return d.Format(icsDate), []ics.PropertyParameter{ics.WithValue(string(ics.ValueDataTypeDate))}
	}
	wall := time.Date(d.Year(), d.Month(), d.Day(), sr.start.Hour(), sr.start.Minute(), sr.start.Second(), 0, time.UTC)
	return wall.Format(icsLocal), []ics.PropertyParameter{ics.WithTZID(sr.loc.String())}
}

// zoneSpan is the instants a timed series' VTIMEZONE must cover: from its
// start to the end of its last day, or vtimezoneYears past its start when it
// never ends, widened to every moved occurrence.
func zoneSpan(a store.CalendarAppointment, excs []store.CalendarAppointmentException, sr *series) (time.Time, time.Time) {
	from := *a.StartsAt
	to := from.AddDate(vtimezoneYears, 0, 0)
	if last, ok := fromPgDate(a.SeriesLastDate); ok {
		to = wallClock(addDays(last, 1), sr.loc).Add(sr.duration)
	}
	for _, e := range excs {
		if e.StartsAt != nil && e.StartsAt.Before(from) {
			from = *e.StartsAt
		}
		if e.EndsAt != nil && e.EndsAt.After(to) {
			to = *e.EndsAt
		}
	}
	return from.UTC(), to.UTC()
}

// vtimezone is loc as a VTIMEZONE over [from, to]: one STANDARD or DAYLIGHT
// component per period of the zone's tzdata in force over the span — the one
// in force at from, then one per transition up to to — each with DTSTART (the
// transition's wall-clock before it), TZOFFSETFROM, TZOFFSETTO and TZNAME.
// The RDATE-free, one-component-per-transition form is valid RFC 5545 and is
// what every client reads, Outlook desktop included. A zone with no
// transitions is one STANDARD from 1970.
func vtimezone(loc *time.Location, from, to time.Time) *ics.VTimezone {
	tz := ics.NewTimezone(loc.String())
	start, end := from.In(loc).ZoneBounds()
	if start.IsZero() {
		name, offset := from.In(loc).Zone()
		zonePeriod(tz, false, "19700101T000000", offset, offset, name)
	} else {
		transition(tz, loc, start)
	}
	for !end.IsZero() && !end.After(to) {
		transition(tz, loc, end)
		_, end = end.In(loc).ZoneBounds()
	}
	return tz
}

// transition writes the period loc enters at instant at.
func transition(tz *ics.VTimezone, loc *time.Location, at time.Time) {
	t := at.In(loc)
	name, offsetTo := t.Zone()
	_, offsetFrom := at.Add(-time.Second).In(loc).Zone()
	onset := at.UTC().Add(time.Duration(offsetFrom) * time.Second).Format(icsLocal)
	zonePeriod(tz, t.IsDST(), onset, offsetFrom, offsetTo, name)
}

// zonePeriod adds one STANDARD or DAYLIGHT component to tz.
func zonePeriod(tz *ics.VTimezone, daylight bool, onset string, offsetFrom, offsetTo int, name string) {
	var c *ics.ComponentBase
	if daylight {
		d := &ics.Daylight{}
		tz.Components = append(tz.Components, d)
		c = &d.ComponentBase
	} else {
		c = &tz.AddStandard().ComponentBase
	}
	c.AddProperty(ics.ComponentPropertyDtStart, onset)
	c.AddProperty(ics.ComponentProperty(ics.PropertyTzoffsetfrom), utcOffset(offsetFrom))
	c.AddProperty(ics.ComponentProperty(ics.PropertyTzoffsetto), utcOffset(offsetTo))
	c.AddProperty(ics.ComponentProperty(ics.PropertyTzname), name)
}

// utcOffset is seconds east of UTC as iCalendar's UTC-OFFSET: ±HHMM, with
// seconds only when there are some.
func utcOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	h, m, s := seconds/3600, seconds%3600/60, seconds%60
	if s != 0 {
		return fmt.Sprintf("%s%02d%02d%02d", sign, h, m, s)
	}
	return fmt.Sprintf("%s%02d%02d", sign, h, m)
}
