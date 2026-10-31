package calendar_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The invitation channel (integrations design T6): the Calendar settings name
// the Communications channel invitations to people outside the company go out
// on, or none, and nothing is sent on the installation's own SMTP credentials.
// These tests drive the real module against modtest.FakeOutboundMail, with
// channels that can send and one that cannot.

const invitationSettingsPath = "/api/v1/calendar/invitation-settings"

// invitationChannels is the channels the tests offer: two that can send and
// one whose credentials are missing.
func invitationChannels() (first, second, broken contracts.OutboundChannel) {
	first = contracts.OutboundChannel{
		ID: uuid.New(), Address: "kalender@selger.example", DisplayName: "Kalender", Provider: "smtp", ProviderDisplayName: "SMTP", Available: true,
	}
	second = contracts.OutboundChannel{
		ID: uuid.New(), Address: "post@selger.example", Provider: "entra", ProviderDisplayName: "Microsoft 365", Available: true,
	}
	broken = contracts.OutboundChannel{ID: uuid.New(), Address: "gammel@selger.example", Provider: "smtp", ProviderDisplayName: "SMTP"}
	return first, second, broken
}

// channelsHarness is an installation with the OutboundMail slot filled by fake
// and invitations off until a test chooses a channel.
func channelsHarness(t *testing.T) (inviting, *modtest.FakeOutboundMail, [3]contracts.OutboundChannel) {
	t.Helper()
	first, second, broken := invitationChannels()
	fake := modtest.NewFakeOutboundMail()
	fake.SetChannels(first, second, broken)
	w := newInviting(t, "en", modtest.WithOutboundMail(fake))
	invitationsOff(t, w.h)
	return w, fake, [3]contracts.OutboundChannel{first, second, broken}
}

// choose stores channel as the invitation channel, as the settings do; nil is
// off.
func choose(t *testing.T, h *harness, channel *uuid.UUID) {
	t.Helper()
	h.Exec(t, `UPDATE calendar.settings SET invitation_channel = $1`, channel)
}

// calendar:manage is the one key of the Calendar settings: sensitive, since it
// decides whose address customers' mail comes from, delegable like the rest,
// in the Administration category.
func TestCalendarManage_IsInTheCatalog(t *testing.T) {
	t.Parallel()
	var found bool
	for _, p := range calendar.Module().Permissions {
		if p.Key != "calendar:manage" {
			continue
		}
		found = true
		if p.Display != "Manage Calendar settings" || p.Category != "Administration" || !p.Sensitive || !p.Delegable {
			t.Errorf("calendar:manage = %+v, want Manage Calendar settings, Administration, sensitive, delegable", p)
		}
	}
	if !found {
		t.Error("calendar:manage is not in the module's catalog")
	}
}

// Both operations need calendar:access and calendar:manage together: access
// alone and manage alone are 403.
func TestInvitationSettings_NeedAccessAndManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	accessOnly, _ := signIn(t, h)
	manageOnly, _ := h.SignInUser(t, "calendar:manage")
	both, _ := signIn(t, h, "calendar:manage")

	for name, c := range map[string]*modtest.Client{"calendar:access alone": accessOnly, "calendar:manage alone": manageOnly} {
		if r := c.Do(http.MethodGet, invitationSettingsPath, nil); r.Status != http.StatusForbidden {
			t.Errorf("GET with %s = %d %s, want 403", name, r.Status, r.Body)
		}
		if r := c.Do(http.MethodPut, invitationSettingsPath, map[string]any{"invitationChannel": nil}, ifMatch(1)); r.Status != http.StatusForbidden {
			t.Errorf("PUT with %s = %d %s, want 403", name, r.Status, r.Body)
		}
	}
	var got gen.CalendarInvitationSettings
	do(t, both, http.MethodGet, invitationSettingsPath, nil, http.StatusOK, &got)
	if got.Revision != 1 {
		t.Errorf("revision = %d, want 1", got.Revision)
	}
	// GET /meta says who the card is for, so the page asks nothing it would be refused.
	if metaOf(t, both).CanManage != true || metaOf(t, accessOnly).CanManage != false {
		t.Errorf("meta canManage: a manager %v, a member %v; want true, false", metaOf(t, both).CanManage, metaOf(t, accessOnly).CanManage)
	}
}

