package calendar_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

func TestRsvpRead_AnswersThePageForALiveTokenAnd404ForAnyOther(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "nb")
	create(t, w.owner, meeting(t, contactAttendee(olaID), emailAttendee(perMail, "")))
	msgs := w.h.mail.messages()
	anonymous := w.h.Client(t)

	var page gen.RsvpPage
	r := anonymous.Do(http.MethodPost, rsvpReadPath, map[string]any{"token": tokenOf(t, to(t, msgs, olaMail))})
	if r.Status != http.StatusOK {
		t.Fatalf("read = %d %s", r.Status, r.Body)
	}
	r.JSON(&page)
	if page.Title != "Kundemøte" || page.AllDay || page.StartsAt == nil || !page.StartsAt.Equal(at(t, "Europe/Oslo", "2026-11-02T09:00")) ||
		page.TimeZone != "Europe/Oslo" || page.Location == nil || *page.Location != "Storgata 1" ||
		page.Organiser.Name != ownerName || page.Attendee.Name != "Ola Hansen" || page.Status != gen.AppointmentAttendeeStatusNeedsAction ||
		page.Cancelled || page.Language != "nb" || page.Company != nil {
		t.Errorf("page = %+v", page)
	}
	if body := string(r.Body); strings.Contains(body, "Agenda") || strings.Contains(body, "@") || strings.Contains(body, perMail) {
		t.Errorf("the page carries the description, another attendee or an address: %s", body)
	}

	do(t, anonymous, http.MethodPost, rsvpReadPath, map[string]any{"token": tokenOf(t, to(t, msgs, perMail))}, http.StatusOK, &page)
	if page.Attendee.Name != "per" {
		t.Errorf("an address with no name is named %q, want its local part", page.Attendee.Name)
	}

	for _, token := range []string{"not-a-token", strings.Repeat("A", 43)} {
		if r := anonymous.Do(http.MethodPost, rsvpReadPath, map[string]any{"token": token}); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("read of %q = %d %s, want the bare 404", token, r.Status, r.Body)
		}
	}
	for _, body := range []map[string]any{{"token": ""}, {"token": "  "}} {
		fieldRefused(t, anonymous.Do(http.MethodPost, rsvpReadPath, body), "token")
		fieldRefused(t, anonymous.Do(http.MethodPost, rsvpPath, with(body, "status", "accepted")), "token")
	}
}

func TestRsvpRead_IsLimitedTo60ReadsPerClientIn10Minutes(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	create(t, w.owner, meeting(t, emailAttendee(perMail, "")))
	token := tokenOf(t, to(t, w.h.mail.messages(), perMail))
	anonymous := w.h.Client(t)
	for i := 0; i < 60; i++ {
		do(t, anonymous, http.MethodPost, rsvpReadPath, map[string]any{"token": token}, http.StatusOK, nil)
	}
	if r := anonymous.Do(http.MethodPost, rsvpReadPath, map[string]any{"token": token}); r.Status != http.StatusTooManyRequests {
		t.Errorf("the 61st read = %d, want 429", r.Status)
	}
	do(t, w.h.Client(t), http.MethodPost, rsvpReadPath, map[string]any{"token": token}, http.StatusOK, nil)
}

