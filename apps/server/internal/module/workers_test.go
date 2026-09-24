package module

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// fakeWorker is a minimal worker.Worker: these tests only need a distinct,
// nameable value to inject through Module.Workers and assert on, never a
// real Run — the mechanics of running one belong to internal/worker's own
// tests.
type fakeWorker struct{ name string }

func (f fakeWorker) Name() string              { return f.name }
func (f fakeWorker) Interval() time.Duration   { return time.Minute }
func (f fakeWorker) Run(context.Context) error { return nil }

func workerNames(workers []worker.Worker) []string {
	names := make([]string, len(workers))
	for i, w := range workers {
		names[i] = w.Name()
	}
	return names
}

// TestWorkers_CollectsEveryEnabledModulesContribution proves Workers
// concatenates every enabled module's own workers, in mods order, each
// module's own slice kept in the order it returns them — not just the
// first module that declares any.
func TestWorkers_CollectsEveryEnabledModulesContribution(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: []string{"alpha", "beta"}}}
	got := Workers(deps,
		Module{Name: "identity"}, // always enabled; no Workers field
		Module{Name: "alpha", Workers: func(Deps) []worker.Worker {
			return []worker.Worker{fakeWorker{name: "alpha-1"}, fakeWorker{name: "alpha-2"}}
		}},
		Module{Name: "beta", Workers: func(Deps) []worker.Worker {
			return []worker.Worker{fakeWorker{name: "beta-1"}}
		}},
	)
	want := []string{"alpha-1", "alpha-2", "beta-1"}
	names := workerNames(got)
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names = %v, want %v", names, want)
			break
		}
	}
}

// A module with a nil Workers field (every module today) contributes none:
// Workers never invents a zero-value worker or panics on the nil func.
func TestWorkers_ModuleWithoutWorkersContributesNone(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: []string{"alpha"}}}
	got := Workers(deps, Module{Name: "alpha"})
	if got != nil {
		t.Errorf("got = %v, want none from a module with a nil Workers field", got)
	}
}

// TestWorkers_DisabledModuleContributesNone is the rule the runner depends
// on for correctness, not just tidiness: a module MODULES has not enabled
// must not have its background job started at all, mirroring Compose's own
// enablement (enabledModules). Its Workers func must never even run.
func TestWorkers_DisabledModuleContributesNone(t *testing.T) {
	called := false
	deps := Deps{Config: &config.Config{Modules: []string{}}} // "alpha" not enabled
	got := Workers(deps, Module{Name: "alpha", Workers: func(Deps) []worker.Worker {
		called = true
		return []worker.Worker{fakeWorker{name: "alpha-1"}}
	}})
	if called {
		t.Error("a disabled module's Workers was called")
	}
	if got != nil {
		t.Errorf("got = %v, want none from a disabled module", got)
	}
}

// identity is always enabled regardless of MODULES, the same rule Compose
// applies (enabledModules): if it ever declared workers, they would always
// run.
func TestWorkers_IdentityIsAlwaysEnabled(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: nil}}
	got := Workers(deps, Module{Name: "identity", Workers: func(Deps) []worker.Worker {
		return []worker.Worker{fakeWorker{name: "identity-1"}}
	}})
	if names := workerNames(got); len(names) != 1 || names[0] != "identity-1" {
		t.Errorf("names = %v, want [identity-1]", names)
	}
}

// A nil Deps.Config enables everything, mirroring enabledModules' own rule
// for the tests that never exercise enablement.
func TestWorkers_NilConfigEnablesEverything(t *testing.T) {
	got := Workers(Deps{}, Module{Name: "alpha", Workers: func(Deps) []worker.Worker {
		return []worker.Worker{fakeWorker{name: "alpha-1"}}
	}})
	if names := workerNames(got); len(names) != 1 || names[0] != "alpha-1" {
		t.Errorf("names = %v, want [alpha-1]", names)
	}
}

// TestWorkers_NoModulesDeclareAnyReturnsNone proves the empty case does not
// panic and returns an empty (nil) slice rather than some non-nil zero
// value a caller would have to special-case.
func TestWorkers_NoModulesDeclareAnyReturnsNone(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: []string{"alpha", "beta"}}}
	got := Workers(deps, Module{Name: "alpha"}, Module{Name: "beta"})
	if got != nil {
		t.Errorf("got = %v, want none", got)
	}
}

// Worker mode never composes, and the anonymisation worker is the one caller
// of contracts.CustomerPersonalData's erase (customers GDPR design D2), so
// Workers collects the slot itself — from every module given, a disabled one
// included — before any module builds its workers.
func TestWorkers_HandTheWorkersEveryModulesCustomerPersonalData(t *testing.T) {
	beta := &fakePersonalData{name: "beta"}
	var seen []contracts.CustomerPersonalDataHolder
	Workers(Deps{Config: &config.Config{Modules: []string{"alpha"}}},
		Module{Name: "alpha", Workers: func(d Deps) []worker.Worker {
			seen = d.CustomerPersonalData
			return nil
		}},
		Module{Name: "beta", CustomerPersonalData: func(Deps) contracts.CustomerPersonalData { return beta }},
	)
	if want := []contracts.CustomerPersonalDataHolder{{Module: "beta", Data: beta}}; !slices.Equal(seen, want) {
		t.Errorf("alpha's workers saw Deps.CustomerPersonalData = %v, want beta's (disabled)", seen)
	}
}
