package calendar

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// calendarBusy is the module's contracts.CalendarBusy, the first contract it
// provides (calendar Outlook sync design O9.8): people's busy time, which
// Planning's board shows beside its bookings.
//
// It is provider-gated (module boundaries' rule 12), by the calendar's own
// colleague rule, which a consumer cannot know: it answers the principal of
// the request in ctx — their own id when they hold calendar:access, anyone
// else's when they hold calendar:view-colleagues — and nothing, not an
// error, for an id the caller may not see or a user who is not active, or
// for every id when ctx carries no request or carries the AsUser mark. A user's busy time is what a colleague's calendar shows as
// busy: the appointments they own or accepted, transparency busy, not
// cancelled, each occurrence in the window (a cancelled occurrence dropped,
// a moved one at its new time), expanded in the business zone; and, while
// their Outlook sync is on, the busy and out-of-office rows it read back.
// Timed spans and all-day dates are each merged, clipped to the window.
// Every call out — identity's Check, the business zone — is made through
// contractscalls.go, with no transaction open.
type calendarBusy struct {
	s *server
}

var _ contracts.CalendarBusy = calendarBusy{}

// newCalendarBusy builds the module's provider for Compose.
func newCalendarBusy(d module.Deps) contracts.CalendarBusy {
	return calendarBusy{s: newServer(d)}
}

// Busy answers the busy time of the userIDs the caller may see over
// from..to, civil dates, both inclusive (contracts.CalendarBusy).
func (b calendarBusy) Busy(ctx context.Context, userIDs []uuid.UUID, from, to time.Time) (map[uuid.UUID]contracts.BusyTime, error) {
	from, to = civil(from), civil(to)
	if len(userIDs) > contracts.CalendarBusyMaxUsers || to.Before(from) || daysBetween(from, to)+1 > contracts.CalendarBusyMaxDays {
		return nil, contracts.ErrBusyWindowOutOfBounds
	}
	out := map[uuid.UUID]contracts.BusyTime{}
	ids, err := b.allowed(ctx, userIDs)
	if err != nil || len(ids) == 0 {
		return out, err
	}
	loc, err := b.s.calendarZone(ctx)
	if err != nil {
		return nil, fmt.Errorf("calendar: read the business time zone: %w", err)
	}
	w := window{from: from, to: to, loc: loc, fromAt: wallClock(from, loc), toAt: wallClock(addDays(to, 1), loc)}

	spans := map[uuid.UUID][]span{}
	if err := b.appointmentSpans(ctx, ids, w, spans); err != nil {
		return nil, err
	}
	rows, err := b.s.queries().ListMailboxBusyForUsers(ctx, store.ListMailboxBusyForUsersParams{
		UserIds: ids, ShowAs: busyShown,
		FromAt: w.fromAt, ToAt: w.toAt, FromDate: pgDate(w.from), ToDate: pgDate(w.to),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: read the Outlook busy time: %w", err)
	}
	for _, row := range rows {
		sp := span{allDay: row.AllDay}
		if row.AllDay {
			sp.startDate, _ = fromPgDate(row.StartDate)
			sp.endDate, _ = fromPgDate(row.EndDate)
		} else if row.StartsAt != nil && row.EndsAt != nil {
			sp.startsAt, sp.endsAt = row.StartsAt.UTC(), row.EndsAt.UTC()
		}
		spans[row.UserID] = append(spans[row.UserID], sp)
	}
	for id, list := range spans {
		if bt := mergeBusy(list, w); len(bt.Spans) > 0 || len(bt.Days) > 0 {
			out[id] = bt
		}
	}
	return out, nil
}

// allowed is, of userIDs, the ones the caller of the request in ctx may see,
// each once and active in the directory: their own id under calendar:access,
// anyone else's under calendar:view-colleagues. Nobody without a request,
// without a principal, or when ctx carries the AsUser mark — a
// provider-gated read answers a request's own principal alone, never a read
// on someone's behalf.
func (b calendarBusy) allowed(ctx context.Context, userIDs []uuid.UUID) ([]uuid.UUID, error) {
	if _, ok := contracts.RequestFrom(ctx); !ok {
		return nil, nil
	}
	if _, ok := contracts.AsUserFrom(ctx); ok {
		return nil, nil
	}
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok {
		return nil, nil
	}
	var own, others *bool
	var out []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, id := range userIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		gate, key := &others, "calendar:view-colleagues"
		if id == p.UserID {
			gate, key = &own, "calendar:access"
		}
		if *gate == nil {
			held, err := b.s.accessHas(ctx, key)
			if err != nil {
				return nil, err
			}
			*gate = &held
		}
		if **gate {
			out = append(out, id)
		}
	}
	return b.active(ctx, out)
}

