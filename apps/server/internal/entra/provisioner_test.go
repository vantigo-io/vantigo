package entra_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestProvisioner_TheKeyTheTraitsAndTheGroups is the provider's shape: it
// claims the key "entra" with no context and no network, declares the two
// group traits delivery C can name — the type, a required choice of
// security or microsoft365, and the mail nickname — and leaves the groups
// themselves to delivery D. The lifecycle's writes are lifecycle_test.go's.
func TestProvisioner_TheKeyTheTraitsAndTheGroups(t *testing.T) {
	t.Parallel()
	build := entra.Module().IdentityProvisioner
	if build == nil {
		t.Fatal("Module().IdentityProvisioner is nil; want the module to fill the slot")
	}
	p := build(module.Deps{})
	if p.Key() != "entra" {
		t.Errorf("Key() = %q, want entra", p.Key())
	}
	traits := p.GroupTraits()
	if len(traits) != 2 {
		t.Fatalf("GroupTraits() = %+v, want type and mailNickname", traits)
	}
	kind, nickname := traits[0], traits[1]
	if kind.Key != "type" || kind.Type != "choice" || !kind.Required || len(kind.Choices) != 2 ||
		kind.Choices[0].Value != "security" || kind.Choices[1].Value != "microsoft365" ||
		kind.Label["en"] == "" || kind.Label["nb"] == "" || !strings.HasPrefix(kind.DocURL, "https://learn.microsoft.com/") {
		t.Errorf("the type trait: %+v", kind)
	}
	for _, c := range kind.Choices {
		if c.Label["en"] == "" || c.Label["nb"] == "" {
			t.Errorf("a choice without both labels: %+v", c)
		}
	}
	if nickname.Key != "mailNickname" || nickname.Type != "string" || nickname.Required || nickname.Label["en"] == "" || nickname.Label["nb"] == "" {
		t.Errorf("the mailNickname trait: %+v", nickname)
	}

	ctx := context.Background()
	user := contracts.Actor{System: contracts.SystemActorStaffAccounts}
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
