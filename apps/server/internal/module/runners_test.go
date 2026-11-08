package module

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// probeRunner records when it starts and whether the dispatcher it was built
// with was bound by then, and returns when its context ends.
type probeRunner struct {
	mu      sync.Mutex
	ops     *operations.Dispatcher
	started chan struct{}
	bound   bool
	stopped bool
}

func (*probeRunner) Name() string { return "probe" }

func (p *probeRunner) Run(ctx context.Context) error {
	// The dispatcher answers a known operation only once it is bound.
	_, ok := p.ops.Info("getAgentsPing")
	p.mu.Lock()
	p.bound = ok
	p.mu.Unlock()
	close(p.started)
	<-ctx.Done()
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	return nil
}

type panicRunner struct{}

func (panicRunner) Name() string              { return "panics" }
func (panicRunner) Run(context.Context) error { panic("boom") }

func TestComposeAPI_RunnersStartAfterTheDispatcherIsBoundAndStopOnCancel(t *testing.T) {
	probe := &probeRunner{started: make(chan struct{})}
	var builtWithOps bool
	deps := Deps{Access: &fakeAccess{}}
	mods := []Module{
		{Name: "agents", Mount: staticHandler("agents"), APIRunners: func(d Deps) []Runner {
			// Built after Bind: the dispatcher already knows the contract.
			_, builtWithOps = d.Operations.Info("getAgentsPing")
			probe.ops = d.Operations
			return []Runner{probe, panicRunner{}}
		}},
	}
	_, runners, err := composeRunners(deps, fakeLoad(map[string]string{"agents": agentsContract}), mods...)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !builtWithOps {
		t.Error("APIRunners ran before the dispatcher was bound")
	}
	if len(runners) != 2 {
		t.Fatalf("composeRunners answered %d runners, want 2", len(runners))
	}

	ctx, cancel := context.WithCancel(context.Background())
	wait := StartRunners(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), runners)
	select {
	case <-probe.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner did not start")
	}
	probe.mu.Lock()
	bound, stopped := probe.bound, probe.stopped
	probe.mu.Unlock()
	if !bound || stopped {
		t.Errorf("bound = %v, stopped = %v at start, want the dispatcher bound and the runner running", bound, stopped)
	}

	cancel()
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not stop the runners")
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if !probe.stopped {
		t.Error("the runner did not see its context end")
	}
}

func TestComposeAPI_ADisabledModulesRunnersAreNotBuilt(t *testing.T) {
	built := false
	_, runners, err := composeRunners(Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Mount: staticHandler("alpha")},
		Module{Name: "beta", Mount: staticHandler("beta"), APIRunners: func(Deps) []Runner { built = true; return []Runner{panicRunner{}} }},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if built || len(runners) != 0 {
		t.Errorf("a module MODULES leaves out built its runners: built = %v, runners = %d", built, len(runners))
	}
}

func TestStartRunners_APanicDoesNotStopTheOthers(t *testing.T) {
	probe := &probeRunner{started: make(chan struct{}), ops: operations.New()}
	ctx, cancel := context.WithCancel(context.Background())
	wait := StartRunners(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), []Runner{panicRunner{}, probe})
	select {
	case <-probe.started:
	case <-time.After(5 * time.Second):
		t.Fatal("a runner after a panicking one did not start")
	}
	cancel()
	wait()
}
