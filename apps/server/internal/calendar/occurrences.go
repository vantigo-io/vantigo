package calendar

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is one occurrence's exception (calendar design D4): cancelled, or
// moved to the pair the series uses, keyed by the day the series put it on —
// which stays its identity wherever it moved. The owner alone writes one;
// a move, a cancel and a restore are each a change of when, so each moves the
// appointment's revision and its sequence on (calendar design D2), and an
// If-Match read before them is stale.

// PutCalendarAppointmentsByIdOccurrencesByDate Move or cancel an occurrence
// (PUT /api/v1/calendar/appointments/{id}/occurrences/{date})
//
// A single appointment has no occurrences (400); a day the rule never
// produces is occurrence_not_in_series (409), judged by expanding the rule to
// that day.
func (s *server) PutCalendarAppointmentsByIdOccurrencesByDate(ctx context.Context, req gen.PutCalendarAppointmentsByIdOccurrencesByDateRequestObject) (gen.PutCalendarAppointmentsByIdOccurrencesByDateResponseObject, error) {
	var body gen.OccurrenceWrite
	if req.Body != nil {
		body = *req.Body
	}
	me := callerID(ctx)
	date := civil(req.Date.Time)
	if msg := s.yearsAway(date); msg != "" {
		var f fieldErrors
		f.add("date", msg)
		return gen.PutCalendarAppointmentsByIdOccurrencesByDate400ApplicationProblemPlusJSONResponse(invalid(invalidOccurrenceTitle, f)), nil
	}
	var exc store.CalendarAppointmentException
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		cur, err := lockOwned(ctx, txq, req.Id, me)
		if err != nil {
			return err
		}
		if cur.Rrule == nil {
			var f fieldErrors
			f.add("date", "This appointment does not repeat, so it has no occurrences to move or cancel")
			return refuse(http.StatusBadRequest, invalid(invalidOccurrenceTitle, f))
		}
		sr, err := seriesOf(cur)
		if err != nil {
			return err
		}
		if !sr.produces(date) {
			return refuse(http.StatusConflict, occurrenceNotInSeries(date))
		}
		params, f := readOccurrence(body, cur.AllDay)
		if f.failed() {
			return refuse(http.StatusBadRequest, invalid(invalidOccurrenceTitle, f))
		}
		params.AppointmentID, params.OccurrenceDate, params.Now = cur.ID, pgDate(date), s.deps.Clock()
		if exc, err = txq.UpsertException(ctx, params); err != nil {
			return fmt.Errorf("calendar: write appointment %d's exception on %s: %w", cur.ID, date.Format(time.DateOnly), err)
		}
		return touch(ctx, txq, cur.ID, s.deps.Clock())
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PutCalendarAppointmentsByIdOccurrencesByDate400ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusConflict:
			return gen.PutCalendarAppointmentsByIdOccurrencesByDate409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PutCalendarAppointmentsByIdOccurrencesByDate404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutCalendarAppointmentsByIdOccurrencesByDate200JSONResponse(occurrenceOut(exc)), nil
}

// readOccurrence holds an exception's body to D4: cancelled, or a move by the
// pair the series uses held to the appointment's own rules — never both, and
// never neither.
func readOccurrence(body gen.OccurrenceWrite, allDay bool) (store.UpsertExceptionParams, fieldErrors) {
	var f fieldErrors
	cancelled := body.Cancelled != nil && *body.Cancelled
	moved := body.StartDate != nil || body.EndDate != nil || body.StartsAt != nil || body.EndsAt != nil
	switch {
	case cancelled && moved:
		f.add("cancelled", "An occurrence is cancelled or moved, not both")
		return store.UpsertExceptionParams{}, f
	case cancelled:
		return store.UpsertExceptionParams{Cancelled: true}, f
	case !moved:
		f.add("cancelled", "Send cancelled true, or the occurrence's new time")
		return store.UpsertExceptionParams{}, f
	}
	sp, _ := readSpan(allDay, body.StartDate, body.EndDate, body.StartsAt, body.EndsAt, &f)
	return store.UpsertExceptionParams{
		StartDate: spanStartDate(sp), EndDate: spanEndDate(sp),
		StartsAt: spanStartsAt(sp), EndsAt: spanEndsAt(sp),
	}, f
}

// DeleteCalendarAppointmentsByIdOccurrencesByDate Restore an occurrence
// (DELETE /api/v1/calendar/appointments/{id}/occurrences/{date})
//
// A day with no exception is already what this asks for, and answers the
// same, with nothing written.
func (s *server) DeleteCalendarAppointmentsByIdOccurrencesByDate(ctx context.Context, req gen.DeleteCalendarAppointmentsByIdOccurrencesByDateRequestObject) (gen.DeleteCalendarAppointmentsByIdOccurrencesByDateResponseObject, error) {
	me := callerID(ctx)
	date := civil(req.Date.Time)
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		cur, err := lockOwned(ctx, txq, req.Id, me)
		if err != nil {
			return err
		}
		n, err := txq.DeleteException(ctx, store.DeleteExceptionParams{AppointmentID: cur.ID, OccurrenceDate: pgDate(date)})
		if err != nil {
			return fmt.Errorf("calendar: remove appointment %d's exception on %s: %w", cur.ID, date.Format(time.DateOnly), err)
		}
		if n == 0 {
			return nil
		}
		return touch(ctx, txq, cur.ID, s.deps.Clock())
	})
	if _, ok := asRefusal(err); ok {
		return gen.DeleteCalendarAppointmentsByIdOccurrencesByDate404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteCalendarAppointmentsByIdOccurrencesByDate204Response{}, nil
}

// touch moves the appointment's revision and sequence on after a write to
// its exceptions alone.
func touch(ctx context.Context, txq *store.Queries, id int64, now time.Time) error {
	if _, err := txq.TouchAppointmentWhen(ctx, store.TouchAppointmentWhenParams{ID: id, Now: now}); err != nil {
		return fmt.Errorf("calendar: touch appointment %d: %w", id, err)
	}
	return nil
}
