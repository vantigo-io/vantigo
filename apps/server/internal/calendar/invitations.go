package calendar

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// This file is the invitation mail (calendar invitations design D3): one
// message per outside attendee, From the installation's SMTP_FROM with the
// owner's name, text and HTML in the owner's language and the iCalendar
// object (ics.go) as the third alternative and as an attachment. It goes
// after the write has committed, never inside it, through the module's one
// SMTP accessor (smtpSend), one after the other on a context detached from
// the request under one budget for the lot. Nothing is queued and nothing
// retries by itself: a send that fails is recorded on the attendee for the
// owner's Resend and logged with the address redacted.
//
// When it goes, and what (D3's table): a create sends REQUEST to every
// outside attendee; a PUT that bumped the sequence REQUEST to every outside
// attendee, kept or new, and one that did not REQUEST to the ones it added; a
// PUT CANCEL to each one it dropped, listing that one alone; an occurrence
// moved, cancelled or put back REQUEST to every outside attendee; a
// cancellation (cancellation.go) CANCEL to every outside attendee; and the
// resend REQUEST to the one named. When the mail driver is not smtp nothing
// is sent and nothing is marked.

// invitationBudget bounds one write's invitations, all of them together.
const invitationBudget = 30 * time.Second

// invitationJob is one write's invitations: what is sent, to whom, and whom
// every copy lists.
type invitationJob struct {
	appt store.CalendarAppointment
	excs []store.CalendarAppointmentException
	// listed is the appointment's attendees as they stand; its outside ones
	// are the ATTENDEE lines of every copy.
	listed []store.CalendarAppointmentAttendee
	// to is who is mailed, outside attendees only.
	to     []store.CalendarAppointmentAttendee
	method string
	// dropped is a CANCEL to attendees the write removed: each copy lists
	// its recipient alone, and no row is left to mark.
	dropped bool
}

// sendInvitations sends job's mails, and records each on its attendee: a
// success sets invited_at and invited_sequence and clears send_failed_at, a
// failure — a contact the directory no longer answers or that has no address
// among them — sets send_failed_at. It runs after the write committed; the
// request's cancellation does not reach the sends.
func (s *server) sendInvitations(ctx context.Context, job invitationJob) {
	to := outsideAttendees(job.to)
	if len(to) == 0 {
		return
	}
	logger := s.deps.Logger
	if !s.mailAvailable() {
		logger.InfoContext(ctx, "calendar: invitations not sent: the mail driver is not smtp",
			"appointment_id", job.appt.ID, "attendees", len(to))
		return
	}
	ctx = context.WithoutCancel(ctx)
	organiser, lang := s.organiserOf(ctx, job.appt)

	addresses := map[int64]icsAttendee{}
	for _, at := range append(outsideAttendees(job.listed), to...) {
		if _, done := addresses[at.ID]; done {
			continue
		}
		if a, ok := s.addressOf(ctx, at); ok {
			addresses[at.ID] = a
		}
	}
	var everyone []icsAttendee
	for _, at := range outsideAttendees(job.listed) {
		if a, ok := addresses[at.ID]; ok {
			everyone = append(everyone, a)
		}
	}

	budget, cancel := context.WithTimeout(ctx, invitationBudget)
	defer cancel()
	for i, at := range to {
		if budget.Err() != nil {
			logger.WarnContext(ctx, "calendar: invitations not sent: the time for them ran out",
				"appointment_id", job.appt.ID, "unsent", len(to)-i)
			for _, rest := range to[i:] {
				s.markFailed(ctx, job, rest)
			}
			return
		}
		recipient, ok := addresses[at.ID]
		if !ok {
			logger.InfoContext(ctx, "calendar: invitation not sent: the contact has no address the directory answers",
				"appointment_id", job.appt.ID, "attendee_id", at.ID)
			s.markFailed(ctx, job, at)
			continue
		}
		out, err := s.invitationMail(job, at, recipient, everyone, organiser, lang)
		if err == nil {
			err = s.smtpSend(budget, s.deps.Config.Mail, out)
		}
		if err != nil {
			redact := regexp.MustCompile("(?i)" + regexp.QuoteMeta(recipient.address))
			logger.WarnContext(ctx, "calendar: an invitation could not be sent",
				"appointment_id", job.appt.ID, "attendee_id", at.ID, "method", job.method,
				"error", redact.ReplaceAllLiteralString(err.Error(), "<recipient>"))
			s.markFailed(ctx, job, at)
			continue
		}
		if job.dropped {
			continue
		}
		if err := s.queries().MarkInvited(ctx, store.MarkInvitedParams{
			InvitedAt: s.deps.Clock(), InvitedSequence: job.appt.Sequence, ID: at.ID,
		}); err != nil {
			logger.ErrorContext(ctx, "calendar: an invitation was sent but could not be recorded",
				"appointment_id", job.appt.ID, "attendee_id", at.ID, "error", err.Error())
		}
	}
}

