package staff_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// fakeAccounts is a contracts.AccountProvisioning a test shapes: the mode
// and its error, the users it reports and what it answers for them, the
// roles an Owner named, and a failure for any read. It records each user
// Account was asked about with the address it was given to compare, and
// every write (writes). Its writes act as identity's provider would on the
// users it holds (Task 4b): Prepare finds the managed user holding the
// number or, for the local kind (or none named), creates one pending and
// disabled by Staff — a link-only kind (external, entra) answers
// ErrAccountNotFound instead, as identity does; Prepare and Activate record
// the kind they were given (callKinds); Activate enables a
// user Staff disabled and invites a pending one; Deactivate disables for
// Staff; Update writes the number; Expect holds the announcement by the
// number's key, replacing one it had, and Unexpect withdraws it (Entra
// delivery B: announcements reads them) — unless a test shaped an error for
// the method (failWith) or a hook (onWrite), run before the write outside
// the lock, which is how a test acts at the moment the worker is inside a
// call. It is safe for concurrent use.
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
	writeErr   map[string]error
	writes     []string
	onWrite    func(method string, user uuid.UUID)
	onRead     func(user uuid.UUID)
	// onMode runs at the start of every Mode call, outside the lock.
	onMode func()
	// readErr is an Account failure for one user alone.
	readErr map[uuid.UUID]error
	// recentInvitation makes a FreshInvitation an ordinary Activate, as
	// identity does within ten minutes of the last invitation it made.
	recentInvitation bool
	// actors is the actor of every write, in order.
	actors []contracts.Actor
	// staffDisabled is the users Staff's Deactivate disabled in identity's
	// own record, whatever the directory reports.
	staffDisabled map[uuid.UUID]bool
	// blocks holds a method's calls, after they are recorded, until the
	// channel is closed or the call's context ends — which they then answer.
	blocks map[string]chan struct{}
	// expected is the announcements Expect holds, by
	// contracts.NormalizeEmployeeNumber's key, until Unexpect withdraws one.
	expected map[string]contracts.ExpectedAccount
	// kinds is the account kind each Prepare and Activate named, in order,
	// as "<method>:<kind>" (Entra design D5: the kind travels on the call).
	kinds []string
}

// callKinds is the "<method>:<kind>" of every Prepare and Activate so far.
func (f *fakeAccounts) callKinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.kinds)
}

// blockUntil holds every call of method until release is closed or the
// call's own context ends, nil to stop.
func (f *fakeAccounts) blockUntil(method string, release chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blocks == nil {
		f.blocks = map[string]chan struct{}{}
	}
	if release == nil {
		delete(f.blocks, method)
		return
	}
	f.blocks[method] = release
}

// wait is a held call's wait: the context's error when it ended first.
func (f *fakeAccounts) wait(ctx context.Context, method string) error {
	f.mu.Lock()
	release := f.blocks[method]
	f.mu.Unlock()
	if release == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return nil
	}
}

// askedAccount is one Account call: the user and the address to compare.
type askedAccount struct {
	user      uuid.UUID
	wantEmail string
}

var _ contracts.AccountProvisioning = (*fakeAccounts)(nil)

// errFakeDown is a failure to retry: identity could not be reached.
var errFakeDown = errors.New("fakeAccounts: identity is down")

// newFakeAccounts is a provider in local mode that knows nobody and has no
// roles named.
func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{mode: contracts.AccountModeLocal, users: map[uuid.UUID]contracts.AccountRef{}, writeErr: map[string]error{}}
}

// withAccounts composes the harness with f as the account provisioning.
func withAccounts(f *fakeAccounts) modtest.Option { return modtest.WithAccounts(f) }

// setUser makes the fake report ref for ref.UserID. As identity, it holds
// no employee number for a user Staff does not manage or a privileged one
// (unmanaging clears it, promotion unmanages, adoption refuses an Owner), so
// such a ref's number is blanked.
func (f *fakeAccounts) setUser(ref contracts.AccountRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ref.Managed || ref.Privileged {
		ref.EmployeeNumber = ""
	}
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
	hook := f.onMode
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode, f.modeErr
}

