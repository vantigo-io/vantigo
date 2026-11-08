package agents_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Wakeups, schedules, the new role fields and the events that wake a role
// (virtual employees design V29, V30), against the module's own tables:
// Communications, Orders and Time are not composed here, so the events they
// publish are published by the test.

// addRoleOf adds a role of a template.
func addRoleOf(t *testing.T, g *rig, template, name string) role {
	t.Helper()
	r := g.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+g.agent.ID+"/roles", map[string]any{"template": template, "name": name, "instructions": ""})
	if r.Status != http.StatusCreated {
		t.Fatalf("add a %s role: %d %s", template, r.Status, r.Body)
	}
	var out role
	r.JSON(&out)
	return out
}

// setSchedule sets a role's schedule as the Owner.
func setSchedule(t *testing.T, g *rig, rl role, kind string, day int, at string) role {
	t.Helper()
	body := roleBody(rl)
	body["schedule"] = map[string]any{"kind": kind, "day": day, "time": at}
	r := g.owner.Do(http.MethodPut, rolePath(g.agent, rl), body, ifMatch(rl.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("set the schedule: %d %s", r.Status, r.Body)
	}
	var out role
	r.JSON(&out)
	return out
}

// wakeupCount is how many wakeups the role has, of a subject type.
func wakeupCount(t *testing.T, g *rig, roleID, subjectType string) int {
	t.Helper()
	return g.h.Count(t, `SELECT count(*) FROM agents.wakeups WHERE role_id = $1 AND subject_type = $2`, roleID, subjectType)
}

func subjectRequests(t *testing.T, g *rig, roleID string) int {
	t.Helper()
	return g.h.Count(t, `SELECT count(*) FROM agents.run_queue WHERE role_id = $1 AND subject_type <> 'conversation' AND taken_at IS NULL`, roleID)
}

// advanceTo moves the harness clock to t.
func advanceTo(g *rig, to time.Time) { g.h.Advance(to.Sub(g.h.Now())) }

// nextWeekday is the next given ISO weekday at hh:mm Oslo time after now.
func nextWeekday(now time.Time, loc *time.Location, weekday int, hour, minute int) time.Time {
	l := now.In(loc)
	wd := int(l.Weekday())
	if wd == 0 {
		wd = 7
	}
	ahead := (weekday - wd + 7) % 7
	at := time.Date(l.Year(), l.Month(), l.Day()+ahead, hour, minute, 0, 0, loc)
	if !at.After(now) {
		at = time.Date(l.Year(), l.Month(), l.Day()+ahead+7, hour, minute, 0, 0, loc)
	}
	return at
}

func TestWakeups_AScheduleWakesTheRoleOnceAtItsTimeInTheBusinessZone(t *testing.T) {
	t.Parallel()
	loc := oslo(t)
	g := newRig(t)
	rl := setSchedule(t, g, addRoleOf(t, g, "project_digest", "Digest"), "weekly", 1, "08:00")
	if rl.Schedule.Kind != "weekly" || rl.Schedule.Day == nil || *rl.Schedule.Day != 1 || rl.Schedule.Time == nil || *rl.Schedule.Time != "08:00" {
		t.Fatalf("schedule = %+v", rl.Schedule)
	}

	monday := nextWeekday(g.h.Now(), loc, 1, 8, 0)
	advanceTo(g, monday.Add(-time.Minute))
	if err := agents.WakeupsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := wakeupCount(t, g, rl.ID, "schedule"); n != 0 {
		t.Fatalf("%d wakeups a minute before 08:00 Oslo time, want none", n)
	}

	g.h.Advance(2 * time.Minute)
	for range 3 { // a second pass, and a second replica, wake it no more
		if err := agents.WakeupsCycle(g.h.Deps()); err != nil {
			t.Fatal(err)
		}
	}
	if n := wakeupCount(t, g, rl.ID, "schedule"); n != 1 {
		t.Fatalf("%d wakeups after 08:00, want one", n)
	}
	var subject, reason string
	if err := g.h.Pool().QueryRow(context.Background(),
		`SELECT subject_type || ':' || subject_id, reason FROM agents.run_queue WHERE role_id = $1`, rl.ID).Scan(&subject, &reason); err != nil {
		t.Fatalf("the run request: %v", err)
	}
	if want := "schedule:" + monday.In(loc).Format(time.DateOnly); subject != want || reason != "wakeup" {
		t.Errorf("request = %s/%s, want %s/wakeup", subject, reason, want)
	}
	if n := g.h.Count(t, `SELECT count(*) FROM agents.wakeups WHERE role_id = $1 AND taken_at IS NOT NULL`, rl.ID); n != 1 {
		t.Errorf("%d wakeups taken, want the one", n)
	}

	g.h.Advance(7 * 24 * time.Hour)
	if err := agents.WakeupsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := wakeupCount(t, g, rl.ID, "schedule"); n != 2 {
		t.Errorf("%d wakeups a week later, want two", n)
	}
}

func TestWakeups_AScheduleJustSetDoesNotWakeForTheMomentItPassed(t *testing.T) {
	t.Parallel()
	loc := oslo(t)
	g := newRig(t)
	rl := addRoleOf(t, g, "project_digest", "Digest")
	monday := nextWeekday(g.h.Now(), loc, 1, 8, 0)
	advanceTo(g, monday.Add(time.Hour)) // 09:00, an hour after Monday's occurrence
	g.owner = owner(t, g.h)             // a session that has not expired over the days
	setSchedule(t, g, rl, "weekly", 1, "08:00")
	if err := agents.WakeupsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := wakeupCount(t, g, rl.ID, "schedule"); n != 0 {
		t.Errorf("%d wakeups for an occurrence that passed before the schedule was set", n)
	}
}

func TestWakeups_AnOccurrenceTheWorkerSleptThroughIsSkippedNotSentLate(t *testing.T) {
	t.Parallel()
	loc := oslo(t)
	g := newRig(t)
	rl := setSchedule(t, g, addRoleOf(t, g, "project_digest", "Digest"), "weekly", 1, "08:00")
	monday := nextWeekday(g.h.Now(), loc, 1, 8, 0)
	advanceTo(g, monday.Add(3*24*time.Hour)) // the worker was down until Thursday
	for range 2 {
		if err := agents.WakeupsCycle(g.h.Deps()); err != nil {
			t.Fatal(err)
		}
	}
	if n := wakeupCount(t, g, rl.ID, "schedule"); n != 0 {
		t.Errorf("%d wakeups, want the digest of Monday not sent on Thursday", n)
	}
}

func TestWakeups_APausedAgentsWakeupsWaitAndAHandedOffConversationIsNotWoken(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	now := g.h.Now()
	conv, other := uuid.New(), uuid.New()
	for _, c := range []uuid.UUID{conv, other} {
		g.h.Exec(t, `INSERT INTO agents.wakeups (id, role_id, subject_type, subject_id, at, reason, created_at)
		             VALUES ($1, $2, 'conversation', $3, $4, 'look again', $4)`, uuid.New(), g.role.ID, c.String(), now.Add(-time.Minute))
	}
	g.h.Exec(t, `INSERT INTO agents.handed_off (conversation_id, role_id, at, reason) VALUES ($1, $2, $3, 'hand_off')`, other, g.role.ID, now)

	if r := g.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+g.agent.ID+"/pause", nil); r.Status != http.StatusOK {
		t.Fatalf("pause: %d %s", r.Status, r.Body)
	}
	if err := agents.WakeupsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := g.h.Count(t, `SELECT count(*) FROM agents.wakeups WHERE taken_at IS NULL`); n != 2 {
		t.Fatalf("%d wakeups pending while paused, want both kept", n)
	}

	if r := g.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+g.agent.ID+"/resume", nil); r.Status != http.StatusOK {
		t.Fatalf("resume: %d %s", r.Status, r.Body)
	}
	if err := agents.WakeupsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if got := g.requests(t, conv); len(got) != 1 || got[0].Reason != "wakeup" {
		t.Errorf("requests for the conversation = %+v, want one wakeup", got)
	}
	if got := g.requests(t, other); len(got) != 0 {
		t.Errorf("the handed-off conversation got %+v: a follow-up does not take it back", got)
	}
	if n := g.h.Count(t, `SELECT count(*) FROM agents.wakeups WHERE taken_at IS NULL`); n != 0 {
		t.Errorf("%d wakeups still pending, want both taken", n)
	}
}

