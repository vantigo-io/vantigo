package products_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// newHarness is one products installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against products.yaml through the package recorder.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(products.Module()),
	}, opts...)...)
}
