package modtest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The fake provider's own rules, which Staff's tests lean on: what each
// method does to the directory, what it records, and what it refuses.

var fakeWorker = contracts.Actor{System: contracts.SystemActorStaffAccounts}

// fakeClock is a settable clock for the fake's passes.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestProvisioner() (*FakeIdentityProvisioner, *fakeClock) {
	clock := &fakeClock{now: Start}
	return NewFakeIdentityProvisioner("entra", "Microsoft Entra ID").WithClock(clock.Now), clock
}

func methods(calls []ProvisionerCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Method
	}
	return out
}

// Prepare creates a disabled account the first time — a principal name
// folded to ASCII, the work email as its address, Created — and finds it
// by number after, however the number is cased, writing the spec to it.
func TestFakeIdentityProvisioner_PrepareCreatesThenFinds(t *testing.T) {
	ctx := context.Background()
	f, _ := newTestProvisioner()
	if got := f.Key(); got != "entra" {
		t.Errorf("Key() = %q, want entra", got)
	}
	info, err := f.Provider(ctx)
	if err != nil || info != (contracts.ProviderInfo{Key: "entra", Name: "Microsoft Entra ID", Status: contracts.ProviderConnected}) {
		t.Errorf("Provider() = %+v, %v; want entra, its name, connected", info, err)
	}

	created, err := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "e-1", GivenName: "Kåre Øyvind", FamilyName: "Ærlig-Strøm"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !created.Created || created.State != "disabled" || created.ExternalID == "" || created.HasPass {
		t.Errorf("Prepare = %+v, want a created, disabled account with an id and no pass", created)
	}
	if want := "kareoyvind.aerlig-strom@fake.example"; created.UserPrincipalName != want || created.Email != want {
		t.Errorf("Prepare UPN, email = %q, %q; want %q for both (no work email given)", created.UserPrincipalName, created.Email, want)
	}

	found, err := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-1", GivenName: "Kåre", FamilyName: "Ærlig", WorkEmail: "kare@example.test"})
	if err != nil {
		t.Fatalf("Prepare again: %v", err)
	}
	if found.Created || found.ExternalID != created.ExternalID {
		t.Errorf("Prepare again = %+v, want the same account, not created", found)
	}
	if spec, ok := f.LastSpec("E-1"); !ok || spec.WorkEmail != "kare@example.test" {
		t.Errorf("LastSpec = %+v, %v; want the second spec", spec, ok)
	}

	// A second person of one name gets a principal name of their own; the
	// work email given is their address.
	other, err := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-2", GivenName: "Kåre Øyvind", FamilyName: "Ærlig-Strøm", WorkEmail: "other@example.test"})
	if err != nil {
		t.Fatalf("Prepare E-2: %v", err)
	}
	if other.UserPrincipalName != "kareoyvind.aerlig-strom2@fake.example" || other.Email != "other@example.test" {
		t.Errorf("Prepare E-2 = %+v, want a numbered principal name and the work email", other)
	}
	// An address another account holds is refused.
	if _, err := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-3", GivenName: "A", FamilyName: "B", WorkEmail: "OTHER@example.test"}); !errors.Is(err, contracts.ErrExternalAddressTaken) {
		t.Errorf("Prepare with E-2's address = %v, want ErrExternalAddressTaken", err)
	}
	if got := methods(f.Calls()); !slices.Equal(got, []string{"Prepare", "Prepare", "Prepare", "Prepare"}) {
		t.Errorf("Calls = %v, want four Prepares", got)
	}
	if c := f.Calls()[0]; c.Actor != contracts.SystemActorStaffAccounts || c.Spec == nil || c.Spec.EmployeeNumber != "e-1" {
		t.Errorf("the first call = %+v, want the worker's, with its spec", c)
	}
}

// Two accounts holding one number — an administrator made one beside the
// provider's — make Prepare ambiguous.
func TestFakeIdentityProvisioner_TwoAccountsOneNumberAreAmbiguous(t *testing.T) {
	ctx := context.Background()
	f, _ := newTestProvisioner()
	f.SetAccount("E-1", contracts.ExternalAccount{State: "enabled"})
	f.SetAccount("e-1", contracts.ExternalAccount{State: "disabled"})
	if _, err := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-1"}); !errors.Is(err, contracts.ErrExternalAmbiguous) {
		t.Errorf("Prepare = %v, want ErrExternalAmbiguous", err)
	}
}