// whenReading runs fn at the start of every Account call, outside the lock.
func (f *fakeAccounts) whenReading(fn func(user uuid.UUID)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onRead = fn
}

func (f *fakeAccounts) Account(_ context.Context, id uuid.UUID, wantEmail string) (*contracts.AccountRef, error) {
	f.mu.Lock()
	hook := f.onRead
	f.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, askedAccount{user: id, wantEmail: wantEmail})
	if f.accountErr != nil {
		return nil, f.accountErr
	}
	if err := f.readErr[id]; err != nil {
		return nil, err
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

// failWith makes every call of method ("Prepare", "Activate", "Deactivate",
// "Update", "Expect", "Unexpect") answer err, nil to stop.
func (f *fakeAccounts) failWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.writeErr, method)
		return
	}
	f.writeErr[method] = err
}

// whenWriting runs fn at the start of every write, outside the fake's lock.
func (f *fakeAccounts) whenWriting(fn func(method string, user uuid.UUID)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onWrite = fn
}

// writeCalls is every write so far, "Method" or "Method:<employee number>"
// for Prepare, Update, Expect and Unexpect, in order.
func (f *fakeAccounts) writeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

// staffDisabledUser is whether Deactivate disabled id's own record.
func (f *fakeAccounts) staffDisabledUser(id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.staffDisabled[id]
}

// writeActors is the actor of every write so far, in order.
func (f *fakeAccounts) writeActors() []contracts.Actor {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.actors)
}

// failReading makes Account answer err for user alone, nil to stop.
func (f *fakeAccounts) failReading(user uuid.UUID, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr == nil {
		f.readErr = map[uuid.UUID]error{}
	}
	if err == nil {
		delete(f.readErr, user)
		return
	}
	f.readErr[user] = err
}

// user is the ref the fake holds for id.
func (f *fakeAccounts) user(id uuid.UUID) contracts.AccountRef {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.users[id]
}