// markFailed records that at's invitation was not sent, for the owner's
// Resend; a dropped attendee has no row left to record it on.
func (s *server) markFailed(ctx context.Context, job invitationJob, at store.CalendarAppointmentAttendee) {
	if job.dropped {
		return
	}
	if err := s.queries().MarkSendFailed(ctx, store.MarkSendFailedParams{At: s.deps.Clock(), ID: at.ID}); err != nil {
		s.deps.Logger.ErrorContext(ctx, "calendar: a failed invitation could not be recorded",
			"appointment_id", job.appt.ID, "attendee_id", at.ID, "error", err.Error())
	}
}

// organiserOf is the owner's display name and the language they write in —
// Norwegian when they prefer nb, English otherwise (D3). A directory that
// fails names nobody, in English, and is logged.
func (s *server) organiserOf(ctx context.Context, a store.CalendarAppointment) (string, string) {
	u, err := s.usersUser(ctx, a.OwnerUserID)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "calendar: the organiser could not be read", "appointment_id", a.ID, "error", err.Error())
		return "", langEnglish
	}
	if u == nil {
		return "", langEnglish
	}
	return u.DisplayName, languageOf(u.PreferredLanguage)
}

// addressOf is an outside attendee's address and name: an email attendee's
// as typed, a contact's as the directory answers it now — ok is false for a
// contact it answers nothing for or that has no address.
func (s *server) addressOf(ctx context.Context, at store.CalendarAppointmentAttendee) (icsAttendee, bool) {
	switch {
	case at.Kind == kindEmail && at.Email != nil:
		out := icsAttendee{address: *at.Email, status: at.Status}
		if at.Name != nil {
			out.name = *at.Name
		}
		return out, true
	case at.Kind == kindContact && at.ContactID != nil:
		c, err := s.directoryContact(ctx, *at.ContactID)
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "calendar: a contact could not be read for an invitation",
				"attendee_id", at.ID, "error", err.Error())
			return icsAttendee{}, false
		}
		if c == nil || c.Email == nil || strings.TrimSpace(*c.Email) == "" {
			return icsAttendee{}, false
		}
		out := icsAttendee{address: strings.TrimSpace(*c.Email), status: at.Status}
		if name := contactName(c); name != nil {
			out.name = *name
		}
		return out, true
	}
	return icsAttendee{}, false
}

// invitationMail is the message to one outside attendee.
func (s *server) invitationMail(job invitationJob, at store.CalendarAppointmentAttendee, recipient icsAttendee, everyone []icsAttendee, organiser, lang string) (mail.Outbound, error) {
	link, err := s.rsvpLink(at)
	if err != nil {
		return mail.Outbound{}, err
	}
	listed := everyone
	if job.dropped {
		listed = []icsAttendee{recipient}
	}
	w := wordsFor(lang)
	body, err := buildICS(icsInput{
		method: job.method, appt: job.appt, excs: job.excs, attendees: listed,
		link: link, rsvpSentence: w.rsvpSentence,
		organiser: organiser, from: s.deps.Config.Mail.From, now: s.deps.Clock(),
	})
	if err != nil {
		return mail.Outbound{}, err
	}
	kind := mailInvitation
	switch {
	case job.method == methodCancel && job.dropped:
		kind = mailRemoved
	case job.method == methodCancel:
		kind = mailCancelled
	case at.InvitedAt != nil:
		kind = mailUpdated
	}
	subject, text, htmlBody := invitationTexts(w, kind, job.appt, organiser, link)
	return mail.Outbound{
		DisplayName: organiser,
		To:          []string{recipient.address},
		Subject:     subject,
		TextBody:    text,
		HTMLBody:    htmlBody,
		Calendar:    &mail.CalendarPart{Method: job.method, Body: body},
	}, nil
}

// The four kinds of invitation mail, by what they tell.
type mailKind int

const (
	mailInvitation mailKind = iota // the first REQUEST to this attendee
	mailUpdated                    // a later REQUEST to one already invited
	mailCancelled                  // the CANCEL of the appointment
	mailRemoved                    // the CANCEL to one dropped from it
)