// Activate enables and issues a pass, alive eight hours, revealed once;
// Deactivate disables and clears it; a fresh Activate issues another.
func TestFakeIdentityProvisioner_ActivateIssuesAPassRevealedOnce(t *testing.T) {
	ctx := context.Background()
	f, clock := newTestProvisioner()
	a, _ := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-1", GivenName: "Kari", FamilyName: "Nordmann"})

	on, err := f.Activate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{EmployeeNumber: "E-1"})
	if err != nil || on.State != "enabled" || !on.HasPass {
		t.Fatalf("Activate = %+v, %v; want enabled with a pass", on, err)
	}
	hr := uuid.New()
	pass, err := f.RevealPass(ctx, contracts.Actor{UserID: &hr}, a.ExternalID)
	if err != nil || pass.Value == "" || !pass.ExpiresAt.Equal(Start.Add(8*time.Hour)) {
		t.Fatalf("RevealPass = %+v, %v; want a pass expiring eight hours on", pass, err)
	}
	if _, err := f.RevealPass(ctx, contracts.Actor{UserID: &hr}, a.ExternalID); !errors.Is(err, contracts.ErrPassGone) {
		t.Errorf("a second RevealPass = %v, want ErrPassGone", err)
	}
	if c := f.Calls()[len(f.Calls())-1]; c.Method != "RevealPass" || c.Actor != hr.String() {
		t.Errorf("the last call = %+v, want RevealPass by HR's user", c)
	}

	// Already enabled, and the pass revealed: an ordinary Activate issues
	// none; HR's Run now (Fresh) does.
	again, _ := f.Activate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{EmployeeNumber: "E-1"})
	if again.HasPass {
		t.Errorf("Activate on an enabled account = %+v, want no new pass", again)
	}
	fresh, _ := f.Activate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{EmployeeNumber: "E-1", Flags: contracts.SpecFlags{Fresh: true}})
	if !fresh.HasPass {
		t.Errorf("a fresh Activate = %+v, want a new pass", fresh)
	}

	// Expired on the fake's clock: gone.
	clock.Advance(8 * time.Hour)
	if acct, _ := f.Account(ctx, a.ExternalID); acct == nil || acct.HasPass {
		t.Errorf("Account after eight hours = %+v, want no pass", acct)
	}
	if _, err := f.RevealPass(ctx, contracts.Actor{UserID: &hr}, a.ExternalID); !errors.Is(err, contracts.ErrPassGone) {
		t.Errorf("RevealPass after its lifetime = %v, want ErrPassGone", err)
	}

	// The rehire: disabled, then activated again — a new pass.
	off, err := f.Deactivate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{EmployeeNumber: "E-1"})
	if err != nil || off.State != "disabled" || off.HasPass {
		t.Fatalf("Deactivate = %+v, %v; want disabled, no pass", off, err)
	}
	back, _ := f.Activate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{EmployeeNumber: "E-1"})
	if back.State != "enabled" || !back.HasPass {
		t.Errorf("Activate after Deactivate = %+v, want enabled with a new pass", back)
	}
}

// Delete refuses an enabled account and marks a disabled one deleted;
// Account answers nil only for an account gone from the directory.
func TestFakeIdentityProvisioner_DeleteAndGone(t *testing.T) {
	ctx := context.Background()
	f, _ := newTestProvisioner()
	a, _ := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-1", GivenName: "Kari", FamilyName: "Nordmann"})
	_, _ = f.Activate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{EmployeeNumber: "E-1"})
	if err := f.Delete(ctx, fakeWorker, a.ExternalID); !errors.Is(err, contracts.ErrExternalEnabled) {
		t.Errorf("Delete of an enabled account = %v, want ErrExternalEnabled", err)
	}
	_, _ = f.Deactivate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{EmployeeNumber: "E-1"})
	if err := f.Delete(ctx, fakeWorker, a.ExternalID); err != nil {
		t.Fatalf("Delete of a disabled account: %v", err)
	}
	if acct, err := f.Account(ctx, a.ExternalID); err != nil || acct == nil || acct.State != "deleted" {
		t.Errorf("Account after Delete = %+v, %v; want it deleted", acct, err)
	}
	f.Gone("e-1")
	if acct, err := f.Account(ctx, a.ExternalID); err != nil || acct != nil {
		t.Errorf("Account after Gone = %+v, %v; want nil, nil", acct, err)
	}
	if _, ok := f.ByNumber("E-1"); ok {
		t.Errorf("ByNumber after Gone found the account")
	}
}

