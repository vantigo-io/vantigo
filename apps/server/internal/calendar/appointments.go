package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is the appointment (calendar design D2, D3): its create, read,
// replace and delete, and the one response shape every operation that answers
// an appointment shares. The owner reads and writes it, an attendee reads it,
// anyone else gets the 404 an unknown id gets. Every write runs under the
// appointment's row lock in one transaction (withLockedTx), and everything it
// needs from another module — the attendees' existence and names, the
// customer's and the project's names — is read before that transaction opens.

// appointmentInput is an AppointmentWrite that passed every rule.
type appointmentInput struct {
	title        string
	description  *string
	location     *string
	when         span
	zone         string
	visibility   string
	transparency string
	rule         *string
	lastDate     pgtype.Date
	attendees    []uuid.UUID // never nil, so "none" deletes every attendee
	customerID   *int32
	projectID    *int32
}

// readSpan is the time a body sets, held to D2's rules: exactly the pair the
// kind uses — two dates for an all-day one, two instants for a timed one —
// the end on or after (dates) or after (instants) the start, and at most 366
// days or 31 days. ok is false when any rule failed.
func readSpan(allDay bool, startDate, endDate *openapi_types.Date, startsAt, endsAt *time.Time, f *fieldErrors) (span, bool) {
	before := len(f.errs)
	s := span{allDay: allDay}
	if allDay {
		if startsAt != nil || endsAt != nil {
			f.add("startsAt", "An all-day appointment has dates, not instants")
		}
		if startDate == nil {
			f.add("startDate", "startDate is required")
		}
		if endDate == nil {
			f.add("endDate", "endDate is required")
		}
		if startDate != nil && endDate != nil {
			s.startDate, s.endDate = civil(startDate.Time), civil(endDate.Time)
			switch {
			case s.endDate.Before(s.startDate):
				f.add("endDate", "endDate cannot be before startDate")
			case daysBetween(s.startDate, s.endDate)+1 > maxAllDayDays:
				f.add("endDate", fmt.Sprintf("An all-day appointment is at most %d days", maxAllDayDays))
			}
		}
	} else {
		if startDate != nil || endDate != nil {
			f.add("startDate", "A timed appointment has instants, not dates")
		}
		if startsAt == nil {
			f.add("startsAt", "startsAt is required")
		}
		if endsAt == nil {
			f.add("endsAt", "endsAt is required")
		}
		if startsAt != nil && endsAt != nil {
			s.startsAt, s.endsAt = startsAt.UTC().Truncate(time.Second), endsAt.UTC().Truncate(time.Second)
			switch {
			case !s.endsAt.After(s.startsAt):
				f.add("endsAt", "endsAt must be after startsAt")
			case s.endsAt.Sub(s.startsAt) > maxTimedSpan:
				f.add("endsAt", "A timed appointment is at most 31 days")
			}
		}
	}
	return s, len(f.errs) == before
}

// readAppointment holds body to every rule of D2 and D3 for an appointment
// owner makes or replaces. It reads the zone's second check from the
// database and the attendees from the directory, so it runs before any
// transaction; it answers the input, the names of the owner and every
// attendee for the response, and the field errors.
func (s *server) readAppointment(ctx context.Context, body gen.AppointmentWrite, owner uuid.UUID) (appointmentInput, map[uuid.UUID]string, fieldErrors, error) {
	var f fieldErrors
	in := appointmentInput{attendees: []uuid.UUID{}, customerID: body.CustomerId, projectID: body.ProjectId}

	in.title = strings.TrimSpace(body.Title)
	switch {
	case in.title == "":
		f.add("title", "A title is required")
	case utf8.RuneCountInString(in.title) > maxTitle:
		f.add("title", fmt.Sprintf("A title is at most %d characters", maxTitle))
	}
	var msg string
	in.description, msg = optionalText(body.Description, maxDescription, "A description")
	f.add("description", msg)
	in.location, msg = optionalText(body.Location, maxLocation, "A location")
	f.add("location", msg)

	in.zone = strings.TrimSpace(body.TimeZone)
	loc, msg, err := s.zone(ctx, in.zone)
	if err != nil {
		return in, nil, f, err
	}
	f.add("timeZone", msg)

	var whenOK bool
	in.when, whenOK = readSpan(body.AllDay, body.StartDate, body.EndDate, body.StartsAt, body.EndsAt, &f)

	in.visibility, in.transparency = visibilityShared, transparencyBusy
	if body.Visibility != nil {
		if !body.Visibility.Valid() {
			f.add("visibility", "visibility is shared or private")
		}
		in.visibility = string(*body.Visibility)
	}
	if body.Transparency != nil {
		if !body.Transparency.Valid() {
			f.add("transparency", "transparency is busy or free")
		}
		in.transparency = string(*body.Transparency)
	}

	if body.Recurrence != nil {
		ruleLoc := loc
		if ruleLoc == nil {
			ruleLoc = time.UTC
		}
		r, msg := parseRule(body.Recurrence.Rule, ruleLoc)
		if msg == "" && whenOK && loc != nil {
			msg, err = in.seriesFrom(r, loc)
			if err != nil {
				return in, nil, f, err
			}
		}
		f.add("recurrence.rule", msg)
	}

	names, err := s.readAttendees(ctx, body.Attendees, owner, &in, &f)
	if err != nil {
		return in, nil, f, err
	}
	return in, names, f, nil
}

