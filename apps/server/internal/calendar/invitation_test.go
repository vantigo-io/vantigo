package calendar_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The contacts the invitations' directory answers, for customer 42: Ola with
// an address, Nina without one, Siri with one.
const (
	olaID     int32 = 7
	ninaID    int32 = 8
	siriID    int32 = 9
	customer  int32 = 42
	olaMail         = "ola@kunde.example"
	siriMail        = "siri@kunde.example"
	perMail         = "per@example.com"
	ownerName       = "Kari Nordmann"
)

// newDirectory is the customer directory of the invitations' tests.
func newDirectory() *contactsDirectory {
	ola, siri := olaMail, siriMail
	return &contactsDirectory{
		customers: map[int32]string{customer: "Fjord AS"},
		contacts: map[int32]contracts.ContactEntry{
			olaID:  {ID: olaID, FirstName: "Ola", LastName: "Hansen", Email: &ola},
			ninaID: {ID: ninaID, FirstName: "Nina", LastName: "Berg"},
			siriID: {ID: siriID, FirstName: "Siri", LastName: "Dahl", Email: &siri},
		},
		of: map[int32][]int32{customer: {olaID, ninaID, siriID}},
	}
}

// inviting is an installation with the contacts directory and an owner, Kari
// Nordmann, who writes in language and may name a customer's contacts.
type inviting struct {
	h       *harness
	dir     *contactsDirectory
	owner   *modtest.Client
	ownerID uuid.UUID
}

func newInviting(t *testing.T, language string, opts ...modtest.Option) inviting {
	t.Helper()
	dir := newDirectory()
	h := newHarness(t, append([]modtest.Option{modtest.WithDirectory(dir)}, opts...)...)
	owner, ownerID := signIn(t, h, "customers:contacts-view")
	nameUser(t, h, ownerID, ownerName, language)
	return inviting{h: h, dir: dir, owner: owner, ownerID: ownerID}
}

// nameUser sets a user's display name and preferred language ("" for none)
// in identity's tables.
func nameUser(t *testing.T, h *harness, id uuid.UUID, display, language string) {
	t.Helper()
	var lang any
	if language != "" {
		lang = language
	}
	h.Exec(t, `UPDATE identity.users SET display_name = $2, preferred_language = $3 WHERE id = $1`, id, display, lang)
}

func contactAttendee(id int32) map[string]any {
	return map[string]any{"kind": "contact", "contactId": id}
}

func emailAttendee(address, name string) map[string]any {
	a := map[string]any{"kind": "email", "email": address}
	if name != "" {
		a["name"] = name
	}
	return a
}