func TestWakeups_AStaleWakeupIsDroppedNotRun(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	old := g.h.Now().Add(-8 * 24 * time.Hour)
	g.h.Exec(t, `INSERT INTO agents.wakeups (id, role_id, subject_type, subject_id, at, reason, created_at)
	             VALUES ($1, $2, 'conversation', $3, $4, 'look again', $4)`, uuid.New(), g.role.ID, conv.String(), old)
	if err := agents.WakeupsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if got := g.requests(t, conv); len(got) != 0 {
		t.Errorf("requests = %+v, want none for a wakeup a week late", got)
	}
}

func TestTrigger_AQuoteSentWakesTheSalesFollowupAfterItsWaitAndAcceptingCancelsIt(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	rl := addRoleOf(t, g, "sales_followup", "Follow-up")
	body := roleBody(rl)
	body["waitDays"] = 10
	if r := g.owner.Do(http.MethodPut, rolePath(g.agent, rl), body, ifMatch(rl.Version)); r.Status != http.StatusOK {
		t.Fatalf("set the wait: %d %s", r.Status, r.Body)
	}
	sent := g.h.Now()
	g.publish(t, contracts.QuoteSent{QuoteID: 42, Number: 1042, Revision: 1, CustomerID: 7, Currency: "NOK", NetTotal: "1000.00", SentAt: sent})

	var at time.Time
	var customer *int32
	var subject string
	if err := g.h.Pool().QueryRow(context.Background(),
		`SELECT at, customer_id, subject_id FROM agents.wakeups WHERE role_id = $1 AND subject_type = 'quote'`, rl.ID).Scan(&at, &customer, &subject); err != nil {
		t.Fatalf("the wakeup: %v", err)
	}
	if want := sent.Add(10 * 24 * time.Hour); !at.Equal(want) || subject != "42" || customer == nil || *customer != 7 {
		t.Errorf("wakeup at %s for quote %s, customer %v; want %s for quote 42, customer 7", at, subject, customer, want)
	}
	// The same quote revised and sent again moves the wakeup, it does not add one.
	g.publish(t, contracts.QuoteSent{QuoteID: 42, Number: 1042, Revision: 2, CustomerID: 7, Currency: "NOK", NetTotal: "900.00", SentAt: sent.Add(24 * time.Hour)})
	if n := wakeupCount(t, g, rl.ID, "quote"); n != 1 {
		t.Errorf("%d wakeups for a quote sent twice, want one", n)
	}
	// A front desk role is not woken by a quote.
	if n := wakeupCount(t, g, g.role.ID, "quote"); n != 0 {
		t.Errorf("the front desk got %d quote wakeups", n)
	}

	g.publish(t, contracts.QuoteAccepted{QuoteID: 42, Number: 1042, Revision: 2, CustomerID: 7, AcceptedIntoKind: "order", AcceptedIntoID: 5, AcceptedAt: sent})
	if n := g.h.Count(t, `SELECT count(*) FROM agents.wakeups WHERE role_id = $1 AND taken_at IS NULL`, rl.ID); n != 0 {
		t.Errorf("%d wakeups after the quote was accepted, want none", n)
	}
}

