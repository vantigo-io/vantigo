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
	"github.com/vantigo-io/vantigo/server/internal/contracts"
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
	attendees    attendeeSet
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
// database and the attendees from the directories, so it runs before any
// transaction; it answers the input, the names of the owner and every
// attendee for the response, and the field errors.
//
// current is the appointment's attendees as they stand, on a PUT by its
// owner: one of them is accepted even if the directory would refuse them
// now, so a colleague who left or a contact erased does not lock the owner
// out of their own appointment (readAttendees).
func (s *server) readAppointment(ctx context.Context, body gen.AppointmentWrite, owner uuid.UUID, current []store.CalendarAppointmentAttendee) (appointmentInput, people, fieldErrors, error) {
	var f fieldErrors
	in := appointmentInput{customerID: body.CustomerId, projectID: body.ProjectId}

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
		return in, people{}, f, err
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
				return in, people{}, f, err
			}
		}
		f.add("recurrence.rule", msg)
	}

	ppl, err := s.readAttendees(ctx, body.Attendees, owner, current, &in, &f)
	if err != nil {
		return in, people{}, f, err
	}
	return in, ppl, f, nil
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
	if !sr.produces(sr.startDay()) {
		return "The rule must repeat on the day the appointment starts, which is its first occurrence", nil
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

// appointmentLinks is the customer and the project an appointment was made
// from, named when their modules are wired.
type appointmentLinks struct {
	customer *gen.AppointmentCustomer
	project  *gen.AppointmentProject
}

// linksFor names the links for the viewer in ctx through the customer and
// project directories — before any transaction. Each name is answered only to
// a viewer who may read it in its own module: the customer's to a holder of
// customers:view, the project's code and name to a holder of
// projects:view-all or projects:manage-all, or of a role on that project —
// otherwise the bare id, so an appointment cannot be made to name what its
// viewer may not see. An id neither resolves is answered bare too: the link
// is a courtesy, never refused (calendar design D2). A directory that fails
// fails the request, Customer 360's rule.
func (s *server) linksFor(ctx context.Context, customerID, projectID *int32) (appointmentLinks, error) {
	var out appointmentLinks
	if customerID != nil {
		out.customer = &gen.AppointmentCustomer{Id: *customerID}
		if s.has(ctx, "customers:view") {
			c, err := s.directoryCustomer(ctx, *customerID)
			if err != nil {
				return out, fmt.Errorf("calendar: name customer %d: %w", *customerID, err)
			}
			if c != nil {
				out.customer.Name = &c.Name
			}
		}
	}
	if projectID != nil {
		out.project = &gen.AppointmentProject{Id: *projectID}
		may, err := s.maySeeProject(ctx, *projectID)
		if err != nil {
			return out, err
		}
		if may {
			p, err := s.projectsProject(ctx, *projectID)
			if err != nil {
				return out, fmt.Errorf("calendar: name project %d: %w", *projectID, err)
			}
			if p != nil {
				out.project.Code, out.project.Name = &p.Code, &p.Name
			}
		}
	}
	return out, nil
}

// maySeeProject is Projects' visibility rule for the viewer in ctx, asked
// of its own contract: everyone's projects with projects:view-all or
// projects:manage-all, otherwise the ones they hold a role on.
func (s *server) maySeeProject(ctx context.Context, id int32) (bool, error) {
	if s.has(ctx, "projects:view-all") || s.has(ctx, "projects:manage-all") {
		return true, nil
	}
	role, err := s.projectsRole(ctx, id, callerID(ctx))
	if err != nil {
		return false, fmt.Errorf("calendar: read the caller's role on project %d: %w", id, err)
	}
	return role != "", nil
}

// peopleFor is the display name of the owner and every user attendee of an
// appointment, and every contact attendee as the customer directory answers
// it, read after its transaction or with none open.
func (s *server) peopleFor(ctx context.Context, a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee) (people, error) {
	ids := []uuid.UUID{a.OwnerUserID}
	ppl := people{users: map[uuid.UUID]string{}, contacts: map[int32]*contracts.ContactEntry{}}
	for _, at := range atts {
		switch {
		case at.UserID != nil:
			ids = append(ids, *at.UserID)
		case at.ContactID != nil:
			if _, done := ppl.contacts[*at.ContactID]; done {
				continue
			}
			c, err := s.directoryContact(ctx, *at.ContactID)
			if err != nil {
				return people{}, fmt.Errorf("calendar: name appointment %d's contact %d: %w", a.ID, *at.ContactID, err)
			}
			ppl.contacts[*at.ContactID] = c
		}
	}
	found, err := s.usersBatch(ctx, ids)
	if err != nil {
		return people{}, fmt.Errorf("calendar: name appointment %d's people: %w", a.ID, err)
	}
	for _, u := range found {
		ppl.users[u.ID] = u.DisplayName
	}
	return ppl, nil
}

// respond is the appointment's response shape for me, with its people and
// its links named outside any transaction.
func (s *server) respond(ctx context.Context, a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee, excs []store.CalendarAppointmentException, me uuid.UUID) (gen.Appointment, error) {
	ppl, err := s.peopleFor(ctx, a, atts)
	if err != nil {
		return gen.Appointment{}, err
	}
	links, err := s.linksFor(ctx, a.CustomerID, a.ProjectID)
	if err != nil {
		return gen.Appointment{}, err
	}
	return appointmentOut(a, atts, excs, me, ppl, links), nil
}

// exceptionsOf is a recurring appointment's exceptions by date, none for a
// single one.
func exceptionsOf(ctx context.Context, q *store.Queries, a store.CalendarAppointment) ([]store.CalendarAppointmentException, error) {
	if a.Rrule == nil {
		return nil, nil
	}
	excs, err := q.ListExceptions(ctx, []int64{a.ID})
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d's exceptions: %w", a.ID, err)
	}
	return excs, nil
}

