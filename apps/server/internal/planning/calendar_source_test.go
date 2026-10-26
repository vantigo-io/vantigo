package planning_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/planning"
)

// calendarCaller signs in a user holding permissions and answers the context
// a provider is called with (calendar design D1): the request the calendar's
// handler is answering, carrying that user's session cookie, and the
// principal the router resolved for it — contracts.HasPermission reads the
// request and asks identity's Access about its cookie. modtest's own session
// is in the client's jar, out of reach, so a second session is minted for
// the same user the way modtest mints one — Staff's test helper
// (internal/staff/calendar_source_test.go), copied because no module's tests
// import another's.
func calendarCaller(t *testing.T, h *harness, permissions ...string) (context.Context, uuid.UUID) {
	t.Helper()
	_, userID := h.SignInUser(t, permissions...)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	h.Exec(t, `INSERT INTO identity.sessions (id, user_id, token_hash, persistent, created_at, last_seen_at)
	           VALUES ($1, $2, $3, false, $4, $4)`, uuid.New(), userID, hash[:], h.Now())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/calendar/items", nil)
	r.AddCookie(&http.Cookie{Name: "vantigo.session", Value: base64.RawURLEncoding.EncodeToString(raw)})
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: userID})
	return contracts.WithRequest(ctx, httptest.NewRecorder(), r), userID
}

// withCalendar is the harness's Deps with the calendar module on: the
// harness's MODULES never carries it, and the source is built only when it
// is (S25).
func withCalendar(h *harness) module.Deps {
	d := h.Deps()
	cfg := *d.Config
	cfg.Modules = append(slices.Clone(cfg.Modules), "calendar")
	d.Config = &cfg
	return d
}

// calendarItems asks the module's own calendar source for October 2026.
func calendarItems(t *testing.T, h *harness, ctx context.Context, userID uuid.UUID) []contracts.CalendarItem {
	t.Helper()
	items, err := planning.Module().CalendarSource(withCalendar(h)).CalendarItems(ctx, contracts.CalendarQuery{
		UserID: userID, From: date("2026-10-01"), To: date("2026-10-31"), Zone: time.UTC})
	if err != nil {
		t.Fatalf("CalendarItems: %v", err)
	}
	return items
}

// TestCalendarSource_Switch is S25: the source is collected only when the
// calendar module is on — nil, contributing nothing, otherwise.
func TestCalendarSource_Switch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if source := planning.Module().CalendarSource(h.Deps()); source != nil {
		t.Errorf("without calendar = %#v, want nil", source)
	}
	if source := planning.Module().CalendarSource(withCalendar(h)); source == nil {
		t.Error("with calendar = nil, want the source")
	}
}

func TestCalendarSource_Kinds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kinds := planning.Module().CalendarSource(withCalendar(h)).CalendarKinds()
	if len(kinds) != 1 || kinds[0] != (contracts.CalendarKind{Key: "planning.allocation", Module: "planning"}) {
		t.Fatalf("kinds = %+v", kinds)
	}
}

// TestCalendarSource_OwnConfirmedRows is S24: the caller's own confirmed
// rows touching the window, all day over their period, titled with the
// project's code and name — by its id when unresolved — subtitled with the
// load in figures (reading 7), linking to My planning; not a tentative row,
// a colleague's or one outside the window. The same under contracts.AsUser,
// as the feed asks.
func TestCalendarSource_OwnConfirmedRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h, "planning:access")
	h.workforce.link(me, personKari)
	edge := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-09-28", to: "2026-10-02", load: 50})
	hours := seedAllocation(t, h, seedRow{person: personKari, project: projectUnknown, from: "2026-10-12", to: "2026-10-16", unit: "hours_per_week", load: 22.5})
	seedAllocation(t, h, seedRow{person: personKari, project: projectPlanned, from: "2026-10-05", to: "2026-10-09", load: 50, status: "tentative"})
	seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: "2026-10-05", to: "2026-10-09", load: 50})
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-11-02", to: "2026-11-06", load: 50})

	for name, ctx := range map[string]context.Context{"a request": ctx, "AsUser": contracts.AsUser(context.Background(), me)} {
		items := calendarItems(t, h, ctx, me)
		if len(items) != 2 {
			t.Fatalf("%s: items = %+v, want two", name, items)
		}
		first, second := items[0], items[1]
		if first.ID != strconv.FormatInt(edge, 10) || first.Kind != "planning.allocation" || first.Title != "KVEM1000 Kraft-Verket modernisering" ||
			first.Subtitle != "50 %" || !first.AllDay || !first.StartDate.Equal(date("2026-09-28")) || !first.EndDate.Equal(date("2026-10-02")) ||
			first.Path != "/planning/me" {
			t.Errorf("%s: the row over the window's edge = %+v", name, first)
		}
		if second.ID != strconv.FormatInt(hours, 10) || second.Title != "project 2999" || second.Subtitle != "22.5 h" {
			t.Errorf("%s: the hours row on an unresolved project = %+v", name, second)
		}
	}
}

// TestCalendarSource_Gates is S24's gate: without planning:access, or
// without a person, the source answers nothing, and that is no error.
func TestCalendarSource_Gates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-09", load: 50})

	ctx, me := calendarCaller(t, h)
	h.workforce.link(me, personKari)
	if items := calendarItems(t, h, ctx, me); items != nil {
		t.Errorf("without planning:access = %+v, want nothing", items)
	}
	if items := calendarItems(t, h, contracts.AsUser(context.Background(), me), me); items != nil {
		t.Errorf("without planning:access under AsUser = %+v, want nothing", items)
	}
	ctx, nobody := calendarCaller(t, h, "planning:access")
	if items := calendarItems(t, h, ctx, nobody); items != nil {
		t.Errorf("without a person = %+v, want nothing", items)
	}
}
