package calendar_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The CalendarBusy provider (calendar Outlook sync design O9.8): gated by the
// calendar's own colleague rule, bounded, and merged.

// busyCaller is the context another module's handler calls CalendarBusy with
// for userID (Staff's calendarCaller): a request carrying a session cookie of
// userID's, minted the way modtest mints one, and the principal the router
// resolved for it.
func busyCaller(t *testing.T, h *harness, userID uuid.UUID) context.Context {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	h.Exec(t, `INSERT INTO identity.sessions (id, user_id, token_hash, persistent, created_at, last_seen_at)
	           VALUES ($1, $2, $3, false, $4, $4)`, uuid.New(), userID, hash[:], h.Now())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/planning/board", nil)
	r.AddCookie(&http.Cookie{Name: "vantigo.session", Value: base64.RawURLEncoding.EncodeToString(raw)})
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: userID})
	return contracts.WithRequest(ctx, httptest.NewRecorder(), r)
}

func provider(h *harness) contracts.CalendarBusy {
	return calendar.Module().CalendarBusy(h.Deps())
}

func TestCalendarBusy_MergesAppointmentsAndOutlookBusyTimeForWhomTheCallerMaySee(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	colleague, colleagueID, mb := r.person(t)
	_, viewerID := signIn(t, r.harness, "calendar:view-colleagues")
	plain, plainID := signIn(t, r.harness)

	create(t, colleague, timed(t, "A", oslo, "2026-11-03T09:00", "2026-11-03T10:00"))
	create(t, colleague, timed(t, "B", oslo, "2026-11-03T09:30", "2026-11-03T11:00"))
	create(t, colleague, timed(t, "C", oslo, "2026-11-03T11:00", "2026-11-03T12:00"))
	create(t, colleague, with(timed(t, "Focus", oslo, "2026-11-03T13:00", "2026-11-03T14:00"), "transparency", "free"))
	create(t, colleague, allDay("Away", oslo, "2026-11-02", "2026-11-03"))
	create(t, colleague, allDay("Away too", oslo, "2026-11-04", "2026-11-04"))
	weekly := create(t, colleague, with(timed(t, "Weekly", oslo, "2026-11-02T08:00", "2026-11-02T08:30"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY;COUNT=3"}))
	do(t, colleague, http.MethodPut, occurrencePath(weekly.Id, "2026-11-09"), map[string]any{"cancelled": true}, http.StatusOK, nil)
	create(t, colleague, timed(t, "Outside", oslo, "2026-12-03T09:00", "2026-12-03T10:00"))
	r.box.SeedForeign(mb, foreign(t, "ev-busy", "2026-11-05T10:00", "2026-11-05T11:00", contracts.ShowAsBusy))
	r.box.SeedForeign(mb, foreign(t, "ev-tent", "2026-11-05T12:00", "2026-11-05T13:00", contracts.ShowAsTentative))
	r.box.SeedForeign(mb, modtest.FakeBusyEvent{ID: "ev-oof", AllDay: true, StartDate: day("2026-11-10"), EndDate: day("2026-11-11"), ShowAs: contracts.ShowAsOOF})
	on := turnOn(t, colleague, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	create(t, plain, timed(t, "Mine", oslo, "2026-11-06T09:00", "2026-11-06T10:00"))

	from, to := day("2026-11-01"), day("2026-11-30")
	busy := provider(r.harness)

	// No request: nothing, and no error.
	got, err := busy.Busy(context.Background(), []uuid.UUID{colleagueID}, from, to)
	if err != nil || len(got) != 0 {
		t.Fatalf("with no request: %+v, %v", got, err)
	}

	// A holder of calendar:view-colleagues sees the colleague's.
	got, err = busy.Busy(busyCaller(t, r.harness, viewerID), []uuid.UUID{colleagueID, viewerID, colleagueID}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[viewerID]; ok || len(got) != 1 {
		t.Errorf("answered %v: the viewer is busy at no time", got)
	}
	want := contracts.BusyTime{
		Spans: []contracts.TimeSpan{
			{StartsAt: at(t, oslo, "2026-11-02T08:00").UTC(), EndsAt: at(t, oslo, "2026-11-02T08:30").UTC()},
			{StartsAt: at(t, oslo, "2026-11-03T09:00").UTC(), EndsAt: at(t, oslo, "2026-11-03T12:00").UTC()},
			{StartsAt: at(t, oslo, "2026-11-05T10:00").UTC(), EndsAt: at(t, oslo, "2026-11-05T11:00").UTC()},
			{StartsAt: at(t, oslo, "2026-11-16T08:00").UTC(), EndsAt: at(t, oslo, "2026-11-16T08:30").UTC()},
		},
		Days: []contracts.DateSpan{{From: day("2026-11-02"), To: day("2026-11-04")}, {From: day("2026-11-10"), To: day("2026-11-11")}},
	}
	if c := got[colleagueID]; !slices.EqualFunc(c.Spans, want.Spans, func(a, b contracts.TimeSpan) bool {
		return a.StartsAt.Equal(b.StartsAt) && a.EndsAt.Equal(b.EndsAt)
	}) || !slices.EqualFunc(c.Days, want.Days, func(a, b contracts.DateSpan) bool {
		return a.From.Equal(b.From) && a.To.Equal(b.To)
	}) {
		t.Errorf("the colleague's busy time = %+v,\nwant %+v", c, want)
	}

	// Clipped to the window.
	got, err = busy.Busy(busyCaller(t, r.harness, viewerID), []uuid.UUID{colleagueID}, day("2026-11-03"), day("2026-11-03"))
	if c := got[colleagueID]; err != nil || len(c.Spans) != 1 || len(c.Days) != 1 || !c.Days[0].From.Equal(day("2026-11-03")) || !c.Days[0].To.Equal(day("2026-11-03")) {
		t.Errorf("one day: %+v, %v", c, err)
	}

	// Without the key, a colleague is absent and one's own is answered.
	got, err = busy.Busy(busyCaller(t, r.harness, plainID), []uuid.UUID{colleagueID, plainID}, from, to)
	if err != nil || len(got) != 1 || len(got[plainID].Spans) != 1 {
		t.Errorf("without calendar:view-colleagues: %+v, %v", got, err)
	}

	// The colleague's sync turned off: their Outlook time is not counted.
	putSync(t, colleague, on.Revision, syncBody(false, "default", "full", "calendar.appointment"))
	got, err = busy.Busy(busyCaller(t, r.harness, viewerID), []uuid.UUID{colleagueID}, from, to)
	if c := got[colleagueID]; err != nil || len(c.Spans) != 3 || len(c.Days) != 1 {
		t.Errorf("with the sync off: %+v, %v", c, err)
	}
}

func TestCalendarBusy_RefusesAWindowOutOfBounds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, me := signIn(t, h)
	ctx := busyCaller(t, h, me)
	many := make([]uuid.UUID, contracts.CalendarBusyMaxUsers+1)
	for i := range many {
		many[i] = uuid.New()
	}
	from := day("2026-11-01")
	for name, tc := range map[string]struct {
		ids      []uuid.UUID
		from, to time.Time
	}{
		"too many users": {many, from, from},
		"too many days":  {[]uuid.UUID{me}, from, from.AddDate(0, 0, contracts.CalendarBusyMaxDays)},
		"inverted":       {[]uuid.UUID{me}, from, from.AddDate(0, 0, -1)},
	} {
		if _, err := provider(h).Busy(ctx, tc.ids, tc.from, tc.to); !errors.Is(err, contracts.ErrBusyWindowOutOfBounds) {
			t.Errorf("%s: %v, want ErrBusyWindowOutOfBounds", name, err)
		}
	}
	if _, err := provider(h).Busy(ctx, many[:contracts.CalendarBusyMaxUsers], from, from.AddDate(0, 0, contracts.CalendarBusyMaxDays-1)); err != nil {
		t.Errorf("at the bounds: %v", err)
	}
}

func TestCalendarBusy_AnswersNothingToACallerWithoutCalendar(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, me := signIn(t, h)
	create(t, c, timed(t, "Mine", oslo, "2026-11-06T09:00", "2026-11-06T10:00"))
	_, outsider := h.SignInUser(t)
	got, err := provider(h).Busy(busyCaller(t, h, outsider), []uuid.UUID{me, outsider}, day("2026-11-01"), day("2026-11-30"))
	if err != nil || len(got) != 0 {
		t.Errorf("a caller without calendar:access: %+v, %v", got, err)
	}
}

// A read on someone's behalf — the AsUser mark, even over a request — is
// answered nothing: the provider-gated read is for a request's own
// principal alone.
func TestCalendarBusy_AnswersNothingUnderAsUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, me := signIn(t, h, "calendar:view-colleagues")
	create(t, c, timed(t, "Mine", oslo, "2026-11-06T09:00", "2026-11-06T10:00"))
	from, to := day("2026-11-01"), day("2026-11-30")
	if got, err := provider(h).Busy(busyCaller(t, h, me), []uuid.UUID{me}, from, to); err != nil || len(got) != 1 {
		t.Fatalf("the request's own: %+v, %v", got, err)
	}
	for name, ctx := range map[string]context.Context{
		"AsUser alone":          contracts.AsUser(context.Background(), me),
		"AsUser over a request": contracts.AsUser(busyCaller(t, h, me), me),
	} {
		if got, err := provider(h).Busy(ctx, []uuid.UUID{me}, from, to); err != nil || len(got) != 0 {
			t.Errorf("%s: %+v, %v, want nothing", name, got, err)
		}
	}
}

// A user who is not active in the directory is busy at no time.
func TestCalendarBusy_LeavesOutAUserWhoIsNotActive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	colleague, colleagueID := signIn(t, h)
	create(t, colleague, timed(t, "Theirs", oslo, "2026-11-06T09:00", "2026-11-06T10:00"))
	_, viewerID := signIn(t, h, "calendar:view-colleagues")
	from, to := day("2026-11-01"), day("2026-11-30")
	if got, err := provider(h).Busy(busyCaller(t, h, viewerID), []uuid.UUID{colleagueID}, from, to); err != nil || len(got) != 1 {
		t.Fatalf("an active colleague: %+v, %v", got, err)
	}
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, colleagueID)
	if got, err := provider(h).Busy(busyCaller(t, h, viewerID), []uuid.UUID{colleagueID}, from, to); err != nil || len(got) != 0 {
		t.Errorf("a disabled colleague: %+v, %v, want nothing", got, err)
	}
}