// occurrenceOut is one exception on the wire.
func occurrenceOut(e store.CalendarAppointmentException) gen.AppointmentOccurrence {
	date, _ := fromPgDate(e.OccurrenceDate)
	return gen.AppointmentOccurrence{
		OccurrenceDate: apiDate(date),
		Cancelled:      e.Cancelled,
		StartDate:      apiDatePtr(e.StartDate),
		EndDate:        apiDatePtr(e.EndDate),
		StartsAt:       utcPtr(e.StartsAt),
		EndsAt:         utcPtr(e.EndsAt),
	}
}

// appointmentOut is an appointment on the wire, as me sees it: myStatus is
// theirs when they attend it, absent when they own it.
func appointmentOut(a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee, excs []store.CalendarAppointmentException, me uuid.UUID, ppl people, links appointmentLinks) gen.Appointment {
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
		Owner:        gen.CalendarUserRef{Id: a.OwnerUserID, DisplayName: ppl.users[a.OwnerUserID]},
		Attendees:    make([]gen.AppointmentAttendee, 0, len(atts)),
		Exceptions:   make([]gen.AppointmentOccurrence, 0, len(excs)),
		Revision:     a.Revision,
		CreatedAt:    a.CreatedAt.UTC(),
		UpdatedAt:    a.UpdatedAt.UTC(),
		CancelledAt:  utcPtr(a.CancelledAt),
	}
	if a.Rrule != nil {
		out.Recurrence = &gen.AppointmentRecurrence{Rule: *a.Rrule, LastDate: apiDatePtr(a.SeriesLastDate)}
	}
	for _, e := range excs {
		out.Exceptions = append(out.Exceptions, occurrenceOut(e))
	}
	for _, at := range atts {
		att := attendeeOut(at, ppl)
		if at.UserID != nil && *at.UserID == me {
			status := att.Status
			out.MyStatus = &status
		}
		out.Attendees = append(out.Attendees, att)
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

// lockWritable is lockOwned for a write that changes the appointment: a
// cancelled one is read-only, and every write to it is 409
// appointment_cancelled (calendar invitations design D6) — judged after the
// ownership, so a stranger still gets the bare 404.
func lockWritable(ctx context.Context, txq *store.Queries, id int64, me uuid.UUID) (store.CalendarAppointment, error) {
	a, err := lockOwned(ctx, txq, id, me)
	if err == nil && a.CancelledAt != nil {
		return a, errCancelled(a)
	}
	return a, err
}

// PostCalendarAppointments Create an appointment
// (POST /api/v1/calendar/appointments)
//
// After the commit, every outside attendee is sent a REQUEST and every
// colleague a notice that they are invited (calendar invitations design D3,
// D4); the answer shows the invitations as the sends left them.
func (s *server) PostCalendarAppointments(ctx context.Context, req gen.PostCalendarAppointmentsRequestObject) (gen.PostCalendarAppointmentsResponseObject, error) {
	var body gen.AppointmentWrite
	if req.Body != nil {
		body = *req.Body
	}
	me := callerID(ctx)
	saved, err := s.createAppointment(ctx, me, body, nil)
	if r, ok := asRefusal(err); ok {
		return gen.PostCalendarAppointments400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	links, err := s.linksFor(ctx, saved.row.CustomerID, saved.row.ProjectID)
	if err != nil {
		return nil, err
	}
	return gen.PostCalendarAppointments201JSONResponse(appointmentOut(saved.row, saved.atts, nil, me, saved.ppl, links)), nil
}

// savedAppointment is an appointment as a write left it: its row, its
// attendees as the invitations' sends recorded them, its exceptions and the
// names of its people.
type savedAppointment struct {
	row  store.CalendarAppointment
	atts []store.CalendarAppointmentAttendee
	excs []store.CalendarAppointmentException
	ppl  people
}

// createAppointment is the create of an appointment me owns from body — the
// screen's POST and the Outlook write-back's new event (calendar Outlook sync
// design O10.9) alike: held to every rule, inserted with its attendees under
// one transaction, inTx's statement (nil for none) run in that transaction on
// the new row, and, after the commit, every outside attendee sent a REQUEST
// and every colleague a notice. A body that breaks a rule is a 400 refusal.
func (s *server) createAppointment(ctx context.Context, me uuid.UUID, body gen.AppointmentWrite, inTx func(ctx context.Context, txq *store.Queries, row store.CalendarAppointment) error) (savedAppointment, error) {
	in, ppl, f, err := s.readAppointment(ctx, body, me, nil)
	if err != nil {
		return savedAppointment{}, err
	}
	if f.failed() {
		return savedAppointment{}, refuse(http.StatusBadRequest, invalid(invalidAppointmentTitle, f))
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
			// The UID the invitations and the feed carry (B, C), stable
			// from the first row.
			IcalUid: uuid.NewString() + "@vantigo", Now: now,
		})
		if err != nil {
			return fmt.Errorf("calendar: insert an appointment: %w", err)
		}
		if _, err := s.insertAttendees(ctx, txq, row.ID, in.attendees, nil, now); err != nil {
			return err
		}
		atts, err = txq.ListAttendees(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("calendar: read appointment %d's attendees: %w", row.ID, err)
		}
		if inTx != nil {
			if err := inTx(ctx, txq, row); err != nil {
				return err
			}
		}
		return s.nudgeMailboxes(ctx, txq, row.OwnerUserID, atts)
	})
	if err != nil {
		return savedAppointment{}, err
	}
	b := s.newBudget(ctx, afterCommitBudget)
	s.sendInvitations(b, invitationJob{appt: row, listed: atts, to: atts, method: methodRequest})
	s.notifyUsers(b, row, userAttendees(atts), noticeInvited)
	b.done()
	if atts, err = s.reread(ctx, row.ID, atts); err != nil {
		return savedAppointment{}, err
	}
	return savedAppointment{row: row, atts: atts, ppl: ppl}, nil
}

