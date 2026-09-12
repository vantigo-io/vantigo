package energy_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// newHarness is one energy installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against energy.yaml through the package recorder.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(energy.Module()),
	}, opts...)...)
}
