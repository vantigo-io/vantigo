package communications_test

import (
	"context"
	"runtime/debug"
	"strings"
	"sync"
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
// newHarness always configures a real fs-backed object store rooted at a
// fresh t.TempDir(), so every attachment test gets a working store by
// default without repeating the same three WithEnv calls — task 6's
// staging and download operations are the only ones in this module that
// ever touch it, and every other test in this package simply never reaches
// the code path that would. A test that needs a storage *failure* overrides
// this with modtest.WithObjectStore(a fake whose Put or Get errors), listed
// after this default so it wins (modtest.New applies Options in order).
//
// Every harness also checks the lock rule when its test ends: a call out of
// the module made from inside one of its transactions (contractscalls.go,
// recorded by TestMain's hook) fails the test.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	before := lockedContractCalls.count()
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call out of this module was made from inside one of its transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(communications.Module()),
		modtest.WithDirectory(fakeDirectory{}),
		modtest.WithEnv("STORAGE_PROVIDER", "fs"),
		modtest.WithEnv("STORAGE_FS_ROOT", t.TempDir()),
		// t.TempDir() honours the process umask, which the sandbox this
		// project's tests run under sets to 0002 rather than 0022 (a
		// documented local-vs-CI gotcha) — group-writable, which NewFS
		// otherwise refuses outside a relaxed root. The harness always runs
		// with APP_ENV=development, so this is the same escape hatch a real
		// development deployment uses, not a test-only bypass of a
		// production check.
		modtest.WithEnv("STORAGE_FS_ALLOW_INSECURE_ROOT", "1"),
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

func (fakeDirectory) Customers(_ context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	entries := []contracts.CustomerEntry{}
	for _, id := range ids {
		if id == 1001 || id == 1002 {
			entries = append(entries, contracts.CustomerEntry{ID: id, Name: "Test customer"})
		}
	}
	return entries, nil
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

func (fakeDirectory) ContactsForCustomer(context.Context, int32) ([]contracts.ContactEntry, error) {
	return nil, nil
}

func (fakeDirectory) CustomersByLegalID(context.Context, string, []string) (map[string][]contracts.CustomerEntry, error) {
	return map[string][]contracts.CustomerEntry{}, nil
}

func (fakeDirectory) BillingProfile(_ context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	if id == 1001 || id == 1002 {
		return &contracts.CustomerBillingProfile{ID: id, Name: "Test customer"}, nil
	}
	return nil, nil
}

// lockedContractCalls is every call out of the module made from inside one of
// its transactions. Every harness checks it when its test ends; it is one
// recorder for the package because the hook is a package-level one
// (TestMain), and each harness checks only what was recorded while it
// existed — calendar's harness, copied.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !communications.InOpenTx(ctx) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, method+"\n"+string(debug.Stack()))
}

func (l *lockedCalls) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *lockedCalls) since(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls[n:]...)
}