// reread is the appointment's attendees again after its invitations went,
// with what the sends recorded on them; atts as they were when none of them
// is mailed.
func (s *server) reread(ctx context.Context, id int64, atts []store.CalendarAppointmentAttendee) ([]store.CalendarAppointmentAttendee, error) {
	if len(outsideAttendees(atts)) == 0 || !s.mailAvailable() {
		return atts, nil
	}
	out, err := s.queries().ListAttendees(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d's attendees: %w", id, err)
	}
	return out, nil
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
	excs, err := exceptionsOf(ctx, q, row)
	if err != nil {
		return nil, err
	}
	out, err := s.respond(ctx, row, atts, excs, me)
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
		if at.UserID != nil && *at.UserID == me {
			return true
		}
	}
	return false
}

// PutCalendarAppointmentsById Change an appointment
// (PUT /api/v1/calendar/appointments/{id})
//
// A change of when (changeOf) or of where bumps the sequence, iCalendar's
// count of the changes an invitation must carry. A change of when also puts
// every attendee back to needs-action, since what they answered is no longer
// what is asked. A change of the start or the rule drops every exception,
// since the days they named may no longer exist (calendar design D3, D4); a
// change of the end alone keeps them. An attendee still listed is kept — their
// answer, their token and their invitation record with them — one who is not
// is dropped, and the new ones are added (calendar invitations design D1).
//
// After the commit (D3, D4): a bumped sequence sends every outside attendee a
// REQUEST and tells every kept colleague of the change; otherwise only the
// outside attendees added are sent one. Each colleague added is told they are
// invited, each outside attendee dropped is sent a CANCEL naming them alone,
// and each colleague dropped is told they were removed. A cancelled
// appointment is 409 appointment_cancelled.
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
	saved, err := s.replaceAppointment(ctx, me, req.Id, revision, body)
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PutCalendarAppointmentsById400ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusConflict:
			return gen.PutCalendarAppointmentsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PutCalendarAppointmentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	links, err := s.linksFor(ctx, saved.row.CustomerID, saved.row.ProjectID)
	if err != nil {
		return nil, err
	}
	return gen.PutCalendarAppointmentsById200JSONResponse(appointmentOut(saved.row, saved.atts, saved.excs, me, saved.ppl, links)), nil
}