// attendees is the attendee lists given, as one.
func attendees(lists ...[]map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// meeting is the invitations' appointment: Kundemøte in Oslo on Monday 2
// November, 09:00 to 10:00.
func meeting(t *testing.T, list ...map[string]any) map[string]any {
	t.Helper()
	return with(timed(t, "Kundemøte", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"),
		"location", "Storgata 1", "description", "Agenda: the contract.", "attendees", list)
}

// put replaces appointment a as c, failing unless 200.
func put(t *testing.T, c *modtest.Client, a gen.Appointment, body map[string]any) gen.Appointment {
	t.Helper()
	var out gen.Appointment
	do(t, c, http.MethodPut, appointmentPath(a.Id), body, http.StatusOK, &out, ifMatch(a.Revision))
	return out
}

// unfold is an iCalendar object with its folded lines joined, so a test reads
// one property per line.
func unfold(ics string) string { return strings.ReplaceAll(ics, "\r\n ", "") }

// icsOf is a mail's iCalendar object, unfolded; it fails the test when the
// mail carries none.
func icsOf(t *testing.T, m mail.Outbound) string {
	t.Helper()
	if m.Calendar == nil {
		t.Fatalf("mail to %v carries no calendar part", m.To)
	}
	return unfold(m.Calendar.Body)
}

// lines is the object's lines that start with prefix.
func lines(ics, prefix string) []string {
	var out []string
	for _, l := range strings.Split(ics, "\r\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// to is the mail addressed to address, failing unless there is exactly one.
func to(t *testing.T, msgs []mail.Outbound, address string) mail.Outbound {
	t.Helper()
	var found []mail.Outbound
	for _, m := range msgs {
		if slices.Equal(m.To, []string{address}) {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d mails to %s among %d", len(found), address, len(msgs))
	}
	return found[0]
}

// recipients is every mail's one recipient, sorted.
func recipients(msgs []mail.Outbound) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.To...)
	}
	slices.Sort(out)
	return out
}

var tokenInLink = regexp.MustCompile(`/rsvp\?token=([A-Za-z0-9_-]+)`)

// tokenOf is the RSVP token a mail's text carries.
func tokenOf(t *testing.T, m mail.Outbound) string {
	t.Helper()
	match := tokenInLink.FindStringSubmatch(m.TextBody)
	if match == nil {
		t.Fatalf("no RSVP link in the mail to %v:\n%s", m.To, m.TextBody)
	}
	return match[1]
}

// attendeeOf is the attendee of a of kind whose contact id, address or user
// id is key.
func attendeeOf(t *testing.T, a gen.Appointment, key any) gen.AppointmentAttendee {
	t.Helper()
	for _, at := range a.Attendees {
		switch k := key.(type) {
		case int32:
			if at.Contact != nil && at.Contact.Id == k {
				return at
			}
		case string:
			if at.Email != nil && *at.Email == k {
				return at
			}
		case uuid.UUID:
			if at.User != nil && at.User.Id == k {
				return at
			}
		}
	}
	t.Fatalf("no attendee %v on appointment %d: %+v", key, a.Id, a.Attendees)
	return gen.AppointmentAttendee{}
}

func TestInvitations_ACreateMailsEachOutsideAttendeeTheIMIPLayoutInTheOwnersLanguage(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "nb")
	colleague := directoryUser(t, w.h, "Liv Lund", true)
	a := create(t, w.owner, meeting(t, attendees(userAttendees(colleague.String()),
		[]map[string]any{contactAttendee(olaID), emailAttendee("Per@Example.com", "Per Olsen")})...))

	msgs := w.h.mail.messages()
	if got := recipients(msgs); !slices.Equal(got, []string{olaMail, perMail}) {
		t.Fatalf("recipients = %v, want the contact and the address, and no colleague", got)
	}
	for _, m := range msgs {
		if m.DisplayName != ownerName || !strings.HasPrefix(m.Subject, "Invitasjon: Kundemøte · man. 2. nov.") {
			t.Errorf("mail to %v: from %q, subject %q", m.To, m.DisplayName, m.Subject)
		}
		if m.TextBody == "" || m.HTMLBody == "" || m.Calendar == nil || m.Calendar.Method != "REQUEST" {
			t.Errorf("mail to %v lacks one of the three alternatives: %+v", m.To, m)
		}
		if !strings.Contains(m.TextBody, "Svar med lenken nedenfor") || !strings.Contains(m.TextBody, "http://modules.example.com/rsvp?token=") ||
			!strings.Contains(m.TextBody, "Når: man. 2. nov. 2026, 09:00–10:00 (Europe/Oslo)") || !strings.Contains(m.TextBody, "Hvor: Storgata 1") {
			t.Errorf("the text to %v is not the Norwegian invitation with its link:\n%s", m.To, m.TextBody)
		}
		ics := icsOf(t, m)
		for _, want := range []string{
			"METHOD:REQUEST", "UID:" + modtest.One[string](t, w.h.Harness, `SELECT ical_uid FROM calendar.appointments WHERE id = $1`, a.Id),
			"SEQUENCE:0", "DTSTART:20261102T080000Z", "DTEND:20261102T090000Z", "SUMMARY:Kundemøte", "LOCATION:Storgata 1",
			"STATUS:CONFIRMED", "CLASS:PUBLIC", "TRANSP:OPAQUE",
			"ORGANIZER;CN=Kari Nordmann:mailto:" + smtpFrom,
			"ATTENDEE;CN=Ola Hansen;PARTSTAT=NEEDS-ACTION;ROLE=REQ-PARTICIPANT;RSVP=TRUE:mailto:" + olaMail,
			"ATTENDEE;CN=Per Olsen;PARTSTAT=NEEDS-ACTION;ROLE=REQ-PARTICIPANT;RSVP=TRUE:mailto:" + perMail,
			"URL:http://modules.example.com/rsvp?token=" + tokenOf(t, m),
		} {
			if !strings.Contains(ics, want+"\r\n") {
				t.Errorf("the object to %v lacks %q:\n%s", m.To, want, ics)
			}
		}
		if n := len(lines(ics, "ATTENDEE")); n != 2 {
			t.Errorf("the object to %v lists %d attendees, want the two outside ones", m.To, n)
		}
		if strings.Contains(ics, "example.test") || strings.Contains(ics, "VTIMEZONE") {
			t.Errorf("the object to %v names a colleague's address or carries a VTIMEZONE:\n%s", m.To, ics)
		}
	}
	if tokenOf(t, to(t, msgs, olaMail)) == tokenOf(t, to(t, msgs, perMail)) {
		t.Error("two attendees carry one token")
	}

	contact, email := attendeeOf(t, a, olaID), attendeeOf(t, a, perMail)
	if contact.Contact.Name == nil || *contact.Contact.Name != "Ola Hansen" || contact.Email != nil {
		t.Errorf("contact attendee = %+v, want named from the directory and no address", contact)
	}
	if email.Name == nil || *email.Name != "Per Olsen" || email.Kind != gen.Email {
		t.Errorf("email attendee = %+v, want the address lower-cased and the name as typed", email)
	}
	for _, at := range []gen.AppointmentAttendee{contact, email} {
		if at.Invitation == nil || at.Invitation.SentAt == nil || at.Invitation.Sequence == nil || *at.Invitation.Sequence != 0 || at.Invitation.FailedAt != nil {
			t.Errorf("attendee %d's invitation = %+v, want sent with sequence 0", at.Id, at.Invitation)
		}
	}
	if colleagueRow := attendeeOf(t, a, colleague); colleagueRow.Invitation != nil {
		t.Errorf("a colleague carries an invitation: %+v", colleagueRow.Invitation)
	}

	notices := w.h.Notices()
	if len(notices) != 1 || notices[0].UserID != colleague {
		t.Fatalf("notices = %+v, want one to the colleague", notices)
	}
	en, nb := notices[0].Notice.Texts["en"], notices[0].Notice.Texts["nb"]
	if en.Subject != "Kari Nordmann invited you: Kundemøte, Mon 2 Nov 09:00" || nb.Subject != "Kari Nordmann inviterte deg: Kundemøte, man. 2. nov. 09:00" {
		t.Errorf("notice subjects = %q / %q", en.Subject, nb.Subject)
	}
	link := fmt.Sprintf("http://modules.example.com/calendar?date=2026-11-02&appointment=%d", a.Id)
	if !strings.Contains(en.Body, link) || !strings.Contains(nb.Body, link) {
		t.Errorf("notice bodies lack the link to the appointment:\n%s\n%s", en.Body, nb.Body)
	}
	if strings.Contains(en.Body+nb.Body+en.Subject+nb.Subject, "@") {
		t.Errorf("a notice names an address: %+v", notices[0].Notice)
	}
}

func TestInvitations_EnglishIsTheLanguageOfAnOwnerWhoChoseNone(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "")
	create(t, w.owner, meeting(t, emailAttendee(perMail, "")))
	m := to(t, w.h.mail.messages(), perMail)
	if !strings.HasPrefix(m.Subject, "Invitation: Kundemøte · Mon 2 Nov") || !strings.Contains(m.TextBody, "Answer with the link below so the organiser sees it in Vantigo") ||
		!strings.Contains(m.TextBody, "When: Mon 2 Nov 2026, 09:00–10:00 (Europe/Oslo)") {
		t.Errorf("an owner with no language mails in English: %q\n%s", m.Subject, m.TextBody)
	}
	if !strings.Contains(icsOf(t, m), "ATTENDEE;PARTSTAT=NEEDS-ACTION;ROLE=REQ-PARTICIPANT;RSVP=TRUE:mailto:"+perMail+"\r\n") {
		t.Errorf("an address with no name is listed without a CN:\n%s", icsOf(t, m))
	}
}

