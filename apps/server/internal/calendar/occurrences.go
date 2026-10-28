package calendar

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is one occurrence's exception (calendar design D4): cancelled, or
// moved to the pair the series uses, keyed by the day the series put it on —
// which stays its identity wherever it moved. The owner alone writes one;
// a move, a cancel and a restore are each a change of when, so each moves the
// appointment's revision and its sequence on (calendar design D2), and an
// If-Match read before them is stale. After the commit, every outside
// attendee is sent the whole series again as a REQUEST with the new
// sequence, which is how the clients learn of an exception (calendar
// invitations design D3). A cancelled appointment is 409
// appointment_cancelled.

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
	exc, err := s.writeOccurrence(ctx, callerID(ctx), req.Id, civil(req.Date.Time), body)
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

// writeOccurrence moves or cancels the occurrence on date of the series id
// me owns — the screen's PUT and the Outlook write-back's move and delete of
// an occurrence's copy (calendar Outlook sync design O10.6, O10.8) alike, as
// the PUT's comment above tells — and, after the commit, sends the series'
// REQUEST. Its refusals: a date too far away, a single appointment or a body
// that breaks a rule 400; an appointment me does not own 404; a day the rule
// never produces 409 occurrence_not_in_series and a cancelled appointment
// 409 appointment_cancelled.
func (s *server) writeOccurrence(ctx context.Context, me uuid.UUID, id int64, date time.Time, body gen.OccurrenceWrite) (store.CalendarAppointmentException, error) {
	if msg := s.yearsAway(date); msg != "" {
		var f fieldErrors
		f.add("date", msg)
		return store.CalendarAppointmentException{}, refuse(http.StatusBadRequest, invalid(invalidOccurrenceTitle, f))
	}
	var (
		exc     store.CalendarAppointmentException
		changed written
	)
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		cur, err := lockWritable(ctx, txq, id, me)
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
		if changed, err = touch(ctx, txq, cur.ID, s.deps.Clock()); err != nil {
			return err
		}
		return s.nudgeMailboxes(ctx, txq, cur.OwnerUserID, changed.atts)
	})
	if err != nil {
		return store.CalendarAppointmentException{}, err
	}
	s.tell(ctx, changed)
	return exc, nil
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
	var changed written
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		cur, err := lockWritable(ctx, txq, req.Id, me)
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
		if changed, err = touch(ctx, txq, cur.ID, s.deps.Clock()); err != nil {
			return err
		}
		return s.nudgeMailboxes(ctx, txq, cur.OwnerUserID, changed.atts)
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.DeleteCalendarAppointmentsByIdOccurrencesByDate409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.DeleteCalendarAppointmentsByIdOccurrencesByDate404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	s.tell(ctx, changed)
	return gen.DeleteCalendarAppointmentsByIdOccurrencesByDate204Response{}, nil
}

// written is a series as an exception's write left it, for the REQUEST that
// tells its outside attendees; the zero value tells nobody.
type written struct {
	appt store.CalendarAppointment
	atts []store.CalendarAppointmentAttendee
	excs []store.CalendarAppointmentException
}

// tell sends w's REQUEST under the request's after-commit budget.
func (s *server) tell(ctx context.Context, w written) {
	b := s.newBudget(ctx, afterCommitBudget)
	defer b.done()
	s.sendInvitations(b, w.job())
}

// job is the REQUEST of the whole series to every outside attendee.
func (w written) job() invitationJob {
	return invitationJob{appt: w.appt, excs: w.excs, listed: w.atts, to: w.atts, method: methodRequest}
}

// touch moves the appointment's revision and sequence on after a write to
// its exceptions alone, and answers the series as it now stands.
func touch(ctx context.Context, txq *store.Queries, id int64, now time.Time) (written, error) {
	row, err := txq.TouchAppointmentWhen(ctx, store.TouchAppointmentWhenParams{ID: id, Now: now})
	if err != nil {
		return written{}, fmt.Errorf("calendar: touch appointment %d: %w", id, err)
	}
	atts, err := txq.ListAttendees(ctx, id)
	if err != nil {
		return written{}, fmt.Errorf("calendar: read appointment %d's attendees: %w", id, err)
	}
	excs, err := exceptionsOf(ctx, txq, row)
	if err != nil {
		return written{}, err
	}
	return written{appt: row, atts: atts, excs: excs}, nil
}
