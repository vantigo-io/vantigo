package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Calendar sync with Google Calendar through the calendar's own worker
// (Google Workspace design G30–G32, delivery E): the real calendar and google
// modules composed together, the calendar picking Google's MailboxCalendar
// by the person's directory account as cmd/vantigo does, against the fake
// Google. Staff is the one imitation — DirectoryAccount answers each person's
// Google account — and the person's connection is seeded as a completed
// connect leaves it, the flow itself being google's own suite's.

// The fake organisation's web client.
const (
	gcalWebClient = "123456789012-integration.apps.googleusercontent.com"
	gcalSecret    = "GOCSPX-integration-secret"
	gcalPublic    = "https://public.example.com"
	gcalOslo      = "Europe/Oslo"
)

// gcalRecorder validates every exchange against the calendar's and google's
// contracts together.
var gcalRecorder = mergedRecorder("calendar", "google")

// gcalScopes are what a connect for the calendar is granted.
var gcalScopes = []string{"email", "https://www.googleapis.com/auth/calendar.app.created", "https://www.googleapis.com/auth/calendar.events", "openid", "profile"}

// gcalWorkforce answers each signed-in person's directory account.
type gcalWorkforce struct {
	contracts.Workforce
	mu       sync.Mutex
	accounts map[uuid.UUID]contracts.DirectoryAccount
}

