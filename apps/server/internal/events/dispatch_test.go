package events_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// thing is a test event for one aggregate.
type thing struct {
	Aggregate string `json:"aggregate"`
	N         int    `json:"n"`
}

func (thing) EventName() string       { return "test.thing.happened" }
func (thing) EventVersion() int       { return 1 }
func (e thing) AggregateID() string   { return e.Aggregate }
func (thingV2) EventName() string     { return "test.thing.happened" }
func (thingV2) EventVersion() int     { return 2 }
func (e thingV2) AggregateID() string { return e.Aggregate }

type thingV2 struct {
	Aggregate string `json:"aggregate"`
}

// quoteThing and orderThing are events of two aggregates whose ids may
// coincide, as quote 42 and sales order 42.
type quoteThing struct {
	ID string `json:"id"`
}
type orderThing struct {
	ID string `json:"id"`
}

func (quoteThing) EventName() string     { return "test.quote.happened" }
func (quoteThing) EventVersion() int     { return 1 }
func (e quoteThing) AggregateID() string { return e.ID }
func (orderThing) EventName() string     { return "test.sales_order.happened" }
func (orderThing) EventVersion() int     { return 1 }
func (e orderThing) AggregateID() string { return e.ID }

// clock is a settable test clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, time.November, 5, 10, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// publish commits ev, returning its event id.
func publish(t *testing.T, pool *pgxpool.Pool, ev interface {
	EventName() string
	EventVersion() int
	AggregateID() string
}, at time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := events.Publish(ctx, tx, "test", ev, at); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT max(id) FROM platform.events`).Scan(&id)
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return id
}

// recorder is a handler that records the events it handled, in order, and
// fails while fail says so. It writes a row of platform.rate_limit on the
// delivery's transaction, so a test can see what committed.
type recorder struct {
	mu    sync.Mutex
	calls []int64
	fail  func(ev events.Delivered, call int) error
}

func (r *recorder) handler(name string) events.Handler {
	return events.Handler{
		Name:   name,
		Events: []events.EventRef{{Name: "test.thing.happened", Version: 1}},
		Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
			r.mu.Lock()
			r.calls = append(r.calls, ev.ID)
			call := len(r.calls)
			r.mu.Unlock()
			if _, err := tx.Exec(ctx, `INSERT INTO platform.rate_limit (key, window_start, hits) VALUES ($1, now(), 1)
ON CONFLICT (key) DO UPDATE SET hits = platform.rate_limit.hits + 1`, fmt.Sprintf("%s:%d", name, ev.ID)); err != nil {
				return err
			}
			if r.fail != nil {
				return r.fail(ev, call)
			}
			return nil
		},
	}
}

func (r *recorder) handled() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.calls...)
}

type deliveryRow struct {
	id        int64
	state     string
	attempts  int
	lastError *string
	note      *string
	completed *time.Time
}

func readDelivery(t *testing.T, pool *pgxpool.Pool, eventID int64, handler string) deliveryRow {
	t.Helper()
	var d deliveryRow
	err := pool.QueryRow(context.Background(), `SELECT id, state, attempts, last_error, note, completed_at
FROM platform.event_deliveries WHERE event_id = $1 AND handler = $2`, eventID, handler).
		Scan(&d.id, &d.state, &d.attempts, &d.lastError, &d.note, &d.completed)
	if err != nil {
		t.Fatalf("read delivery of event %d to %s: %v", eventID, handler, err)
	}
	return d
}

func committed(t *testing.T, pool *pgxpool.Pool, handler string, eventID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT coalesce(sum(hits), 0) FROM platform.rate_limit WHERE key = $1`,
		fmt.Sprintf("%s:%d", handler, eventID)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func runOnce(t *testing.T, d *events.Dispatcher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

// TestDispatch_ARolledBackPublishLeavesNothing: an event rolled back with
// its publisher's transaction is never delivered.
func TestDispatch_ARolledBackPublishLeavesNothing(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	c := newClock()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Publish(ctx, tx, "test", thing{Aggregate: "a"}, c.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	runOnce(t, events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, nil, c.Now))
	if n := count(t, pool, `SELECT count(*) FROM platform.event_deliveries`); n != 0 || len(rec.handled()) != 0 {
		t.Errorf("deliveries = %d, handled = %v; want none", n, rec.handled())
	}
}

// TestDispatch_FailsTwiceThenSucceeds: at least once, with backoff, a
// failure's writes rolled back, ending done with two attempts.
func TestDispatch_FailsTwiceThenSucceeds(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	id := publish(t, pool, thing{Aggregate: "a"}, c.Now())
	rec := &recorder{fail: func(_ events.Delivered, call int) error {
		if call <= 2 {
			return fmt.Errorf("transient %d", call)
		}
		return nil
	}}
	d := events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, nil, c.Now)

	runOnce(t, d)
	got := readDelivery(t, pool, id, "test.h")
	if got.state != "failed" || got.attempts != 1 || got.lastError == nil || *got.lastError != "transient 1" {
		t.Fatalf("after one failure: %+v", got)
	}
	if n := committed(t, pool, "test.h", id); n != 0 {
		t.Errorf("a failed attempt's write committed (%d)", n)
	}
	// Not due before its backoff.
	c.Advance(59 * time.Second)
	runOnce(t, d)
	if n := len(rec.handled()); n != 1 {
		t.Fatalf("handled %d times before the first backoff ran out, want 1", n)
	}
	c.Advance(time.Second)
	runOnce(t, d)
	if got := readDelivery(t, pool, id, "test.h"); got.state != "failed" || got.attempts != 2 {
		t.Fatalf("after two failures: %+v", got)
	}
	c.Advance(5 * time.Minute)
	runOnce(t, d)
	got = readDelivery(t, pool, id, "test.h")
	if got.state != "done" || got.attempts != 2 || got.completed == nil {
		t.Errorf("after the success: %+v, want done with 2 attempts", got)
	}
	if n := committed(t, pool, "test.h", id); n != 1 {
		t.Errorf("the success's write committed %d times, want once", n)
	}
	if n := len(rec.handled()); n != 3 {
		t.Errorf("handled %d times, want 3", n)
	}
	// Done stays done.
	c.Advance(time.Hour)
	runOnce(t, d)
	if n := len(rec.handled()); n != 3 {
		t.Errorf("handled %d times after done, want 3", n)
	}
}

// TestDispatch_FansOutToSubscribedHandlersOnly: one delivery per handler
// that takes the event's name and version, none for another version.
func TestDispatch_FansOutToSubscribedHandlersOnly(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	v1 := publish(t, pool, thing{Aggregate: "a"}, c.Now())
	v2 := publish(t, pool, thingV2{Aggregate: "a"}, c.Now())
	one, two := &recorder{}, &recorder{}
	runOnce(t, events.NewDispatcher(pool, []events.Handler{one.handler("test.one"), two.handler("test.two")}, nil, c.Now))
	if n := count(t, pool, `SELECT count(*) FROM platform.event_deliveries WHERE event_id = $1 AND state = 'done'`, v1); n != 2 {
		t.Errorf("v1 deliveries done = %d, want 2", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM platform.event_deliveries WHERE event_id = $1`, v2); n != 0 {
		t.Errorf("v2 deliveries = %d, want none: no handler takes v2", n)
	}
	// A handler added later gets no delivery for an event fanned out before
	// it — there is no backfill — but does for the events published after.
	three := &recorder{}
	ds := []events.Handler{one.handler("test.one"), two.handler("test.two"), three.handler("test.three")}
	runOnce(t, events.NewDispatcher(pool, ds, nil, c.Now))
	if n := count(t, pool, `SELECT count(*) FROM platform.event_deliveries WHERE event_id = $1 AND handler = 'test.three'`, v1); n != 0 {
		t.Errorf("the new handler has %d deliveries of an event fanned out before it, want none", n)
	}
	later := publish(t, pool, thing{Aggregate: "a"}, c.Now())
	runOnce(t, events.NewDispatcher(pool, ds, nil, c.Now))
	if got := readDelivery(t, pool, later, "test.three"); got.state != "done" {
		t.Errorf("the new handler's delivery of a later event = %+v, want done", got)
	}
	if len(one.handled()) != 2 || len(two.handled()) != 2 || len(three.handled()) != 1 {
		t.Errorf("the handlers ran %v, %v and %v, want twice, twice and once", one.handled(), two.handled(), three.handled())
	}
}

// TestDispatch_OrdersPerAggregate: a later event of one aggregate waits
// while an earlier one is failed or dead; another aggregate goes on; skip
// and retry unblock (O3, O4).
func TestDispatch_OrdersPerAggregate(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	a1 := publish(t, pool, thing{Aggregate: "a", N: 1}, c.Now())
	a2 := publish(t, pool, thing{Aggregate: "a", N: 2}, c.Now())
	b1 := publish(t, pool, thing{Aggregate: "b", N: 1}, c.Now())
	a3 := publish(t, pool, thing{Aggregate: "a", N: 3}, c.Now())
	var broken sync.Map
	broken.Store(a1, true)
	rec := &recorder{fail: func(ev events.Delivered, _ int) error {
		if _, ok := broken.Load(ev.ID); ok {
			return errors.New("broken")
		}
		return nil
	}}
	d := events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, nil, c.Now)
	op := events.NewOperator(pool, c.Now)

	runOnce(t, d)
	if got := rec.handled(); len(got) != 2 || got[0] != a1 || got[1] != b1 {
		t.Fatalf("handled %v, want a1 then b1 — a2 waits behind failed a1", got)
	}
	if got := readDelivery(t, pool, a2, "test.h"); got.state != "pending" || got.attempts != 0 {
		t.Errorf("a2 = %+v, want pending, untried", got)
	}

	// Dead blocks too.
	if _, err := pool.Exec(context.Background(), `UPDATE platform.event_deliveries SET state = 'dead' WHERE event_id = $1`, a1); err != nil {
		t.Fatal(err)
	}
	c.Advance(24 * time.Hour)
	runOnce(t, d)
	if got := rec.handled(); len(got) != 2 {
		t.Fatalf("handled %v after a1 died, want nothing more", got)
	}

	// Retry: a1 pending again, attempts kept, and it now succeeds; a2 and
	// a3 follow in order in the same pass.
	broken.Delete(a1)
	if err := op.Retry(context.Background(), readDelivery(t, pool, a1, "test.h").id); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got := readDelivery(t, pool, a1, "test.h"); got.state != "pending" || got.attempts != 1 {
		t.Errorf("a1 after retry = %+v, want pending with its 1 attempt", got)
	}
	runOnce(t, d)
	if got := rec.handled(); len(got) != 5 || got[2] != a1 || got[3] != a2 || got[4] != a3 {
		t.Errorf("handled %v, want a1, a2, a3 in order after the retry", got)
	}
}

// TestDispatch_OrdersPerAggregateNotPerID: quote 42 dead does not hold back
// sales order 42 for a handler that takes both — the aggregate is the name
// less its last segment, with the id.
func TestDispatch_OrdersPerAggregateNotPerID(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	quote := publish(t, pool, quoteThing{ID: "42"}, c.Now())
	order := publish(t, pool, orderThing{ID: "42"}, c.Now())
	rec := &recorder{fail: func(ev events.Delivered, _ int) error {
		if ev.ID == quote {
			return errors.New("broken")
		}
		return nil
	}}
	h := rec.handler("test.h")
	h.Events = []events.EventRef{{Name: "test.quote.happened", Version: 1}, {Name: "test.sales_order.happened", Version: 1}}
	d := events.NewDispatcher(pool, []events.Handler{h}, nil, c.Now)
	runOnce(t, d)
	if _, err := pool.Exec(context.Background(), `UPDATE platform.event_deliveries SET state = 'dead' WHERE event_id = $1`, quote); err != nil {
		t.Fatal(err)
	}
	if got := readDelivery(t, pool, order, "test.h"); got.state != "done" {
		t.Errorf("sales order 42 = %+v, want done though quote 42 failed", got)
	}
	var aggregate string
	if err := pool.QueryRow(context.Background(), `SELECT aggregate FROM platform.event_deliveries WHERE event_id = $1`, order).Scan(&aggregate); err != nil || aggregate != "test.sales_order" {
		t.Errorf("the delivery's aggregate = %q, %v; want test.sales_order", aggregate, err)
	}
	// A later quote 42 still waits behind the dead one.
	quote2 := publish(t, pool, quoteThing{ID: "42"}, c.Now())
	runOnce(t, d)
	if got := readDelivery(t, pool, quote2, "test.h"); got.state != "pending" || got.attempts != 0 {
		t.Errorf("the later quote 42 = %+v, want pending behind the dead one", got)
	}
}

// TestDispatch_AnEarlierEventNotFannedOutHoldsBack: while another dispatcher
// holds an earlier event of an aggregate mid fan-out, a later event of it
// may be fanned out, but its delivery waits until the earlier one's is
// written, then they run in order.
func TestDispatch_AnEarlierEventNotFannedOutHoldsBack(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	c := newClock()
	first := publish(t, pool, thing{Aggregate: "a", N: 1}, c.Now())
	second := publish(t, pool, thing{Aggregate: "a", N: 2}, c.Now())
	other := publish(t, pool, thing{Aggregate: "b", N: 1}, c.Now())
	// The other dispatcher's fan-out, mid statement: it holds the first
	// event's row lock, its deliveries not yet committed.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `SELECT id FROM platform.events WHERE id = $1 FOR UPDATE`, first); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	d := events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, nil, c.Now)
	runOnce(t, d)
	if got := rec.handled(); len(got) != 1 || got[0] != other {
		t.Fatalf("handled %v, want only the other aggregate's event", got)
	}
	if got := readDelivery(t, pool, second, "test.h"); got.state != "pending" || got.attempts != 0 {
		t.Errorf("the second event = %+v, want pending behind the first, not fanned out yet", got)
	}
	if n := count(t, pool, `SELECT count(*) FROM platform.event_deliveries WHERE event_id = $1`, first); n != 0 {
		t.Fatalf("the locked event has %d deliveries, want none yet", n)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	runOnce(t, d)
	if got := rec.handled(); len(got) != 3 || got[1] != first || got[2] != second {
		t.Errorf("handled %v, want the other, then first and second in order", got)
	}
}

// TestDispatch_SkipUnblocks: the operator's skip marks a dead delivery done
// with its note, and the next one of its aggregate runs.
func TestDispatch_SkipUnblocks(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	a1 := publish(t, pool, thing{Aggregate: "a", N: 1}, c.Now())
	a2 := publish(t, pool, thing{Aggregate: "a", N: 2}, c.Now())
	rec := &recorder{fail: func(ev events.Delivered, _ int) error {
		if ev.ID == a1 {
			return errors.New("broken")
		}
		return nil
	}}
	d := events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, nil, c.Now)
	op := events.NewOperator(pool, c.Now)
	runOnce(t, d)
	if _, err := pool.Exec(context.Background(), `UPDATE platform.event_deliveries SET state = 'dead' WHERE event_id = $1`, a1); err != nil {
		t.Fatal(err)
	}
	dead, err := op.List(context.Background(), "dead")
	if err != nil || len(dead) != 1 || dead[0].EventID != a1 || dead[0].Handler != "test.h" || dead[0].Event != "test.thing.happened" ||
		dead[0].AggregateID != "a" || dead[0].LastError == nil || *dead[0].LastError != "broken" {
		t.Fatalf("List(dead) = %+v, %v", dead, err)
	}
	if err := op.Skip(context.Background(), dead[0].ID, "fixed by hand"); err != nil {
		t.Fatalf("Skip: %v", err)
	}
	got := readDelivery(t, pool, a1, "test.h")
	if got.state != "done" || got.note == nil || *got.note != "fixed by hand" || got.completed == nil {
		t.Errorf("a1 after skip = %+v", got)
	}
	runOnce(t, d)
	if got := readDelivery(t, pool, a2, "test.h"); got.state != "done" {
		t.Errorf("a2 after a1's skip = %+v, want done", got)
	}
	// A second skip, or a retry of a done one, is refused; an unknown id is
	// not found.
	if err := op.Skip(context.Background(), dead[0].ID, "again"); !errors.Is(err, events.ErrDeliveryState) {
		t.Errorf("Skip of a done delivery = %v, want ErrDeliveryState", err)
	}
	if err := op.Retry(context.Background(), dead[0].ID); !errors.Is(err, events.ErrDeliveryState) {
		t.Errorf("Retry of a done delivery = %v, want ErrDeliveryState", err)
	}
	if err := op.Retry(context.Background(), 999999); !errors.Is(err, events.ErrDeliveryNotFound) {
		t.Errorf("Retry of no delivery = %v, want ErrDeliveryNotFound", err)
	}
}

// TestDispatch_DeadAfterEightAttempts: the eighth failure is dead, logged at
// error, and never tried again on its own.
func TestDispatch_DeadAfterEightAttempts(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	id := publish(t, pool, thing{Aggregate: "a"}, c.Now())
	rec := &recorder{fail: func(events.Delivered, int) error { return errors.New(strings.Repeat("x", 3000)) }}
	var logs bytes.Buffer
	d := events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, slog.New(slog.NewTextHandler(&logs, nil)), c.Now)
	runOnce(t, d)
	got := readDelivery(t, pool, id, "test.h")
	if got.lastError == nil || len(*got.lastError) != 2000 {
		t.Fatalf("last_error is not cut to 2000: %+v", got)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE platform.event_deliveries SET attempts = 7 WHERE id = $1`, got.id); err != nil {
		t.Fatal(err)
	}
	c.Advance(12 * time.Hour)
	runOnce(t, d)
	if got := readDelivery(t, pool, id, "test.h"); got.state != "dead" || got.attempts != 8 {
		t.Fatalf("after the eighth failure: %+v, want dead with 8", got)
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "events delivery is dead") {
		t.Errorf("no error log for the dead delivery: %s", logs.String())
	}
	c.Advance(30 * 24 * time.Hour)
	runOnce(t, d)
	if n := len(rec.handled()); n != 2 {
		t.Errorf("handled %d times, want 2: dead waits for the operator", n)
	}
}

// TestDispatch_BackoffSteps pins 1m, 5m, 30m, 2h, 12h, then 12h.
func TestDispatch_BackoffSteps(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	id := publish(t, pool, thing{Aggregate: "a"}, c.Now())
	rec := &recorder{fail: func(events.Delivered, int) error { return errors.New("no") }}
	d := events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, nil, c.Now)
	for i, wait := range []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour, 12 * time.Hour} {
		runOnce(t, d)
		var next time.Time
		if err := pool.QueryRow(context.Background(), `SELECT next_attempt_at FROM platform.event_deliveries WHERE event_id = $1`, id).Scan(&next); err != nil {
			t.Fatal(err)
		}
		if got := next.Sub(c.Now()); got != wait {
			t.Errorf("after failure %d: next attempt in %v, want %v", i+1, got, wait)
		}
		c.Advance(wait)
	}
}

