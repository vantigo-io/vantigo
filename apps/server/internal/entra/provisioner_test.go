package entra_test

import (
	"context"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestProvisioner_TheKey is the provider's shape: it fills the slot and
// claims the key "entra" with no context and no network. The traits and the
// groups are groups_test.go's, the lifecycle's writes lifecycle_test.go's.
func TestProvisioner_TheKey(t *testing.T) {
	t.Parallel()
	build := entra.Module().IdentityProvisioner
	if build == nil {
		t.Fatal("Module().IdentityProvisioner is nil; want the module to fill the slot")
	}
	if p := build(module.Deps{}); p.Key() != "entra" {
		t.Errorf("Key() = %q, want entra", p.Key())
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
