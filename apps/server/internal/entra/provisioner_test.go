package entra_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestStub_Provisioner is the skeleton's provider (plan task 2): it claims
// the key "entra", names itself unconfigured, writes nothing — every write
// answers ErrProviderUnavailable, which Staff retries — and leaves the
// groups to delivery D. Tasks 3 and 4 replace it with the provider's own
// tests.
func TestStub_Provisioner(t *testing.T) {
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
	info, err := p.Provider(ctx)
	if err != nil || info != (contracts.ProviderInfo{Key: "entra", Name: "Microsoft Entra ID", Status: contracts.ProviderUnconfigured}) {
		t.Errorf("Provider() = %+v, %v; want entra, Microsoft Entra ID, unconfigured", info, err)
	}

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
