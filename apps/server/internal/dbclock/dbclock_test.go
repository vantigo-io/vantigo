package dbclock_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/dbclock"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// fakeMeasurer answers the offsets it is given, in order, repeating the last.
type fakeMeasurer struct {
	mu      sync.Mutex
	offsets []time.Duration
	err     error
}

func (f *fakeMeasurer) set(offsets ...time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offsets, f.err = offsets, nil
}

func (f *fakeMeasurer) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeMeasurer) measure(context.Context) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	d := f.offsets[0]
	if len(f.offsets) > 1 {
		f.offsets = f.offsets[1:]
	}
	return d, nil
}

// fixedLocal is a local clock a test moves by hand.
type fixedLocal struct {
	mu sync.Mutex
	t  time.Time
}

func (l *fixedLocal) now() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t
}

func (l *fixedLocal) set(t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.t = t
}

var epoch = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

func newClock(t *testing.T, m *fakeMeasurer, local *fixedLocal, logs *bytes.Buffer, maxSkew time.Duration) *dbclock.Clock {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	if logs != nil {
		logger = slog.New(slog.NewTextHandler(logs, nil))
	}
	return dbclock.New(dbclock.Options{Measure: m.measure, MaxSkew: maxSkew, Logger: logger, Local: local.now})
}

// KR8: the first measurement applies in full, before anything else reads the
// clock.
func TestSync_AppliesTheFirstMeasurementInFull(t *testing.T) {
	m := &fakeMeasurer{}
	m.set(3*time.Second, 3*time.Second, 3*time.Second)
	local := &fixedLocal{t: epoch}
	c := newClock(t, m, local, nil, 0)
	if got := c.Now(); !got.Equal(epoch) {
		t.Errorf("before Sync: %v, want the local clock", got)
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Offset() != 3*time.Second {
		t.Errorf("offset = %v, want 3s", c.Offset())
	}
	local.set(epoch.Add(time.Minute))
	if got := c.Now(); !got.Equal(epoch.Add(time.Minute + 3*time.Second)) {
		t.Errorf("Now = %v, want local + 3s", got)
	}
}

// The first measurement is the median of its samples, so one slow round trip
// at start does not set it.
func TestSync_TakesTheMedianOfItsSamples(t *testing.T) {
	m := &fakeMeasurer{}
	m.set(time.Second, 900*time.Millisecond, 40*time.Second)
	c := newClock(t, m, &fixedLocal{t: epoch}, nil, 0)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Offset() != time.Second {
		t.Errorf("offset = %v, want the median 1s", c.Offset())
	}
}

// A failed start keeps the local clock, and the next measurement that
// succeeds is the first and applies in full.
func TestSync_AFailureLeavesTheFirstMeasurementForLater(t *testing.T) {
	m := &fakeMeasurer{}
	m.fail(errors.New("down"))
	c := newClock(t, m, &fixedLocal{t: epoch}, nil, 0)
	if err := c.Sync(context.Background()); err == nil {
		t.Fatal("Sync = nil, want the measurer's error")
	}
	if c.Offset() != 0 {
		t.Errorf("offset = %v, want 0", c.Offset())
	}
	m.set(2*time.Second, 2*time.Second, 2*time.Second)
	c.Tick(context.Background())
	if c.Offset() != 2*time.Second {
		t.Errorf("offset = %v, want 2s in full", c.Offset())
	}
}

// The late first measurement is the median of its samples too, so one slow
// round trip after a failed start cannot set the clock.
func TestTick_ALateFirstMeasurementTakesTheMedian(t *testing.T) {
	m := &fakeMeasurer{}
	m.fail(errors.New("down"))
	c := newClock(t, m, &fixedLocal{t: epoch}, nil, 0)
	if err := c.Sync(context.Background()); err == nil {
		t.Fatal("Sync = nil, want the measurer's error")
	}
	m.set(time.Second, 40*time.Second, 1100*time.Millisecond)
	c.Tick(context.Background())
	if c.Offset() != 1100*time.Millisecond {
		t.Errorf("offset = %v, want the median 1.1s, not one sample", c.Offset())
	}
}

// KR8: after the first, the offset moves toward the median of the last five
// by at most 100 ms a minute, and a single outlier moves nothing.
func TestTick_SlewsTowardTheMedianOfTheLastFive(t *testing.T) {
	m := &fakeMeasurer{}
	m.set(0)
	c := newClock(t, m, &fixedLocal{t: epoch}, nil, 0)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	c.Tick(context.Background())
	c.Tick(context.Background()) // the window is [0, 0, 0]
	m.set(10 * time.Second)      // one slow round trip: the median stays 0
	c.Tick(context.Background())
	if c.Offset() != 0 {
		t.Fatalf("after one outlier: offset = %v, want 0", c.Offset())
	}

	m.set(time.Second)
	want := []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond, 400 * time.Millisecond}
	// [0 0 0 10s 1s] has median 0; from [0 0 10s 1s 1s] on it is 1s.
	for i, w := range want {
		c.Tick(context.Background())
		if c.Offset() != w {
			t.Errorf("tick %d: offset = %v, want %v", i+1, c.Offset(), w)
		}
	}
}