// invitationTexts is one mail's subject, text and HTML in w's language: what
// happened, the title, when (in the appointment's zone, named), where, who is
// organising, the description, and on a REQUEST the sentence that asks for
// the answer by the link, then the link; last, on whose behalf the
// installation writes.
func invitationTexts(w words, kind mailKind, a store.CalendarAppointment, organiser, link string) (string, string, string) {
	if organiser == "" {
		organiser = w.theOrganiser
	}
	prefix, opening := w.invitation, w.invites
	switch kind {
	case mailUpdated:
		prefix, opening = w.updated, w.changed
	case mailCancelled:
		prefix, opening = w.cancelled, w.cancelledIt
	case mailRemoved:
		prefix, opening = w.cancelled, w.removedYou
	}
	subject := fmt.Sprintf("%s: %s · %s", prefix, a.Title, dayOf(w, a))
	when := whenOf(w, a) + " (" + a.TimeZone + ")"
	if a.Rrule != nil {
		when += ", " + w.repeating
	}
	type line struct{ label, value string }
	facts := []line{{w.when, when}}
	if a.Location != nil && *a.Location != "" {
		facts = append(facts, line{w.where, *a.Location})
	}
	facts = append(facts, line{w.organiser, organiser})
	asks := kind == mailInvitation || kind == mailUpdated

	var t strings.Builder
	fmt.Fprintf(&t, opening+"\n\n", organiser)
	t.WriteString(a.Title + "\n")
	for _, f := range facts {
		t.WriteString(f.label + ": " + f.value + "\n")
	}
	if a.Description != nil && strings.TrimSpace(*a.Description) != "" {
		t.WriteString("\n" + *a.Description + "\n")
	}
	if asks {
		t.WriteString("\n" + w.rsvpSentence + "\n" + link + "\n")
	}
	t.WriteString("\n" + fmt.Sprintf(w.behalf, organiser) + "\n")

	var h strings.Builder
	esc := html.EscapeString
	h.WriteString("<!doctype html><html><body>")
	h.WriteString("<p>" + esc(fmt.Sprintf(opening, organiser)) + "</p>")
	h.WriteString("<h2>" + esc(a.Title) + "</h2><p>")
	for i, f := range facts {
		if i > 0 {
			h.WriteString("<br>")
		}
		h.WriteString("<strong>" + esc(f.label) + ":</strong> " + esc(f.value))
	}
	h.WriteString("</p>")
	if a.Description != nil && strings.TrimSpace(*a.Description) != "" {
		h.WriteString("<p>" + strings.ReplaceAll(esc(*a.Description), "\n", "<br>") + "</p>")
	}
	if asks {
		h.WriteString("<p>" + esc(w.rsvpSentence) + "<br><a href=\"" + esc(link) + "\">" + esc(link) + "</a></p>")
	}
	h.WriteString("<p>" + esc(fmt.Sprintf(w.behalf, organiser)) + "</p></body></html>")
	return subject, t.String(), h.String()
}

// PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend Resend an invitation
// (POST /api/v1/calendar/appointments/{id}/attendees/{attendeeId}/resend)
//
// The owner sends one outside attendee their invitation again: a REQUEST
// with the current sequence and the same link the first carried. An
// appointment the caller does not own, an attendee not on it and a
// colleague, who is never mailed an invitation, are each the bare 404; a
// cancelled appointment is 409 appointment_cancelled; with no smtp driver,
// 503 invitations_unavailable. A send that fails is recorded on the
// attendee, not answered as an error.
func (s *server) PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend(ctx context.Context, req gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResendRequestObject) (gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResendResponseObject, error) {
	me := callerID(ctx)
	q := s.queries()
	a, err := q.GetAppointment(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && a.OwnerUserID != me) {
		return gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d: %w", req.Id, err)
	}
	at, err := q.GetAttendee(ctx, store.GetAttendeeParams{ID: req.AttendeeId, AppointmentID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !isOutside(at)) {
		return gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d's attendee %d: %w", req.Id, req.AttendeeId, err)
	}
	if a.CancelledAt != nil {
		return gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend409ApplicationProblemPlusJSONResponse(appointmentCancelled(*a.CancelledAt)), nil
	}
	if !s.mailAvailable() {
		return gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend503ApplicationProblemPlusJSONResponse(invitationsUnavailable()), nil
	}
	atts, err := q.ListAttendees(ctx, a.ID)
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d's attendees: %w", a.ID, err)
	}
	excs, err := exceptionsOf(ctx, q, a)
	if err != nil {
		return nil, err
	}
	s.sendInvitations(ctx, invitationJob{appt: a, excs: excs, listed: atts, to: []store.CalendarAppointmentAttendee{at}, method: methodRequest})
	if atts, err = q.ListAttendees(ctx, a.ID); err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d's attendees: %w", a.ID, err)
	}
	out, err := s.respond(ctx, a, atts, excs, me)
	if err != nil {
		return nil, err
	}
	return gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend200JSONResponse(out), nil
}