// The settings answer the stored channel, what it is now and the choices; a
// replace stores a channel that exists, clears it with null, refuses a body
// without the field, a malformed id and an id no channel has with a 400 on
// invitationChannel, and a stale revision with a 409. A stored channel that has
// since gone is shown as gone, and can be kept by naming it again.
func TestInvitationSettings_ChooseClearAndRefuse(t *testing.T) {
	t.Parallel()
	w, fake, ch := channelsHarness(t)
	first, second, broken := ch[0], ch[1], ch[2]
	manager, _ := signIn(t, w.h, "calendar:manage")
	read := func() gen.CalendarInvitationSettings {
		t.Helper()
		var s gen.CalendarInvitationSettings
		do(t, manager, http.MethodGet, invitationSettingsPath, nil, http.StatusOK, &s)
		return s
	}
	put := func(revision int32, body map[string]any) *modtest.Response {
		t.Helper()
		return manager.Do(http.MethodPut, invitationSettingsPath, body, ifMatch(revision))
	}

	s := read()
	if s.InvitationChannel != nil || s.State != gen.CalendarInvitationSettingsStateDisabled || len(s.Channels) != 3 {
		t.Fatalf("a fresh installation: channel %v state %q channels %d; want none, disabled, three", s.InvitationChannel, s.State, len(s.Channels))
	}
	if got := s.Channels[0]; got.Id != first.ID || got.Address != first.Address || got.DisplayName != "Kalender" || got.Provider != "smtp" ||
		got.ProviderDisplayName != "SMTP" || !got.Available {
		t.Errorf("the first choice = %+v, want the SMTP channel that can send", got)
	}
	if got := s.Channels[2]; got.Id != broken.ID || got.Available {
		t.Errorf("the third choice = %+v, want the channel that cannot send", got)
	}

	var saved gen.CalendarInvitationSettings
	r := put(s.Revision, map[string]any{"invitationChannel": second.ID.String()})
	if r.Status != http.StatusOK {
		t.Fatalf("choose a channel = %d %s", r.Status, r.Body)
	}
	r.JSON(&saved)
	if saved.InvitationChannel == nil || *saved.InvitationChannel != second.ID || saved.State != gen.CalendarInvitationSettingsStateReady || saved.Revision != s.Revision+1 {
		t.Errorf("after choosing: %v %q revision %d, want the second channel, ready, revision %d", saved.InvitationChannel, saved.State, saved.Revision, s.Revision+1)
	}
	if mailAvailable := metaOf(t, w.owner).InvitationsAvailable; !mailAvailable {
		t.Error("invitationsAvailable is false with a channel that can send chosen")
	}
	r = put(saved.Revision, map[string]any{"invitationChannel": broken.ID.String()})
	r.JSON(&saved)
	if r.Status != http.StatusOK || saved.State != gen.CalendarInvitationSettingsStateUnavailable {
		t.Errorf("choosing the channel that cannot send = %d %q, want 200 and unavailable", r.Status, saved.State)
	}
	if metaOf(t, w.owner).InvitationsAvailable {
		t.Error("invitationsAvailable is true with a channel that cannot send chosen")
	}
	r = put(saved.Revision, map[string]any{"invitationChannel": nil})
	r.JSON(&saved)
	if r.Status != http.StatusOK || saved.InvitationChannel != nil || saved.State != gen.CalendarInvitationSettingsStateDisabled {
		t.Errorf("a null invitationChannel = %d %v %q, want 200, none, disabled", r.Status, saved.InvitationChannel, saved.State)
	}

	// Refused.
	for name, body := range map[string]map[string]any{
		"a body without the field": {},
		"an unknown channel":       {"invitationChannel": uuid.NewString()},
		"a malformed id":           {"invitationChannel": "not-a-uuid"},
		"a number":                 {"invitationChannel": 7},
	} {
		r := put(saved.Revision, body)
		var p problem
		r.JSON(&p)
		if r.Status != http.StatusBadRequest || len(p.Errors["invitationChannel"]) == 0 {
			t.Errorf("%s = %d %s, want 400 on invitationChannel", name, r.Status, r.Body)
		}
	}
	refused(t, put(saved.Revision+5, map[string]any{"invitationChannel": nil}), http.StatusConflict, "revision_mismatch")
	if r := manager.Do(http.MethodPut, invitationSettingsPath, map[string]any{"invitationChannel": nil}); r.Status != http.StatusBadRequest {
		t.Errorf("a PUT without If-Match = %d %s, want 400", r.Status, r.Body)
	}
	if read().Revision != saved.Revision {
		t.Error("a refused replace moved the revision")
	}

	// A channel that goes away.
	r = put(saved.Revision, map[string]any{"invitationChannel": first.ID.String()})
	r.JSON(&saved)
	fake.SetChannels(second, broken)
	if gone := read(); gone.InvitationChannel == nil || *gone.InvitationChannel != first.ID || gone.State != gen.CalendarInvitationSettingsStateGone {
		t.Errorf("after the channel went: %v %q, want the id kept and gone", gone.InvitationChannel, gone.State)
	}
	r = put(read().Revision, map[string]any{"invitationChannel": first.ID.String()})
	if r.Status != http.StatusOK {
		t.Errorf("naming the gone stored channel again = %d %s, want 200", r.Status, r.Body)
	}
}

