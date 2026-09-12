package energy_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// newHarness is one energy installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against energy.yaml through the package recorder. It also stubs
// Deps.Directory with fakeDirectory: Energy resolves customers only through
// contracts.CustomerDirectory (never the customers schema — depguard
// forbids internal/energy/** from importing internal/customers, even in
// tests), and .NET's own harness (EnergyApiFactory) always registers a
// FakeCustomerDirectory the same way, regardless of which test runs.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(energy.Module()),
		modtest.WithDirectory(fakeDirectory{}),
	}, opts...)...)
}

// fakeDirectory is contracts.CustomerDirectory, ported from
// EnergyApiFactory.cs's FakeCustomerDirectory (energy inventory §7 line
// 430): customers 1001 and 1002 exist, everything else does not. Only
// Customer is ever called by this task's operations (Create/Switch supply
// period); Contact and ContactsByEmail are implemented to satisfy the
// interface and are never exercised here.
type fakeDirectory struct{}

var _ contracts.CustomerDirectory = fakeDirectory{}

func (fakeDirectory) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	if id == 1001 || id == 1002 {
		return &contracts.CustomerEntry{ID: id, Name: "Test customer"}, nil
	}
	return nil, nil
}

func (fakeDirectory) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (fakeDirectory) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}