// replaceAppointment replaces the appointment id me owns with body at
// revision — the screen's PUT and the Outlook write-back's edit (calendar
// Outlook sync design O10.7) alike, as the PUT's comment above tells. Its
// refusals: a body that breaks a rule is 400, an appointment me does not own
// 404, a revision moved on 409 revision_mismatch and a cancelled appointment
// 409 appointment_cancelled.
func (s *server) replaceAppointment(ctx context.Context, me uuid.UUID, id int64, revision int32, body gen.AppointmentWrite) (savedAppointment, error) {
	current, err := s.currentAttendees(ctx, id, me)
	if err != nil {
		return savedAppointment{}, err
	}
	in, ppl, f, err := s.readAppointment(ctx, body, me, current)
	if err != nil {
		return savedAppointment{}, err
	}
	if f.failed() {
		return savedAppointment{}, refuse(http.StatusBadRequest, invalid(invalidAppointmentTitle, f))
	}
	var (
		row            store.CalendarAppointment
		atts           []store.CalendarAppointmentAttendee
		excs           []store.CalendarAppointmentException
		added, dropped []store.CalendarAppointmentAttendee
		bumped         bool
	)
	now := s.deps.Clock()
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		cur, err := lockWritable(ctx, txq, id, me)
		if err != nil {
			return err
		}
		if cur.Revision != revision {
			return refuse(http.StatusConflict, revisionMismatch(cur.Revision, revision))
		}
		c := changeOf(cur, in)
		whenChanged, whereChanged := c.when, c.where
		bumped = whenChanged || whereChanged
		row, err = txq.UpdateAppointment(ctx, store.UpdateAppointmentParams{
			ID: cur.ID, Title: in.title, Description: in.description, Location: in.location,
			AllDay: in.when.allDay, StartDate: spanStartDate(in.when), EndDate: spanEndDate(in.when),
			StartsAt: spanStartsAt(in.when), EndsAt: spanEndsAt(in.when), TimeZone: in.zone,
			Visibility: in.visibility, Transparency: in.transparency, Rrule: in.rule, SeriesLastDate: in.lastDate,
			CustomerID: in.customerID, ProjectID: in.projectID,
			BumpSequence: bumped, Now: now,
		})
		if err != nil {
			return fmt.Errorf("calendar: update appointment %d: %w", cur.ID, err)
		}
		if whenChanged {
			if err := txq.ResetAttendeeStatuses(ctx, cur.ID); err != nil {
				return fmt.Errorf("calendar: reset appointment %d's answers: %w", cur.ID, err)
			}
		}
		if c.startOrRule {
			if err := txq.DeleteExceptions(ctx, cur.ID); err != nil {
				return fmt.Errorf("calendar: drop appointment %d's exceptions: %w", cur.ID, err)
			}
		}
		var kept map[int64]bool
		if dropped, kept, err = dropUnlisted(ctx, txq, cur.ID, in.attendees); err != nil {
			return err
		}
		if added, err = s.insertAttendees(ctx, txq, cur.ID, in.attendees, kept, now); err != nil {
			return err
		}
		atts, err = txq.ListAttendees(ctx, cur.ID)
		if err != nil {
			return fmt.Errorf("calendar: read appointment %d's attendees: %w", cur.ID, err)
		}
		if err := s.nudgeMailboxes(ctx, txq, cur.OwnerUserID, atts, dropped); err != nil {
			return err
		}
		excs, err = exceptionsOf(ctx, txq, row)
		return err
	})
	if err != nil {
		return savedAppointment{}, err
	}
	s.afterChange(ctx, row, atts, excs, added, dropped, bumped)
	if atts, err = s.reread(ctx, row.ID, atts); err != nil {
		return savedAppointment{}, err
	}
	return savedAppointment{row: row, atts: atts, excs: excs, ppl: ppl}, nil
}

