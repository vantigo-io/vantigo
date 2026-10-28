package entra

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestChangeOf pins how a delta's event is read (design O9.3, O9.4): the
// times in the zone answered — UTC or an IANA name; any other zone leaves
// the event out rather than reading it as UTC —, an
// all-day event's dates rounded to the nearest midnight with the end made
// inclusive and never before the start, series by type, showAs whatever
// its case, and an event whose times do not read left out.
func TestChangeOf(t *testing.T) {
	dt := func(v, zone string) graphDateTimeZone { return graphDateTimeZone{DateTime: v, TimeZone: zone} }
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		name string
		ev   deltaEvent
		want contracts.MailboxChange
		ok   bool
	}{
		{"timed in UTC", deltaEvent{ID: "a", ShowAs: "busy", Start: dt("2026-11-03T09:00:00.0000000", "UTC"), End: dt("2026-11-03T10:30:00.0000000", "UTC")},
			contracts.MailboxChange{EventID: "a", ShowAs: "busy", StartsAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC)}, true},
		{"timed in an IANA zone", deltaEvent{ID: "b", ShowAs: "OOF", Start: dt("2026-11-03T09:00:00", "Europe/Oslo"), End: dt("2026-11-03T10:00:00", "Europe/Oslo")},
			contracts.MailboxChange{EventID: "b", ShowAs: "oof", StartsAt: time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)}, true},
		{"a zone that is no IANA name, left out", deltaEvent{ID: "c", ShowAs: "workingelsewhere", Start: dt("2026-11-03T09:00:00.0000000", "W. Europe Standard Time"), End: dt("2026-11-03T10:00:00.0000000", "W. Europe Standard Time")},
			contracts.MailboxChange{}, false},
		{"one end's zone unknown, left out", deltaEvent{ID: "c2", Start: dt("2026-11-03T09:00:00", "UTC"), End: dt("2026-11-03T10:00:00", "Mars/Olympus")},
			contracts.MailboxChange{}, false},
		{"an end before the start", deltaEvent{ID: "d", Start: dt("2026-11-03T09:00:00", "UTC"), End: dt("2026-11-03T08:00:00", "UTC")},
			contracts.MailboxChange{EventID: "d", StartsAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)}, true},
		{"all-day at midnight", deltaEvent{ID: "e", IsAllDay: true, ShowAs: "oof", Start: dt("2026-07-06T00:00:00.0000000", "UTC"), End: dt("2026-07-25T00:00:00.0000000", "UTC")},
			contracts.MailboxChange{EventID: "e", AllDay: true, ShowAs: "oof", StartDate: day(2026, 7, 6), EndDate: day(2026, 7, 24)}, true},
		{"all-day laid in Oslo, answered in UTC", deltaEvent{ID: "f", IsAllDay: true, Start: dt("2026-07-05T22:00:00.0000000", "UTC"), End: dt("2026-07-06T22:00:00.0000000", "UTC")},
			contracts.MailboxChange{EventID: "f", AllDay: true, StartDate: day(2026, 7, 6), EndDate: day(2026, 7, 6)}, true},
		{"all-day laid west of UTC", deltaEvent{ID: "g", IsAllDay: true, Start: dt("2026-07-06T07:00:00", "UTC"), End: dt("2026-07-08T07:00:00", "UTC")},
			contracts.MailboxChange{EventID: "g", AllDay: true, StartDate: day(2026, 7, 6), EndDate: day(2026, 7, 7)}, true},
		{"all-day with no length", deltaEvent{ID: "h", IsAllDay: true, Start: dt("2026-07-06T00:00:00", "UTC"), End: dt("2026-07-06T00:00:00", "UTC")},
			contracts.MailboxChange{EventID: "h", AllDay: true, StartDate: day(2026, 7, 6), EndDate: day(2026, 7, 6)}, true},
		{"an occurrence, cancelled", deltaEvent{ID: "i", Type: "occurrence", IsCancelled: true, ShowAs: "free", Start: dt("2026-11-03T09:00:00", "UTC"), End: dt("2026-11-03T10:00:00", "UTC")},
			contracts.MailboxChange{EventID: "i", Series: true, Cancelled: true, ShowAs: "free", StartsAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}, true},
		{"an exception, an unknown showAs", deltaEvent{ID: "j", Type: "Exception", ShowAs: "unknown", Start: dt("2026-11-03T09:00:00", "UTC"), End: dt("2026-11-03T10:00:00", "UTC")},
			contracts.MailboxChange{EventID: "j", Series: true, StartsAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}, true},
		{"removed", deltaEvent{ID: "k", Removed: json.RawMessage(`{"reason":"deleted"}`), ShowAs: "busy"},
			contracts.MailboxChange{EventID: "k", Removed: true}, true},
		{"times that do not read", deltaEvent{ID: "l", Start: dt("tomorrow", "UTC"), End: dt("2026-11-03T10:00:00", "UTC")}, contracts.MailboxChange{}, false},
		{"no id", deltaEvent{Start: dt("2026-11-03T09:00:00", "UTC"), End: dt("2026-11-03T10:00:00", "UTC")}, contracts.MailboxChange{}, false},
	} {
		got, ok := changeOf(c.ev)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestDeltaExpired pins the refusals read as an expired link: a 410
// whatever its code, a 4xx with a sync state's code whatever its case; not
// a 5xx, a 403 or another 400.
func TestDeltaExpired(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&graphError{Status: http.StatusGone}, true},
		{&graphError{Status: http.StatusBadRequest, Code: "SyncStateNotFound"}, true},
		{&graphError{Status: http.StatusBadRequest, Code: "syncStateInvalid"}, true},
		{&graphError{Status: http.StatusConflict, Code: "resyncRequired"}, true},
		{&graphError{Status: http.StatusBadRequest, Code: "ErrorInvalidRequest", Detail: "ErrorInvalidSyncStateData"}, true},
		{&graphError{Status: http.StatusBadRequest, Code: "ErrorInvalidRequest"}, false},
		{&graphError{Status: http.StatusForbidden, Code: "ErrorAccessDenied"}, false},
		{&graphError{Status: http.StatusServiceUnavailable, Code: "syncStateNotFound"}, false},
		{errTenantUnreachable, false},
	} {
		if got := deltaExpired(c.err); got != c.want {
			t.Errorf("deltaExpired(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// TestDeltaPath pins the link check: the session's own delta, whatever the
// case of its path, answered on the canonical path built from the oid, and
// nothing else.
func TestDeltaPath(t *testing.T) {
	oid := uuid.MustParse("6f1c2b3a-4d5e-4f60-8a7b-9c0d1e2f3a4b")
	s := mailboxSession{oid: oid}
	base := "https://graph.microsoft.com/v1.0/users/"
	want := "/users/" + oid.String() + "/calendarView/delta?$deltatoken=abc"
	for _, link := range []string{
		base + oid.String() + "/calendarView/delta?$deltatoken=abc",
		base + oid.String() + "/calendarview/delta?$deltatoken=abc",
		base + "6F1C2B3A-4D5E-4F60-8A7B-9C0D1E2F3A4B/calendarView/delta?$deltatoken=abc",
		"https://graph.microsoft.com/V1.0/USERS/6F1C2B3A-4D5E-4F60-8A7B-9C0D1E2F3A4B/CALENDARVIEW/DELTA?$deltatoken=abc",
	} {
		if path, ok := s.deltaPath(link); !ok || path != want {
			t.Errorf("deltaPath(%q) = %q %v, want %q", link, path, ok, want)
		}
	}
	for _, link := range []string{
		"",
		base + oid.String() + "/calendarView/delta",
		base + oid.String() + "/calendarView/delta?",
		"https://GRAPH.microsoft.com/v1.0/users/" + oid.String() + "/calendarView/delta?$deltatoken=abc",
		"https://graph.microsoft.com:443/v1.0/users/" + oid.String() + "/calendarView/delta?$deltatoken=abc",
		"https://x@graph.microsoft.com/v1.0/users/" + oid.String() + "/calendarView/delta?$deltatoken=abc",
		base + oid.String() + "/calendarView%2Fdelta?$deltatoken=abc",
		base + oid.String() + "/calendarView/delt%61?$deltatoken=abc",
		base + "('" + oid.String() + "')/calendarView/delta?$deltatoken=abc",
		base + uuid.NewString() + "/calendarView/delta?$deltatoken=abc",
		base + oid.String() + "/calendarView/delta?$deltatoken=abc#x",
		base + oid.String() + "/calendarView/delta?$deltatoken=a b",
		"https://graph.microsoft.com/beta/users/" + oid.String() + "/calendarView/delta?$deltatoken=abc",
		"/users/" + oid.String() + "/calendarView/delta?$deltatoken=abc",
	} {
		if _, ok := s.deltaPath(link); ok {
			t.Errorf("deltaPath(%q) taken", link)
		}
	}
}
