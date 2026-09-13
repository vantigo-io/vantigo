package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeWorker records its calls and lets a test control exactly when Run
// returns, so these tests never need to sleep for real time to let a
// worker "do its poll": run is invoked synchronously from the runner's
// goroutine and decides everything.
type fakeWorker struct {
	name     string
	interval time.Duration
	calls    atomic.Int32
	run      func(ctx context.Context) error
}

func (f *fakeWorker) Name() string            { return f.name }
func (f *fakeWorker) Interval() time.Duration { return f.interval }
func (f *fakeWorker) Run(ctx context.Context) error {
	f.calls.Add(1)
	return f.run(ctx)
}

// waitFor blocks on ch until it fires or the guard elapses, failing the
// test on timeout. It exists only as a hang-safety net for goroutine
// synchronization — nothing here uses it to simulate a poll interval or
// the passage of real time.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// TestRunner_StartRunsEveryWorkerConcurrently proves Start launches each
// worker as its own goroutine rather than one after another: both fake
// workers signal they entered Run before either is allowed to return, which
// a sequential implementation (running the next worker only once the
// previous one's Run returns) could never observe.
func TestRunner_StartRunsEveryWorkerConcurrently(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})

	mk := func(name string) *fakeWorker {
		return &fakeWorker{name: name, interval: time.Minute, run: func(ctx context.Context) error {
			entered <- name
			<-release
			return nil
		}}
	}
	a, b := mk("a"), mk("b")

	r := NewRunner(slog.New(slog.DiscardHandler))
	r.Start(context.Background(), []Worker{a, b})

	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-entered:
			seen[name] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("only saw %v enter Run before the timeout", seen)
		}
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("seen = %v, want both a and b", seen)
	}

	close(release)
	if err := r.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if a.calls.Load() != 1 || b.calls.Load() != 1 {
		t.Errorf("calls = a:%d b:%d, want 1 each", a.calls.Load(), b.calls.Load())
	}
}

// TestRunner_WorkerErrorIsLoggedAndDoesNotStopSiblings is the bite for "a
// worker returning an error is logged and does not take down its
// siblings": worker a fails immediately, worker b is deliberately left
// running (blocked on its own channel, never selecting on ctx) to prove the
// runner does nothing that would stop it — no shared cancellation, no
// early Wait return — because of a's error.
func TestRunner_WorkerErrorIsLoggedAndDoesNotStopSiblings(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	bEntered := make(chan struct{})
	bRelease := make(chan struct{})
	a := &fakeWorker{name: "a", interval: time.Second, run: func(ctx context.Context) error {
		return errors.New("boom")
	}}
	b := &fakeWorker{name: "b", interval: time.Second, run: func(ctx context.Context) error {
		close(bEntered)
		<-bRelease
		return nil
	}}

	r := NewRunner(logger)
	r.Start(context.Background(), []Worker{a, b})
	waitFor(t, bEntered, "b to start")

	// a has already returned its error (Start does not block on it); b is
	// still blocked on bRelease, which only this goroutine controls — proof
	// that nothing about a's failure reached b.
	close(bRelease)
	if err := r.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if b.calls.Load() != 1 {
		t.Errorf("b.calls = %d, want 1: a's error must not have stopped b", b.calls.Load())
	}
	logs := buf.String()
	if !strings.Contains(logs, `"worker":"a"`) || !strings.Contains(logs, "boom") {
		t.Errorf("logs are missing a's error: %s", logs)
	}
	if !strings.Contains(logs, `"worker":"b"`) || strings.Contains(logs, `"worker":"b","error"`) {
		t.Errorf("logs show b failing when it returned cleanly: %s", logs)
	}
}