// writeOf is the appointment a with its attendees atts as a body that
// replaces it unchanged — every attendee by their kind (a colleague by id, a
// contact by id, an address with its name), the rule, the customer and the
// project, the zone, the visibility, the transparency and the span — which
// the Outlook write-back changes the taken fields of (design O10.7).
func writeOf(a store.CalendarAppointment, atts []store.CalendarAppointmentAttendee) gen.AppointmentWrite {
	visibility := gen.AppointmentVisibility(a.Visibility)
	transparency := gen.AppointmentTransparency(a.Transparency)
	list := make([]gen.AttendeeWrite, 0, len(atts))
	for _, at := range atts {
		switch {
		case at.Kind == kindUser && at.UserID != nil:
			id := *at.UserID
			list = append(list, gen.AttendeeWrite{Kind: gen.User, UserId: &id})
		case at.Kind == kindContact && at.ContactID != nil:
			id := *at.ContactID
			list = append(list, gen.AttendeeWrite{Kind: gen.Contact, ContactId: &id})
		case at.Kind == kindEmail && at.Email != nil:
			list = append(list, gen.AttendeeWrite{Kind: gen.Email, Email: at.Email, Name: at.Name})
		}
	}
	body := gen.AppointmentWrite{
		Title: a.Title, Description: a.Description, Location: a.Location,
		AllDay: a.AllDay, StartDate: apiDatePtr(a.StartDate), EndDate: apiDatePtr(a.EndDate),
		StartsAt: utcPtr(a.StartsAt), EndsAt: utcPtr(a.EndsAt), TimeZone: a.TimeZone,
		Visibility: &visibility, Transparency: &transparency,
		CustomerId: a.CustomerID, ProjectId: a.ProjectID, Attendees: &list,
	}
	if a.Rrule != nil {
		body.Recurrence = &gen.AppointmentRecurrenceWrite{Rule: *a.Rrule}
	}
	return body
}

// afterChange is what a PUT that committed tells (D3's table, D4's notices),
// all of it under one budget: the CANCELs to the dropped first, so that when
// one address is both dropped and listed anew the invitation is the last mail
// it gets; then the REQUESTs; then the notices.
func (s *server) afterChange(ctx context.Context, row store.CalendarAppointment, atts []store.CalendarAppointmentAttendee, excs []store.CalendarAppointmentException, added, dropped []store.CalendarAppointmentAttendee, bumped bool) {
	b := s.newBudget(ctx, afterCommitBudget)
	defer b.done()
	addedIDs := map[int64]bool{}
	for _, at := range added {
		addedIDs[at.ID] = true
	}
	to := added
	if bumped {
		to = atts
	}
	s.sendInvitations(b, invitationJob{appt: row, excs: excs, listed: atts, to: dropped, method: methodCancel, dropped: true})
	s.sendInvitations(b, invitationJob{appt: row, excs: excs, listed: atts, to: to, method: methodRequest})

	var invited, changed []uuid.UUID
	for _, at := range atts {
		switch {
		case at.Kind != kindUser || at.UserID == nil:
		case addedIDs[at.ID]:
			invited = append(invited, *at.UserID)
		case bumped:
			changed = append(changed, *at.UserID)
		}
	}
	s.notifyUsers(b, row, invited, noticeInvited)
	s.notifyUsers(b, row, changed, noticeChanged)
	s.notifyUsers(b, row, userAttendees(dropped), noticeRemoved)
}

// change is what a PUT changes of an appointment that matters beyond its
// text.
type change struct {
	when        bool // the time, the rule, or the zone a series expands in
	where       bool // the location
	startOrRule bool // the start, the kind, the rule, or a series' zone
}

// changeOf is what in changes of cur. The zone is a change of when only for a
// series, whose wall-clock expansion it decides: a single appointment's
// instants or dates mean the same in any zone, so a zone-only change of one
// moves nothing.
func changeOf(cur store.CalendarAppointment, in appointmentInput) change {
	was, is := spanOf(cur), in.when
	ruleChanged := !sameText(cur.Rrule, in.rule)
	seriesZone := in.rule != nil && cur.TimeZone != in.zone
	var startMoved, endMoved bool
	switch {
	case was.allDay != is.allDay:
		startMoved, endMoved = true, true
	case is.allDay:
		startMoved, endMoved = !was.startDate.Equal(is.startDate), !was.endDate.Equal(is.endDate)
	default:
		startMoved, endMoved = !was.startsAt.Equal(is.startsAt), !was.endsAt.Equal(is.endsAt)
	}
	return change{
		when:        startMoved || endMoved || ruleChanged || seriesZone,
		where:       !sameText(cur.Location, in.location),
		startOrRule: startMoved || ruleChanged || seriesZone,
	}
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