func TestTrigger_AnUnsubmittedWeekMakesOneRequestForTheTimesheetRemindersOnly(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	rl := addRoleOf(t, g, "timesheet_reminders", "Reminders")
	person := uuid.New()
	ev := contracts.WeekUnsubmitted{UserID: person, WeekStart: "2026-11-02", Hours: "12.50"}
	g.publish(t, ev)
	g.publish(t, ev) // delivered twice: still one request
	if n := subjectRequests(t, g, rl.ID); n != 1 {
		t.Fatalf("%d requests, want one", n)
	}
	var subject, note string
	if err := g.h.Pool().QueryRow(context.Background(), `SELECT subject_id, note FROM agents.run_queue WHERE role_id = $1`, rl.ID).Scan(&subject, &note); err != nil {
		t.Fatal(err)
	}
	if subject != person.String()+"|2026-11-02" || !strings.Contains(note, "12.50") {
		t.Errorf("request = %s %q", subject, note)
	}
	if n := subjectRequests(t, g, g.role.ID); n != 0 {
		t.Errorf("the front desk got %d requests for a week", n)
	}
	// A paused agent's request waits, and the worker drops it after a day without a hand-off.
	if r := g.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+g.agent.ID+"/pause", nil); r.Status != http.StatusOK {
		t.Fatalf("pause: %d %s", r.Status, r.Body)
	}
	if id, err := agents.ClaimDue(g.h.Deps(), uuid.New()); err != nil || id != uuid.Nil {
		t.Errorf("a paused agent's request was claimed: %v %v", id, err)
	}
	g.h.Advance(25 * time.Hour)
	if err := agents.WorkerCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := subjectRequests(t, g, rl.ID); n != 0 {
		t.Errorf("%d requests a day on, want the stale one dropped", n)
	}
	if n := g.h.Count(t, `SELECT count(*) FROM agents.handed_off`); n != 0 {
		t.Errorf("%d hand-off markers for a run with no conversation", n)
	}
}

