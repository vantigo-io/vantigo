package calendar

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
)

// This file is the window (calendar design D5): one person's calendar from
// one civil date to another, judged in the viewer's zone. Items are built in
// order — the caller's appointments, owned or attended with any status,
// expanded in their own zones with their exceptions applied; then every
// enabled module's calendar source, concurrently, in module order; then the
// public holidays beside them — and sorted by start, all-day first within a
// day. Nothing is paged: 93 days of one person's calendar is hundreds of rows.

// sourcesBudget is how long the providers have, all of them together.
const sourcesBudget = 10 * time.Second

// seriesReachDays widens the database's window back for a series: an
// occurrence that started before the window may still cover it, by as much
// as the longest an appointment may be (an all-day one, 366 days).
const seriesReachDays = maxAllDayDays + 1

// errSourceFailed is a calendar source that answered an error, or did not
// answer in time: the request is a 503 naming no module.
var errSourceFailed = errors.New("calendar: a calendar source could not be read")

// GetCalendarItems The caller's calendar
// (GET /api/v1/calendar/items)
func (s *server) GetCalendarItems(ctx context.Context, req gen.GetCalendarItemsRequestObject) (gen.GetCalendarItemsResponseObject, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	w, f, err := s.parseWindow(ctx, req.Params.From, req.Params.To, req.Params.TimeZone, c.zone())
	if err != nil {
		return nil, err
	}
	if f.failed() {
		return gen.GetCalendarItems400ApplicationProblemPlusJSONResponse(invalid(invalidWindowTitle, f)), nil
	}
	kinds := parseKinds(req.Params.Kinds)
	list := []gen.CalendarItem{}
	if kinds == nil || kinds[appointmentKind] {
		own, err := s.ownItems(ctx, c.UserID, w)
		if err != nil {
			return nil, err
		}
		list = append(list, own...)
	}
	projected, err := s.sourceItemsIn(ctx, c.UserID, w, kinds)
	if errors.Is(err, errSourceFailed) {
		return gen.GetCalendarItems503ApplicationProblemPlusJSONResponse(sourceUnavailable()), nil
	}
	if err != nil {
		return nil, err
	}
	list = append(list, projected...)
	sources, err := s.sourceKinds(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetCalendarItems200JSONResponse(windowOut(w, sources, list)), nil
}

// parseKinds is the kinds query parameter as a set, nil when absent or
// blank — every kind.
func parseKinds(raw *string) map[string]bool {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	out := map[string]bool{}
	for _, k := range strings.Split(*raw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out[k] = true
		}
	}
	return out
}

// windowOut is the answer's shape: the window, the sources, the public
// holidays in it and the items sorted.
func windowOut(w window, sources []gen.CalendarSourceKind, list []gen.CalendarItem) gen.CalendarItems {
	sortItems(list, w.loc)
	return gen.CalendarItems{
		From: apiDate(w.from), To: apiDate(w.to), TimeZone: w.loc.String(),
		Sources: sources, PublicHolidays: publicHolidays(w.from, w.to), Items: list,
	}
}

