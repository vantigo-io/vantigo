package staff_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /meta's account fields are staff:manage's (design §6): whether an
// Owner has named the roles Staff may give, asked of identity before any
// snapshot, and canRunAccounts. Anyone else reads none of them and
// canRunAccounts false. No provider, or identity failing, leaves the roles
// out, and meta still answers; accountMode is gone from the contract.
func TestMeta_TheAccountFields(t *testing.T) {
	t.Parallel()
	fake := newFakeAccounts()
	fake.set(func(f *fakeAccounts) { f.mode = contracts.AccountModeFederated })
	h := newHarness(t, withAccounts(fake))
	hr, hrID := signIn(t, h, "staff:manage")
	viewer, _ := signIn(t, h, "staff:view", "staff:personal-data")

	meta := raw(t, hr.Do(http.MethodGet, metaPath, nil))
	caps, _ := meta["capabilities"].(map[string]any)
	// The kinds seeded from the federated mode: the default is external; no
	// provider in the slot is an empty list.
	if meta["defaultAccountKind"] != "external" || meta["providers"] == nil || len(meta["providers"].([]any)) != 0 {
		t.Errorf("HR's meta = defaultAccountKind %v, providers %v; want external and []", meta["defaultAccountKind"], meta["providers"])
	}
	if meta["provisioningRolesSet"] != false || caps["canRunAccounts"] != true {
		t.Errorf("HR's meta = provisioningRolesSet %v, canRunAccounts %v; want false, true",
			meta["provisioningRolesSet"], caps["canRunAccounts"])
	}
	for _, c := range contractCalls.by(hrID) {
		if c.inTx {
			t.Errorf("meta called %s inside a transaction", c.method)
		}
	}
	other := raw(t, viewer.Do(http.MethodGet, metaPath, nil))
	otherCaps, _ := other["capabilities"].(map[string]any)
	_, hasMode := other["accountMode"]
	_, hasRoles := other["provisioningRolesSet"]
	_, hasKind := other["defaultAccountKind"]
	_, hasProviders := other["providers"]
	if hasMode || hasRoles || hasKind || hasProviders || otherCaps["canRunAccounts"] != false {
		t.Errorf("a caller without staff:manage reads %v, want no account fields and canRunAccounts false", other)
	}

	fake.set(func(f *fakeAccounts) {
		f.roles = []contracts.RoleRef{{ID: uuid.New(), Name: "Employee"}}
		f.modeErr = contracts.ErrModeMismatch
	})
	meta = raw(t, hr.Do(http.MethodGet, metaPath, nil))
	if _, ok := meta["accountMode"]; ok || meta["provisioningRolesSet"] != true {
		t.Errorf("on a mismatch with roles named: accountMode %v, provisioningRolesSet %v; want absent and true", meta["accountMode"], meta["provisioningRolesSet"])
	}
	fake.set(func(f *fakeAccounts) {
		f.modeErr = errors.New("identity is down")
		f.rolesErr = errors.New("identity is down")
	})
	meta = raw(t, hr.Do(http.MethodGet, metaPath, nil))
	if _, ok := meta["accountMode"]; ok {
		t.Errorf("identity failing: accountMode %v, want absent", meta["accountMode"])
	}
	if _, ok := meta["provisioningRolesSet"]; ok {
		t.Errorf("identity failing: provisioningRolesSet %v, want absent", meta["provisioningRolesSet"])
	}

	bare := newHarness(t, modtest.WithoutAccounts())
	bareHR, _ := signIn(t, bare, "staff:manage")
	meta = raw(t, bareHR.Do(http.MethodGet, metaPath, nil))
	_, hasMode = meta["accountMode"]
	_, hasRoles = meta["provisioningRolesSet"]
	_, hasKind = meta["defaultAccountKind"]
	if hasMode || hasRoles || hasKind {
		t.Errorf("no provider: meta = %v, want no account fields", meta)
	}
}

// GET /meta's providers (Entra design D5): every provider in the slot with
// the status it answers now, asked on no transaction — staff:manage's alone.
// A provider that cannot be asked reads failing, unreachable, by its key.
func TestMeta_TheProviders(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeIdentityProvisioner(contracts.AccountKindEntra, "Microsoft Entra ID")
	h := newHarness(t, modtest.WithIdentityProvisioners(fake))
	hr, hrID := signIn(t, h, "staff:manage")
	viewer, _ := signIn(t, h, "staff:view")

	meta := getMeta(t, hr)
	if meta.Providers == nil || len(*meta.Providers) != 1 {
		t.Fatalf("providers = %v, want the fake", meta.Providers)
	}
	if p := (*meta.Providers)[0]; p.Key != "entra" || p.Name != "Microsoft Entra ID" || p.Status != "connected" || p.Reason != nil {
		t.Errorf("provider = %+v, want entra, its name, connected", p)
	}
	if meta.DefaultAccountKind == nil || *meta.DefaultAccountKind != "local" {
		t.Errorf("defaultAccountKind = %v, want local", meta.DefaultAccountKind)
	}
	for _, c := range contractCalls.by(hrID) {
		if c.inTx {
			t.Errorf("meta called %s inside a transaction", c.method)
		}
	}

	fake.SetStatus(contracts.ProviderFailing, "secret_expired")
	if p := (*getMeta(t, hr).Providers)[0]; p.Status != "failing" || p.Reason == nil || *p.Reason != "secret_expired" {
		t.Errorf("a failing provider = %+v, want failing, secret_expired", p)
	}
	fake.FailWith("Provider", errors.New("the directory is down"))
	if p := (*getMeta(t, hr).Providers)[0]; p.Key != "entra" || p.Status != "failing" || p.Reason == nil || *p.Reason != "unreachable" {
		t.Errorf("a provider that cannot be asked = %+v, want failing, unreachable", p)
	}
	if other := getMeta(t, viewer); other.Providers != nil || other.DefaultAccountKind != nil {
		t.Errorf("staff:view reads providers %v, defaultAccountKind %v; want neither", other.Providers, other.DefaultAccountKind)
	}
}
