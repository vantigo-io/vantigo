package planning_test

import (
	"context"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning"
)

// harness is one planning installation for one test: internal/modtest's
// shared harness composing this module beside identity, every exchange
// validated against planning.yaml through the package recorder, and the
// three fakes it reads in place of Staff, Projects and Customers
// (fakes_test.go), so a test shapes the register, the projects and the
// customers' names it plans against.
//
// The user directory is identity's own: modtest always composes identity,
// which provides contracts.UserDirectory and records every Notify
// (Harness.Notices) — the mails of S19.
type harness struct {
	*modtest.Harness
	workforce *fakeWorkforce
	projects  *fakeProjects
	customers *fakeCustomers
}

// newHarness is a planning installation. Every harness carries the module's
// lock rule out of its test: no call out of the module's own database — the
// register, the directories, the users, the mail — is made from inside one
// of its transactions, a locked one or a read-only snapshot, each of which
// holds a pool connection the call's own read could starve for (see
// lockedContractCalls).
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	workforce, projects, customers := newFakeWorkforce(), newFakeProjects(), newFakeCustomers()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(planning.Module()),
		modtest.WithProjects(projects),
		modtest.WithWorkforce(workforce),
		modtest.WithDirectory(customers),
	}
	before := lockedContractCalls.count()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), workforce: workforce, projects: projects, customers: customers}
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// signIn seeds a user holding planning:access and the given permissions,
// and returns their client and id.
func signIn(t *testing.T, h *harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, append([]string{"planning:access"}, permissions...)...)
}

// lockedContractCalls is every call out of the module made from inside one of
// its transactions (planning.InOpenTx: a locked one, or a read-only
// snapshot), reported by contractscalls.go's hook (TestMain) and by the fakes
// themselves. It is one recorder for the package because the hook is a
// package-level one; each harness checks only what was recorded while it
// existed, and the stack beside each call names the path that made it.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !planning.InOpenTx(ctx) {
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
