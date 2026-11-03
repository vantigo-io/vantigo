package inventory_test

import (
	"context"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// harness is one inventory installation for one test: internal/modtest's
// shared harness composing this module beside identity, every exchange
// validated against inventory.yaml through the package recorder, and a fake
// product catalog (fakes_test.go) in place of Products, so a test shapes the
// variants it keeps stock of. modtest lists products in MODULES whenever
// inventory is composed, so config's "inventory requires products" holds.
type harness struct {
	*modtest.Harness
	catalog *fakeCatalog
}

// newHarness is an inventory installation. Every harness carries the
// module's lock rule out of its test: no call out of the module's own
// database is made from inside one of its transactions, but LockVariants.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	catalog := newFakeCatalog()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(inventory.Module()),
		modtest.WithProducts(catalog),
	}
	before := lockedContractCalls.count()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), catalog: catalog}
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// The permission sets a test signs in with (§5): the structure alone, stock
// on top, and each writer.
var (
	locationsViewer  = []string{"inventory:locations-view"}
	locationsManager = []string{"inventory:locations-view", "inventory:locations-manage"}
	stockViewer      = []string{"inventory:locations-view", "inventory:stock-view"}
	stockAdjuster    = []string{"inventory:locations-view", "inventory:stock-view", "inventory:stock-adjust"}
	receiver         = []string{"inventory:locations-view", "inventory:stock-view", "inventory:receipts-manage"}
	everything       = []string{"inventory:locations-view", "inventory:locations-manage", "inventory:stock-view", "inventory:stock-adjust", "inventory:receipts-manage", "inventory:counts-manage"}
)

// signIn seeds a user holding the given permissions, and returns their client
// and id.
func signIn(t *testing.T, h *harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, permissions...)
}

// lockedContractCalls is every call out of the module made from inside one of
// its transactions, but LockVariants, reported by contractscalls.go's hook
// (TestMain) and by the fakes themselves.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !inventory.InOpenTx(ctx) || method == inventory.LockVariantsCall {
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
	if n >= len(l.calls) {
		return nil
	}
	return slices.Clone(l.calls[n:])
}