// A failed measurement after the first keeps the offset.
func TestTick_AFailureKeepsTheOffset(t *testing.T) {
	m := &fakeMeasurer{}
	m.set(time.Second)
	c := newClock(t, m, &fixedLocal{t: epoch}, nil, 0)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.fail(errors.New("down"))
	c.Tick(context.Background())
	if c.Offset() != time.Second {
		t.Errorf("offset = %v, want 1s", c.Offset())
	}
}

// KR8: the clock never runs backwards, not when the offset shrinks and not
// when the local clock steps back.
func TestNow_NeverRunsBackwards(t *testing.T) {
	m := &fakeMeasurer{}
	m.set(time.Second)
	local := &fixedLocal{t: epoch}
	c := newClock(t, m, local, nil, 0)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := c.Now()

	m.set(-5 * time.Second)
	for range 5 {
		c.Tick(context.Background())
	}
	if got := c.Now(); got.Before(first) {
		t.Errorf("after the offset shrank: %v, before %v", got, first)
	}
	local.set(epoch.Add(-time.Hour))
	if got := c.Now(); got.Before(first) {
		t.Errorf("after the local clock stepped back: %v, before %v", got, first)
	}
	local.set(epoch.Add(time.Hour))
	if got := c.Now(); !got.After(first) {
		t.Errorf("once the local clock passes the clamp: %v, want after %v", got, first)
	}
}

// The clamp is safe under concurrent callers: no call sees a time before one
// that finished before it started.
func TestNow_ConcurrentCallsAreMonotonic(t *testing.T) {
	m := &fakeMeasurer{}
	m.set(0)
	c := dbclock.New(dbclock.Options{Measure: m.measure, Logger: slog.New(slog.DiscardHandler)})
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			prev := c.Now()
			for range 2000 {
				now := c.Now()
				if now.Before(prev) {
					t.Errorf("went backwards: %v after %v", now, prev)
					return
				}
				prev = now
			}
		})
	}
	wg.Wait()
}

// KR9: an offset above CLOCK_MAX_SKEW logs a warning, at most once an hour.
func TestSkew_WarnsAtMostOnceAnHour(t *testing.T) {
	var logs bytes.Buffer
	m := &fakeMeasurer{}
	m.set(10 * time.Second)
	local := &fixedLocal{t: epoch}
	c := newClock(t, m, local, &logs, 5*time.Second)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Tick(context.Background())
	local.set(epoch.Add(30 * time.Minute))
	c.Tick(context.Background())
	if n := strings.Count(logs.String(), "CLOCK_MAX_SKEW"); n != 1 {
		t.Errorf("%d warnings in the first hour, want 1:\n%s", n, logs.String())
	}
	local.set(epoch.Add(61 * time.Minute))
	c.Tick(context.Background())
	if n := strings.Count(logs.String(), "CLOCK_MAX_SKEW"); n != 2 {
		t.Errorf("%d warnings after an hour, want 2", n)
	}
}

func TestSkew_NoWarningWithinTheLimit(t *testing.T) {
	var logs bytes.Buffer
	m := &fakeMeasurer{}
	m.set(-4 * time.Second)
	c := newClock(t, m, &fixedLocal{t: epoch}, &logs, 5*time.Second)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "CLOCK_MAX_SKEW") {
		t.Errorf("warned within the limit: %s", logs.String())
	}
}

// PoolMeasurer against a real PostgreSQL on the same host: the offset is
// small, and the anchored clock reads close to the database's.
func TestPoolMeasurer(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	d, err := dbclock.PoolMeasurer(pool)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d > 2*time.Second || d < -2*time.Second {
		t.Errorf("offset against a local database = %v, want near 0", d)
	}
	c := dbclock.New(dbclock.Options{Measure: dbclock.PoolMeasurer(pool), Logger: slog.New(slog.DiscardHandler)})
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var db time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&db); err != nil {
		t.Fatal(err)
	}
	if diff := c.Now().Sub(db); diff > time.Second || diff < -time.Second {
		t.Errorf("anchored clock is %v from the database's", diff)
	}
}
