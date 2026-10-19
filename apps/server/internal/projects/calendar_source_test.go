package projects_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
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
func calendarItems(t *testing.T, h *modtest.Harness, ctx context.Context, userID uuid.UUID) []contracts.CalendarItem {
	t.Helper()
	source := projects.Module().CalendarSource(h.Deps())
	items, err := source.CalendarItems(ctx, contracts.CalendarQuery{
		UserID: userID, From: calDay(2026, 10, 1), To: calDay(2026, 10, 31), Zone: time.UTC})
	if err != nil {
		t.Fatalf("CalendarItems: %v", err)
	}
	return items
}

func TestCalendarSource_Kinds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kinds := projects.Module().CalendarSource(h.Deps()).CalendarKinds()
	if len(kinds) != 1 || kinds[0] != (contracts.CalendarKind{Key: "projects.task", Module: "projects"}) {
		t.Fatalf("kinds = %+v", kinds)
	}
}

// The caller's tasks due in the window on a project they can see, done ones
// struck; not another's, not one outside the window, not one without a due
// date, and not one on a project they hold no role on.
func TestCalendarSource_Tasks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h, "projects:access")
	_, other := h.SignInUser(t, "projects:access")

	mine := insertProjectFor(t, h, "CAL1", nil)
	addRole(t, h, mine, me, "member")
	hidden := insertProjectFor(t, h, "CAL2", nil)

	due, later, outside := calDay(2026, 10, 5), calDay(2026, 10, 6), calDay(2026, 11, 2)
	open := insertTask(t, h, mine, "Draw the plan", "todo", &me, &due, 1)
	done := insertTask(t, h, mine, "Pour the base", "done", &me, &later, 2)
	insertTask(t, h, mine, "Theirs", "todo", &other, &due, 3)
	insertTask(t, h, mine, "Next month", "todo", &me, &outside, 4)
	insertTask(t, h, mine, "Someday", "todo", &me, nil, 5)
	insertTask(t, h, hidden, "On a project I cannot see", "todo", &me, &due, 1)

	items := calendarItems(t, h, ctx, me)
	if len(items) != 2 {
		t.Fatalf("items = %+v, want two", items)
	}
	first, second := items[0], items[1]
	if first.ID != fmt.Sprint(open) || first.Kind != "projects.task" || first.Title != "Draw the plan" || first.Subtitle != "CAL1" ||
		!first.AllDay || !first.StartDate.Equal(due) || !first.EndDate.Equal(due) || first.Done ||
		first.Path != fmt.Sprintf("/projects/%d/tasks?task=%d", mine, open) {
		t.Fatalf("the open task = %+v", first)
	}
	if second.ID != fmt.Sprint(done) || !second.Done {
		t.Fatalf("the done task = %+v, want Done", second)
	}
}

// projects:view-all widens which of the caller's own tasks are visible: the
// one on a project they hold no role on comes back.
func TestCalendarSource_ViewAll(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h, "projects:access", "projects:view-all")
	hidden := insertProjectFor(t, h, "CAL3", nil)
	due := calDay(2026, 10, 5)
	task := insertTask(t, h, hidden, "Seen with view-all", "todo", &me, &due, 1)

	items := calendarItems(t, h, ctx, me)
	if len(items) != 1 || items[0].ID != fmt.Sprint(task) {
		t.Fatalf("items = %+v, want the task on the project without a role", items)
	}
}

// Without projects:access the source answers nothing, a role notwithstanding.
func TestCalendarSource_GateOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h, "projects:view-all")
	mine := insertProjectFor(t, h, "CAL4", nil)
	addRole(t, h, mine, me, "member")
	due := calDay(2026, 10, 5)
	insertTask(t, h, mine, "Mine", "todo", &me, &due, 1)

	if items := calendarItems(t, h, ctx, me); items != nil {
		t.Fatalf("items = %+v, want nothing", items)
	}
}