// metaOf is GET /meta as c reads it.
func metaOf(t *testing.T, c *modtest.Client) gen.CalendarMeta {
	t.Helper()
	var meta gen.CalendarMeta
	do(t, c, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	return meta
}

// With Communications off the slot is empty: every choice but none is refused
// and the settings read as off.
func TestInvitationSettings_WithoutCommunications(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithOutboundMail(nil))
	h.Exec(t, `UPDATE calendar.settings SET invitation_channel = NULL`)
	manager, _ := signIn(t, h, "calendar:manage")
	var s gen.CalendarInvitationSettings
	do(t, manager, http.MethodGet, invitationSettingsPath, nil, http.StatusOK, &s)
	if s.InvitationChannel != nil || s.State != gen.CalendarInvitationSettingsStateDisabled || len(s.Channels) != 0 {
		t.Errorf("settings = %v %q %d channels, want none, disabled, none", s.InvitationChannel, s.State, len(s.Channels))
	}
	r := manager.Do(http.MethodPut, invitationSettingsPath, map[string]any{"invitationChannel": uuid.NewString()}, ifMatch(s.Revision))
	if r.Status != http.StatusBadRequest {
		t.Errorf("a channel with no Communications = %d %s, want 400", r.Status, r.Body)
	}
}

// Invitations go out on the chosen channel - From its address, the ORGANIZER of
// the object too - and nowhere else; with none chosen, a channel that cannot
// send or one that has gone, nothing is sent and nothing is marked, the meta
// says so and a resend is a 503.
func TestInvitations_GoThroughTheChosenChannelOrNotAtAll(t *testing.T) {
	t.Parallel()
	w, fake, ch := channelsHarness(t)
	first, second, broken := ch[0], ch[1], ch[2]
	outside := []map[string]any{emailAttendee(perMail, "Per Olsen")}

	unsent := func(what string) {
		t.Helper()
		a := create(t, w.owner, meeting(t, outside...))
		if n := len(fake.Sent()); n != 0 {
			t.Errorf("%s: %d mails sent, want none", what, n)
		}
		for _, at := range a.Attendees {
			if at.Invitation == nil || at.Invitation.SentAt != nil || at.Invitation.FailedAt != nil || at.Invitation.Sequence != nil {
				t.Errorf("%s: attendee %d's invitation = %+v, want not sent and not marked", what, at.Id, at.Invitation)
			}
		}
		if metaOf(t, w.owner).InvitationsAvailable {
			t.Errorf("%s: invitationsAvailable is true", what)
		}
		refused(t, w.owner.Do(http.MethodPost, resendPath(a.Id, a.Attendees[0].Id), nil), http.StatusServiceUnavailable, "invitations_unavailable")
	}
	unsent("with no channel chosen")
	choose(t, w.h, &broken.ID)
	unsent("with a channel that cannot send")
	gone := uuid.New()
	choose(t, w.h, &gone)
	unsent("with a channel that is gone")

	choose(t, w.h, &second.ID)
	a := create(t, w.owner, meeting(t, outside...))
	sent := fake.Sent()
	if len(sent) != 1 || sent[0].Channel != second.ID || !slices.Equal(sent[0].Out.To, []string{perMail}) {
		t.Fatalf("sends = %+v, want one to %s on the chosen channel", sent, perMail)
	}
	if sent[0].Out.DisplayName != ownerName || sent[0].Out.Calendar == nil {
		t.Errorf("the mail = name %q calendar %v, want the organiser's name and the iCalendar object", sent[0].Out.DisplayName, sent[0].Out.Calendar)
	}
	if ics := icsOf(t, sent[0].Out); !strings.Contains(ics, "ORGANIZER;CN=Kari Nordmann:mailto:"+second.Address+"\r\n") {
		t.Errorf("the object does not name the channel's address as ORGANIZER:\n%s", ics)
	}
	if a.Attendees[0].Invitation == nil || a.Attendees[0].Invitation.SentAt == nil {
		t.Errorf("the attendee's invitation = %+v, want sent", a.Attendees[0].Invitation)
	}

	// Another channel, and a resend on it.
	choose(t, w.h, &first.ID)
	do(t, w.owner, http.MethodPost, resendPath(a.Id, a.Attendees[0].Id), nil, http.StatusOK, nil)
	if sent := fake.Sent(); len(sent) != 2 || sent[1].Channel != first.ID {
		t.Errorf("sends after a resend on another channel = %+v, want the second on %s", sent, first.ID)
	}

	// A send that fails marks the attendee for Resend.
	fake.FailWith("Send", contracts.ErrChannelUnavailable)
	failed := create(t, w.owner, meeting(t, outside...))
	if failed.Attendees[0].Invitation == nil || failed.Attendees[0].Invitation.FailedAt == nil || failed.Attendees[0].Invitation.SentAt != nil {
		t.Errorf("after a failing send: invitation %+v, want failed and not sent", failed.Attendees[0].Invitation)
	}
}