// seriesFrom fills in the rule's canonical form and its last day, or answers
// why the rule does not fit the appointment's start: an UNTIL before it or
// more than five years after it, or a rule that never produces an occurrence.
func (in *appointmentInput) seriesFrom(r rule, loc *time.Location) (string, error) {
	sr, err := newSeries(r, in.when, loc)
	if err != nil {
		return "", err
	}
	if msg := r.untilMessage(sr.startDay()); msg != "" {
		return msg, nil
	}
	if r.bounded() {
		last, ok := sr.lastDate()
		if !ok {
			return "The rule produces no occurrence from the appointment's start", nil
		}
		in.lastDate = pgDate(last)
	}
	canonical := r.String()
	in.rule = &canonical
	return "", nil
}

// readAttendees holds the attendee list to D3: at most 50, the owner never
// among them, each an active user in the directory, a repeated id kept once.
// An element is refused by its index in the request. It answers the names of
// the owner and of every attendee, from one directory read.
func (s *server) readAttendees(ctx context.Context, raw *[]openapi_types.UUID, owner uuid.UUID, in *appointmentInput, f *fieldErrors) (map[uuid.UUID]string, error) {
	var list []uuid.UUID
	if raw != nil {
		list = *raw
	}
	if len(list) > maxAttendees {
		f.add("attendees", fmt.Sprintf("An appointment has at most %d attendees", maxAttendees))
		list = nil
	}
	index := map[uuid.UUID]int{}
	for i, id := range list {
		if id == owner {
			f.add(fmt.Sprintf("attendees[%d]", i), "The owner is not one of the attendees")
			continue
		}
		if _, seen := index[id]; !seen {
			index[id] = i
			in.attendees = append(in.attendees, id)
		}
	}
	found, err := s.usersBatch(ctx, append([]uuid.UUID{owner}, in.attendees...))
	if err != nil {
		return nil, fmt.Errorf("calendar: read the attendees: %w", err)
	}
	names := map[uuid.UUID]string{}
	active := map[uuid.UUID]bool{}
	for _, u := range found {
		names[u.ID] = u.DisplayName
		active[u.ID] = u.Active
	}
	for _, id := range in.attendees {
		if !active[id] {
			f.add(fmt.Sprintf("attendees[%d]", index[id]), "No active user has this id")
		}
	}
	return names, nil
}

// appointmentLinks is the customer and the project an appointment was made
// from, named when their modules are wired.
type appointmentLinks struct {
	customer *gen.AppointmentCustomer
	project  *gen.AppointmentProject
}

// linksFor names the links through the customer and project directories —
// before any transaction. An id neither resolves is answered bare: the link
// is a courtesy, never refused (calendar design D2). A directory that fails
// fails the request, Customer 360's rule.
func (s *server) linksFor(ctx context.Context, customerID, projectID *int32) (appointmentLinks, error) {
	var out appointmentLinks
	if customerID != nil {
		out.customer = &gen.AppointmentCustomer{Id: *customerID}
		c, err := s.directoryCustomer(ctx, *customerID)
		if err != nil {
			return out, fmt.Errorf("calendar: name customer %d: %w", *customerID, err)
		}
		if c != nil {
			out.customer.Name = &c.Name
		}
	}
	if projectID != nil {
		out.project = &gen.AppointmentProject{Id: *projectID}
		p, err := s.projectsProject(ctx, *projectID)
		if err != nil {
			return out, fmt.Errorf("calendar: name project %d: %w", *projectID, err)
		}
		if p != nil {
			out.project.Code, out.project.Name = &p.Code, &p.Name
		}
	}
	return out, nil
}