func TestInvitations_APutSendsAsTheTableSays(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	colleague := directoryUser(t, w.h, "Liv Lund", true)
	both := attendees(userAttendees(colleague.String()), []map[string]any{contactAttendee(olaID), emailAttendee(perMail, "Per")})
	a := create(t, w.owner, meeting(t, both...))
	w.h.mail.take()
	noticesBefore := len(w.h.Notices())

	// A title change bumps no sequence: nothing goes.
	a = put(t, w.owner, a, with(meeting(t, both...), "title", "Customer meeting"))
	if msgs := w.h.mail.take(); len(msgs) != 0 || len(w.h.Notices()) != noticesBefore {
		t.Errorf("a title change sent %d mails and %d notices", len(msgs), len(w.h.Notices())-noticesBefore)
	}

	// A time change sends REQUEST with SEQUENCE 1 to both, and tells the
	// colleague it changed.
	a = put(t, w.owner, a, with(timed(t, "Customer meeting", "Europe/Oslo", "2026-11-02T13:00", "2026-11-02T14:00"),
		"location", "Storgata 1", "attendees", both))
	msgs := w.h.mail.take()
	if got := recipients(msgs); !slices.Equal(got, []string{olaMail, perMail}) {
		t.Fatalf("a time change mailed %v", got)
	}
	for _, m := range msgs {
		ics := icsOf(t, m)
		if m.Calendar.Method != "REQUEST" || !strings.Contains(ics, "SEQUENCE:1\r\n") || !strings.Contains(ics, "DTSTART:20261102T120000Z\r\n") ||
			!strings.HasPrefix(m.Subject, "Updated: Customer meeting") {
			t.Errorf("the time change's mail to %v: %q\n%s", m.To, m.Subject, ics)
		}
	}
	notices := w.h.Notices()[noticesBefore:]
	if len(notices) != 1 || notices[0].UserID != colleague || notices[0].Notice.Texts["en"].Subject != "Kari Nordmann changed: Customer meeting, Mon 2 Nov 13:00" {
		t.Errorf("the time change's notices = %+v", notices)
	}
	if at := attendeeOf(t, a, perMail); at.Invitation.Sequence == nil || *at.Invitation.Sequence != 1 {
		t.Errorf("after the time change the invitation = %+v, want sequence 1", at.Invitation)
	}

	// Adding a third sends to the third alone, as an invitation; a
	// colleague added is told they are invited.
	newcomer := directoryUser(t, w.h, "Mons Moe", true)
	three := attendees(both, []map[string]any{contactAttendee(siriID)}, userAttendees(newcomer.String()))
	body := with(timed(t, "Customer meeting", "Europe/Oslo", "2026-11-02T13:00", "2026-11-02T14:00"), "location", "Storgata 1")
	a = put(t, w.owner, a, with(body, "attendees", three))
	msgs = w.h.mail.take()
	if got := recipients(msgs); !slices.Equal(got, []string{siriMail}) {
		t.Fatalf("adding a third mailed %v", got)
	}
	if m := msgs[0]; !strings.HasPrefix(m.Subject, "Invitation: ") || !strings.Contains(icsOf(t, m), "SEQUENCE:1\r\n") || len(lines(icsOf(t, m), "ATTENDEE")) != 3 {
		t.Errorf("the third's mail: %q\n%s", m.Subject, icsOf(t, m))
	}
	notices = w.h.Notices()[noticesBefore+1:]
	if len(notices) != 1 || notices[0].UserID != newcomer || !strings.Contains(notices[0].Notice.Texts["en"].Subject, "invited you") {
		t.Errorf("adding a colleague told %+v", notices)
	}

	// Dropping one sends CANCEL to that one, naming them alone; a
	// colleague dropped is told they were removed.
	a = put(t, w.owner, a, with(body, "attendees", attendees([]map[string]any{contactAttendee(olaID), contactAttendee(siriID)}, userAttendees(newcomer.String()))))
	msgs = w.h.mail.take()
	if got := recipients(msgs); !slices.Equal(got, []string{perMail}) {
		t.Fatalf("dropping one mailed %v", got)
	}
	ics := icsOf(t, msgs[0])
	if msgs[0].Calendar.Method != "CANCEL" || !strings.Contains(ics, "METHOD:CANCEL\r\n") || !strings.Contains(ics, "STATUS:CANCELLED\r\n") ||
		!slices.Equal(lines(ics, "ATTENDEE"), []string{"ATTENDEE;CN=Per;PARTSTAT=NEEDS-ACTION;ROLE=REQ-PARTICIPANT;RSVP=TRUE:mailto:" + perMail}) ||
		!strings.HasPrefix(msgs[0].Subject, "Cancelled: ") || !strings.Contains(msgs[0].TextBody, "has removed you") {
		t.Errorf("the dropped one's mail: %q\n%s\n%s", msgs[0].Subject, msgs[0].TextBody, ics)
	}
	notices = w.h.Notices()[noticesBefore+2:]
	if len(notices) != 1 || notices[0].UserID != colleague || !strings.Contains(notices[0].Notice.Texts["en"].Subject, "removed you") {
		t.Errorf("dropping a colleague told %+v", notices)
	}
	if len(a.Attendees) != 3 {
		t.Errorf("attendees after the drop = %+v", a.Attendees)
	}
}