// begin records one write and answers its shaped error, running the hook
// first, outside the lock.
func (f *fakeAccounts) begin(method, note string, user uuid.UUID, actor contracts.Actor) error {
	f.mu.Lock()
	hook := f.onWrite
	f.mu.Unlock()
	if hook != nil {
		hook(method, user)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	call := method
	if note != "" {
		call += ":" + note
	}
	f.writes = append(f.writes, call)
	f.actors = append(f.actors, actor)
	return f.writeErr[method]
}

func (f *fakeAccounts) Prepare(_ context.Context, actor contracts.Actor, spec contracts.AccountSpec) (contracts.AccountRef, error) {
	if !actor.Valid() {
		return contracts.AccountRef{}, contracts.ErrInvalidActor
	}
	if err := f.begin("Prepare", spec.EmployeeNumber, uuid.Nil, actor); err != nil {
		return contracts.AccountRef{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds = append(f.kinds, "Prepare:"+spec.Kind)
	for _, ref := range f.users {
		if contracts.SameEmployeeNumber(ref.EmployeeNumber, spec.EmployeeNumber) {
			if !ref.Managed {
				return contracts.AccountRef{}, contracts.ErrEmployeeNumberTaken
			}
			return ref, nil
		}
	}
	// A link-only kind finds only, as identity does (contracts.AccountSpec.Kind).
	if spec.Kind == contracts.AccountKindExternal || spec.Kind == contracts.AccountKindEntra {
		return contracts.AccountRef{}, contracts.ErrAccountNotFound
	}
	ref := contracts.AccountRef{
		UserID: uuid.New(), State: contracts.AccountStatePending, DisabledBy: contracts.DisabledByStaff, Managed: true,
		EmployeeNumber: spec.EmployeeNumber, Invitation: contracts.Invitation{State: contracts.InvitationNone},
		EmailFollows: true,
	}
	f.users[ref.UserID] = ref
	ref.Created = true
	return ref, nil
}

func (f *fakeAccounts) Activate(ctx context.Context, actor contracts.Actor, id uuid.UUID, opts contracts.ActivateOpts) (contracts.AccountRef, error) {
	if !actor.Valid() {
		return contracts.AccountRef{}, contracts.ErrInvalidActor
	}
	note := ""
	if opts.FreshInvitation {
		note = "fresh"
	}
	if err := f.begin("Activate", note, id, actor); err != nil {
		return contracts.AccountRef{}, err
	}
	if err := f.wait(ctx, "Activate"); err != nil {
		return contracts.AccountRef{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds = append(f.kinds, "Activate:"+opts.Kind)
	ref, ok := f.users[id]
	switch {
	case !ok:
		return contracts.AccountRef{}, contracts.ErrUserGone
	case !ref.Managed:
		return contracts.AccountRef{}, contracts.ErrAccountNotManaged
	case ref.Privileged:
		return contracts.AccountRef{}, contracts.ErrAccountPrivileged
	case ref.DisabledBy != "" && ref.DisabledBy != contracts.DisabledByStaff:
		return contracts.AccountRef{}, contracts.ErrBlockedByOwner
	}
	ref.DisabledBy = ""
	if ref.State == contracts.AccountStateDisabled {
		ref.State = contracts.AccountStateActive
	}
	// An Owner's revoke and three unanswered are re-issued only by a fresh
	// invitation, which within ten minutes of the last is an ordinary call.
	fresh := opts.FreshInvitation && !f.recentInvitation
	held := ref.Invitation.State == contracts.InvitationRevokedByOwner || ref.Invitation.State == contracts.InvitationExhausted
	if ref.State == contracts.AccountStatePending && !ref.HasCredential && ref.Invitation.State != contracts.InvitationActive && (fresh || !held) {
		issued := ref.Invitation.Issued + 1
		if fresh {
			issued = 1
		}
		ref.Invitation = contracts.Invitation{State: contracts.InvitationActive, Issued: issued}
		// Delivered: the address follows the person's no more (D1).
		ref.EmailFollows = false
	}
	f.users[id] = ref
	return ref, nil
}

func (f *fakeAccounts) Deactivate(ctx context.Context, actor contracts.Actor, id uuid.UUID) (contracts.AccountRef, error) {
	if !actor.Valid() {
		return contracts.AccountRef{}, contracts.ErrInvalidActor
	}
	if err := f.wait(ctx, "Deactivate"); err != nil {
		return contracts.AccountRef{}, err
	}
	if err := f.begin("Deactivate", "", id, actor); err != nil {
		return contracts.AccountRef{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, ok := f.users[id]
	switch {
	case !ok:
		return contracts.AccountRef{}, contracts.ErrUserGone
	case !ref.Managed:
		return contracts.AccountRef{}, contracts.ErrAccountNotManaged
	case ref.Privileged:
		return contracts.AccountRef{}, contracts.ErrAccountPrivileged
	}
	if f.staffDisabled == nil {
		f.staffDisabled = map[uuid.UUID]bool{}
	}
	f.staffDisabled[id] = true
	// identity's record is disabled by Staff; a user the directory keeps
	// inactive still reads disabled by the directory, as identity reports.
	if ref.DisabledBy == "" {
		ref.DisabledBy = contracts.DisabledByStaff
	}
	if ref.State == contracts.AccountStateActive {
		ref.State = contracts.AccountStateDisabled
	}
	if ref.Invitation.State == contracts.InvitationActive {
		ref.Invitation.State = contracts.InvitationNone
	}
	f.users[id] = ref
	return ref, nil
}

func (f *fakeAccounts) Update(_ context.Context, actor contracts.Actor, id uuid.UUID, spec contracts.AccountSpec) (contracts.AccountRef, error) {
	if !actor.Valid() {
		return contracts.AccountRef{}, contracts.ErrInvalidActor
	}
	if err := f.begin("Update", spec.EmployeeNumber, id, actor); err != nil {
		return contracts.AccountRef{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, ok := f.users[id]
	switch {
	case !ok:
		return contracts.AccountRef{}, contracts.ErrUserGone
	case !ref.Managed:
		return contracts.AccountRef{}, contracts.ErrAccountNotManaged
	}
	ref.EmployeeNumber = spec.EmployeeNumber
	ref.EmailDiffers = ref.EmailDiffers && !ref.EmailFollows
	f.users[id] = ref
	return ref, nil
}

// announcements is the announcements the fake holds, by the employee
// number's key (contracts.NormalizeEmployeeNumber).
func (f *fakeAccounts) announcements() map[string]contracts.ExpectedAccount {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.expected)
}

// Expect records the announcement under the number's key, replacing one it
// held, as a write "Expect:<employee number>".
func (f *fakeAccounts) Expect(_ context.Context, actor contracts.Actor, spec contracts.ExpectedAccount) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	if err := f.begin("Expect", spec.EmployeeNumber, uuid.Nil, actor); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.expected == nil {
		f.expected = map[string]contracts.ExpectedAccount{}
	}
	if spec.ReplaceUserID != nil {
		id := *spec.ReplaceUserID
		spec.ReplaceUserID = &id
	}
	f.expected[contracts.NormalizeEmployeeNumber(spec.EmployeeNumber)] = spec
	return nil
}

// Unexpect withdraws the announcement for the number, if the fake holds
// one, as a write "Unexpect:<employee number>".
func (f *fakeAccounts) Unexpect(_ context.Context, actor contracts.Actor, employeeNumber string) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	if err := f.begin("Unexpect", employeeNumber, uuid.Nil, actor); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.expected, contracts.NormalizeEmployeeNumber(employeeNumber))
	return nil
}

// Erase deletes a disabled Staff-managed user from the fake, as a write
// "Erase".
func (f *fakeAccounts) Erase(ctx context.Context, actor contracts.Actor, id uuid.UUID) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	if err := f.wait(ctx, "Erase"); err != nil {
		return err
	}
	if err := f.begin("Erase", "", id, actor); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, ok := f.users[id]
	switch {
	case !ok:
		return nil
	case !ref.Managed:
		return contracts.ErrAccountNotManaged
	case ref.Privileged:
		return contracts.ErrAccountPrivileged
	case ref.State != contracts.AccountStateDisabled && ref.DisabledBy == "":
		return contracts.ErrAccountEnabled
	}
	delete(f.users, id)
	delete(f.expected, contracts.NormalizeEmployeeNumber(ref.EmployeeNumber))
	return nil
}

// The fake's announcements, which Task 4's tests read: held by the number's
// key, replaced by a second Expect, withdrawn by Unexpect, each a write.
func TestFakeAccounts_ExpectHoldsTheAnnouncementByNumber(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFakeAccounts()
	worker := contracts.Actor{System: contracts.SystemActorStaffAccounts}
	pending := uuid.New()
	if err := f.Expect(ctx, worker, contracts.ExpectedAccount{EmployeeNumber: "e-1", ObjectID: "oid-1"}); err != nil {
		t.Fatalf("Expect: %v", err)
	}
	if err := f.Expect(ctx, worker, contracts.ExpectedAccount{EmployeeNumber: "E-1", ObjectID: "oid-2", ReplaceUserID: &pending}); err != nil {
		t.Fatalf("Expect again: %v", err)
	}
	got := f.announcements()
	if len(got) != 1 || got["E-1"].ObjectID != "oid-2" || got["E-1"].ReplaceUserID == nil || *got["E-1"].ReplaceUserID != pending {
		t.Errorf("announcements = %+v, want E-1's second alone", got)
	}
	if err := f.Unexpect(ctx, worker, "e-1"); err != nil {
		t.Fatalf("Unexpect: %v", err)
	}
	if got := f.announcements(); len(got) != 0 {
		t.Errorf("announcements after Unexpect = %+v, want none", got)
	}
	if want := []string{"Expect:e-1", "Expect:E-1", "Unexpect:e-1"}; !slices.Equal(f.writeCalls(), want) {
		t.Errorf("writes = %v, want %v", f.writeCalls(), want)
	}
	if err := f.Expect(ctx, contracts.Actor{}, contracts.ExpectedAccount{EmployeeNumber: "E-2"}); !errors.Is(err, contracts.ErrInvalidActor) {
		t.Errorf("Expect with no actor = %v, want ErrInvalidActor", err)
	}
}
