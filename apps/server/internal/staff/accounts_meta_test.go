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
	// Task 4: defaultAccountKind and providers in place of accountMode.
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
	if hasMode || hasRoles || otherCaps["canRunAccounts"] != false {
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
	if hasMode || hasRoles {
		t.Errorf("no provider: meta = %v, want no account fields", meta)
	}
}