// active is, of ids, the users the directory answers as active, in one
// batch: a user who left is busy at no time.
func (b calendarBusy) active(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	users, err := b.s.usersBatch(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("calendar: read the users: %w", err)
	}
	on := make(map[uuid.UUID]bool, len(users))
	for _, u := range users {
		on[u.ID] = u.Active
	}
	return slices.DeleteFunc(ids, func(id uuid.UUID) bool { return !on[id] }), nil
}

// appointmentSpans adds to spans each user's appointment occurrences in the
// window: those that block their time (ListBusyAppointmentsForUsers, the
// colleague query's rule), every series expanded with its exceptions.
func (b calendarBusy) appointmentSpans(ctx context.Context, ids []uuid.UUID, w window, spans map[uuid.UUID][]span) error {
	rows, err := b.s.queries().ListBusyAppointmentsForUsers(ctx, store.ListBusyAppointmentsForUsersParams{
		UserIds: ids, FromAt: w.fromAt.Add(-24 * time.Hour), ToAt: w.toAt.Add(24 * time.Hour),
		FromDate: pgDate(addDays(w.from, -seriesReachDays)), ToDate: pgDate(addDays(w.to, 1)),
	})
	if err != nil {
		return fmt.Errorf("calendar: read the busy appointments: %w", err)
	}
	var recurring []int64
	for _, r := range rows {
		if r.CalendarAppointment.Rrule != nil && !slices.Contains(recurring, r.CalendarAppointment.ID) {
			recurring = append(recurring, r.CalendarAppointment.ID)
		}
	}
	exceptions := map[int64][]store.CalendarAppointmentException{}
	if len(recurring) > 0 {
		excs, err := b.s.queries().ListExceptions(ctx, recurring)
		if err != nil {
			return fmt.Errorf("calendar: read the exceptions in the window: %w", err)
		}
		for _, e := range excs {
			exceptions[e.AppointmentID] = append(exceptions[e.AppointmentID], e)
		}
	}
	for _, r := range rows {
		occs, err := occurrencesIn(r.CalendarAppointment, exceptions[r.CalendarAppointment.ID], w)
		if err != nil {
			return err
		}
		for _, o := range occs {
			spans[r.BusyUserID] = append(spans[r.BusyUserID], o.span)
		}
	}
	return nil
}

// mergeBusy is one user's spans as contracts.BusyTime: the timed ones
// clipped to the window's instants and the all-day ones to its dates, each
// sorted and merged where they overlap or touch — an all-day run ending the
// day before another starts is one run.
func mergeBusy(list []span, w window) contracts.BusyTime {
	var timed []contracts.TimeSpan
	var days []contracts.DateSpan
	for _, sp := range list {
		if sp.allDay {
			from, to := maxTime(sp.startDate, w.from), minTime(sp.endDate, w.to)
			if !to.Before(from) {
				days = append(days, contracts.DateSpan{From: from, To: to})
			}
			continue
		}
		from, to := maxTime(sp.startsAt, w.fromAt).UTC(), minTime(sp.endsAt, w.toAt).UTC()
		if to.After(from) {
			timed = append(timed, contracts.TimeSpan{StartsAt: from, EndsAt: to})
		}
	}
	slices.SortFunc(timed, func(a, b contracts.TimeSpan) int { return a.StartsAt.Compare(b.StartsAt) })
	slices.SortFunc(days, func(a, b contracts.DateSpan) int { return cmp.Compare(a.From.Unix(), b.From.Unix()) })
	var out contracts.BusyTime
	for _, t := range timed {
		if n := len(out.Spans); n > 0 && !t.StartsAt.After(out.Spans[n-1].EndsAt) {
			out.Spans[n-1].EndsAt = maxTime(out.Spans[n-1].EndsAt, t.EndsAt)
			continue
		}
		out.Spans = append(out.Spans, t)
	}
	for _, d := range days {
		if n := len(out.Days); n > 0 && !d.From.After(addDays(out.Days[n-1].To, 1)) {
			out.Days[n-1].To = maxTime(out.Days[n-1].To, d.To)
			continue
		}
		out.Days = append(out.Days, d)
	}
	return out
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
