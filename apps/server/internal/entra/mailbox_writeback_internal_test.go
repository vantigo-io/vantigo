package entra

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestListPath pins the next link check of Versions: a page of the very
// list sent, whatever the case of its path, answered on the path sent with
// the link's query; nothing else — and a link's percent-encoding only when
// the path sent carries its own.
func TestListPath(t *testing.T) {
	oid := uuid.MustParse("6f1c2b3a-4d5e-4f60-8a7b-9c0d1e2f3a4b")
	s := mailboxSession{oid: oid}
	events := userPath(oid, "/events")
	view := s.calendarPath("AAMkcal=", "/calendarView")
	escaped := s.calendarPath("AAMk/cal+x", "/calendarView")
	base := "https://graph.microsoft.com/v1.0"
	for _, c := range []struct{ link, sent, want string }{
		{base + events + "?$skiptoken=5", events, events + "?$skiptoken=5"},
		{base + "/users/6F1C2B3A-4D5E-4F60-8A7B-9C0D1E2F3A4B/Events?$skiptoken=5", events, events + "?$skiptoken=5"},
		{base + view + "?startDateTime=x&$skiptoken=5", view, view + "?startDateTime=x&$skiptoken=5"},
		{base + "/users/" + oid.String() + "/calendars/AAMkcal=/calendarview?$skiptoken=5", view, view + "?$skiptoken=5"},
		{base + "/users/" + oid.String() + "/calendars/AAMk%2Fcal+x/calendarView?$skiptoken=5", escaped, escaped + "?$skiptoken=5"},
	} {
		if got, ok := listPath(c.link, c.sent); !ok || got != c.want {
			t.Errorf("listPath(%q, %q) = %q %v, want %q", c.link, c.sent, got, ok, c.want)
		}
	}
	for _, c := range []struct{ link, sent string }{
		{"", events},
		{base + events, events},
		{base + events + "?", events},
		{"http://graph.microsoft.com/v1.0" + events + "?$skiptoken=5", events},
		{"https://GRAPH.microsoft.com/v1.0" + events + "?$skiptoken=5", events},
		{"https://graph.microsoft.com:443/v1.0" + events + "?$skiptoken=5", events},
		{"https://x@graph.microsoft.com/v1.0" + events + "?$skiptoken=5", events},
		{"https://graph.microsoft.com/beta" + events + "?$skiptoken=5", events},
		{base + events + "?$skiptoken=5#x", events},
		{base + events + "?$skiptoken=a b", events},
		{base + "/users/" + oid.String() + "/event%73?$skiptoken=5", events},
		{base + "/users/" + uuid.NewString() + "/events?$skiptoken=5", events},
		{base + "/users/" + oid.String() + "/calendar/events?$skiptoken=5", events},
		{base + "/users/" + oid.String() + "/events/../events?$skiptoken=5", events},
		{base + "/users/" + oid.String() + "/calendars/AAMkother=/calendarView?$skiptoken=5", view},
		{base + view + "?$skiptoken=5", events},
		{events + "?$skiptoken=5", events},
	} {
		if _, ok := listPath(c.link, c.sent); ok {
			t.Errorf("listPath(%q, %q) taken", c.link, c.sent)
		}
	}
}

// TestEventOfAnUpdateWithAKey: an update naming a Key carries the marker,
// as a create does.
func TestEventOfAnUpdateWithAKey(t *testing.T) {
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	e := &contracts.MailboxEvent{Subject: "Taken", StartsAt: start, EndsAt: start.Add(time.Hour)}
	ev, err := eventOf(contracts.MailboxOp{Kind: contracts.MailboxOpUpdate, EventID: "AAMk", Key: "appointment:7", Event: e}, testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Properties) != 1 || ev.Properties[0] != (graphProperty{ID: mailboxMarker, Value: testPrefix + "appointment:7"}) {
		t.Errorf("an update with a key: %+v, want the marker", ev.Properties)
	}
}

// TestStateOf: a marker of this installation is the key; another's — or
// this prefix with no key — is Foreign; a type of a series is Series; the
// attendees are a count.
func TestStateOf(t *testing.T) {
	at := graphDateTimeZone{DateTime: "2026-11-02T09:00:00.0000000", TimeZone: "UTC"}
	for name, c := range map[string]struct {
		ev            wholeEvent
		key           string
		foreign, more bool
	}{
		"ours":       {wholeEvent{Properties: []graphProperty{{ID: mailboxMarker, Value: testPrefix + "appointment:1"}}}, "appointment:1", false, false},
		"another's":  {wholeEvent{Properties: []graphProperty{{ID: mailboxMarker, Value: "vantigo:other:appointment:1"}}}, "", true, false},
		"no key":     {wholeEvent{Properties: []graphProperty{{ID: mailboxMarker, Value: testPrefix}}}, "", true, false},
		"none":       {wholeEvent{}, "", false, false},
		"a meeting":  {wholeEvent{Attendees: make([]struct{}, 2)}, "", false, true},
		"seriesMstr": {wholeEvent{Type: "SeriesMaster"}, "", false, false},
	} {
		c.ev.ID, c.ev.Start, c.ev.End = "AAMk", at, at
		st, ok := stateOf(c.ev, testPrefix)
		if !ok || st.Key != c.key || st.Foreign != c.foreign || st.Meeting != c.more {
			t.Errorf("%s: %+v %v", name, st, ok)
		}
		if name == "seriesMstr" && !st.Series {
			t.Errorf("a series master: %+v", st)
		}
	}
	if _, ok := stateOf(wholeEvent{ID: "AAMk", Start: graphDateTimeZone{DateTime: "never"}, End: at}, testPrefix); ok {
		t.Error("times that do not read: taken")
	}
}
