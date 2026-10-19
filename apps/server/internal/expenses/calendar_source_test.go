package expenses_test

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
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
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
// a viewer in Oslo: the window runs from 22:00Z on 30 September (CEST) to
// 23:00Z on 31 October (CET).
func calendarItems(t *testing.T, h *harness, ctx context.Context, userID uuid.UUID) []contracts.CalendarItem {
	t.Helper()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	source := expenses.Module().CalendarSource(h.Deps())
	items, err := source.CalendarItems(ctx, contracts.CalendarQuery{
		UserID: userID, From: calDay(2026, 10, 1), To: calDay(2026, 10, 31), Zone: oslo})
	if err != nil {
		t.Fatalf("CalendarItems: %v", err)
	}
	return items
}

func insertCalendarClaim(t *testing.T, h *harness, userID uuid.UUID, purpose string, destination *string, departure, ret time.Time, status string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `INSERT INTO expenses.claims
	    (user_id, created_by_user_id, purpose, destination, departure_at, return_at, status, created_at, updated_at)
	    VALUES ($1, $1, $2, $3, $4, $5, $6, now(), now())
	    RETURNING id`, userID, purpose, destination, departure, ret, status)
}

func TestCalendarSource_Kinds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kinds := expenses.Module().CalendarSource(h.Deps()).CalendarKinds()
	if len(kinds) != 1 || kinds[0] != (contracts.CalendarKind{Key: "expenses.claim", Module: "expenses"}) {
		t.Fatalf("kinds = %+v", kinds)
	}
}

// The caller's own claims whose journey overlaps the window in the viewer's
// zone — one that only reaches past the window's first midnight included —
// and not one that returns exactly at it, one after the window, or another's.
func TestCalendarSource_Claims(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h.Harness, "expenses:access")
	_, other := h.SignInUser(t, "expenses:access")

	windowStart := time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)
	edgeReturn := windowStart.Add(time.Hour)
	edge := insertCalendarClaim(t, h, me, "Site visit", nil, time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC), edgeReturn, "submitted")
	departs, returns := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC)
	inside := insertCalendarClaim(t, h, me, "Customer workshop", ptr("Bergen"), departs, returns, "approved")
	insertCalendarClaim(t, h, me, "Back at midnight", nil, time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC), windowStart, "draft")
	insertCalendarClaim(t, h, me, "After", nil, time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC), "draft")
	insertCalendarClaim(t, h, other, "Theirs", nil, departs, returns, "draft")

	items := calendarItems(t, h, ctx, me)
	if len(items) != 2 {
		t.Fatalf("items = %+v, want two", items)
	}
	first, second := items[0], items[1]
	if first.ID != strconv.FormatInt(edge, 10) || first.Kind != "expenses.claim" || first.AllDay ||
		!first.EndsAt.Equal(edgeReturn) || first.Done || first.Subtitle != "" {
		t.Fatalf("the claim over the window's edge = %+v", first)
	}
	if second.ID != strconv.FormatInt(inside, 10) || second.Title != "Customer workshop" || second.Subtitle != "Bergen" ||
		!second.StartsAt.Equal(departs) || !second.EndsAt.Equal(returns) || !second.Done ||
		second.Path != "/expenses/claims/"+strconv.FormatInt(inside, 10) {
		t.Fatalf("the approved claim = %+v", second)
	}
}

// Without expenses:access the source answers nothing.
func TestCalendarSource_GateOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h.Harness)
	insertCalendarClaim(t, h, me, "Mine", nil, time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC), "draft")
	if items := calendarItems(t, h, ctx, me); items != nil {
		t.Fatalf("items = %+v, want nothing", items)
	}
}