// Not connected, every write but Deactivate answers ErrProviderUnavailable
// (recorded); Deactivate runs. An invalid actor is refused unrecorded; a
// failure a test shaped is answered; reported codes travel on every answer.
func TestFakeIdentityProvisioner_StatusActorFailuresAndReports(t *testing.T) {
	ctx := context.Background()
	f, _ := newTestProvisioner()
	a, _ := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-1", GivenName: "Kari", FamilyName: "Nordmann"})
	_, _ = f.Activate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{EmployeeNumber: "E-1"})

	f.SetStatus(contracts.ProviderFailing, "credential_invalid")
	if info, _ := f.Provider(ctx); info.Status != contracts.ProviderFailing || info.Reason != "credential_invalid" {
		t.Errorf("Provider = %+v, want failing, credential_invalid", info)
	}
	before := len(f.Calls())
	if _, err := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-2"}); !errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Errorf("Prepare while failing = %v, want ErrProviderUnavailable", err)
	}
	if _, err := f.Update(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{}); !errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Errorf("Update while failing = %v, want ErrProviderUnavailable", err)
	}
	if _, err := f.EnsureGroup(ctx, fakeWorker, contracts.GroupSpec{GroupID: "1"}); !errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Errorf("EnsureGroup while failing = %v, want ErrProviderUnavailable", err)
	}
	if off, err := f.Deactivate(ctx, fakeWorker, a.ExternalID, contracts.ExternalSpec{}); err != nil || off.State != "disabled" {
		t.Errorf("Deactivate while failing = %+v, %v; want it disabled", off, err)
	}
	if got := methods(f.Calls()[before:]); !slices.Equal(got, []string{"Prepare", "Update", "EnsureGroup", "Deactivate"}) {
		t.Errorf("Calls while failing = %v, want every call recorded", got)
	}
	f.SetStatus(contracts.ProviderConnected, "")

	before = len(f.Calls())
	if _, err := f.Activate(ctx, contracts.Actor{}, a.ExternalID, contracts.ExternalSpec{}); !errors.Is(err, contracts.ErrInvalidActor) {
		t.Errorf("Activate with no actor = %v, want ErrInvalidActor", err)
	}
	if len(f.Calls()) != before {
		t.Errorf("a call with no actor was recorded")
	}

	down := errors.New("the directory is down")
	f.FailWith("Account", down)
	if _, err := f.Account(ctx, a.ExternalID); !errors.Is(err, down) {
		t.Errorf("Account = %v, want the shaped failure", err)
	}
	f.FailWith("Account", nil)

	f.ReportWith("E-1", contracts.ReportedHireDateNotWritten, contracts.ReportedLicenceUnavailable+":ENTERPRISEPACK")
	acct, _ := f.Account(ctx, a.ExternalID)
	if acct == nil || !slices.Equal(acct.Reported, []string{"hire_date_not_written", "licence_unavailable:ENTERPRISEPACK"}) {
		t.Errorf("Account after ReportWith = %+v, want both codes", acct)
	}
	acct.Reported[0] = "changed"
	if again, _ := f.Account(ctx, a.ExternalID); again.Reported[0] != "hire_date_not_written" {
		t.Errorf("an answer's Reported writes through to the fake")
	}
}

// The groups: one required choice trait; EnsureGroup creates once; and
// SetMembers reconciles to exactly the given members.
func TestFakeIdentityProvisioner_Groups(t *testing.T) {
	ctx := context.Background()
	f, _ := newTestProvisioner()
	traits := f.GroupTraits()
	if len(traits) != 1 || traits[0].Key != "type" || traits[0].Type != "choice" || !traits[0].Required || len(traits[0].Choices) != 2 ||
		traits[0].Choices[0].Value != "security" || traits[0].Choices[1].Value != "microsoft365" {
		t.Errorf("GroupTraits = %+v, want the required type choice of security and microsoft365", traits)
	}
	if _, err := f.SetMembers(ctx, fakeWorker, "7", []string{"a"}); !errors.Is(err, contracts.ErrGroupNotManageable) {
		t.Errorf("SetMembers before EnsureGroup = %v, want ErrGroupNotManageable", err)
	}
	g, err := f.EnsureGroup(ctx, fakeWorker, contracts.GroupSpec{GroupID: "7", Name: "Sales"})
	if err != nil || !g.Created || g.ExternalID == "" {
		t.Fatalf("EnsureGroup = %+v, %v; want created", g, err)
	}
	if again, _ := f.EnsureGroup(ctx, fakeWorker, contracts.GroupSpec{GroupID: "7", Name: "Sales"}); again.Created || again.ExternalID != g.ExternalID {
		t.Errorf("EnsureGroup again = %+v, want the same group, not created", again)
	}
	if r, _ := f.SetMembers(ctx, fakeWorker, "7", []string{"a", "b"}); r.Added != 2 || r.Removed != 0 {
		t.Errorf("SetMembers = %+v, want two added", r)
	}
	if r, _ := f.SetMembers(ctx, fakeWorker, "7", []string{"b", "c"}); r.Added != 1 || r.Removed != 1 {
		t.Errorf("SetMembers again = %+v, want one added and one removed", r)
	}
	if err := f.DeleteGroup(ctx, fakeWorker, "7"); err != nil {
		t.Errorf("DeleteGroup: %v", err)
	}
}

// The option presets the slot: the harness's Deps carry the fake.
func TestWithIdentityProvisioners_PresetsTheSlot(t *testing.T) {
	f, _ := newTestProvisioner()
	var s setup
	WithIdentityProvisioners(f)(&s)
	if len(s.provisioners) != 1 || s.provisioners[0] != f {
		t.Errorf("setup.provisioners = %v, want the fake", s.provisioners)
	}
}
