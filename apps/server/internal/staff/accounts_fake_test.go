package staff_test

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// fakeAccounts is a contracts.AccountProvisioning a test shapes: the mode
// and its error, the users it reports and what it answers for them, the
// roles an Owner named, and a failure for any read. It records each user
// Account was asked about with the address it was given to compare. Its
// writes are delivery C's Task 4's to fill; until then they answer
// errFakeWrite. It is safe for concurrent use.
//
// The harness installs it with withAccounts (modtest.WithAccounts), which
// replaces identity's provider in the composition: identity always provides
// Accounts, and Compose would overwrite a preset Deps.Accounts. A test whose
// subject is identity's real answers composes without it.
type fakeAccounts struct {
	mu         sync.Mutex
	mode       string
	modeErr    error
	users      map[uuid.UUID]contracts.AccountRef
	accountErr error
	roles      []contracts.RoleRef
	rolesErr   error
	asked      []askedAccount
}

// askedAccount is one Account call: the user and the address to compare.
type askedAccount struct {
	user      uuid.UUID
	wantEmail string
}

var _ contracts.AccountProvisioning = (*fakeAccounts)(nil)

// errFakeWrite is the fake's answer to a write: no test of Task 3 writes.
var errFakeWrite = errors.New("fakeAccounts: no write is shaped")

// newFakeAccounts is a provider in local mode that knows nobody and has no
// roles named.
func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{mode: contracts.AccountModeLocal, users: map[uuid.UUID]contracts.AccountRef{}}
}

// withAccounts composes the harness with f as the account provisioning.
func withAccounts(f *fakeAccounts) modtest.Option { return modtest.WithAccounts(f) }

// setUser makes the fake report ref for ref.UserID.
func (f *fakeAccounts) setUser(ref contracts.AccountRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[ref.UserID] = ref
}

// set runs fn on the fake under its lock, for a test changing an answer.
func (f *fakeAccounts) set(fn func(*fakeAccounts)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// accountCalls is every Account call so far, in order.
func (f *fakeAccounts) accountCalls() []askedAccount {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

func (f *fakeAccounts) Mode(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode, f.modeErr
}

func (f *fakeAccounts) Account(_ context.Context, id uuid.UUID, wantEmail string) (*contracts.AccountRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, askedAccount{user: id, wantEmail: wantEmail})
	if f.accountErr != nil {
		return nil, f.accountErr
	}
	ref, ok := f.users[id]
	if !ok {
		return nil, nil
	}
	return &ref, nil
}

func (f *fakeAccounts) ProvisioningRoles(context.Context) ([]contracts.RoleRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.roles), f.rolesErr
}

func (f *fakeAccounts) Prepare(context.Context, contracts.Actor, contracts.AccountSpec) (contracts.AccountRef, error) {
	return contracts.AccountRef{}, errFakeWrite
}

func (f *fakeAccounts) Activate(context.Context, contracts.Actor, uuid.UUID, contracts.ActivateOpts) (contracts.AccountRef, error) {
	return contracts.AccountRef{}, errFakeWrite
}

func (f *fakeAccounts) Deactivate(context.Context, contracts.Actor, uuid.UUID) (contracts.AccountRef, error) {
	return contracts.AccountRef{}, errFakeWrite
}

func (f *fakeAccounts) Update(context.Context, contracts.Actor, uuid.UUID, contracts.AccountSpec) (contracts.AccountRef, error) {
	return contracts.AccountRef{}, errFakeWrite
}
