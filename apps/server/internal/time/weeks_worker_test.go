package timetracking_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
)

// time.week.unsubmitted (virtual employees design V30): a week that has ended and
// was not submitted is announced once for each person, from the Tuesday after, for
// the week just ended, with ids, a date and hours and no personal data.

func plantEntry(t *testing.T, h *harness, user uuid.UUID, date string, hours float64, status string) {
	t.Helper()
	h.Exec(t, `INSERT INTO time.entries (user_id, project_id, entry_date, hours, billable, rate_source, status, created_at, updated_at)
	           VALUES ($1, 1001, $2::date, $3, true, 'none', $4, now(), now())`, user, date, hours, status)
}

func announcements(t *testing.T, h *harness) []contracts.WeekUnsubmitted {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(), `SELECT payload FROM platform.events WHERE name = 'time.week.unsubmitted' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []contracts.WeekUnsubmitted
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var ev contracts.WeekUnsubmitted
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(raw, &keys)
		if len(keys) != 3 {
			t.Errorf("the event carries %d fields, want userId, weekStart and hours: %s", len(keys), raw)
		}
		out = append(out, ev)
	}
	return out
}

// advanceToWeekday moves the clock to hh:00 on the next given ISO weekday in the business zone.
func advanceToWeekday(t *testing.T, h *harness, weekday, hour int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skip("no tz database")
	}
	now := h.Now().In(loc)
	wd := int(now.Weekday())
	if wd == 0 {
		wd = 7
	}
	at := time.Date(now.Year(), now.Month(), now.Day()+(weekday-wd+7)%7, hour, 0, 0, 0, loc)
	if !at.After(now) {
		at = at.AddDate(0, 0, 7)
	}
	h.Advance(at.Sub(h.Now()))
	return at
}

func TestWeeksWorker_AnnouncesTheWeekJustEndedOnceFromTheTuesday(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := timetracking.NewWeeksWorker(h.Deps())
	ctx := context.Background()

	// A Monday: the week has only just ended, and people still submit it.
	monday := advanceToWeekday(t, h, 1, 10)
	week := monday.AddDate(0, 0, -7).Format(time.DateOnly)
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	plantEntry(t, h, a, week, 8, "draft")
	plantEntry(t, h, a, week, 4.5, "draft")
	plantEntry(t, h, b, week, 8, "submitted")
	h.Exec(t, `INSERT INTO time.week_submissions (user_id, week_start, submitted_at) VALUES ($1, $2::date, now())`, b, week)
	// c submitted a week before and registered nothing in this one; d registered a week long ago.
	h.Exec(t, `INSERT INTO time.week_submissions (user_id, week_start, submitted_at) VALUES ($1, $2::date, now())`, c, monday.AddDate(0, 0, -14).Format(time.DateOnly))
	plantEntry(t, h, d, monday.AddDate(0, 0, -70).Format(time.DateOnly), 8, "draft")
	if n, err := w.RunCycle(ctx); err != nil || n != 0 {
		t.Fatalf("on the Monday: announced %d (%v), want none", n, err)
	}

	h.Advance(24 * time.Hour) // Tuesday
	n, err := w.RunCycle(ctx)
	if err != nil || n != 2 {
		t.Fatalf("on the Tuesday: announced %d (%v), want a and c", n, err)
	}
	got := announcements(t, h)
	byUser := map[uuid.UUID]contracts.WeekUnsubmitted{}
	for _, ev := range got {
		byUser[ev.UserID] = ev
	}
	if ev, ok := byUser[a]; !ok || ev.WeekStart != week || ev.Hours != "12.50" {
		t.Errorf("a's event = %+v, want the week %s with 12.50 hours", ev, week)
	}
	if ev, ok := byUser[c]; !ok || ev.Hours != "0.00" {
		t.Errorf("c's event = %+v, want a regular submitter's empty week", ev)
	}
	if _, ok := byUser[b]; ok {
		t.Error("b submitted the week and was announced")
	}
	if _, ok := byUser[d]; ok {
		t.Error("d, who has not used Time for ten weeks, was announced")
	}

	// Once for each person and week, whatever the replica or the pass.
	h.Advance(24 * time.Hour)
	if n, err := w.RunCycle(ctx); err != nil || n != 0 {
		t.Errorf("the Wednesday pass announced %d (%v), want none", n, err)
	}
	if n := len(announcements(t, h)); n != 2 {
		t.Errorf("%d events in all, want two", n)
	}
}
