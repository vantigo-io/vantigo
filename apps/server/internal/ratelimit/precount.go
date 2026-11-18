package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// flushEvery is how often the flusher adds what Count counted in process to
// the shared rows (API access design K24).
const flushEvery = time.Second

// flushTimeout bounds one flush, which runs on no request's context.
const flushTimeout = 5 * time.Second

// flushSQL adds hits counted in process to the shared row of their own
// window only. A row that has moved on to a newer window is left alone and
// the hits are dropped: their window is over, and adding them to the next
// one would count them twice.
const flushSQL = `
UPDATE platform.rate_limit SET hits = hits + $3
WHERE key = $1 AND window_start = $2
RETURNING hits`

// preCount is Count's state for one policy and client: the window it counts
// in, the shared row's count as last read, the hits counted here since, and
// whether a flush of them is under way. seeding is open while the window's
// first hit is being recorded in the shared row; every other Count of the
// key waits for it, so the seed is taken once and every hit after it counts
// exactly once.
type preCount struct {
	window   time.Time
	length   time.Duration
	shared   int
	pending  int
	flushing bool
	seeding  chan struct{}
}

// Count is Hit for a key that is hit often, such as an integration's API key
// requests (K24). The first hit of a window is recorded in the shared row as
// Hit records it, once per process and key: concurrent callers wait for it.
// The hits after it are counted in process, and a flusher adds them to that
// row every second, reading back what every replica has counted. A request
// then waits on the database once a window, not once a hit.
//
// The shared row stays the truth that replicas agree on. Each replica is at
// most about a second of its own traffic behind in it, whether or not more
// requests arrive, so together the replicas may let through up to a second's
// worth more than Limit; a process that stops without Close loses at most a
// second of its count. Close stops the flusher after a last flush.
func (l *Limiter) Count(ctx context.Context, p Policy, client string) (Decision, error) {
	if err := p.validate(); err != nil {
		return Decision{}, err
	}
	l.flusher.Do(func() { go l.runFlusher() })
	key := p.Name + ":" + client
	for {
		now := l.now().UTC()
		window := now.Truncate(p.Window)

		l.mu.Lock()
		if l.counts == nil {
			l.counts = map[string]*preCount{}
		}
		st, ok := l.counts[key]
		switch {
		case ok && st.window.Equal(window) && st.seeding != nil:
			// Another request is recording the window's first hit: wait for
			// it, then count on top of it.
			seeding := st.seeding
			l.mu.Unlock()
			select {
			case <-seeding:
				continue
			case <-ctx.Done():
				return Decision{}, fmt.Errorf("ratelimit: record hit: %w", ctx.Err())
			}
		case ok && st.window.Equal(window):
			st.pending++
			d := decide(p, st.shared+st.pending, st.window, now)
			l.mu.Unlock()
			return d, nil
		case ok && st.window.After(window):
			// This clock lags the window already counted: judge by the
			// shared row alone, as Hit does.
			l.mu.Unlock()
			return l.Hit(ctx, p, client)
		}
		// The window's first hit here, or a new window: the count of an
		// older window is let go, its window being over.
		seed := &preCount{window: window, length: p.Window, seeding: make(chan struct{})}
		l.counts[key] = seed
		l.mu.Unlock()
		return l.seed(ctx, p, key, seed, now)
	}
}

// seed records the window's first hit in the shared row, which also says
// what the other replicas have counted, and opens the window for the
// callers waiting on it.
func (l *Limiter) seed(ctx context.Context, p Policy, key string, st *preCount, now time.Time) (Decision, error) {
	var hits int
	var start time.Time
	err := l.pool.QueryRow(ctx, hitSQL, key, st.window, 1).Scan(&hits, &start)

	l.mu.Lock()
	defer l.mu.Unlock()
	seeding := st.seeding
	st.seeding = nil
	defer close(seeding)
	if err != nil || !start.Equal(st.window) {
		// A failed seed, or a shared row in a newer window than this clock's,
		// keeps no count: the next caller seeds again.
		if l.counts[key] == st {
			delete(l.counts, key)
		}
		if err != nil {
			return Decision{}, fmt.Errorf("ratelimit: record hit: %w", err)
		}
		return decide(p, hits, start, now), nil
	}
	st.shared = hits
	return decide(p, st.shared+st.pending, st.window, now), nil
}

// runFlusher flushes every flushEvery until Close, and once more then.
func (l *Limiter) runFlusher() {
	defer close(l.stopped)
	ticker := time.NewTicker(l.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.flushAll()
		case <-l.stop:
			l.flushAll()
			return
		}
	}
}

// Close stops the flusher after a last flush, and waits for it at most
// flushTimeout. A Limiter whose Count was never called has nothing to stop.
// Count must not be called after Close.
func (l *Limiter) Close() {
	l.closing.Do(func() {
		started := true
		l.flusher.Do(func() { started = false })
		close(l.stop)
		if !started {
			return
		}
		select {
		case <-l.stopped:
		case <-time.After(flushTimeout):
		}
	})
}

type flushTask struct {
	key    string
	window time.Time
	n      int
}

// flushAll adds every key's hits counted in process to its shared row, one
// key after another, and then lets go of the keys whose window is over.
func (l *Limiter) flushAll() {
	l.mu.Lock()
	var tasks []flushTask
	for key, st := range l.counts {
		if st.pending > 0 && !st.flushing && st.seeding == nil {
			st.flushing = true
			tasks = append(tasks, flushTask{key, st.window, st.pending})
		}
	}
	l.mu.Unlock()
	for _, task := range tasks {
		l.flush(task)
	}
	l.mu.Lock()
	l.prune(l.now().UTC())
	l.mu.Unlock()
}

// flush adds task's hits to the shared row of their window and reads back
// its count. When the row has moved on to a newer window, the hits are
// dropped; a flush that fails otherwise keeps them to try again.
func (l *Limiter) flush(task flushTask) {
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	var hits int
	err := l.pool.QueryRow(ctx, flushSQL, task.key, task.window, task.n).Scan(&hits)

	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.counts[task.key]
	if !ok || !st.window.Equal(task.window) {
		return
	}
	st.flushing = false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		st.pending -= task.n
	case err == nil:
		st.pending -= task.n
		st.shared = hits
	}
}

// prune lets go of every pre-count whose window is over, so the map holds
// only the keys hit in a current window. The flusher calls it once a flush,
// after adding what those windows counted. l.mu is held.
func (l *Limiter) prune(now time.Time) {
	for key, st := range l.counts {
		if !st.flushing && st.seeding == nil && !now.Before(st.window.Add(st.length)) {
			delete(l.counts, key)
		}
	}
}
