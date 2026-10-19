package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is the answer by web link (calendar invitations design D5), on
// the pay link's pattern: two anonymous operations, rate limited per client
// address (module.go's limits), that find the attendee by the hash of the
// token the page posts — never a GET with the token in an API path. Neither
// has a principal; neither answers the description, another attendee or an
// address. A blank token is a 400; a token nobody holds, an erased
// attendee's included, is the bare 404.

// invalidRsvpTitle is the title of a refused RSVP body.
const invalidRsvpTitle = "Invalid answer"

// blankToken is the field error of a body with no token.
func blankToken(token string) (fieldErrors, bool) {
	var f fieldErrors
	if strings.TrimSpace(token) == "" {
		f.add("token", "token is required")
	}
	return f, f.failed()
}

// PostCalendarRsvpRead Read an invitation for the RSVP page
// (POST /api/v1/calendar/rsvp/read)
func (s *server) PostCalendarRsvpRead(ctx context.Context, req gen.PostCalendarRsvpReadRequestObject) (gen.PostCalendarRsvpReadResponseObject, error) {
	var token string
	if req.Body != nil {
		token = req.Body.Token
	}
	if f, blank := blankToken(token); blank {
		return gen.PostCalendarRsvpRead400ApplicationProblemPlusJSONResponse(invalid(invalidRsvpTitle, f)), nil
	}
	row, err := s.queries().GetAttendeeByRsvpToken(ctx, tokenSHA256(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCalendarRsvpRead404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: find an RSVP token: %w", err)
	}
	page, err := s.rsvpPage(ctx, row.CalendarAppointment, row.CalendarAppointmentAttendee)
	if err != nil {
		return nil, err
	}
	return gen.PostCalendarRsvpRead200JSONResponse(page), nil
}

// PostCalendarRsvp Answer an invitation
// (POST /api/v1/calendar/rsvp)
//
// The invitee's answer sets their status and respondedAt under the
// appointment's lock, which neither its revision nor its sequence moves; a
// cancelled appointment is 409 appointment_cancelled. After the commit the
// owner is told (D4), under a budget of its own. The answer they already gave
// writes nothing and tells nobody: the page is answered as it stands. An
// attendee or an appointment erased between the lookup and the lock is the
// 404 of a token nobody holds.
func (s *server) PostCalendarRsvp(ctx context.Context, req gen.PostCalendarRsvpRequestObject) (gen.PostCalendarRsvpResponseObject, error) {
	var body gen.RsvpWrite
	if req.Body != nil {
		body = *req.Body
	}
	f, _ := blankToken(body.Token)
	if !body.Status.Valid() {
		f.add("status", "status is accepted, declined or tentative")
	}
	if f.failed() {
		return gen.PostCalendarRsvp400ApplicationProblemPlusJSONResponse(invalid(invalidRsvpTitle, f)), nil
	}
	var (
		appt    store.CalendarAppointment
		att     store.CalendarAppointmentAttendee
		changed bool
	)
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		found, err := txq.GetAttendeeByRsvpToken(ctx, tokenSHA256(body.Token))
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return fmt.Errorf("calendar: find an RSVP token: %w", err)
		}
		appt, err = txq.GetAppointmentForUpdate(ctx, found.CalendarAppointment.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return fmt.Errorf("calendar: lock appointment %d: %w", found.CalendarAppointment.ID, err)
		}
		if appt.CancelledAt != nil {
			return errCancelled(appt)
		}
		att = found.CalendarAppointmentAttendee
		if att.Status == string(body.Status) {
			return nil
		}
		att, err = txq.SetAttendeeStatusByID(ctx, store.SetAttendeeStatusByIDParams{
			ID: found.CalendarAppointmentAttendee.ID, Status: string(body.Status), Now: s.deps.Clock(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return fmt.Errorf("calendar: record attendee %d's answer: %w", found.CalendarAppointmentAttendee.ID, err)
		}
		changed = true
		return nil
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostCalendarRsvp409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostCalendarRsvp404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	page, err := s.rsvpPage(ctx, appt, att)
	if err != nil {
		return nil, err
	}
	if changed {
		b := s.newBudget(ctx, noticeBudget)
		defer b.done()
		who, named, err := s.answererName(b.ctx, att)
		if err != nil {
			s.deps.Logger.WarnContext(b.records(), "calendar: the invitee could not be named for the owner's notice",
				"appointment_id", appt.ID, "attendee_id", att.ID, "error", err.Error())
		}
		if !named {
			who = ""
		}
		s.notifyAnswer(b, appt, who, att.Kind == kindContact, att.Status)
	}
	return gen.PostCalendarRsvp200JSONResponse(page), nil
}

// rsvpPage is what the page shows the invitee of a (D5): the title, when —
// the first occurrence of a series — where, the organiser's name, the
// invitee's name and answer, whether it is cancelled, and the organiser's
// language, which the invitation was written in. company, the workspace's
// name, is left out: no contract this module consumes answers one.
func (s *server) rsvpPage(ctx context.Context, a store.CalendarAppointment, at store.CalendarAppointmentAttendee) (gen.RsvpPage, error) {
	organiser, lang := s.organiserOf(ctx, a)
	name, err := s.inviteeName(ctx, at)
	if err != nil {
		return gen.RsvpPage{}, err
	}
	return gen.RsvpPage{
		Title:     a.Title,
		AllDay:    a.AllDay,
		StartsAt:  utcPtr(a.StartsAt),
		EndsAt:    utcPtr(a.EndsAt),
		StartDate: apiDatePtr(a.StartDate),
		EndDate:   apiDatePtr(a.EndDate),
		TimeZone:  a.TimeZone,
		Location:  a.Location,
		Organiser: gen.RsvpPerson{Name: organiser},
		Attendee:  gen.RsvpPerson{Name: name},
		Status:    gen.AppointmentAttendeeStatus(at.Status),
		Cancelled: a.CancelledAt != nil,
		Language:  gen.RsvpPageLanguage(lang),
	}, nil
}

// inviteeName is the name the RSVP page greets an outside attendee by: a
// contact's as the directory answers it, an email attendee's as typed, else
// the address's local part — never the address itself.
func (s *server) inviteeName(ctx context.Context, at store.CalendarAppointmentAttendee) (string, error) {
	name, _, err := s.answererName(ctx, at)
	return name, err
}

// answererName is inviteeName, and whether it is a name the appointment holds
// — a contact's from the directory or an email attendee's as typed — rather
// than an address's local part. The owner's notice names a contact by the
// first alone, and says "a contact" otherwise; an email attendee falls back
// to the local part.
func (s *server) answererName(ctx context.Context, at store.CalendarAppointmentAttendee) (string, bool, error) {
	address := ""
	switch {
	case at.Kind == kindContact && at.ContactID != nil:
		c, err := s.directoryContact(ctx, *at.ContactID)
		if err != nil {
			return "", false, fmt.Errorf("calendar: read contact %d: %w", *at.ContactID, err)
		}
		if name := contactName(c); name != nil {
			return *name, true, nil
		}
		if c != nil && c.Email != nil {
			address = *c.Email
		}
	case at.Kind == kindEmail:
		if at.Name != nil && *at.Name != "" {
			return *at.Name, true, nil
		}
		if at.Email != nil {
			address = *at.Email
		}
	}
	local, _, _ := strings.Cut(address, "@")
	return local, at.Kind == kindEmail && local != "", nil
}
