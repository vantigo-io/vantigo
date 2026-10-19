package staff_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// calendarCaller signs in a user holding permissions and answers the context
// a provider is called with (calendar design D1): the request the calendar's
// handler is answering, carrying that user's session cookie, and the principal
// the router resolved for it — contracts.HasPermission reads the request and
// asks identity's Access about its cookie. modtest's own session is in the
// client's jar, out of reach, so a second session is minted for the same user
// the way modtest mints one (identity/tokens.go's shape: the base64url text of
// 32 random bytes, the row storing their SHA-256).
func calendarCaller(t *testing.T, h *modtest.Harness, permissions ...string) (context.Context, uuid.UUID) {
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

func calDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// calendarItems asks the module's own calendar source for October 2026.
func calendarItems(t *testing.T, h *harness, ctx context.Context, userID uuid.UUID) []contracts.CalendarItem {
	t.Helper()
	source := staff.Module().CalendarSource(h.Deps())
	items, err := source.CalendarItems(ctx, contracts.CalendarQuery{
		UserID: userID, From: calDay(2026, 10, 1), To: calDay(2026, 10, 31), Zone: time.UTC})
	if err != nil {
		t.Fatalf("CalendarItems: %v", err)
	}
	return items
}

// insertCalendarAbsence writes one absence of the type keyed typeKey.
func insertCalendarAbsence(t *testing.T, h *harness, person int64, typeKey, first, last, status string, hours *float64, by uuid.UUID) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.absences (person_id, type_id, first_day, last_day, hours, status, requested_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now()) RETURNING id`,
		person, typeID(t, h, typeKey), first, last, hours, status, by)
}

func TestCalendarSource_Kinds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kinds := staff.Module().CalendarSource(h.Deps()).CalendarKinds()
	if len(kinds) != 1 || kinds[0] != (contracts.CalendarKind{Key: "staff.absence", Module: "staff"}) {
		t.Fatalf("kinds = %+v", kinds)
	}
}

// The linked person's own absences overlapping the window — requested,
// approved and registered, an HR-only type's included, an hour type's hours in
// the subtitle — and not a rejected one, one outside the window or another
// person's.
func TestCalendarSource_OwnAbsences(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h.Harness, "staff:access")
	kari := seedPerson(t, h, "Kari", "Nordmann")
	link(t, h, kari, me)
	ola := seedPerson(t, h, "Ola", "Nordmann")

	edge := insertCalendarAbsence(t, h, kari, "ferie", "2026-09-28", "2026-10-02", "approved", nil, me)
	requested := insertCalendarAbsence(t, h, kari, "ferie", "2026-10-12", "2026-10-16", "requested", nil, me)
	three := 3.0
	hourly := insertCalendarAbsence(t, h, kari, "svangerskapskontroll", "2026-10-20", "2026-10-20", "registered", &three, me)
	insertCalendarAbsence(t, h, kari, "ferie", "2026-10-26", "2026-10-27", "rejected", nil, me)
	insertCalendarAbsence(t, h, kari, "ferie", "2026-11-02", "2026-11-03", "approved", nil, me)
	insertCalendarAbsence(t, h, ola, "ferie", "2026-10-05", "2026-10-06", "approved", nil, me)

	items := calendarItems(t, h, ctx, me)
	if len(items) != 3 {
		t.Fatalf("items = %+v, want three", items)
	}
	first, second, third := items[0], items[1], items[2]
	if first.ID != idText(edge) || first.Kind != "staff.absence" || first.Title != "Ferie" || first.Subtitle != "" ||
		!first.AllDay || !first.StartDate.Equal(calDay(2026, 9, 28)) || !first.EndDate.Equal(calDay(2026, 10, 2)) ||
		first.Path != "/staff/me?tab=absence" {
		t.Fatalf("the approved absence over the window's edge = %+v", first)
	}
	if second.ID != idText(requested) || second.Subtitle != "Søkt om" {
		t.Fatalf("the requested absence = %+v", second)
	}
	if third.ID != idText(hourly) || third.Title != "Svangerskapskontroll" || third.Subtitle != "3 h" {
		t.Fatalf("the hour type = %+v", third)
	}

	// The type is named in the person's language.
	h.Exec(t, `UPDATE staff.people SET preferred_language = 'en' WHERE id = $1`, kari)
	items = calendarItems(t, h, ctx, me)
	if items[0].Title != "Holiday" || items[1].Subtitle != "Requested" {
		t.Fatalf("in English = %+v", items)
	}
}

// A caller linked to nobody has no absences to show, and that is no error.
func TestCalendarSource_NoLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h.Harness, "staff:access")
	if items := calendarItems(t, h, ctx, me); items != nil {
		t.Fatalf("items = %+v, want nothing", items)
	}
}

// Without staff:access the source answers nothing, the link notwithstanding.
func TestCalendarSource_GateOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h.Harness)
	kari := seedPerson(t, h, "Kari", "Nordmann")
	link(t, h, kari, me)
	insertCalendarAbsence(t, h, kari, "ferie", "2026-10-05", "2026-10-06", "approved", nil, me)
	if items := calendarItems(t, h, ctx, me); items != nil {
		t.Fatalf("items = %+v, want nothing", items)
	}
}

func idText(id int64) string { return strconv.FormatInt(id, 10) }