func TestRsvp_SetsTheStatusAndTellsTheOwner(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	a := create(t, w.owner, meeting(t, emailAttendee(perMail, "Per Olsen")))
	token := tokenOf(t, to(t, w.h.mail.messages(), perMail))
	anonymous := w.h.Client(t)

	var page gen.RsvpPage
	do(t, anonymous, http.MethodPost, rsvpPath, map[string]any{"token": token, "status": "accepted"}, http.StatusOK, &page)
	if page.Status != gen.AppointmentAttendeeStatusAccepted {
		t.Errorf("the page after answering = %+v", page)
	}
	var got gen.Appointment
	do(t, w.owner, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &got)
	per := attendeeOf(t, got, perMail)
	if per.Status != gen.AppointmentAttendeeStatusAccepted || per.RespondedAt == nil || !per.RespondedAt.Equal(w.h.Now()) {
		t.Errorf("the attendee after answering = %+v", per)
	}
	if got.Revision != a.Revision {
		t.Errorf("an answer moved the revision from %d to %d", a.Revision, got.Revision)
	}
	// The same answer again writes nothing and tells nobody.
	answeredAt := *per.RespondedAt
	w.h.Advance(time.Hour)
	do(t, anonymous, http.MethodPost, rsvpPath, map[string]any{"token": token, "status": "accepted"}, http.StatusOK, &page)
	do(t, w.owner, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &got)
	if at := attendeeOf(t, got, perMail); page.Status != gen.AppointmentAttendeeStatusAccepted || !at.RespondedAt.Equal(answeredAt) {
		t.Errorf("a repeated answer = %+v, respondedAt %v; want nothing written", page, at.RespondedAt)
	}
	notices := w.h.Notices()
	if len(notices) != 1 || notices[0].UserID != w.ownerID ||
		notices[0].Notice.Texts["en"].Subject != "Per Olsen accepted: Kundemøte" || notices[0].Notice.Texts["nb"].Subject != "Per Olsen takket ja: Kundemøte" {
		t.Errorf("notices = %+v, want the owner told", notices)
	}

	do(t, anonymous, http.MethodPost, rsvpPath, map[string]any{"token": token, "status": "tentative"}, http.StatusOK, &page)
	if page.Status != gen.AppointmentAttendeeStatusTentative {
		t.Errorf("a changed answer = %+v", page)
	}
	if n := w.h.Notices(); len(n) != 2 || n[1].Notice.Texts["en"].Subject != "Per Olsen tentatively accepted: Kundemøte" {
		t.Errorf("notices = %+v", n)
	}
	if r := anonymous.Do(http.MethodPost, rsvpPath, map[string]any{"token": "nobody", "status": "accepted"}); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("an answer by an unknown token = %d %s", r.Status, r.Body)
	}

	do(t, w.owner, http.MethodDelete, appointmentPath(a.Id), nil, http.StatusNoContent, nil)
	refused(t, anonymous.Do(http.MethodPost, rsvpPath, map[string]any{"token": token, "status": "declined"}), http.StatusConflict, "appointment_cancelled")
	do(t, anonymous, http.MethodPost, rsvpReadPath, map[string]any{"token": token}, http.StatusOK, &page)
	if !page.Cancelled || page.Status != gen.AppointmentAttendeeStatusTentative {
		t.Errorf("the cancelled appointment's page = %+v", page)
	}
}

func TestRsvp_IsLimitedTo30AnswersPerClientIn10Minutes(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	create(t, w.owner, meeting(t, emailAttendee(perMail, "")))
	token := tokenOf(t, to(t, w.h.mail.messages(), perMail))
	anonymous := w.h.Client(t)
	answers := []string{"accepted", "declined", "tentative"}
	for i := 0; i < 30; i++ {
		do(t, anonymous, http.MethodPost, rsvpPath, map[string]any{"token": token, "status": answers[i%3]}, http.StatusOK, nil)
	}
	if r := anonymous.Do(http.MethodPost, rsvpPath, map[string]any{"token": token, "status": "accepted"}); r.Status != http.StatusTooManyRequests {
		t.Errorf("the 31st answer = %d, want 429", r.Status)
	}
	do(t, w.h.Client(t), http.MethodPost, rsvpPath, map[string]any{"token": token, "status": "declined"}, http.StatusOK, nil)
}

func TestRsvp_TheOwnersNoticeNamesAContactWithNoNameAsAContactNeverByItsAddress(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	create(t, w.owner, meeting(t, contactAttendee(anonID), emailAttendee(perMail, "")))
	msgs := w.h.mail.messages()
	anonymous := w.h.Client(t)
	do(t, anonymous, http.MethodPost, rsvpPath, map[string]any{"token": tokenOf(t, to(t, msgs, anonMail)), "status": "accepted"}, http.StatusOK, nil)
	do(t, anonymous, http.MethodPost, rsvpPath, map[string]any{"token": tokenOf(t, to(t, msgs, perMail)), "status": "declined"}, http.StatusOK, nil)
	notices := w.h.Notices()
	if len(notices) != 2 {
		t.Fatalf("notices = %+v", notices)
	}
	if en, nb := notices[0].Notice.Texts["en"].Subject, notices[0].Notice.Texts["nb"].Subject; en != "A contact accepted: Kundemøte" || nb != "En kontakt takket ja: Kundemøte" {
		t.Errorf("a nameless contact's answer = %q / %q", en, nb)
	}
	if en := notices[1].Notice.Texts["en"].Subject; en != "per declined: Kundemøte" {
		t.Errorf("a nameless address's answer = %q, want its local part", en)
	}
}