// TestRunner_PanicIsRecoveredAndDoesNotStopSiblingsOrTheProcess is the bite
// for the panic decision: a panicking worker is recovered rather than
// crashing the process (an unrecovered panic in a's goroutine would crash
// this entire test binary, not just fail one assertion — the strongest
// possible check that recover() is really there) and, like a returned
// error, does not stop b.
func TestRunner_PanicIsRecoveredAndDoesNotStopSiblingsOrTheProcess(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	bEntered := make(chan struct{})
	bRelease := make(chan struct{})
	a := &fakeWorker{name: "a", interval: time.Second, run: func(ctx context.Context) error {
		panic("kaboom")
	}}
	b := &fakeWorker{name: "b", interval: time.Second, run: func(ctx context.Context) error {
		close(bEntered)
		<-bRelease
		return nil
	}}

	r := NewRunner(logger)
	r.Start(context.Background(), []Worker{a, b})
	waitFor(t, bEntered, "b to start")
	close(bRelease)

	if err := r.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if b.calls.Load() != 1 {
		t.Errorf("b.calls = %d, want 1: a's panic must not have stopped b", b.calls.Load())
	}
	logs := buf.String()
	if !strings.Contains(logs, `"worker":"a"`) || !strings.Contains(logs, "kaboom") {
		t.Errorf("logs are missing a's panic: %s", logs)
	}
	// The stack must actually be the panicking goroutine's, not an empty or
	// boilerplate string: it should name this test's own Run closure, which
	// only appears in a real captured stack trace.
	if !strings.Contains(logs, `"stack"`) || !strings.Contains(logs, "TestRunner_PanicIsRecoveredAndDoesNotStopSiblingsOrTheProcess") {
		t.Errorf("logs are missing a's panic stack trace: %s", logs)
	}
}

// TestRunner_CancellationStopsEveryWorkerAndWaitReturns is the bite for
// "cancellation stops all of them": every worker's Run only returns once
// ctx is done, so if Start (or Wait) ever forgot to hand ctx through
// unchanged, this would hang until the test's own guard fires.
func TestRunner_CancellationStopsEveryWorkerAndWaitReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started sync.WaitGroup
	started.Add(3)

	mk := func(name string) *fakeWorker {
		return &fakeWorker{name: name, interval: time.Second, run: func(ctx context.Context) error {
			started.Done()
			<-ctx.Done()
			return ctx.Err()
		}}
	}
	workers := []Worker{mk("a"), mk("b"), mk("c")}

	r := NewRunner(slog.New(slog.DiscardHandler))
	r.Start(ctx, workers)

	startedCh := make(chan struct{})
	go func() { started.Wait(); close(startedCh) }()
	waitFor(t, startedCh, "every worker to start")

	cancel()

	waitDone := make(chan error, 1)
	go func() { waitDone <- r.Wait(context.Background()) }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Errorf("Wait: %v, want nil once every worker returned", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after cancellation stopped every worker")
	}
}

// TestRunner_WaitRespectsTheCallersDeadline proves Wait bounds itself by
// the ctx its caller passes — cmd/vantigo's own shutdown timeout — rather
// than blocking forever on a worker that ignores cancellation. The worker
// here deliberately never observes ctx.Done, simulating a bug elsewhere;
// Wait must still return once its own deadline passes.
func TestRunner_WaitRespectsTheCallersDeadline(t *testing.T) {
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) }) // let the goroutine finish so it does not outlive the test

	w := &fakeWorker{name: "stuck", interval: time.Second, run: func(ctx context.Context) error {
		<-stuck
		return nil
	}}

	r := NewRunner(slog.New(slog.DiscardHandler))
	r.Start(context.Background(), []Worker{w})

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := r.Wait(waitCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait = %v, want context.DeadlineExceeded", err)
	}
}

// A nil Runner (a mode that starts no workers, such as server) has nothing
// to wait for and must not panic or block.
func TestRunner_NilRunnerWaitReturnsImmediately(t *testing.T) {
	var r *Runner
	if err := r.Wait(context.Background()); err != nil {
		t.Errorf("nil Runner Wait = %v, want nil", err)
	}
}

func TestRunner_StartWithNoWorkersWaitsImmediately(t *testing.T) {
	r := NewRunner(slog.New(slog.DiscardHandler))
	r.Start(context.Background(), nil)
	if err := r.Wait(context.Background()); err != nil {
		t.Errorf("Wait = %v, want nil", err)
	}
}