// TestDispatch_UnknownHandlerIsLeftAndWarned: a delivery to a handler no
// longer composed stays as it is, with a warning, and the pass goes on.
func TestDispatch_UnknownHandlerIsLeftAndWarned(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	id := publish(t, pool, thing{Aggregate: "a"}, c.Now())
	if _, err := pool.Exec(context.Background(), `INSERT INTO platform.event_deliveries
(event_id, handler, aggregate, aggregate_id, next_attempt_at, created_at, updated_at) VALUES ($1, 'gone.handler', 'test.thing', 'a', $2, $2, $2)`, id, c.Now()); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	var logs bytes.Buffer
	runOnce(t, events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, slog.New(slog.NewTextHandler(&logs, nil)), c.Now))
	if got := readDelivery(t, pool, id, "gone.handler"); got.state != "pending" || got.attempts != 0 {
		t.Errorf("the unknown handler's delivery = %+v, want left pending", got)
	}
	if got := readDelivery(t, pool, id, "test.h"); got.state != "done" {
		t.Errorf("the known handler's delivery = %+v, want done", got)
	}
	if n := strings.Count(logs.String(), "gone.handler"); n != 1 || !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("want one warning naming gone.handler, got: %s", logs.String())
	}
}

// TestDispatch_Prunes: events created over 30 days ago with every delivery
// done, or none, go with their deliveries; one with an undone delivery, or
// younger by created_at — whatever its occurred_at — stays.
func TestDispatch_Prunes(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	old := c.Now().Add(-31 * 24 * time.Hour)
	done := publish(t, pool, thing{Aggregate: "done"}, c.Now())
	stuck := publish(t, pool, thing{Aggregate: "stuck"}, c.Now())
	unheard := publish(t, pool, thingV2{Aggregate: "unheard"}, c.Now())
	young := publish(t, pool, thing{Aggregate: "young"}, old)
	if _, err := pool.Exec(context.Background(), `UPDATE platform.events SET created_at = $1 WHERE id = ANY($2)`,
		old, []int64{done, stuck, unheard}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE platform.events SET created_at = $1 WHERE id = $2`,
		c.Now().Add(-29*24*time.Hour), young); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{fail: func(ev events.Delivered, _ int) error {
		if ev.ID == stuck {
			return errors.New("stuck")
		}
		return nil
	}}
	runOnce(t, events.NewDispatcher(pool, []events.Handler{rec.handler("test.h")}, nil, c.Now))
	for id, want := range map[int64]int{done: 0, stuck: 1, unheard: 0, young: 1} {
		if n := count(t, pool, `SELECT count(*) FROM platform.events WHERE id = $1`, id); n != want {
			t.Errorf("event %d: %d rows, want %d", id, n, want)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM platform.event_deliveries WHERE event_id = $1`, done); n != 0 {
		t.Errorf("the pruned event's deliveries = %d, want cascaded away", n)
	}
}