func (w *gcalWorkforce) DirectoryAccount(ctx context.Context) (*contracts.DirectoryAccount, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok {
		return nil, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	a, ok := w.accounts[p.UserID]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

// gcalRig is the installation: calendar and google composed, the fake
// Google its transport, Connection for people on.
type gcalRig struct {
	*modtest.Harness
	g  *googletest.FakeGoogle
	wf *gcalWorkforce
	n  int
}

func newGcalRig(t *testing.T) *gcalRig {
	t.Helper()
	g := googletest.New()
	wf := &gcalWorkforce{accounts: map[uuid.UUID]contracts.DirectoryAccount{}}
	h := modtest.New(t,
		modtest.WithRecorder(gcalRecorder),
		modtest.WithModule(calendar.Module()),
		modtest.WithModule(google.Module()),
		modtest.WithWorkforce(wf),
		modtest.WithTransport(g),
		modtest.WithBackoff(func(int) time.Duration { return 0 }),
		modtest.WithEnv("PUBLIC_BASE_URL", gcalPublic),
	)
	g.Configure(func(g *googletest.FakeGoogle) { g.Now = h.Now })
	h.SetBusinessTimeZone(t, gcalOslo)
	g.AddWebClient(gcalWebClient, gcalSecret)
	sealed, err := h.Deps().Secrets.Seal("google/oauth-client-secret", []byte(gcalSecret))
	if err != nil {
		t.Fatal(err)
	}
	h.Exec(t, `INSERT INTO google.credentials (kind, sealed, created_at) VALUES ('oauth_client_secret', $1, $2)`, sealed, h.Now())
	h.Exec(t, `UPDATE google.connection SET oauth_client_id = $1, domains = '{nordlys.no}', personal_connection = true, personal_connection_at = $2`, gcalWebClient, h.Now())
	return &gcalRig{Harness: h, g: g, wf: wf}
}

// person is a signed-in user holding calendar:access whose Staff record names
// a Google account the module made, connected for the calendar: the client,
// the user and the account's Google id.
func (r *gcalRig) person(t *testing.T) (*modtest.Client, uuid.UUID, string) {
	t.Helper()
	r.n++
	c, id := r.SignInUser(t, "calendar:access")
	gid := fmt.Sprintf("1%020d", r.n)
	email := fmt.Sprintf("person%d@nordlys.no", r.n)
	r.Exec(t, `INSERT INTO google.accounts (user_id, employee_number, primary_email, state, created_by_us, created_at, updated_at)
	           VALUES ($1, $2, $3, 'enabled', true, $4, $4)`, gid, "E-"+strconv.Itoa(r.n), email, r.Now())
	refresh := r.g.IssueRefresh(googletest.CodeGrant{ClientID: gcalWebClient, Sub: gid, Email: email, HD: "nordlys.no", Granted: gcalScopes})
	sealed, err := r.Deps().Secrets.Seal("google/connection-refresh", []byte(refresh))
	if err != nil {
		t.Fatal(err)
	}
	r.Exec(t, `INSERT INTO google.connections (google_user_id, user_id, scopes, refresh_sealed, state, connected_at, changed_at, created_at, updated_at)
	           VALUES ($1, $2, $3, $4, 'active', $5, $5, $5, $5)`, gid, id, gcalScopes, sealed, r.Now().Add(-time.Hour))
	r.wf.mu.Lock()
	r.wf.accounts[id] = contracts.DirectoryAccount{Provider: "google", ExternalID: gid, Enabled: true}
	r.wf.mu.Unlock()
	return c, id, gid
}

// cycle runs one cycle of the calendar's own mailbox worker, answering how
// many people it synced.
func (r *gcalRig) cycle(t *testing.T) int {
	t.Helper()
	for _, w := range r.Workers(r.Deps()) {
		if mw, ok := w.(*calendar.MailboxSyncWorker); ok {
			n, err := mw.RunCycle(context.Background())
			if err != nil {
				t.Fatalf("cycle: %v", err)
			}
			return n
		}
	}
	t.Fatal("no calendar-mailbox-sync worker")
	return 0
}

// next moves the clock past the poll and runs a cycle.
func (r *gcalRig) next(t *testing.T) int {
	t.Helper()
	r.Advance(16 * time.Minute)
	return r.cycle(t)
}

// gcalDo sends one request, failing unless it answered want.
func gcalDo(t *testing.T, c *modtest.Client, method, path string, body any, want int, out any, opts ...modtest.RequestOption) {
	t.Helper()
	resp := c.Do(method, path, body, opts...)
	if resp.Status != want {
		t.Fatalf("%s %s: %d %s, want %d", method, path, resp.Status, resp.Body, want)
	}
	if out != nil {
		resp.JSON(out)
	}
}

// turnOnGoogle turns the person's sync on: the default calendar, full detail,
// their appointments.
func turnOnGoogle(t *testing.T, c *modtest.Client) map[string]any {
	t.Helper()
	var got struct {
		Revision int32 `json:"revision"`
	}
	gcalDo(t, c, http.MethodGet, "/api/v1/calendar/mailbox-sync", nil, http.StatusOK, &got)
	var out map[string]any
	gcalDo(t, c, http.MethodPut, "/api/v1/calendar/mailbox-sync",
		map[string]any{"enabled": true, "target": "default", "detail": "full", "kinds": []string{"calendar.appointment"}},
		http.StatusOK, &out, modtest.Header("If-Match", strconv.Itoa(int(got.Revision))))
	return out
}

// appointmentIn creates a timed appointment of title on Monday 14 September
// 2026 from hour, for an hour, in Oslo: its id and revision.
func appointmentIn(t *testing.T, c *modtest.Client, title string, hour int) (int64, int32) {
	t.Helper()
	oslo, _ := time.LoadLocation(gcalOslo)
	start := time.Date(2026, 9, 14, hour, 0, 0, 0, oslo)
	var a struct {
		ID       int64 `json:"id"`
		Revision int32 `json:"revision"`
	}
	gcalDo(t, c, http.MethodPost, "/api/v1/calendar/appointments", map[string]any{
		"title": title, "allDay": false, "timeZone": gcalOslo,
		"startsAt": start.UTC().Format(time.RFC3339), "endsAt": start.Add(time.Hour).UTC().Format(time.RFC3339),
	}, http.StatusCreated, &a)
	return a.ID, a.Revision
}

// copyOf is the copy in the person's primary calendar carrying the key of
// appointment id, failing when there is none.
func (r *gcalRig) copyOf(t *testing.T, gid string, id int64) googletest.Event {
	t.Helper()
	for _, ev := range r.g.LiveEvents(gid, "") {
		if ev.Private["vantigoKey"] == "appointment:"+strconv.FormatInt(id, 10) {
			return ev
		}
	}
	t.Fatalf("no copy of appointment %d in Google", id)
	return googletest.Event{}
}

// busy is the person's busy rows the calendar keeps: event id → show_as.
func (r *gcalRig) busy(t *testing.T, userID uuid.UUID) map[string]string {
	t.Helper()
	rows, err := r.Pool().Query(context.Background(), `SELECT event_id, show_as FROM calendar.mailbox_busy WHERE user_id = $1`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, showAs string
		if err := rows.Scan(&id, &showAs); err != nil {
			t.Fatal(err)
		}
		out[id] = showAs
	}
	return out
}

// title is the appointment's title as its owner reads it.
func title(t *testing.T, c *modtest.Client, id int64) string {
	t.Helper()
	var a struct {
		Title string `json:"title"`
	}
	gcalDo(t, c, http.MethodGet, "/api/v1/calendar/appointments/"+strconv.FormatInt(id, 10), nil, http.StatusOK, &a)
	return a.Title
}

func TestGoogleCalendar_ThroughTheCalendarsOwnWorker(t *testing.T) {
	t.Parallel()
	r := newGcalRig(t)
	c, me, gid := r.person(t)
	oslo, _ := time.LoadLocation(gcalOslo)

	// The provider is named for the person.
	var sync struct {
		Provider *struct {
			Key         string `json:"key"`
			DisplayName string `json:"displayName"`
		} `json:"provider"`
	}
	gcalDo(t, c, http.MethodGet, "/api/v1/calendar/mailbox-sync", nil, http.StatusOK, &sync)
	if sync.Provider == nil || sync.Provider.Key != "google" || sync.Provider.DisplayName != "Google Calendar" {
		t.Fatalf("the provider %+v, want Google Calendar", sync.Provider)
	}

	// A push: the appointment appears in Google Calendar, marked, and the
	// primary calendar is watched.
	turnOnGoogle(t, c)
	plan, _ := appointmentIn(t, c, "Planning", 9)
	if n := r.cycle(t); n != 1 {
		t.Fatalf("synced %d, want the person", n)
	}
	cp := r.copyOf(t, gid, plan)
	if cp.Summary != "Planning" || cp.Private["vantigo"] == "" {
		t.Errorf("the copy %+v", cp)
	}
	channels := r.g.Channels()
	if len(channels) != 1 || channels[0].Owner != gid || channels[0].Address != gcalPublic+"/api/v1/google/calendar-notifications" {
		t.Fatalf("channels %+v, want the person's primary calendar watched", channels)
	}

	// A busy read: an out-of-office in Google is the calendar's busy row.
	away := r.g.PutEvent(gid, "", googletest.Event{Summary: "Away", EventType: "outOfOffice",
		Start: googletest.EventTime{DateTime: time.Date(2026, 9, 15, 8, 0, 0, 0, oslo).Format(time.RFC3339)},
		End:   googletest.EventTime{DateTime: time.Date(2026, 9, 15, 16, 0, 0, 0, oslo).Format(time.RFC3339)}})
	// Nothing is due yet; the channel's message makes the sync due at once.
	r.Advance(time.Minute)
	if n := r.cycle(t); n != 0 {
		t.Fatalf("synced %d before the notification, want none", n)
	}
	anon := r.Client(t)
	ch := channels[0]
	post := func(token string) {
		t.Helper()
		gcalDo(t, anon, http.MethodPost, "/api/v1/google/calendar-notifications", nil, http.StatusNoContent, nil,
			modtest.Header("X-Goog-Channel-ID", ch.ID), modtest.Header("X-Goog-Channel-Token", token),
			modtest.Header("X-Goog-Resource-ID", ch.ResourceID), modtest.Header("X-Goog-Resource-State", "exists"))
	}
	// A forged token is ignored: still nothing due.
	post("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	r.Advance(time.Minute)
	if n := r.cycle(t); n != 0 {
		t.Fatalf("synced %d after a forged notification, want none", n)
	}
	post(ch.Token)
	r.Advance(time.Minute)
	if n := r.cycle(t); n != 1 {
		t.Fatalf("synced %d after the notification, want the person", n)
	}
	if got := r.busy(t, me); got[away.ID] != contracts.ShowAsOutOfOffice || got[cp.ID] != "" {
		t.Errorf("busy rows %v, want the out-of-office alone (a copy is never busy)", got)
	}

	// A write-back taken: the person renames the copy in Google.
	r.g.EditEvent(gid, "", cp.ID, func(e *googletest.Event) { e.Summary = "Planning, renamed" })
	r.next(t)
	if got := title(t, c, plan); got != "Planning, renamed" {
		t.Errorf("the appointment's title %q, want the edit taken", got)
	}
	// One put back: a span the rules refuse is written over.
	r.g.EditEvent(gid, "", cp.ID, func(e *googletest.Event) {
		e.End.DateTime = time.Date(2026, 10, 30, 10, 0, 0, 0, oslo).Format(time.RFC3339)
	})
	r.next(t)
	if got := r.copyOf(t, gid, plan); got.End.DateTime == time.Date(2026, 10, 30, 10, 0, 0, 0, oslo).Format(time.RFC3339) {
		t.Errorf("the refused span was kept in Google: %+v", got.End)
	}

	// The delta restarted on 410: the sync goes on, the busy time still read.
	r.g.ExpireSyncTokens()
	r.next(t)
	if got := r.busy(t, me); got[away.ID] != contracts.ShowAsOutOfOffice {
		t.Errorf("after a 410: busy rows %v", got)
	}
	// No sync token at all (U7): every sync re-reads the window, and the busy
	// time follows a delete.
	r.g.SetNoSyncToken(true)
	r.next(t)
	r.g.EditEvent(gid, "", away.ID, func(e *googletest.Event) { e.Status = "cancelled" })
	r.next(t)
	r.next(t)
	if got := r.busy(t, me); got[away.ID] != "" {
		t.Errorf("without a sync token, a deleted absence stays busy: %v", got)
	}

	// A disconnect cleans up: the copies deleted, the channel stopped.
	gcalDo(t, c, http.MethodDelete, "/api/v1/google/connection/me", nil, http.StatusNoContent, nil)
	r.next(t)
	if live := r.g.LiveEvents(gid, ""); slices.ContainsFunc(live, func(e googletest.Event) bool { return e.Private["vantigo"] != "" }) {
		t.Errorf("copies left after the disconnect: %d events", len(live))
	}
	if len(r.g.Channels()) != 0 {
		t.Error("the channel was not stopped")
	}
}

func TestGoogleCalendar_ALeaversCopiesAreLeft(t *testing.T) {
	t.Parallel()
	r := newGcalRig(t)
	c, _, gid := r.person(t)
	turnOnGoogle(t, c)
	plan, _ := appointmentIn(t, c, "Planning", 9)
	r.cycle(t)
	r.copyOf(t, gid, plan)

	// The leaver's connection closes with its token forgotten (G19): the
	// copies stay in the suspended account, and the sync does not loop.
	r.Exec(t, `UPDATE google.connections SET state = 'closing', refresh_sealed = NULL, changed_at = $2, revision = revision + 1 WHERE google_user_id = $1`, gid, r.Now().Add(time.Second))
	r.next(t)
	r.next(t)
	if len(r.g.LiveEvents(gid, "")) != 1 {
		t.Errorf("the leaver's copies: %d, want the one left", len(r.g.LiveEvents(gid, "")))
	}
}
