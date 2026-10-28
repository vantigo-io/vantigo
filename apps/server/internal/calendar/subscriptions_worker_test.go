package calendar_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The calendar-subscriptions worker (calendar feed design D7).

// cycle runs one cycle of the worker, failing on its error, and answers how
// many it fetched.
func cycle(t *testing.T, w *calendar.SubscriptionsWorker) int {
	t.Helper()
	n, err := w.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	return n
}

// nextFetch is the subscription's next_fetch_at.
func nextFetch(t *testing.T, h *harness, id int64) time.Time {
	t.Helper()
	return modtest.One[time.Time](t, h.Harness, `SELECT next_fetch_at FROM calendar.subscriptions WHERE id = $1`, id)
}

// insertDue writes a subscription never fetched, due at once, as the worker
// finds a row whose inline fetch never ran.
func insertDue(t *testing.T, h *harness, path string) int64 {
	t.Helper()
	_, userID := signIn(t, h)
	return modtest.One[int64](t, h.Harness, `INSERT INTO calendar.subscriptions (user_id, name, url, colour, busy_only, created_at, updated_at)
	           VALUES ($1, 'Due', $2, 'blue', false, $3, $3) RETURNING id`, userID, feedURL(path), h.Now())
}

func TestSubscriptionsWorker_FetchesWhatIsDueAndSetsTheNextFetchPastThePoll(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serveCalendar("/a.ics", matchCalendar, "")
	f.serveCalendar("/b.ics", matchCalendar, "")
	fetched := subscribe(t, c, subscriptionBody("Fetched", feedURL("/a.ics"), "blue", false))
	poll := 30 * time.Minute
	if next := nextFetch(t, h, fetched.Id); next.Before(h.Now().Add(poll)) || !next.Before(h.Now().Add(poll+poll/10)) {
		t.Errorf("after the inline fetch next_fetch_at = %v, want within [%v, %v)", next, h.Now().Add(poll), h.Now().Add(poll+poll/10))
	}
	due := insertDue(t, h, "/b.ics")
	w := calendar.NewSubscriptionsWorker(h.Deps())

	if n := cycle(t, w); n != 1 || !slices.Equal(f.paths(), []string{"/a.ics", "/b.ics"}) {
		t.Errorf("the first cycle fetched %d: %v", n, f.paths())
	}
	if next := nextFetch(t, h, due); next.Before(h.Now().Add(poll)) || !next.Before(h.Now().Add(poll+poll/10)) {
		t.Errorf("after the worker's fetch next_fetch_at = %v", next)
	}
	if body := storedBody(t, h, due); body != matchCalendar {
		t.Errorf("the worker stored %q", body)
	}
	if n := cycle(t, w); n != 0 {
		t.Errorf("a cycle with nothing due fetched %d", n)
	}
	h.Advance(poll + poll/10)
	if n := cycle(t, w); n != 2 {
		t.Errorf("a cycle after the poll fetched %d", n)
	}
}

func TestSubscriptionsWorker_HonoursAFeedsRefreshIntervalOfADay(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	daily := icsCalendar([]string{"REFRESH-INTERVAL;VALUE=DURATION:P1D", "X-PUBLISHED-TTL:P1D"},
		[]string{"UID:x", "SUMMARY:X", "DTSTART:20260915T170000Z"})
	f.serveCalendar("/daily.ics", daily, "")
	id := insertDue(t, h, "/daily.ics")
	w := calendar.NewSubscriptionsWorker(h.Deps())
	cycle(t, w)
	day := 24 * time.Hour
	if next := nextFetch(t, h, id); next.Before(h.Now().Add(day)) || !next.Before(h.Now().Add(day+3*time.Minute)) {
		t.Errorf("a daily feed's next_fetch_at = %v, want within a day and three minutes of %v", next, h.Now())
	}
	h.Advance(12 * time.Hour)
	if n := cycle(t, w); n != 0 {
		t.Errorf("a daily feed was fetched again after twelve hours")
	}
}

