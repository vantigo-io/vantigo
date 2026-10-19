package calendar

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the colleague notices (calendar invitations design D4): a
// plain mail to a user, composed here in English and Norwegian and delivered
// by identity in the user's own language through UserDirectory.Notify, so
// this module never holds a colleague's address. A user attendee is told when
// invited — on a create, or added by a PUT — when the appointment changes
// time or place (a PUT that bumped the sequence), when it is cancelled or
// deleted, and when they are removed from it; the owner is told when an
// outside attendee answers through the link (rsvp.go). The body carries the
// link into the app and nothing else: no ICS and no native buttons for a
// colleague, by design. Every notice goes after the commit, never inside it,
// under the request's after-commit budget (invitations.go), and a notice that
// fails or that the budget cut off is logged and never fails the request.

// noticeKind is what a colleague notice tells.
type noticeKind int

const (
	noticeInvited noticeKind = iota
	noticeChanged
	noticeCancelled
	noticeRemoved
)

// notifyUsers tells each of users what happened to a, in their language,
// under b.
func (s *server) notifyUsers(b *budget, a store.CalendarAppointment, users []uuid.UUID, kind noticeKind) {
	if len(users) == 0 {
		return
	}
	if b.spent() {
		s.noticesCutOff(b, a, len(users))
		return
	}
	organiser, _ := s.organiserOf(b.ctx, a)
	a, organiser = cleanText(a), oneLine(organiser)
	texts := map[string]contracts.NoticeText{}
	for _, w := range []words{english, norwegian} {
		who := organiser
		if who == "" {
			who = w.theOrganiser
		}
		format := w.noticeInvited
		switch kind {
		case noticeChanged:
			format = w.noticeChanged
		case noticeCancelled:
			format = w.noticeCancelled
		case noticeRemoved:
			format = w.noticeRemoved
		}
		subject := fmt.Sprintf(format, who, a.Title, noticeWhenOf(w, a))
		texts[w.lang] = contracts.NoticeText{Subject: subject, Body: s.noticeBody(w, subject, a)}
	}
	for i, id := range users {
		if b.spent() {
			s.noticesCutOff(b, a, len(users)-i)
			return
		}
		s.notify(b, a, id, contracts.Notice{Texts: texts})
	}
}

// noticesCutOff logs the notices the budget left unsent.
func (s *server) noticesCutOff(b *budget, a store.CalendarAppointment, unsent int) {
	s.deps.Logger.WarnContext(b.records(), "calendar: notices not sent: the time for them ran out",
		"appointment_id", a.ID, "unsent", unsent)
}

// notifyAnswer tells the owner of a, under b, that an outside attendee
// answered status through the link: named who, or — when who is "" — "a
// contact" for a contact the directory names no one, never their address.
func (s *server) notifyAnswer(b *budget, a store.CalendarAppointment, who string, contact bool, status string) {
	if b.spent() {
		s.noticesCutOff(b, a, 1)
		return
	}
	a, who = cleanText(a), oneLine(who)
	texts := map[string]contracts.NoticeText{}
	for _, w := range []words{english, norwegian} {
		format := w.answerAccepted
		switch status {
		case "declined":
			format = w.answerDeclined
		case "tentative":
			format = w.answerTentative
		}
		name := who
		switch {
		case name != "":
		case contact:
			name = w.aContact
		default:
			name = w.anInvitee
		}
		subject := fmt.Sprintf(format, name, a.Title)
		texts[w.lang] = contracts.NoticeText{Subject: subject, Body: s.noticeBody(w, subject, a)}
	}
	s.notify(b, a, a.OwnerUserID, contracts.Notice{Texts: texts})
}

// noticeBody is a notice's sentence and the link to the appointment in the
// app, when the installation has an origin.
func (s *server) noticeBody(w words, sentence string, a store.CalendarAppointment) string {
	body := sentence + "\n"
	if link := s.appointmentLink(a); link != "" {
		body += "\n" + w.openInVantigo + " " + link + "\n"
	}
	return body
}

// appointmentLink is the appointment in the app — its first day's calendar
// with the appointment open — "" when the installation has no origin.
func (s *server) appointmentLink(a store.CalendarAppointment) string {
	cfg := s.deps.Config
	if cfg == nil || cfg.AppOrigin == "" {
		return ""
	}
	return cfg.AppOrigin + cfg.BasePath + "/calendar?date=" + startDay(a).Format("2006-01-02") +
		"&appointment=" + strconv.FormatInt(a.ID, 10)
}

// notify sends one notice under b, after the commit and outside every
// transaction; a failure is logged — Notify's error names no address — and
// goes no further.
func (s *server) notify(b *budget, a store.CalendarAppointment, id uuid.UUID, n contracts.Notice) {
	if err := s.usersNotify(b.ctx, id, n); err != nil {
		s.deps.Logger.WarnContext(b.records(), "calendar: a notice could not be sent",
			"appointment_id", a.ID, "user_id", id.String(), "error", err.Error())
	}
}
