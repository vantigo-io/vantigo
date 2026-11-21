package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/health"
)

// lifecycle is what readiness knows beyond the dependencies, and the one
// deadline the shutdown tail runs under (Kubernetes readiness design KR4,
// KR5).
//
// Readiness fails with "schema" until the database is at the binary's
// schema, with "startup" until the process is serving, and with "draining"
// from the first shutdown signal on. The probes are served from the start —
// the management listener opens before the schema wait — so an orchestrator
// can tell a pod that is waiting from one that is broken.
type lifecycle struct {
	schema   atomic.Bool
	started  atomic.Bool
	draining atomic.Bool

	// delay is SHUTDOWN_DRAIN_DELAY, timeout SHUTDOWN_TIMEOUT.
	delay, timeout time.Duration

	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
}

func newLifecycle(delay, timeout time.Duration) *lifecycle {
	return &lifecycle{delay: delay, timeout: timeout}
}

var (
	errSchemaBehind = errors.New("the database is not yet at this binary's schema")
	errStarting     = errors.New("the process is starting")
	errDraining     = errors.New("the process is shutting down")
)

// checks are the lifecycle's readiness checks, beside the dependencies'.
func (l *lifecycle) checks() []health.Check {
	return []health.Check{
		{Name: "schema", Run: func(context.Context) error { return failUnless(l.schema.Load(), errSchemaBehind) }},
		{Name: "startup", Run: func(context.Context) error { return failUnless(l.started.Load(), errStarting) }},
		{Name: "draining", Run: func(context.Context) error { return failUnless(!l.draining.Load(), errDraining) }},
	}
}

func failUnless(ok bool, err error) error {
	if ok {
		return nil
	}
	return err
}

// budget is the shutdown tail's one context: its SHUTDOWN_TIMEOUT starts at
// the first call — after the drain delay on the graceful path, at the first
// deferred close on an early return — and every later call shares it, so
// the HTTP drain, the workers, the management listener, the live hub, the
// rate limiter's flush and the pool close together take SHUTDOWN_TIMEOUT
// at most, not each their own.
func (l *lifecycle) budget() context.Context {
	l.once.Do(func() { l.ctx, l.cancel = context.WithTimeout(context.Background(), l.timeout) })
	return l.ctx
}

// remaining is what is left of the budget, for a close that takes a
// duration rather than a context. Never negative.
func (l *lifecycle) remaining() time.Duration {
	deadline, _ := l.budget().Deadline()
	return max(time.Until(deadline), 0)
}

// release frees the budget's timer once serve has returned.
func (l *lifecycle) release() {
	l.budget()
	l.cancel()
}