func TestInvitations_AFailedSendIsMarkedAndTheResendClearsIt(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	w.h.mail.failWith(func(out mail.Outbound) error {
		if out.To[0] == perMail {
			return errors.New("550 5.1.1 <" + strings.ToUpper(perMail) + ">: mailbox unavailable")
		}
		return nil
	})
	a := create(t, w.owner, meeting(t, contactAttendee(olaID), emailAttendee(perMail, "")))
	per := attendeeOf(t, a, perMail)
	if per.Invitation == nil || per.Invitation.FailedAt == nil || per.Invitation.SentAt != nil {
		t.Fatalf("the failed invitation = %+v, want failedAt and no sentAt", per.Invitation)
	}
	if ola := attendeeOf(t, a, olaID); ola.Invitation.SentAt == nil || ola.Invitation.FailedAt != nil {
		t.Errorf("the other invitation = %+v, want sent", ola.Invitation)
	}
	if logs := strings.ToLower(w.h.Logs()); strings.Contains(logs, perMail) || !strings.Contains(logs, "an invitation could not be sent") {
		t.Errorf("the failure is not logged, or names the address:\n%s", w.h.Logs())
	}

	w.h.mail.failWith(nil)
	w.h.mail.take()
	var got gen.Appointment
	do(t, w.owner, http.MethodPost, resendPath(a.Id, per.Id), nil, http.StatusOK, &got)
	if at := attendeeOf(t, got, perMail); at.Invitation.SentAt == nil || at.Invitation.FailedAt != nil || *at.Invitation.Sequence != 0 {
		t.Errorf("after the resend the invitation = %+v, want sent and the failure cleared", at.Invitation)
	}
	msgs := w.h.mail.take()
	if got := recipients(msgs); !slices.Equal(got, []string{perMail}) || !strings.HasPrefix(msgs[0].Subject, "Invitation: ") {
		t.Fatalf("the resend mailed %v", got)
	}

	// A second resend carries the same link, and is an update.
	do(t, w.owner, http.MethodPost, resendPath(a.Id, per.Id), nil, http.StatusOK, nil)
	again := w.h.mail.take()
	if len(again) != 1 || tokenOf(t, again[0]) != tokenOf(t, msgs[0]) || !strings.HasPrefix(again[0].Subject, "Updated: ") {
		t.Errorf("the second resend: %+v", again)
	}

	colleague := directoryUser(t, w.h, "Liv Lund", true)
	b := create(t, w.owner, meeting(t, userAttendees(colleague.String())...))
	stranger, _ := signIn(t, w.h)
	for name, path := range map[string]string{
		"a colleague":            resendPath(b.Id, b.Attendees[0].Id),
		"an attendee not on it":  resendPath(b.Id, per.Id),
		"someone else's":         resendPath(a.Id, per.Id),
		"an unknown appointment": resendPath(a.Id+1000, per.Id),
	} {
		client := w.owner
		if name == "someone else's" {
			client = stranger
		}
		if r := client.Do(http.MethodPost, path, nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("resend of %s = %d %s, want the bare 404", name, r.Status, r.Body)
		}
	}
}