// TestDispatch_TwoDispatchersNeverHandleOneDeliveryTwice runs two
// dispatchers at once over many events: every delivery is handled exactly
// once, each aggregate's in event order.
func TestDispatch_TwoDispatchersNeverHandleOneDeliveryTwice(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	c := newClock()
	const aggregates, perAggregate = 8, 15
	want := make(map[int64]string)
	for n := range perAggregate {
		for a := range aggregates {
			agg := fmt.Sprintf("agg-%d", a)
			want[publish(t, pool, thing{Aggregate: agg, N: n}, c.Now())] = agg
		}
	}
	rec := &recorder{}
	h := rec.handler("test.h")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := events.NewDispatcher(pool, []events.Handler{h}, nil, c.Now)
			for range 5 {
				if err := d.RunOnce(context.Background()); err != nil {
					errs[i] = err
					return
				}
			}
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("dispatcher %d: %v", i, err)
		}
	}
	// Each pass ends when it claims nothing, which it may while the other
	// holds the only claimable deliveries; one last pass finishes the rest.
	runOnce(t, events.NewDispatcher(pool, []events.Handler{h}, nil, c.Now))
	seen := make(map[int64]int)
	last := make(map[string]int64)
	for _, id := range rec.handled() {
		seen[id]++
		agg := want[id]
		if id < last[agg] {
			t.Errorf("aggregate %s: event %d handled after %d", agg, id, last[agg])
		}
		last[agg] = id
	}
	for id := range want {
		if seen[id] != 1 {
			t.Errorf("event %d handled %d times, want exactly once", id, seen[id])
		}
		if n := committed(t, pool, "test.h", id); n != 1 {
			t.Errorf("event %d committed %d times", id, n)
		}
	}
}

// TestDispatch_RunsOnAPoolOfOne: the dispatcher never holds one connection
// while asking for another, so a pool of one carries a whole pass — fan
// out, claim, handle, a failure's record, prune.
func TestDispatch_RunsOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	shared, _ := testdb.Migrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	one, err := db.Open(ctx, shared.Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(one.Close)
	c := newClock()
	ok := publish(t, shared, thing{Aggregate: "a"}, c.Now())
	bad := publish(t, shared, thing{Aggregate: "b"}, c.Now())
	rec := &recorder{fail: func(ev events.Delivered, _ int) error {
		if ev.ID == bad {
			return errors.New("bad")
		}
		return nil
	}}
	d := events.NewDispatcher(one, []events.Handler{rec.handler("test.h")}, nil, c.Now)
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce on a pool of one: %v", err)
	}
	if got := readDelivery(t, shared, ok, "test.h"); got.state != "done" {
		t.Errorf("ok = %+v, want done", got)
	}
	if got := readDelivery(t, shared, bad, "test.h"); got.state != "failed" || got.attempts != 1 {
		t.Errorf("bad = %+v, want failed once", got)
	}
}
