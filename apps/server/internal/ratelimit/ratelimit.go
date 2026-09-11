// Package ratelimit is a fixed-window rate limiter whose counters live in
// PostgreSQL (platform.rate_limit), so a limit holds across replicas and
// restarts rather than per process.
package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// defaultMessage is shown to a rejected client when Policy.Message is empty.
const defaultMessage = "Too many attempts. Please try again later."

// Policy is a named limit: at most Limit hits per client per Window.
type Policy struct {
	Name   string
	Limit  int
	Window time.Duration
	// Message overrides the 429 body's error message. Empty keeps
	// defaultMessage.
	Message string
	// NoRetryAfter omits the Retry-After header from the 429 response, for
	// throttles (such as the login attempt throttle) that must not tell a
	// caller exactly when to retry.
	NoRetryAfter bool
}

func (p Policy) validate() error {
	if p.Name == "" || p.Limit < 1 || p.Window < time.Second {
		return fmt.Errorf("ratelimit: invalid policy %+v (need a name, a limit of at least 1 and a window of at least 1s)", p)
	}
	if contains(p.Name, ':') {
		return fmt.Errorf("ratelimit: invalid policy %+v (policy names may not contain ':')", p)
	}
	return nil
}

// contains reports whether the string s contains any byte equal to b.
func contains(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

// Decision is the outcome of one hit.
type Decision struct {
	Allowed bool
	// RetryAfter is the time left in the current window when a hit is rejected.
	RetryAfter time.Duration
}

// Limiter records hits in PostgreSQL.
type Limiter struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a limiter on pool.
func New(pool *pgxpool.Pool) *Limiter {
	return &Limiter{pool: pool, now: time.Now}
}

// hitSQL counts one hit atomically: a new key or a newer window starts at 1,
// otherwise the counter increments. A hit stamped with an older window (a
// replica whose clock lags) counts toward the newest window recorded rather
// than resetting the counter or moving the window back. It returns the
// effective window start. Concurrent hits serialise on the row.
const hitSQL = `
INSERT INTO platform.rate_limit (key, window_start, hits)
VALUES ($1, $2, 1)
ON CONFLICT (key) DO UPDATE SET
    hits = CASE WHEN EXCLUDED.window_start > platform.rate_limit.window_start
                THEN 1
                ELSE platform.rate_limit.hits + 1 END,
    window_start = GREATEST(platform.rate_limit.window_start, EXCLUDED.window_start)
RETURNING hits, window_start`

// Hit records one hit by client under p and reports whether it is within the
// limit.
func (l *Limiter) Hit(ctx context.Context, p Policy, client string) (Decision, error) {
	if err := p.validate(); err != nil {
		return Decision{}, err
	}
	now := l.now().UTC()
	windowStart := now.Truncate(p.Window)

	var hits int
	var effectiveStart time.Time
	// Key construction: p.Name cannot contain ':' (validated above), so the first ':' is an unambiguous separator between policy and client.
	if err := l.pool.QueryRow(ctx, hitSQL, p.Name+":"+client, windowStart).Scan(&hits, &effectiveStart); err != nil {
		return Decision{}, fmt.Errorf("ratelimit: record hit: %w", err)
	}
	if hits <= p.Limit {
		return Decision{Allowed: true}, nil
	}
	return Decision{RetryAfter: effectiveStart.Add(p.Window).Sub(now)}, nil
}

// Allow is Hit under its original name, kept for existing callers.
func (l *Limiter) Allow(ctx context.Context, p Policy, client string) (Decision, error) {
	return l.Hit(ctx, p, client)
}

// Blocked reads the current window's hit count for client under p without
// recording a hit, and reports whether it is already at or past the limit.
// It mirrors the .NET LoginAttemptThrottle: a caller checks Blocked before
// doing expensive or sensitive work (such as verifying a password) so that
// the Limit-th failure blocks the very next attempt, rather than letting one
// more through before Hit itself starts rejecting.
//
// A missing row is not blocked: nothing has ever hit this key. A row left
// over from an older window is not blocked either: that window has expired,
// even though no new hit has arrived yet to advance it. A row whose window is
// current or newer than ours (a replica whose clock lags, as hitSQL also
// tolerates) is judged as stored.
func (l *Limiter) Blocked(ctx context.Context, p Policy, client string) (Decision, error) {
	if err := p.validate(); err != nil {
		return Decision{}, err
	}
	now := l.now().UTC()
	windowStart := now.Truncate(p.Window)

	var hits int
	var storedStart time.Time
	err := l.pool.QueryRow(ctx,
		`SELECT hits, window_start FROM platform.rate_limit WHERE key = $1`,
		p.Name+":"+client,
	).Scan(&hits, &storedStart)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Decision{Allowed: true}, nil
	case err != nil:
		return Decision{}, fmt.Errorf("ratelimit: read hit count: %w", err)
	}
	if storedStart.Before(windowStart) {
		return Decision{Allowed: true}, nil
	}
	if hits < p.Limit {
		return Decision{Allowed: true}, nil
	}
	return Decision{RetryAfter: storedStart.Add(p.Window).Sub(now)}, nil
}

// Reset clears client's counter under p, so its next Hit starts a fresh
// count. A successful login clears the throttle this way rather than waiting
// out the window.
func (l *Limiter) Reset(ctx context.Context, p Policy, client string) error {
	if err := p.validate(); err != nil {
		return err
	}
	if _, err := l.pool.Exec(ctx, `DELETE FROM platform.rate_limit WHERE key = $1`, p.Name+":"+client); err != nil {
		return fmt.Errorf("ratelimit: reset: %w", err)
	}
	return nil
}

// Middleware limits requests per client address (httpx.ClientIP, so it must
// run inside httpx.Forwarded). A counter that cannot be recorded fails the
// request closed with a 500 rather than letting it through unmetered.
func (l *Limiter) Middleware(p Policy) func(http.Handler) http.Handler {
	if err := p.validate(); err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d, err := l.Hit(r.Context(), p, httpx.ClientIP(r))
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			if !d.Allowed {
				Reject(w, r, p, d)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rejectBody is the {"error":{"code","message"}} shape Reject writes.
type rejectBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Reject writes the 429 response for a decision with Allowed = false: status
// 429, a JSON body carrying p.Message (or defaultMessage when empty), and a
// Retry-After header giving the whole seconds left in the window (at least
// 1), unless p.NoRetryAfter suppresses it. It is the one place this body is
// written, so every rejection - Middleware's and a caller's own, such as the
// login throttle's - looks the same on the wire.
func Reject(w http.ResponseWriter, _ *http.Request, p Policy, d Decision) {
	message := p.Message
	if message == "" {
		message = defaultMessage
	}
	if !p.NoRetryAfter {
		seconds := int(math.Ceil(d.RetryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(seconds, 1)))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)

	var body rejectBody
	body.Error.Code = "rate_limited"
	body.Error.Message = message
	// frontend-api-client displays error.message; the code is for callers that branch on it.
	data, _ := json.Marshal(body) // body is plain strings: Marshal cannot fail
	_, _ = w.Write(append(data, '\n'))
}
