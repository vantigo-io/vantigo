// Package dbclock is the process's clock anchored to PostgreSQL's
// (Kubernetes readiness design KR7–KR9). Lease, claim and window deadlines
// are computed in Go from Deps.Clock and compared in SQL against deadlines
// another process wrote, so every process must read the same time: a pod
// whose clock runs ahead would take over a claim another pod still holds.
//
// Clock.Now is the local clock plus an offset measured against the
// database's clock_timestamp(), corrected by half the round trip. The first
// measurement applies in full; later ones, every minute, keep the median of
// the last five and move the offset by at most 100 ms per minute, so one slow
// round trip cannot jump the clock. Now never returns a time before one it
// already returned.
package dbclock

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Measurer reports the database's clock minus the local clock: positive when
// the database is ahead.
type Measurer func(ctx context.Context) (time.Duration, error)

// Defaults are KR7 and KR8's numbers.
const (
	// Every is how often the offset is measured again.
	Every = time.Minute
	// Window is how many measurements the median is taken over.
	Window = 5
	// Slew is how far the offset may move per minute after the first.
	Slew = 100 * time.Millisecond
	// warnEvery bounds the skew warning to one an hour.
	warnEvery = time.Hour
	// measureTimeout bounds one measurement.
	measureTimeout = 5 * time.Second
	// initialSamples are taken back to back at start, their median applied.
	initialSamples = 3
)

const meterName = "vantigo.dbclock"

// Options configure a Clock.
type Options struct {
	Measure Measurer
	// MaxSkew is CLOCK_MAX_SKEW: an offset larger than it, either way, logs
	// a warning at most once an hour. 0 disables the warning.
	MaxSkew time.Duration
	Logger  *slog.Logger
	// Local is the local clock; nil is time.Now. A test passes its own.
	Local func() time.Time
	// Meter is where the gauge vantigo.clock.offset is registered; nil is
	// the global provider.
	Meter metric.MeterProvider
}

// Clock is the database-anchored clock. Its zero value is not usable; build
// one with New.
type Clock struct {
	measure Measurer
	maxSkew time.Duration
	logger  *slog.Logger
	local   func() time.Time

	// offset is the applied offset in nanoseconds; last is the latest
	// instant Now returned, in Unix nanoseconds, for the clamp.
	offset atomic.Int64
	last   atomic.Int64

	mu       sync.Mutex
	anchored bool
	samples  []time.Duration
	lastWarn time.Time

	gauge metric.Registration
}

// New returns a clock on o.Measure with a zero offset: until Sync succeeds
// it reads the local clock.
func New(o Options) *Clock {
	c := &Clock{measure: o.Measure, maxSkew: o.MaxSkew, logger: o.Logger, local: o.Local}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	if c.local == nil {
		c.local = time.Now
	}
	mp := o.Meter
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(meterName)
	if g, err := meter.Float64ObservableGauge("vantigo.clock.offset",
		metric.WithDescription("The database's clock minus this process's, as applied to Deps.Clock."),
		metric.WithUnit("s")); err == nil {
		c.gauge, _ = meter.RegisterCallback(func(_ context.Context, obs metric.Observer) error {
			obs.ObserveFloat64(g, c.Offset().Seconds())
			return nil
		}, g)
	}
	return c
}

// Now is the local clock plus the offset, never before a time it already
// returned. The time it returns carries no monotonic reading: the clamp is
// on the wall clock, which is what a lease compares in SQL, and a monotonic
// reading would let two calls compare the other way round in Go.
func (c *Clock) Now() time.Time {
	t := c.local().Add(time.Duration(c.offset.Load())).Round(0)
	n := t.UnixNano()
	for {
		last := c.last.Load()
		if n <= last {
			return t.Add(time.Duration(last - n))
		}
		if c.last.CompareAndSwap(last, n) {
			return t
		}
	}
}

// Offset is the offset Now applies.
func (c *Clock) Offset() time.Duration { return time.Duration(c.offset.Load()) }

// Sync takes the first measurement and applies it in full: the median of a
// few samples back to back, so one slow round trip at start does not set it.
// It returns the last error when no sample succeeded; the clock then keeps
// reading the local clock, and the next Tick takes the first measurement
// the same way, from several samples, rather than one.
func (c *Clock) Sync(ctx context.Context) error {
	var got []time.Duration
	var err error
	for range initialSamples {
		var d time.Duration
		if d, err = c.sample(ctx); err == nil {
			got = append(got, d)
		}
	}
	if len(got) == 0 {
		return err
	}
	c.observe(median(got))
	return nil
}

// Run measures every Every until ctx ends, then unregisters the gauge.
func (c *Clock) Run(ctx context.Context) {
	defer func() {
		if c.gauge != nil {
			_ = c.gauge.Unregister()
		}
	}()
	t := time.NewTicker(Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Tick(ctx)
		}
	}
}

// Tick takes one measurement and moves the offset toward the median of the
// last Window, by at most Slew. Until a first measurement has succeeded it
// takes that one instead, as Sync does. Run calls it every Every; a test
// calls it directly.
func (c *Clock) Tick(ctx context.Context) {
	if !c.isAnchored() {
		if err := c.Sync(ctx); err != nil && ctx.Err() == nil {
			c.logger.WarnContext(ctx, "clock: could not measure the database's clock; still using this host's", "error", err)
		}
		return
	}
	d, err := c.sample(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.WarnContext(ctx, "clock: could not measure the database's clock; keeping the offset", "error", err)
		}
		return
	}
	c.observe(d)
}

func (c *Clock) isAnchored() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.anchored
}

func (c *Clock) sample(ctx context.Context) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, measureTimeout)
	defer cancel()
	return c.measure(ctx)
}

// observe records a measurement and applies it: in full the first time,
// slewed toward the window's median after.
func (c *Clock) observe(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.samples = append(c.samples, d)
	if len(c.samples) > Window {
		c.samples = c.samples[len(c.samples)-Window:]
	}
	target := median(c.samples)
	if !c.anchored {
		c.anchored = true
		c.offset.Store(int64(target))
	} else {
		current := time.Duration(c.offset.Load())
		step := min(max(target-current, -Slew), Slew)
		c.offset.Store(int64(current + step))
	}
	c.warnSkew(target)
}

func (c *Clock) warnSkew(measured time.Duration) {
	if c.maxSkew <= 0 || abs(measured) <= c.maxSkew {
		return
	}
	now := c.local()
	if !c.lastWarn.IsZero() && now.Sub(c.lastWarn) < warnEvery {
		return
	}
	c.lastWarn = now
	c.logger.Warn("clock: this host's clock is off from the database's by more than CLOCK_MAX_SKEW; leases follow the database's clock, but check the node's time synchronisation",
		"offset", measured, "max_skew", c.maxSkew)
}

func median(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// PoolMeasurer measures against pool: the database's clock_timestamp()
// minus the midpoint of the local clock before and after the round trip.
// clock_timestamp, not now(), because now() is the transaction's start. The
// connection is acquired first, so a wait for a free one is not counted as
// round trip.
func PoolMeasurer(pool *pgxpool.Pool) Measurer {
	return func(ctx context.Context) (time.Duration, error) {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return 0, err
		}
		defer conn.Release()
		var db time.Time
		before := time.Now()
		if err := conn.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&db); err != nil {
			return 0, err
		}
		after := time.Now()
		mid := before.Add(after.Sub(before) / 2)
		return db.Sub(mid.Round(0)), nil
	}
}