// namesFor is the display name of the owner and every attendee of an
// appointment, read after its transaction or with none open.
func (s *server) namesFor(ctx context.Context, a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee) (map[uuid.UUID]string, error) {
	ids := []uuid.UUID{a.OwnerUserID}
	for _, at := range atts {
		ids = append(ids, at.UserID)
	}
	found, err := s.usersBatch(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("calendar: name appointment %d's people: %w", a.ID, err)
	}
	names := make(map[uuid.UUID]string, len(found))
	for _, u := range found {
		names[u.ID] = u.DisplayName
	}
	return names, nil
}

// respond is the appointment's response shape for me, with its people and
// its links named outside any transaction.
func (s *server) respond(ctx context.Context, a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee, me uuid.UUID) (gen.Appointment, error) {
	names, err := s.namesFor(ctx, a, atts)
	if err != nil {
		return gen.Appointment{}, err
	}
	links, err := s.linksFor(ctx, a.CustomerID, a.ProjectID)
	if err != nil {
		return gen.Appointment{}, err
	}
	return appointmentOut(a, atts, me, names, links), nil
}

// appointmentOut is an appointment on the wire, as me sees it: myStatus is
// theirs when they attend it, absent when they own it.
func appointmentOut(a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee, me uuid.UUID, names map[uuid.UUID]string, links appointmentLinks) gen.Appointment {
	out := gen.Appointment{
		Id:           a.ID,
		Title:        a.Title,
		Description:  a.Description,
		Location:     a.Location,
		AllDay:       a.AllDay,
		StartDate:    apiDatePtr(a.StartDate),
		EndDate:      apiDatePtr(a.EndDate),
		StartsAt:     utcPtr(a.StartsAt),
		EndsAt:       utcPtr(a.EndsAt),
		TimeZone:     a.TimeZone,
		Visibility:   gen.AppointmentVisibility(a.Visibility),
		Transparency: gen.AppointmentTransparency(a.Transparency),
		Customer:     links.customer,
		Project:      links.project,
		Owner:        gen.CalendarUserRef{Id: a.OwnerUserID, DisplayName: names[a.OwnerUserID]},
		Attendees:    make([]gen.AppointmentAttendee, 0, len(atts)),
		Revision:     a.Revision,
		CreatedAt:    a.CreatedAt.UTC(),
		UpdatedAt:    a.UpdatedAt.UTC(),
	}
	if a.Rrule != nil {
		out.Recurrence = &gen.AppointmentRecurrence{Rule: *a.Rrule, LastDate: apiDatePtr(a.SeriesLastDate)}
	}
	for _, at := range atts {
		status := gen.AppointmentAttendeeStatus(at.Status)
		out.Attendees = append(out.Attendees, gen.AppointmentAttendee{
			User:        gen.CalendarUserRef{Id: at.UserID, DisplayName: names[at.UserID]},
			Status:      status,
			RespondedAt: utcPtr(at.RespondedAt),
		})
		if at.UserID == me {
			out.MyStatus = &status
		}
	}
	return out
}

// lockOwned takes the appointment's row lock and answers it when me owns it;
// anything else is the 404 an unknown id gets.
func lockOwned(ctx context.Context, txq *store.Queries, id int64, me uuid.UUID) (store.CalendarAppointment, error) {
	a, err := txq.GetAppointmentForUpdate(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return a, errNotFound
	case err != nil:
		return a, fmt.Errorf("calendar: lock appointment %d: %w", id, err)
	case a.OwnerUserID != me:
		return a, errNotFound
	}
	return a, nil
}

