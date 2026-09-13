package communications_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// newHarness is one communications installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against communications.yaml through the package recorder. It
// also stubs Deps.Directory with fakeDirectory: communications resolves
// customers and contacts only through contracts.CustomerDirectory (never
// the customers schema — depguard forbids internal/communications/** from
// importing internal/customers, even in tests), and this harness does not
// compose the customers module beside it (the same seam energy's own
// harness_test.go documents).
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(communications.Module()),
		modtest.WithDirectory(fakeDirectory{}),
	}, opts...)...)
}

// fakeDirectory is contracts.CustomerDirectory for every communications
// test: customers 1001 and 1002 exist (task 5's PATCH/create tests use
// them), contact 2001 exists, everything else does not.
type fakeDirectory struct{}

var _ contracts.CustomerDirectory = fakeDirectory{}

func (fakeDirectory) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	if id == 1001 || id == 1002 {
		return &contracts.CustomerEntry{ID: id, Name: "Test customer"}, nil
	}
	return nil, nil
}

func (fakeDirectory) Contact(_ context.Context, id int32) (*contracts.ContactEntry, error) {
	if id == 2001 {
		return &contracts.ContactEntry{ID: id, FirstName: "Test", LastName: "Contact"}, nil
	}
	return nil, nil
}

func (fakeDirectory) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}