func TestInvitations_WithoutTheSMTPDriverAttendeesAreStoredUnsentAndMetaSaysSo(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en", modtest.WithEnv("MAIL_DRIVER", "log"))
	a := create(t, w.owner, meeting(t, contactAttendee(olaID), emailAttendee(perMail, "")))
	if n := len(w.h.mail.messages()); n != 0 {
		t.Errorf("%d mails sent with the log driver", n)
	}
	for _, at := range a.Attendees {
		if at.Invitation == nil || at.Invitation.SentAt != nil || at.Invitation.FailedAt != nil || at.Invitation.Sequence != nil {
			t.Errorf("attendee %d's invitation = %+v, want not sent", at.Id, at.Invitation)
		}
	}
	var meta gen.CalendarMeta
	do(t, w.owner, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	if meta.InvitationsAvailable {
		t.Error("invitationsAvailable is true with the log driver")
	}
	refused(t, w.owner.Do(http.MethodPost, resendPath(a.Id, a.Attendees[0].Id), nil), http.StatusServiceUnavailable, "invitations_unavailable")

	// Nothing marked, so a delete is the plain delete.
	do(t, w.owner, http.MethodDelete, appointmentPath(a.Id), nil, http.StatusNoContent, nil)
	do(t, w.owner, http.MethodGet, appointmentPath(a.Id), nil, http.StatusNotFound, nil)

	h := newHarness(t)
	c, _ := signIn(t, h)
	do(t, c, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	if !meta.InvitationsAvailable {
		t.Error("invitationsAvailable is false with the smtp driver")
	}
}

func TestInvitations_RefusesEachAttendeeRuleOnItsElement(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	plain, _ := signIn(t, w.h)
	many := make([]map[string]any, 51)
	for i := range many {
		many[i] = emailAttendee(fmt.Sprintf("guest%d@example.com", i), "")
	}
	for _, tc := range []struct {
		name   string
		client *modtest.Client
		list   []map[string]any
		field  string
	}{
		{"a contact without an address", w.owner, []map[string]any{contactAttendee(ninaID)}, "attendees[0]"},
		{"an unknown contact", w.owner, []map[string]any{emailAttendee(perMail, ""), contactAttendee(99)}, "attendees[1]"},
		{"a malformed address", w.owner, []map[string]any{emailAttendee("not an address", "")}, "attendees[0]"},
		{"an address with a display name", w.owner, []map[string]any{emailAttendee("Per <per@example.com>", "")}, "attendees[0]"},
		{"a name too long", w.owner, []map[string]any{emailAttendee(perMail, strings.Repeat("n", 201))}, "attendees[0]"},
		{"the owner", w.owner, userAttendees(w.ownerID.String()), "attendees[0]"},
		{"a 51st", w.owner, many, "attendees"},
		{"a writer without customers:contacts-view", plain, []map[string]any{contactAttendee(olaID)}, "attendees[0]"},
		{"another kind's field", w.owner, []map[string]any{{"kind": "email", "email": perMail, "contactId": olaID}}, "attendees[0]"},
		{"a contact with no id", w.owner, []map[string]any{{"kind": "contact"}}, "attendees[0]"},
	} {
		fieldRefused(t, tc.client.Do(http.MethodPost, appointmentsPath, meeting(t, tc.list...)), tc.field)
	}
	if n := len(w.h.mail.messages()); n != 0 {
		t.Errorf("a refused write sent %d mails", n)
	}

	// Without Customers, no contact can be named.
	h := newHarness(t)
	c, _ := signIn(t, h, "customers:contacts-view")
	fieldRefused(t, c.Do(http.MethodPost, appointmentsPath, meeting(t, contactAttendee(olaID))), "attendees[0]")

	// Two naming the same contact or address are one.
	a := create(t, w.owner, meeting(t, contactAttendee(olaID), contactAttendee(olaID), emailAttendee(perMail, ""), emailAttendee(strings.ToUpper(perMail), "")))
	if len(a.Attendees) != 2 {
		t.Errorf("attendees = %+v, want the duplicates collapsed", a.Attendees)
	}
}

func TestInvitations_TheContactIsNamedFromTheDirectoryAndItsAddressIsNeverOnTheWire(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	colleague := directoryUser(t, w.h, "Liv Lund", true)
	a := create(t, w.owner, meeting(t, attendees(userAttendees(colleague.String()), []map[string]any{contactAttendee(olaID)})...))
	attendeeClient, attendeeID := signIn(t, w.h)
	a = put(t, w.owner, a, meeting(t, attendees(userAttendees(colleague.String(), attendeeID.String()), []map[string]any{contactAttendee(olaID)})...))
	for who, c := range map[string]*modtest.Client{"the owner": w.owner, "an attendee": attendeeClient} {
		r := c.Do(http.MethodGet, appointmentPath(a.Id), nil)
		if r.Status != http.StatusOK || strings.Contains(string(r.Body), olaMail) || !strings.Contains(string(r.Body), "Ola Hansen") {
			t.Errorf("GET as %s = %d %s, want the contact named and no address", who, r.Status, r.Body)
		}
	}
	r := w.owner.Do(http.MethodGet, windowPath("2026-11-02", "2026-11-02"), nil)
	if strings.Contains(string(r.Body), olaMail) {
		t.Errorf("the window names the contact's address: %s", r.Body)
	}

	// An erased contact is "a contact" with no name, and is not mailed: its
	// invitation is marked failed. The owner may still change the
	// appointment with it on.
	w.dir.erase(olaID)
	w.h.mail.take()
	a = put(t, w.owner, a, with(meeting(t, attendees(userAttendees(colleague.String(), attendeeID.String()), []map[string]any{contactAttendee(olaID)})...),
		"startsAt", at(t, "Europe/Oslo", "2026-11-03T09:00").UTC().Format("2006-01-02T15:04:05Z"),
		"endsAt", at(t, "Europe/Oslo", "2026-11-03T10:00").UTC().Format("2006-01-02T15:04:05Z")))
	ola := attendeeOf(t, a, olaID)
	if ola.Contact.Name != nil || ola.Invitation.FailedAt == nil {
		t.Errorf("the erased contact = %+v, invitation %+v; want no name and the send failed", ola.Contact, ola.Invitation)
	}
	if n := len(w.h.mail.take()); n != 0 {
		t.Errorf("an erased contact was mailed %d times", n)
	}
}

func TestInvitations_TheTokenIsNotStoredAndTheLinkResolvesByItsHash(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	a := create(t, w.owner, meeting(t, emailAttendee(perMail, "Per")))
	token := tokenOf(t, to(t, w.h.mail.messages(), perMail))
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	if n := w.h.Count(t, `SELECT count(*) FROM calendar.appointment_attendees WHERE rsvp_token_sha256 = $1`, hash); n != 1 {
		t.Errorf("%d rows hold the token's hash", n)
	}
	if n := w.h.Count(t, `SELECT count(*) FROM calendar.appointment_attendees
	                      WHERE rsvp_token_sha256 = $1 OR position($1 in coalesce(rsvp_link_ciphertext, '')) > 0`, token); n != 0 {
		t.Errorf("the token itself is stored on %d rows", n)
	}
	sealed := modtest.One[string](t, w.h.Harness, `SELECT rsvp_link_ciphertext FROM calendar.appointment_attendees WHERE appointment_id = $1`, a.Id)
	link, err := w.h.Deps().Secrets.OpenString("calendar/rsvp-link", sealed)
	if err != nil || link != "http://modules.example.com/rsvp?token="+token {
		t.Errorf("the sealed link = %q, %v", link, err)
	}
	var page gen.RsvpPage
	do(t, w.h.Client(t), http.MethodPost, rsvpReadPath, map[string]any{"token": token}, http.StatusOK, &page)
	if page.Title != "Kundemøte" {
		t.Errorf("the link resolves to %+v", page)
	}
}

func TestInvitations_TheLinkIsOnThePublicHostWhenThereIsOne(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en", modtest.WithEnv("PUBLIC_BASE_URL", "https://public.example.com"))
	create(t, w.owner, meeting(t, emailAttendee(perMail, "")))
	if m := to(t, w.h.mail.messages(), perMail); !strings.Contains(m.TextBody, "https://public.example.com/rsvp?token=") {
		t.Errorf("the link is not on the public host:\n%s", m.TextBody)
	}
}

func TestInvitations_AnOccurrenceWriteSendsTheWholeSeriesInItsZone(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	body := with(timed(t, "Stand-up", "Europe/Oslo", "2026-10-19T09:00", "2026-10-19T09:30"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY;COUNT=6"}, "attendees", []map[string]any{emailAttendee(perMail, "")})
	a := create(t, w.owner, body)
	first := icsOf(t, to(t, w.h.mail.take(), perMail))
	for _, want := range []string{
		"DTSTART;TZID=Europe/Oslo:20261019T090000", "DTEND;TZID=Europe/Oslo:20261019T093000",
		"RRULE:FREQ=WEEKLY;INTERVAL=1;COUNT=6", "BEGIN:VTIMEZONE", "TZID:Europe/Oslo",
		"BEGIN:STANDARD", "DTSTART:20261025T030000", "TZOFFSETFROM:+0200", "TZOFFSETTO:+0100", "TZNAME:CET",
	} {
		if !strings.Contains(first, want+"\r\n") {
			t.Errorf("the series' object lacks %q:\n%s", want, first)
		}
	}

	do(t, w.owner, http.MethodPut, occurrencePath(a.Id, "2026-10-26"), map[string]any{"cancelled": true}, http.StatusOK, nil)
	if msgs := w.h.mail.take(); len(msgs) != 1 || !strings.Contains(icsOf(t, msgs[0]), "EXDATE;TZID=Europe/Oslo:20261026T090000\r\n") ||
		!strings.Contains(icsOf(t, msgs[0]), "SEQUENCE:1\r\n") {
		t.Errorf("a cancelled occurrence sent %+v", msgs)
	}
	do(t, w.owner, http.MethodPut, occurrencePath(a.Id, "2026-11-02"), map[string]any{
		"startsAt": at(t, "Europe/Oslo", "2026-11-02T11:00").UTC().Format("2006-01-02T15:04:05Z"),
		"endsAt":   at(t, "Europe/Oslo", "2026-11-02T11:30").UTC().Format("2006-01-02T15:04:05Z"),
	}, http.StatusOK, nil)
	msgs := w.h.mail.take()
	if len(msgs) != 1 {
		t.Fatalf("a moved occurrence sent %d mails", len(msgs))
	}
	ics := icsOf(t, msgs[0])
	for _, want := range []string{
		"SEQUENCE:2", "EXDATE;TZID=Europe/Oslo:20261026T090000", "RECURRENCE-ID;TZID=Europe/Oslo:20261102T090000",
		"DTSTART;TZID=Europe/Oslo:20261102T110000", "DTEND;TZID=Europe/Oslo:20261102T113000",
	} {
		if !strings.Contains(ics, want+"\r\n") {
			t.Errorf("the moved occurrence's object lacks %q:\n%s", want, ics)
		}
	}
	if n := strings.Count(ics, "BEGIN:VEVENT"); n != 2 {
		t.Errorf("%d VEVENTs, want the series and the moved one", n)
	}
	do(t, w.owner, http.MethodDelete, occurrencePath(a.Id, "2026-10-26"), nil, http.StatusNoContent, nil)
	if msgs := w.h.mail.take(); len(msgs) != 1 || strings.Contains(icsOf(t, msgs[0]), "EXDATE") {
		t.Errorf("a restored occurrence sent %+v", msgs)
	}
	do(t, w.owner, http.MethodDelete, occurrencePath(a.Id, "2026-10-26"), nil, http.StatusNoContent, nil)
	if msgs := w.h.mail.take(); len(msgs) != 0 {
		t.Errorf("restoring a day with no exception sent %d mails", len(msgs))
	}
}