// PostCalendarAppointments Create an appointment
// (POST /api/v1/calendar/appointments)
func (s *server) PostCalendarAppointments(ctx context.Context, req gen.PostCalendarAppointmentsRequestObject) (gen.PostCalendarAppointmentsResponseObject, error) {
	var body gen.AppointmentWrite
	if req.Body != nil {
		body = *req.Body
	}
	me := callerID(ctx)
	in, names, f, err := s.readAppointment(ctx, body, me)
	if err != nil {
		return nil, err
	}
	if f.failed() {
		return gen.PostCalendarAppointments400ApplicationProblemPlusJSONResponse(invalid(invalidAppointmentTitle, f)), nil
	}
	links, err := s.linksFor(ctx, in.customerID, in.projectID)
	if err != nil {
		return nil, err
	}
	var (
		row  store.CalendarAppointment
		atts []store.CalendarAppointmentAttendee
	)
	now := s.deps.Clock()
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		var err error
		row, err = txq.InsertAppointment(ctx, store.InsertAppointmentParams{
			OwnerUserID: me, Title: in.title, Description: in.description, Location: in.location,
			AllDay: in.when.allDay, StartDate: spanStartDate(in.when), EndDate: spanEndDate(in.when),
			StartsAt: spanStartsAt(in.when), EndsAt: spanEndsAt(in.when), TimeZone: in.zone,
			Visibility: in.visibility, Transparency: in.transparency, Rrule: in.rule, SeriesLastDate: in.lastDate,
			CustomerID: in.customerID, ProjectID: in.projectID,
			// The UID the invitations and the feed will carry (B, C), stable
			// from the first row.
			IcalUid: uuid.NewString() + "@vantigo", Now: now,
		})
		if err != nil {
			return fmt.Errorf("calendar: insert an appointment: %w", err)
		}
		if len(in.attendees) > 0 {
			if err := txq.InsertAttendees(ctx, store.InsertAttendeesParams{AppointmentID: row.ID, Now: now, UserIds: in.attendees}); err != nil {
				return fmt.Errorf("calendar: insert appointment %d's attendees: %w", row.ID, err)
			}
		}
		atts, err = txq.ListAttendees(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("calendar: read appointment %d's attendees: %w", row.ID, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.PostCalendarAppointments201JSONResponse(appointmentOut(row, atts, me, names, links)), nil
}

// GetCalendarAppointmentsById Get an appointment
// (GET /api/v1/calendar/appointments/{id})
func (s *server) GetCalendarAppointmentsById(ctx context.Context, req gen.GetCalendarAppointmentsByIdRequestObject) (gen.GetCalendarAppointmentsByIdResponseObject, error) {
	me := callerID(ctx)
	q := s.queries()
	row, err := q.GetAppointment(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCalendarAppointmentsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d: %w", req.Id, err)
	}
	atts, err := q.ListAttendees(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d's attendees: %w", req.Id, err)
	}
	if !mayRead(row, atts, me) {
		return gen.GetCalendarAppointmentsById404Response{}, nil
	}
	out, err := s.respond(ctx, row, atts, me)
	if err != nil {
		return nil, err
	}
	return gen.GetCalendarAppointmentsById200JSONResponse(out), nil
}

// mayRead reports whether me owns or attends the appointment.
func mayRead(a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee, me uuid.UUID) bool {
	if a.OwnerUserID == me {
		return true
	}
	for _, at := range atts {
		if at.UserID == me {
			return true
		}
	}
	return false
}

// PutCalendarAppointmentsById Change an appointment
// (PUT /api/v1/calendar/appointments/{id})
//
// A change of when — the dates or instants, the rule or the zone — or of where
// bumps the sequence, iCalendar's count of the changes an invitation must
// carry. A change of when also puts every attendee back to needs-action, since
// what they answered is no longer what is asked, and drops every exception,
// since the days they named may no longer exist (calendar design D3, D4).
func (s *server) PutCalendarAppointmentsById(ctx context.Context, req gen.PutCalendarAppointmentsByIdRequestObject) (gen.PutCalendarAppointmentsByIdResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutCalendarAppointmentsById400ApplicationProblemPlusJSONResponse(invalid(invalidAppointmentTitle, badIfMatch())), nil
	}
	var body gen.AppointmentWrite
	if req.Body != nil {
		body = *req.Body
	}
	me := callerID(ctx)
	in, names, f, err := s.readAppointment(ctx, body, me)
	if err != nil {
		return nil, err
	}
	if f.failed() {
		return gen.PutCalendarAppointmentsById400ApplicationProblemPlusJSONResponse(invalid(invalidAppointmentTitle, f)), nil
	}
	links, err := s.linksFor(ctx, in.customerID, in.projectID)
	if err != nil {
		return nil, err
	}
	var (
		row  store.CalendarAppointment
		atts []store.CalendarAppointmentAttendee
	)
	now := s.deps.Clock()
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		cur, err := lockOwned(ctx, txq, req.Id, me)
		if err != nil {
			return err
		}
		if cur.Revision != revision {
			return refuse(http.StatusConflict, revisionMismatch(cur.Revision, revision))
		}
		whenChanged := spanChanged(spanOf(cur), in.when) || !sameText(cur.Rrule, in.rule) || cur.TimeZone != in.zone
		whereChanged := !sameText(cur.Location, in.location)
		row, err = txq.UpdateAppointment(ctx, store.UpdateAppointmentParams{
			ID: cur.ID, Title: in.title, Description: in.description, Location: in.location,
			AllDay: in.when.allDay, StartDate: spanStartDate(in.when), EndDate: spanEndDate(in.when),
			StartsAt: spanStartsAt(in.when), EndsAt: spanEndsAt(in.when), TimeZone: in.zone,
			Visibility: in.visibility, Transparency: in.transparency, Rrule: in.rule, SeriesLastDate: in.lastDate,
			CustomerID: in.customerID, ProjectID: in.projectID,
			BumpSequence: whenChanged || whereChanged, Now: now,
		})
		if err != nil {
			return fmt.Errorf("calendar: update appointment %d: %w", cur.ID, err)
		}
		if whenChanged {
			if err := txq.ResetAttendeeStatuses(ctx, cur.ID); err != nil {
				return fmt.Errorf("calendar: reset appointment %d's answers: %w", cur.ID, err)
			}
			if err := txq.DeleteExceptions(ctx, cur.ID); err != nil {
				return fmt.Errorf("calendar: drop appointment %d's exceptions: %w", cur.ID, err)
			}
		}
		if err := txq.DeleteAttendeesExcept(ctx, store.DeleteAttendeesExceptParams{AppointmentID: cur.ID, UserIds: in.attendees}); err != nil {
			return fmt.Errorf("calendar: remove appointment %d's attendees: %w", cur.ID, err)
		}
		if len(in.attendees) > 0 {
			if err := txq.InsertAttendees(ctx, store.InsertAttendeesParams{AppointmentID: cur.ID, Now: now, UserIds: in.attendees}); err != nil {
				return fmt.Errorf("calendar: add appointment %d's attendees: %w", cur.ID, err)
			}
		}
		atts, err = txq.ListAttendees(ctx, cur.ID)
		if err != nil {
			return fmt.Errorf("calendar: read appointment %d's attendees: %w", cur.ID, err)
		}
		return nil
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PutCalendarAppointmentsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PutCalendarAppointmentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutCalendarAppointmentsById200JSONResponse(appointmentOut(row, atts, me, names, links)), nil
}

// DeleteCalendarAppointmentsById Delete an appointment
// (DELETE /api/v1/calendar/appointments/{id})
//
// The whole series goes, its attendees and exceptions with it (ON DELETE
// CASCADE): nobody outside the company has been told of it in A, so no
// cancelled state is kept (calendar design D3).
func (s *server) DeleteCalendarAppointmentsById(ctx context.Context, req gen.DeleteCalendarAppointmentsByIdRequestObject) (gen.DeleteCalendarAppointmentsByIdResponseObject, error) {
	me := callerID(ctx)
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if _, err := lockOwned(ctx, txq, req.Id, me); err != nil {
			return err
		}
		if _, err := txq.DeleteAppointment(ctx, req.Id); err != nil {
			return fmt.Errorf("calendar: delete appointment %d: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.DeleteCalendarAppointmentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteCalendarAppointmentsById204Response{}, nil
}

// spanChanged reports whether two times differ — the kind, the dates or the
// instants.
func spanChanged(a, b span) bool {
	if a.allDay != b.allDay {
		return true
	}
	if a.allDay {
		return !a.startDate.Equal(b.startDate) || !a.endDate.Equal(b.endDate)
	}
	return !a.startsAt.Equal(b.startsAt) || !a.endsAt.Equal(b.endsAt)
}

// The columns a span fills: its dates when all-day, its instants when timed,
// NULL for the pair it does not use.
func spanStartDate(s span) pgtype.Date {
	if !s.allDay {
		return pgtype.Date{}
	}
	return pgDate(s.startDate)
}

func spanEndDate(s span) pgtype.Date {
	if !s.allDay {
		return pgtype.Date{}
	}
	return pgDate(s.endDate)
}

func spanStartsAt(s span) *time.Time {
	if s.allDay {
		return nil
	}
	v := s.startsAt
	return &v
}

func spanEndsAt(s span) *time.Time {
	if s.allDay {
		return nil
	}
	v := s.endsAt
	return &v
}
