package entra_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestProvisioner_WritesAwaitTheLifecycle is the provider before task 4: it
// claims the key "entra", writes nothing — every write answers
// ErrProviderUnavailable, which Staff retries — and leaves the groups to
// delivery D. Task 4 replaces the writes' half with the lifecycle's tests.
func TestProvisioner_WritesAwaitTheLifecycle(t *testing.T) {
	t.Parallel()
	build := entra.Module().IdentityProvisioner
	if build == nil {
		t.Fatal("Module().IdentityProvisioner is nil; want the module to fill the slot")
	}
	p := build(module.Deps{})
	if p.Key() != "entra" {
		t.Errorf("Key() = %q, want entra", p.Key())
	}
	ctx := context.Background()
	user := contracts.Actor{System: contracts.SystemActorStaffAccounts}
	spec := contracts.ExternalSpec{EmployeeNumber: "E-1"}
	_, prepare := p.Prepare(ctx, user, spec)
	_, activate := p.Activate(ctx, user, "id", spec)
	_, deactivate := p.Deactivate(ctx, user, "id", spec)
	_, update := p.Update(ctx, user, "id", spec)
	_, account := p.Account(ctx, "id")
	_, reveal := p.RevealPass(ctx, user, "id")
	for name, err := range map[string]error{
		"Prepare": prepare, "Activate": activate, "Deactivate": deactivate, "Update": update,
		"Delete": p.Delete(ctx, user, "id"), "Account": account, "RevealPass": reveal,
	} {
		if !errors.Is(err, contracts.ErrProviderUnavailable) {
			t.Errorf("%s: %v, want ErrProviderUnavailable", name, err)
		}
	}
	_, ensure := p.EnsureGroup(ctx, user, contracts.GroupSpec{})
	_, members := p.SetMembers(ctx, user, "g", nil)
	for name, err := range map[string]error{
		"EnsureGroup": ensure, "SetMembers": members, "DeleteGroup": p.DeleteGroup(ctx, user, "g"),
	} {
		if !errors.Is(err, contracts.ErrGroupNotManageable) {
			t.Errorf("%s: %v, want ErrGroupNotManageable", name, err)
		}
	}
}

// TestProvider_ReadsTheConnectionWithoutTheNetwork follows the provider's
// status from a fresh installation to connected, and checks that asking it
// costs no call to the tenant (plan reading 2: Staff's GET /meta asks it on
// every request).
func TestProvider_ReadsTheConnectionWithoutTheNetwork(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	p := provider(h)
	ctx := context.Background()
	want := func(status, reason string) {
		t.Helper()
		info, err := p.Provider(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.Key != "entra" || info.Name != "Microsoft Entra ID" || info.Status != status || info.Reason != reason {
			t.Errorf("Provider() = %+v, want %s/%s", info, status, reason)
		}
	}

	want(contracts.ProviderUnconfigured, "")
	o, _ := owner(t, h)
	register(t, o)
	want(contracts.ProviderUnconfigured, "")
	addSecret(t, o, secretValue, "Vantigo", h.Now().AddDate(1, 0, 0))
	want(contracts.ProviderUnconfigured, "") // registered, never tested
	testConnection(t, o)
	calls := len(g.Calls())
	want(contracts.ProviderConnected, "")
	if n := len(g.Calls()); n != calls {
		t.Errorf("Provider() made %d calls to the tenant", n-calls)
	}

	// Three sensitive writes refused in a row read as role_missing.
	h.Exec(t, `UPDATE entra.connection SET sensitive_denied_streak = 3`)
	want(contracts.ProviderConnected, "role_missing")
	h.Exec(t, `UPDATE entra.connection SET sensitive_denied_streak = 0`)

	// Within thirty days of the last secret's end, secret_expiring; past it,
	// secret_expired.
	h.Advance(340 * 24 * time.Hour)
	want(contracts.ProviderConnected, "secret_expiring")
	h.Advance(30 * 24 * time.Hour)
	want(contracts.ProviderFailing, "secret_expired")
}
