package ratelimit

import (
	"context"
	"fmt"
	"time"
)

// flushEvery is how often Count adds what it counted in process to the shared
// row (API access design K24).
const flushEvery = time.Second

// flushTimeout bounds one flush, which runs on no request's context.
const flushTimeout = 5 * time.Second

// preCount is Count's state for one policy and client: the window it counts
// in, the shared row's count as last read, the hits counted here since, and
// whether a flush of them is under way.
type preCount struct {
	window    time.Time
	length    time.Duration
	shared    int
	pending   int
	flushedAt time.Time
	flushing  bool
}

// Count is Hit for a key that is hit often, such as an integration's API key
// requests (K24): the first hit of a window is recorded in the shared row as
// Hit records it, and the hits after it are counted in process and added to
// that row in the background at most once a second, reading back what every
// replica has counted. A request then waits on the database once a window,
// not once a hit. The shared row stays the truth that replicas agree on; a
// replica's own hits of the last second are not in it yet, so together the
// replicas may let through up to a second's worth more than Limit, and a
// process that stops loses at most a second of its count.
func (l *Limiter) Count(ctx context.Context, p Policy, client string) (Decision, error) {
	if err := p.validate(); err != nil {
		return Decision{}, err
	}
	key := p.Name + ":" + client
	now := l.now().UTC()
	window := now.Truncate(p.Window)

	l.mu.Lock()
	st, ok := l.counts[key]
	if ok && !st.window.Before(window) {
		st.pending++
		d := decide(p, st.shared+st.pending, st.window, now)
		if !st.flushing && now.Sub(st.flushedAt) >= flushEvery && st.pending > 0 {
			st.flushing = true
			go l.flush(key, st.window, st.pending)
		}
		l.mu.Unlock()
		return d, nil
	}
	l.mu.Unlock()

	// The window's first hit here: recorded in the shared row, which also
	// says what the other replicas have counted.
	var hits int
	var start time.Time
	if err := l.pool.QueryRow(ctx, hitSQL, key, window, 1).Scan(&hits, &start); err != nil {
		return Decision{}, fmt.Errorf("ratelimit: record hit: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts == nil {
		l.counts = map[string]*preCount{}
	}
	st, ok = l.counts[key]
	switch {
	case !ok || st.window.Before(start):
		// A count of an older window is let go: its window is over.
		l.prune(now)
		st = &preCount{window: start, length: p.Window, shared: hits, flushedAt: now}
		l.counts[key] = st
	case st.window.Equal(start):
		// Another request seeded the window at the same time; this hit is in
		// the shared row already.
		st.shared = max(st.shared, hits)
	default:
		// The shared row is in an older window than ours (a replica whose
		// clock runs ahead set ours): judge by the row alone.
		return decide(p, hits, start, now), nil
	}
	return decide(p, st.shared+st.pending, st.window, now), nil
}

// flush adds n hits counted in process for key in window to the shared row
// and reads back its count. A flush that fails keeps them to try again.
func (l *Limiter) flush(key string, window time.Time, n int) {
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	var hits int
	var start time.Time
	err := l.pool.QueryRow(ctx, hitSQL, key, window, n).Scan(&hits, &start)

	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.counts[key]
	if !ok || !st.window.Equal(window) {
		return
	}
	st.flushing = false
	if err != nil {
		return
	}
	st.pending -= n
	st.flushedAt = l.now().UTC()
	if start.Equal(window) {
		st.shared = hits
	}
}

// prune lets go of every pre-count whose window is over, so the map holds
// only the keys hit in a current window. l.mu is held.
func (l *Limiter) prune(now time.Time) {
	for key, st := range l.counts {
		if !st.flushing && !now.Before(st.window.Add(st.length)) {
			delete(l.counts, key)
		}
	}
}