// ownItems is userID's own appointments in the window — owned, or attended
// with any status — as the owner or attendee sees them.
func (s *server) ownItems(ctx context.Context, userID uuid.UUID, w window) ([]gen.CalendarItem, error) {
	rows, err := s.queries().ListAppointmentsInWindow(ctx, store.ListAppointmentsInWindowParams{
		UserID: userID, FromAt: w.fromAt.Add(-24 * time.Hour), ToAt: w.toAt.Add(24 * time.Hour),
		FromDate: pgDate(addDays(w.from, -seriesReachDays)), ToDate: pgDate(addDays(w.to, 1)),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: read the appointments in the window: %w", err)
	}
	appts := make([]store.CalendarAppointment, len(rows))
	status := map[int64]*string{}
	for i, r := range rows {
		appts[i] = r.CalendarAppointment
		status[r.CalendarAppointment.ID] = r.MyStatus
	}
	return s.expandItems(ctx, appts, w, func(a store.CalendarAppointment) itemView {
		return itemView{isOwner: a.OwnerUserID == userID, myStatus: status[a.ID], full: true}
	})
}

// itemView is how one appointment is shown to the viewer: as its owner or an
// attendee (full), or as a colleague's busy block.
type itemView struct {
	isOwner  bool
	myStatus *string
	full     bool
}

// expandItems is every occurrence of appts in the window, with its
// exceptions applied, each as view says.
func (s *server) expandItems(ctx context.Context, appts []store.CalendarAppointment, w window, view func(store.CalendarAppointment) itemView) ([]gen.CalendarItem, error) {
	if len(appts) == 0 {
		return nil, nil
	}
	var recurring []int64
	for _, a := range appts {
		if a.Rrule != nil {
			recurring = append(recurring, a.ID)
		}
	}
	exceptions := map[int64][]store.CalendarAppointmentException{}
	if len(recurring) > 0 {
		rows, err := s.queries().ListExceptions(ctx, recurring)
		if err != nil {
			return nil, fmt.Errorf("calendar: read the exceptions in the window: %w", err)
		}
		for _, e := range rows {
			exceptions[e.AppointmentID] = append(exceptions[e.AppointmentID], e)
		}
	}
	var out []gen.CalendarItem
	for _, a := range appts {
		occs, err := occurrencesIn(a, exceptions[a.ID], w)
		if err != nil {
			return nil, err
		}
		v := view(a)
		for _, o := range occs {
			out = append(out, appointmentItem(a, o, v))
		}
	}
	return out, nil
}

// occurrence is one appointment's time in the window: the series' date for an
// occurrence (nil for a single appointment), and its span.
type occurrence struct {
	date *time.Time
	span
}

// occurrencesIn is a's occurrences that overlap the window: a single one
// itself; a series expanded in its zone over the window, widened back by as
// much as an occurrence may reach, its cancelled days dropped and its moved
// ones at their new time — a moved one included wherever it came from.
func occurrencesIn(a store.CalendarAppointment, excs []store.CalendarAppointmentException, w window) ([]occurrence, error) {
	if a.Rrule == nil {
		if sp := spanOf(a); overlaps(sp, w) {
			return []occurrence{{span: sp}}, nil
		}
		return nil, nil
	}
	sr, err := seriesOf(a)
	if err != nil {
		return nil, err
	}
	byDate := map[time.Time]store.CalendarAppointmentException{}
	for _, e := range excs {
		if d, ok := fromPgDate(e.OccurrenceDate); ok {
			byDate[d] = e
		}
	}
	var out []occurrence
	seen := map[time.Time]bool{}
	for _, f := range sr.on(addDays(w.from, -sr.reachDays()), addDays(w.to, 2)) {
		d := civil(f)
		seen[d] = true
		sp := sr.occurrence(f)
		if e, ok := byDate[d]; ok {
			if e.Cancelled {
				continue
			}
			sp = exceptionSpan(e, a.AllDay)
		}
		if overlaps(sp, w) {
			out = append(out, occurrence{date: &d, span: sp})
		}
	}
	// A moved occurrence from a day outside the expansion may have moved
	// into the window. Its day was the rule's when it was written, and a
	// change of the rule drops every exception, so it is taken as written.
	for d, e := range byDate {
		if seen[d] || e.Cancelled {
			continue
		}
		if sp := exceptionSpan(e, a.AllDay); overlaps(sp, w) {
			out = append(out, occurrence{date: &d, span: sp})
		}
	}
	return out, nil
}

// exceptionSpan is a moved occurrence's new time.
func exceptionSpan(e store.CalendarAppointmentException, allDay bool) span {
	sp := span{allDay: allDay}
	if allDay {
		sp.startDate, _ = fromPgDate(e.StartDate)
		sp.endDate, _ = fromPgDate(e.EndDate)
	} else if e.StartsAt != nil && e.EndsAt != nil {
		sp.startsAt, sp.endsAt = e.StartsAt.UTC(), e.EndsAt.UTC()
	}
	return sp
}

// overlaps reports whether sp is in the window as the viewer's zone judges
// it: a timed span when its instants overlap [fromAt, toAt), an all-day one
// when its dates overlap [from, to].
func overlaps(sp span, w window) bool {
	if sp.allDay {
		return !sp.startDate.After(w.to) && !sp.endDate.Before(w.from)
	}
	return sp.startsAt.Before(w.toAt) && sp.endsAt.After(w.fromAt)
}

// appointmentItem is one occurrence on the wire. Its id is the appointment's,
// with the occurrence's date after a colon for a series. A colleague's
// private appointment carries no title and no location — the client labels
// it — and a colleague is never its owner nor has an answer of their own.
func appointmentItem(a store.CalendarAppointment, o occurrence, v itemView) gen.CalendarItem {
	id := strconv.FormatInt(a.ID, 10)
	appt := &gen.CalendarItemAppointment{
		Id: a.ID, IsOwner: v.isOwner, Recurring: a.Rrule != nil,
		Visibility: gen.AppointmentVisibility(a.Visibility), Transparency: gen.AppointmentTransparency(a.Transparency),
	}
	if v.myStatus != nil {
		st := gen.AppointmentAttendeeStatus(*v.myStatus)
		appt.MyStatus = &st
	}
	if o.date != nil {
		id += ":" + o.date.Format(time.DateOnly)
		d := apiDate(*o.date)
		appt.OccurrenceDate = &d
	}
	it := gen.CalendarItem{Kind: appointmentKind, Id: id, Title: a.Title, Subtitle: a.Location, AllDay: o.allDay, Appointment: appt}
	if !v.full && a.Visibility == visibilityPrivate {
		it.Title, it.Subtitle = "", nil
	}
	setSpan(&it, o.span)
	return it
}

// setSpan puts sp on the item: its dates when all-day, its instants in UTC
// when timed.
func setSpan(it *gen.CalendarItem, sp span) {
	if sp.allDay {
		sd, ed := apiDate(sp.startDate), apiDate(sp.endDate)
		it.StartDate, it.EndDate = &sd, &ed
		return
	}
	sa, ea := sp.startsAt.UTC(), sp.endsAt.UTC()
	it.StartsAt, it.EndsAt = &sa, &ea
}

// sourceItemsIn is every calendar source's items for userID in the window,
// in module order (calendar design D1): the sources kinds names, or all of
// them, asked concurrently under one budget, each with the request's context
// so its own gate sees the caller, and none inside a transaction. A source
// that fails fails the lot (errSourceFailed): absent must never also mean
// "could not be read". Which one failed is logged, never answered.
func (s *server) sourceItemsIn(ctx context.Context, userID uuid.UUID, w window, kinds map[string]bool) ([]gen.CalendarItem, error) {
	srcs, err := s.sources(ctx)
	if err != nil {
		return nil, err
	}
	q := contracts.CalendarQuery{UserID: userID, From: w.from, To: w.to, Zone: w.loc}
	budget, cancel := context.WithTimeout(ctx, sourcesBudget)
	defer cancel()
	g, gctx := errgroup.WithContext(budget)
	answers := make([][]contracts.CalendarItem, len(srcs))
	for i, src := range srcs {
		if !asked(src, kinds) {
			continue
		}
		g.Go(func() error {
			items, err := s.sourceItems(gctx, src, q)
			if err != nil {
				s.deps.Logger.ErrorContext(ctx, "calendar: a calendar source failed",
					"module", sourceModule(src), "error", err)
				return errSourceFailed
			}
			answers[i] = items
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var out []gen.CalendarItem
	for _, items := range answers {
		for _, p := range items {
			if kinds != nil && !kinds[p.Kind] {
				continue
			}
			out = append(out, projectedItem(p))
		}
	}
	return out, nil
}

// asked reports whether a source is asked under kinds: always when kinds is
// nil, otherwise when any kind it declares is named.
func asked(src contracts.CalendarSource, kinds map[string]bool) bool {
	if kinds == nil {
		return true
	}
	for _, k := range src.CalendarKinds() {
		if kinds[k.Key] {
			return true
		}
	}
	return false
}

// sourceModule names a source's module for the log.
func sourceModule(src contracts.CalendarSource) string {
	if k := src.CalendarKinds(); len(k) > 0 {
		return k[0].Module
	}
	return "unknown"
}

// projectedItem is a provider's item on the wire.
func projectedItem(p contracts.CalendarItem) gen.CalendarItem {
	it := gen.CalendarItem{Kind: p.Kind, Id: p.ID, Title: p.Title, AllDay: p.AllDay, Done: p.Done}
	if p.Subtitle != "" {
		it.Subtitle = &p.Subtitle
	}
	if p.Path != "" {
		it.Path = &p.Path
	}
	setSpan(&it, span{allDay: p.AllDay, startDate: p.StartDate, endDate: p.EndDate, startsAt: p.StartsAt, endsAt: p.EndsAt})
	return it
}

// sortItems sorts the list by start — the day it starts on in loc, all-day
// before timed within a day, then the instant — keeping the build order
// (appointments, then the sources in module order) among equals.
func sortItems(list []gen.CalendarItem, loc *time.Location) {
	type key struct {
		day   time.Time
		timed bool
		at    time.Time
	}
	keyOf := func(it gen.CalendarItem) key {
		if it.AllDay && it.StartDate != nil {
			return key{day: civil(it.StartDate.Time), at: civil(it.StartDate.Time)}
		}
		if it.StartsAt != nil {
			return key{day: civil(it.StartsAt.In(loc)), timed: true, at: *it.StartsAt}
		}
		return key{}
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := keyOf(list[i]), keyOf(list[j])
		if !a.day.Equal(b.day) {
			return a.day.Before(b.day)
		}
		if a.timed != b.timed {
			return !a.timed
		}
		return a.at.Before(b.at)
	})
}

// holidayName is one Norwegian public holiday's name, and the form it takes
// after another's in "Grunnlovsdag og andre pinsedag" — a proper noun keeps
// its capital.
type holidayName struct{ name, joined string }

// fixedHolidays are the holidays on a date of their own, by month and day.
var fixedHolidays = map[[2]int]holidayName{
	{1, 1}:   {"Nyttårsdag", "nyttårsdag"},
	{5, 1}:   {"Arbeidernes dag", "arbeidernes dag"},
	{5, 17}:  {"Grunnlovsdag", "grunnlovsdag"},
	{12, 25}: {"Første juledag", "første juledag"},
	{12, 26}: {"Andre juledag", "andre juledag"},
}

// easterHolidays are the holidays Easter moves, by their distance from
// Easter Sunday in days.
var easterHolidays = map[int]holidayName{
	-3: {"Skjærtorsdag", "skjærtorsdag"},
	-2: {"Langfredag", "langfredag"},
	0:  {"Første påskedag", "første påskedag"},
	1:  {"Andre påskedag", "andre påskedag"},
	39: {"Kristi himmelfartsdag", "Kristi himmelfartsdag"},
	49: {"Første pinsedag", "første pinsedag"},
	50: {"Andre pinsedag", "andre pinsedag"},
}

// publicHolidays is Norway's public holidays from from to to, named: the
// days are the platform package's, the names this module's, matched by date
// and by Easter arithmetic. A day two holidays share (Whit Monday on 17 May)
// is one entry naming both.
func publicHolidays(from, to time.Time) []gen.CalendarPublicHoliday {
	out := []gen.CalendarPublicHoliday{}
	for _, d := range holidays.PublicHolidays(from, to) {
		var names []holidayName
		if n, ok := fixedHolidays[[2]int{int(d.Month()), d.Day()}]; ok {
			names = append(names, n)
		}
		if n, ok := easterHolidays[daysBetween(holidays.EasterSunday(d.Year()), civil(d))]; ok {
			names = append(names, n)
		}
		name := ""
		for i, n := range names {
			if i == 0 {
				name = n.name
			} else {
				name += " og " + n.joined
			}
		}
		out = append(out, gen.CalendarPublicHoliday{Date: apiDate(d), Name: name})
	}
	return out
}
