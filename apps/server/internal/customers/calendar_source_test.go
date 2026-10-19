package customers_test

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
	"github.com/vantigo-io/vantigo/server/internal/customers"
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

// calendarEntry is one timeline entry as the calendar tests need it written.
type calendarEntry struct {
	customerID int32
	eventType  string
	summary    string
	occurredOn time.Time
	occurredAt *time.Time
	actor      *uuid.UUID
	followUpOn *time.Time
	assignee   *uuid.UUID
	doneAt     *time.Time
}

func insertCalendarEntry(t *testing.T, h *modtest.Harness, e calendarEntry) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO customers.customers_timeline_entries (
			customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
			payload_version, current_revision, state, actor_kind, actor_display, created_at, updated_at,
			actor_user_id, follow_up_on, follow_up_assignee_user_id, follow_up_done_at)
		VALUES ($1, 'manual', 'customers.api', $2, $3, $4, $5, $5, 1, 1, 'active', 'user', 'Someone', $6, $6,
		        $7, $8, $9, $10)
		RETURNING id`,
		e.customerID, e.eventType, e.occurredOn, e.occurredAt, e.summary, h.Now(),
		e.actor, e.followUpOn, e.assignee, e.doneAt)
}

func calDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func calendarItems(t *testing.T, h *modtest.Harness, ctx context.Context, userID uuid.UUID, from, to time.Time) []contracts.CalendarItem {
	t.Helper()
	source := customers.Module().CalendarSource(h.Deps())
	items, err := source.CalendarItems(ctx, contracts.CalendarQuery{UserID: userID, From: from, To: to, Zone: time.UTC})
	if err != nil {
		t.Fatalf("CalendarItems: %v", err)
	}
	return items
}

func TestCalendarSource_Kinds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kinds := customers.Module().CalendarSource(h.Deps()).CalendarKinds()
	if len(kinds) != 2 || kinds[0].Key != "customers.follow-up" || kinds[1].Key != "customers.interaction" ||
		kinds[0].Module != "customers" || kinds[1].Module != "customers" {
		t.Fatalf("kinds = %+v", kinds)
	}
}

// The follow-ups assigned to the caller in the window, done ones struck, and
// not another's, nor one outside the window, nor one on a customer merged away.
func TestCalendarSource_FollowUps(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h, "customers:view", "customers:timeline-view")
	_, other := h.SignInUser(t)

	live := insertCustomer(t, h, "Live Co", "active")
	merged := insertCustomer(t, h, "Merged Co", "archived")
	h.Exec(t, `UPDATE customers.customers SET merged_into_customer_id = $1 WHERE id = $2`, live, merged)

	on := calDay(2026, 10, 2)
	open := insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "note", summary: "Call back about the offer",
		occurredOn: calDay(2026, 9, 20), followUpOn: &on, assignee: &me})
	doneAt := h.Now()
	done := insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "note", summary: "Send the contract",
		occurredOn: calDay(2026, 9, 20), followUpOn: &on, assignee: &me, doneAt: &doneAt})
	insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "note", summary: "Theirs",
		occurredOn: calDay(2026, 9, 20), followUpOn: &on, assignee: &other})
	late := calDay(2026, 11, 2)
	insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "note", summary: "Outside",
		occurredOn: calDay(2026, 9, 20), followUpOn: &late, assignee: &me})
	insertCalendarEntry(t, h, calendarEntry{customerID: merged, eventType: "note", summary: "Merged away",
		occurredOn: calDay(2026, 9, 20), followUpOn: &on, assignee: &me})

	items := calendarItems(t, h, ctx, me, calDay(2026, 10, 1), calDay(2026, 10, 31))
	if len(items) != 2 {
		t.Fatalf("items = %+v, want the open and the done follow-up", items)
	}
	byID := map[string]contracts.CalendarItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	o, ok := byID[calID(open)]
	if !ok || o.Kind != "customers.follow-up" || o.Title != "Call back about the offer" || o.Subtitle != "Live Co" ||
		!o.AllDay || !o.StartDate.Equal(on) || !o.EndDate.Equal(on) || o.Done || o.Path != "/customers/"+calID(live) {
		t.Fatalf("open follow-up = %+v", o)
	}
	if d, ok := byID[calID(done)]; !ok || !d.Done {
		t.Fatalf("done follow-up = %+v, want Done", d)
	}
}

// The caller's own interactions in the window: a point at occurred_at when it
// is set, all-day on occurred_on when not; not another's, nor a plain note.
func TestCalendarSource_Interactions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h, "customers:view", "customers:timeline-view")
	_, other := h.SignInUser(t)
	live := insertCustomer(t, h, "Live Co", "active")

	at := time.Date(2026, 10, 5, 8, 30, 0, 0, time.UTC)
	timed := insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "interaction.meeting", summary: "Kick-off",
		occurredOn: calDay(2026, 10, 5), occurredAt: &at, actor: &me})
	allDay := insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "interaction.call", summary: "Rang them",
		occurredOn: calDay(2026, 10, 6), actor: &me})
	insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "interaction.email", summary: "Theirs",
		occurredOn: calDay(2026, 10, 6), actor: &other})
	insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "note", summary: "A note",
		occurredOn: calDay(2026, 10, 6), actor: &me})

	items := calendarItems(t, h, ctx, me, calDay(2026, 10, 1), calDay(2026, 10, 31))
	if len(items) != 2 {
		t.Fatalf("items = %+v, want two interactions", items)
	}
	first, second := items[0], items[1]
	if first.ID != calID(timed) || first.Kind != "customers.interaction" || first.AllDay ||
		!first.StartsAt.Equal(at) || !first.EndsAt.Equal(at) || first.Title != "Kick-off" || first.Subtitle != "Live Co" {
		t.Fatalf("timed interaction = %+v", first)
	}
	if second.ID != calID(allDay) || !second.AllDay || !second.StartDate.Equal(calDay(2026, 10, 6)) || !second.StartsAt.IsZero() {
		t.Fatalf("all-day interaction = %+v", second)
	}
}

// Without the timeline pair the source answers nothing, and no error.
func TestCalendarSource_GateOff(t *testing.T) {
	t.Parallel()
	// The gate is the timeline pair: either key alone answers nothing.
	for _, held := range [][]string{{"customers:view"}, {"customers:timeline-view"}} {
		h := newHarness(t)
		ctx, me := calendarCaller(t, h, held...)
		live := insertCustomer(t, h, "Live Co", "active")
		on := calDay(2026, 10, 2)
		insertCalendarEntry(t, h, calendarEntry{customerID: live, eventType: "interaction.call", summary: "Mine",
			occurredOn: on, actor: &me, followUpOn: &on, assignee: &me})

		if items := calendarItems(t, h, ctx, me, calDay(2026, 10, 1), calDay(2026, 10, 31)); items != nil {
			t.Fatalf("with %v held, items = %+v, want nothing", held, items)
		}
	}
}

// An anonymised customer's follow-ups are on nobody's calendar, as a merged-away
// customer's are.
func TestCalendarSource_AnonymisedCustomerAbsent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, me := calendarCaller(t, h, "customers:view", "customers:timeline-view")
	gone := insertCustomer(t, h, "Gone Co", "archived")
	h.Exec(t, `UPDATE customers.customers SET anonymised_at = now() WHERE id = $1`, gone)
	on := calDay(2026, 10, 2)
	insertCalendarEntry(t, h, calendarEntry{customerID: gone, eventType: "note", summary: "Anonymised",
		occurredOn: calDay(2026, 9, 20), followUpOn: &on, assignee: &me})

	if items := calendarItems(t, h, ctx, me, calDay(2026, 10, 1), calDay(2026, 10, 31)); len(items) != 0 {
		t.Fatalf("items = %+v, want nothing", items)
	}
}

func calID(id int32) string { return fmt.Sprint(id) }