func TestSubscriptionsWorker_AFailedAttemptKeepsTheBodyAndRecordsTheError(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serveCalendar("/flaky.ics", matchCalendar, "")
	s := subscribe(t, c, subscriptionBody("Flaky", feedURL("/flaky.ics"), "blue", false))
	f.serve("/flaky.ics", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	h.Advance(time.Hour)
	w := calendar.NewSubscriptionsWorker(h.Deps())
	if n := cycle(t, w); n != 1 {
		t.Fatalf("the cycle fetched %d", n)
	}
	if body := storedBody(t, h, s.Id); body != matchCalendar {
		t.Errorf("a failed attempt replaced the body")
	}
	got := modtest.One[string](t, h.Harness, `SELECT error FROM calendar.subscriptions WHERE id = $1`, s.Id)
	if got != "the address answered 500 Internal Server Error" {
		t.Errorf("error = %q", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM calendar.subscriptions WHERE id = $1 AND fetched_at = $2 AND attempted_at = $3`,
		s.Id, *s.FetchedAt, h.Now()); n != 1 {
		t.Errorf("fetched_at moved or attempted_at did not")
	}
	if next := nextFetch(t, h, s.Id); next.Before(h.Now().Add(30*time.Minute)) || !next.Before(h.Now().Add(33*time.Minute)) {
		t.Errorf("after a failure next_fetch_at = %v", next)
	}
	if items := ofKind(items(t, c, windowPath("2026-09-14", "2026-09-20")), "calendar.subscription"); len(items) != 1 {
		t.Errorf("the last good read shows %d items", len(items))
	}
	var list []map[string]any
	do(t, c, http.MethodGet, subscriptionsPath, nil, http.StatusOK, &list)
	if len(list) != 1 || list[0]["error"] != got || list[0]["fetchedAt"] == nil {
		t.Errorf("the screen reads %+v", list)
	}
}

// TestSubscriptionsWorker_DefersThoseOfAPersonWhoMayNoLongerUseCalendar: a
// person disabled, or without calendar:access, is asked about once a cycle,
// and their subscriptions are neither fetched nor read again every tick —
// each is put off by the poll — while everyone else's are fetched as ever.
func TestSubscriptionsWorker_DefersThoseOfAPersonWhoMayNoLongerUseCalendar(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	f.serveCalendar("/active.ics", matchCalendar, "")
	f.serveCalendar("/disabled.ics", matchCalendar, "")
	f.serveCalendar("/revoked.ics", matchCalendar, "")
	active := insertDue(t, h, "/active.ics")
	disabled := insertDue(t, h, "/disabled.ics")
	revoked := insertDue(t, h, "/revoked.ics")
	owner := func(id int64) uuid.UUID {
		return modtest.One[uuid.UUID](t, h.Harness, `SELECT user_id FROM calendar.subscriptions WHERE id = $1`, id)
	}
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, owner(disabled))
	h.Exec(t, `DELETE FROM identity.role_permissions WHERE permission_key = 'calendar:access'
	           AND role_id IN (SELECT role_id FROM identity.user_roles WHERE user_id = $1)`, owner(revoked))

	w := calendar.NewSubscriptionsWorker(h.Deps())
	if n := cycle(t, w); n != 1 || !slices.Equal(f.paths(), []string{"/active.ics"}) {
		t.Errorf("the cycle fetched %d: %v, want the active person's alone", n, f.paths())
	}
	poll := 30 * time.Minute
	for _, id := range []int64{disabled, revoked} {
		if next := nextFetch(t, h, id); next.Before(h.Now().Add(poll)) || !next.Before(h.Now().Add(poll+poll/10)) {
			t.Errorf("subscription %d's next_fetch_at = %v, want put off by the poll", id, next)
		}
		if n := h.Count(t, `SELECT count(*) FROM calendar.subscriptions WHERE id = $1 AND attempted_at IS NULL AND body IS NULL AND error IS NULL`, id); n != 1 {
			t.Errorf("subscription %d was recorded as attempted", id)
		}
	}
	if body := storedBody(t, h, active); body != matchCalendar {
		t.Errorf("the active person's subscription stored %q", body)
	}
	if n := cycle(t, w); n != 0 || len(f.requests()) != 1 {
		t.Errorf("the next tick fetched %d: %v", n, f.paths())
	}
}

// A claimed subscription is put off until its claim lapses, and the fetch's
// record sets when it is due next; a second cycle finds nothing due.
func TestSubscriptionsWorker_AClaimKeepsAnotherCycleOff(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	inFetch, proceed := make(chan struct{}), make(chan struct{})
	f.serve("/due.ics", func(w http.ResponseWriter, _ *http.Request) {
		close(inFetch)
		<-proceed
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		_, _ = w.Write([]byte(matchCalendar))
	})
	id := insertDue(t, h, "/due.ics")
	first, second := calendar.NewSubscriptionsWorker(h.Deps()), calendar.NewSubscriptionsWorker(h.Deps())
	done := make(chan int, 1)
	go func() { done <- cycle(t, first) }()
	<-inFetch
	if next := nextFetch(t, h, id); !next.After(h.Now()) {
		t.Errorf("while it is fetched the subscription is due at %v, want claimed past now", next)
	}
	if n := cycle(t, second); n != 0 {
		t.Errorf("a second cycle fetched %d of a claimed subscription", n)
	}
	close(proceed)
	if n := <-done; n != 1 {
		t.Errorf("the first cycle fetched %d", n)
	}
	if next := nextFetch(t, h, id); next.Before(h.Now().Add(30 * time.Minute)) {
		t.Errorf("after the fetch the next is due %v, want a poll ahead, not the claim's", next)
	}
}

// The cycle holds no connection while it fetches: on a pool of four, three
// held by others, the fourth stays free while an address is fetched.
func TestSubscriptionsWorker_ASmallPoolStaysUsableWhileACycleFetches(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t, modtest.WithPoolMaxConns(4))
	inFetch, proceed := make(chan struct{}), make(chan struct{})
	f.serve("/due.ics", func(w http.ResponseWriter, _ *http.Request) {
		close(inFetch)
		<-proceed
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		_, _ = w.Write([]byte(matchCalendar))
	})
	insertDue(t, h, "/due.ics")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for range 3 {
		conn, err := h.Pool().Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
	}
	w := calendar.NewSubscriptionsWorker(h.Deps())
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := w.RunCycle(ctx)
		done <- result{n, err}
	}()
	select {
	case <-inFetch:
	case res := <-done:
		t.Fatalf("the cycle ended before it fetched: %d, %v", res.n, res.err)
	case <-ctx.Done():
		t.Fatal("the cycle never fetched: it waited for a connection")
	}
	ping, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	err := h.Pool().Ping(ping)
	cancelPing()
	close(proceed)
	if err != nil {
		t.Errorf("the pool's last connection was not free while a fetch ran: %v", err)
	}
	if res := <-done; res.err != nil || res.n != 1 {
		t.Errorf("the cycle fetched %d, %v", res.n, res.err)
	}
}

func TestSubscriptionsWorker_IsContributedUnlessTurnedOffAndTicksEveryFiveMinutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "on by default", want: true},
		{name: "the poll does not change the tick", env: map[string]string{"CALENDAR_SUBSCRIPTIONS_POLL": "2h"}, want: true},
		{name: "turned off", env: map[string]string{"CALENDAR_SUBSCRIPTIONS_ENABLED": "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var opts []modtest.Option
			for k, v := range tc.env {
				opts = append(opts, modtest.WithEnv(k, v))
			}
			h := newHarness(t, opts...)
			found := false
			for _, w := range module.Workers(h.Deps(), calendar.Module()) {
				if w.Name() != "calendar-subscriptions" {
					continue
				}
				found = true
				if w.Interval() != 5*time.Minute {
					t.Errorf("the worker ticks every %v", w.Interval())
				}
			}
			if found != tc.want {
				t.Errorf("calendar-subscriptions contributed: %v, want %v", found, tc.want)
			}
		})
	}
}
