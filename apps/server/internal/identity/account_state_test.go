package identity

import (
	"slices"
	"testing"
	"time"
)

var accountStateNow = time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)

// Ported from AuthAccountStateTests.DisabledAndTransientLockoutAreIndependent.
func TestDisabledAndTransientLockoutAreIndependent(t *testing.T) {
	lockedUntil := accountStateNow.Add(15 * time.Minute)

	if isActive(true, nil, accountStateNow) {
		t.Error("a disabled account is active")
	}
	if isLockedOut(nil, accountStateNow) {
		t.Error("a disabled account without a lockout is locked out")
	}
	if isActive(false, &lockedUntil, accountStateNow) {
		t.Error("a locked-out account is active")
	}
	if !isLockedOut(&lockedUntil, accountStateNow) {
		t.Error("a lockout ending in 15 minutes is not in force")
	}
	if !isActive(false, nil, accountStateNow) {
		t.Error("an available account is not active")
	}
}

// Ported from AuthAccountStateTests.LockoutExpiresWithoutChangingPersistentDisableState.
func TestLockoutExpiresWithoutChangingPersistentDisableState(t *testing.T) {
	user := struct {
		disabled   bool
		lockoutEnd *time.Time
	}{disabled: true, lockoutEnd: new(accountStateNow.Add(-time.Minute))}

	if isLockedOut(user.lockoutEnd, accountStateNow) {
		t.Error("an expired lockout is in force")
	}
	if isActive(user.disabled, user.lockoutEnd, accountStateNow) {
		t.Error("a disabled account whose lockout expired is active")
	}
	if !user.disabled {
		t.Error("the disable state changed")
	}
}

// Ported from AuthAccountStateTests.RolesAreOrderedOwnerThenUserThenOtherRoles.
func TestRolesAreOrderedOwnerThenUserThenOtherRoles(t *testing.T) {
	if got, want := orderRoles([]string{"User", "Billing", "Owner", "User"}), []string{"Owner", "User", "Billing"}; !slices.Equal(got, want) {
		t.Errorf("orderRoles = %v, want %v", got, want)
	}
	if got := managedRole([]string{"User", "Owner"}); got != "Owner" {
		t.Errorf("managedRole = %q, want Owner", got)
	}
}

// TestOrderRolesPutsSystemAdminFirstAndDropsBlanks covers what the ported
// test does not reach: SystemAdmin's rank, blank names, and ordinal order
// among custom roles.
func TestOrderRolesPutsSystemAdminFirstAndDropsBlanks(t *testing.T) {
	got := orderRoles([]string{"billing", "User", " ", "", "Billing", "SystemAdmin", "Owner"})
	if want := []string{"SystemAdmin", "Owner", "User", "Billing", "billing"}; !slices.Equal(got, want) {
		t.Errorf("orderRoles = %v, want %v", got, want)
	}
	if got := orderRoles(nil); got == nil || len(got) != 0 {
		t.Errorf("orderRoles(nil) = %#v, want an empty, non-nil slice", got)
	}
}

// TestManagedRoleFallsBackToUser covers managedRole without Owner.
func TestManagedRoleFallsBackToUser(t *testing.T) {
	for _, roles := range [][]string{nil, {"Billing"}, {"User", "SystemAdmin"}} {
		if got := managedRole(roles); got != "User" {
			t.Errorf("managedRole(%v) = %q, want User", roles, got)
		}
	}
}
