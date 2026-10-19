package timetracking_test

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
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
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

// calendarItems asks the module's own calendar source for October 2026, from
// a viewer in Tokyo — a zone the entries' blocks must not be read in.
func calendarItems(t *testing.T, h *harness, ctx context.Context, userID uuid.UUID) []contracts.CalendarItem {
	t.Helper()
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	source := timetracking.Module().CalendarSource(h.Deps())
	items, err := source.CalendarItems(ctx, contracts.CalendarQuery{
		UserID: userID, From: calDay(2026, 10, 1), To: calDay(2026, 10, 31), Zone: tokyo})
	if err != nil {
		t.Fatalf("CalendarItems: %v", err)
	}
	return items
}

// insertCalendarEntry writes one entry; start and end are wall-clock "HH:MM"
// or "" for none.
func insertCalendarEntry(t *testing.T, h *harness, userID uuid.UUID, projectID int32, date, start, end, hours, status string, taskTitle, note *string) int64 {
	t.Helper()
	clock := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO time.entries (user_id, project_id, entry_date, start_time, end_time, hours, task_title, note,
		                          billable, rate_source, status, created_at, updated_at)
		VALUES ($1, $2, $3::date, $4::time, $5::time, $6::numeric, $7, $8, false, 'none', $9, now(), now())
		RETURNING id`, userID, projectID, date, clock(start), clock(end), hours, taskTitle, note, status)
}

func TestCalendarSource_Kinds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kinds := timetracking.Module().CalendarSource(h.Deps()).CalendarKinds()
	if len(kinds) != 1 || kinds[0] != (contracts.CalendarKind{Key: "time.entry", Module: "time"}) {
		t.Fatalf("kinds = %+v", kinds)
	}
}

// The caller's own entries in the window: a block from the wall-clock pair in
// the business zone (New York, not the viewer's Tokyo), all-day without a pair
// or with an end before the start; titled by the project's name, else the
// task's title; and not another's, nor one outside the window.
func TestCalendarSource_Entries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.SetBusinessTimeZone(t, "America/New_York")
	ctx, me := calendarCaller(t, h.Harness, "time:access")
	_, other := h.SignInUser(t, "time:access")

	block := insertCalendarEntry(t, h, me, projectKraftVerket, "2026-10-05", "09:00", "11:30", "2.50", "approved", nil, nil)
	allDay := insertCalendarEntry(t, h, me, projectInternal, "2026-10-06", "", "", "7.50", "draft", nil, ptr("Planning\nand more"))
	backwards := insertCalendarEntry(t, h, me, 9999, "2026-10-07", "15:00", "14:00", "1.00", "submitted", ptr("Write the report"), nil)
	insertCalendarEntry(t, h, other, projectKraftVerket, "2026-10-05", "09:00", "10:00", "1.00", "draft", nil, nil)
	insertCalendarEntry(t, h, me, projectKraftVerket, "2026-11-02", "09:00", "10:00", "1.00", "draft", nil, nil)

	items := calendarItems(t, h, ctx, me)
	if len(items) != 3 {
		t.Fatalf("items = %+v, want three", items)
	}
	first, second, third := items[0], items[1], items[2]
	// 09:00 and 11:30 on 5 October in New York (EDT, UTC-4).
	if first.ID != strconv.FormatInt(block, 10) || first.Kind != "time.entry" || first.AllDay ||
		!first.StartsAt.Equal(time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)) ||
		!first.EndsAt.Equal(time.Date(2026, 10, 5, 15, 30, 0, 0, time.UTC)) ||
		first.Title != projectKraftVerketName || first.Subtitle != "2.5 h" || !first.Done ||
		first.Path != "/time/day?date=2026-10-05" {
		t.Fatalf("the block = %+v", first)
	}
	if second.ID != strconv.FormatInt(allDay, 10) || !second.AllDay || !second.StartDate.Equal(calDay(2026, 10, 6)) ||
		!second.EndDate.Equal(calDay(2026, 10, 6)) || second.Title != "Internt arbeid" || second.Subtitle != "7.5 h" || second.Done {
		t.Fatalf("the all-day entry = %+v", second)
	}
	if third.ID != strconv.FormatInt(backwards, 10) || !third.AllDay || third.Title != "Write the report" {
		t.Fatalf("the entry whose end is before its start = %+v", third)
	}
}

// Without time:access the source answers nothing.
func TestCalendarSource_GateOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h.Harness)
	insertCalendarEntry(t, h, me, projectKraftVerket, "2026-10-05", "09:00", "10:00", "1.00", "draft", nil, nil)
	if items := calendarItems(t, h, ctx, me); items != nil {
		t.Fatalf("items = %+v, want nothing", items)
	}
}
