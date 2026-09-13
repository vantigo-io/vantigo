package communications_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// newHarness is one communications installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against communications.yaml through the package recorder.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(communications.Module()),
	}, opts...)...)
}