func TestRoles_TheNewFieldsAreValidatedAndLooseningThemIsTheOwnersAlone(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	mgr := manager(t, g.h)
	digest := addRoleOf(t, g, "project_digest", "Digest")
	sales := addRoleOf(t, g, "sales_followup", "Follow-up")
	if sales.StartMode != "review" || sales.ChangeMode != "review" || sales.WaitDays != 7 || digest.Schedule.Kind != "none" {
		t.Fatalf("new roles = %+v %+v, want Review, Review, a wait of 7 days and no schedule", sales, digest)
	}
	update := func(c *modtest.Client, rl role, mutate func(map[string]any)) *modtest.Response {
		body := roleBody(rl)
		mutate(body)
		return c.Do(http.MethodPut, rolePath(g.agent, rl), body, ifMatch(rl.Version), modtest.SkipContract("a value the contract refuses"))
	}

	// Loosening the start mode or the change mode is the Owner's, as the reply mode is.
	for _, field := range []string{"startMode", "changeMode"} {
		r := update(mgr, sales, func(b map[string]any) { b[field] = "auto_above" })
		if r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), "owner_required") {
			t.Errorf("a manager choosing %s auto_above = %d %s, want 403 owner_required", field, r.Status, r.Body)
		}
		r = update(g.owner, sales, func(b map[string]any) { b[field] = "auto_above" })
		if r.Status != http.StatusOK {
			t.Fatalf("the Owner choosing %s = %d %s", field, r.Status, r.Body)
		}
		r.JSON(&sales)
		if r := update(mgr, sales, func(b map[string]any) { b[field] = "review"; b["threshold"] = 85 }); r.Status != http.StatusOK {
			t.Errorf("a manager going back to Review = %d %s", r.Status, r.Body)
		} else {
			r.JSON(&sales)
		}
	}
	// The threshold the three modes share: lowering it while a mode is not Review is the Owner's too.
	r := update(g.owner, sales, func(b map[string]any) { b["startMode"] = "auto_above" })
	r.JSON(&sales)
	if r := update(mgr, sales, func(b map[string]any) { b["threshold"] = 60 }); r.Status != http.StatusForbidden {
		t.Errorf("a manager lowering the threshold under a start mode that is not Review = %d, want 403", r.Status)
	}

	bad := map[string]func(map[string]any){
		"a weekly schedule on day 8":    func(b map[string]any) { b["schedule"] = map[string]any{"kind": "weekly", "day": 8, "time": "08:00"} },
		"a monthly schedule on day 29":  func(b map[string]any) { b["schedule"] = map[string]any{"kind": "monthly", "day": 29, "time": "08:00"} },
		"a schedule with no time":       func(b map[string]any) { b["schedule"] = map[string]any{"kind": "weekly", "day": 1, "time": nil} },
		"a time of 25:00":               func(b map[string]any) { b["schedule"] = map[string]any{"kind": "weekly", "day": 1, "time": "25:00"} },
		"a wait of 0 days":              func(b map[string]any) { b["waitDays"] = 0 },
		"a wait of 61 days":             func(b map[string]any) { b["waitDays"] = 61 },
		"a recipient that is no person": func(b map[string]any) { b["recipientUserId"] = uuid.NewString() },
	}
	for name, mutate := range bad {
		if r := update(g.owner, digest, mutate); r.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, r.Status, r.Body)
		}
	}
	// A template with no schedule takes none.
	r = update(g.owner, sales, func(b map[string]any) { b["schedule"] = map[string]any{"kind": "weekly", "day": 1, "time": "08:00"} })
	if r.Status != http.StatusBadRequest {
		t.Errorf("a schedule on the sales follow-up = %d %s, want 400", r.Status, r.Body)
	}
	// Clearing a schedule: kind none.
	digest = setSchedule(t, g, digest, "monthly", 15, "09:30")
	r = update(g.owner, digest, func(b map[string]any) { b["schedule"] = map[string]any{"kind": "none", "day": nil, "time": nil} })
	if r.Status != http.StatusOK {
		t.Fatalf("clear the schedule: %d %s", r.Status, r.Body)
	}
	r.JSON(&digest)
	if digest.Schedule.Kind != "none" || digest.Schedule.Day != nil {
		t.Errorf("schedule after clearing = %+v", digest.Schedule)
	}
}
