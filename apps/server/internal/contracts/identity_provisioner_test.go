package contracts

import (
	"errors"
	"testing"
)

// TestIdentityProvisionerConstants pins the wire-stable values a caller
// records: the provider's statuses, and the codes a provider reports, which
// Staff stores on its link verbatim and raises attention items from.
func TestIdentityProvisionerConstants(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{ProviderUnconfigured, "unconfigured"}, {ProviderConnected, "connected"}, {ProviderFailing, "failing"},
		{ReportedHireDateNotWritten, "hire_date_not_written"}, {ReportedPrivileged, "privileged"},
		{ReportedPassNotAllowed, "pass_not_allowed"}, {ReportedLicenceUnavailable, "licence_unavailable"},
		{ReportedManagerNotFound, "manager_not_found"}, {ReportedPhotoNotWritten, "photo_not_written"},
	} {
		if c.got != c.want {
			t.Errorf("constant = %q, want %q", c.got, c.want)
		}
	}
}

// TestIdentityProvisionerErrors_AreDistinct proves each refusal is its own
// sentinel, and none is one of AccountProvisioning's, so a caller's
// errors.Is tells a provider's answer from identity's.
func TestIdentityProvisionerErrors_AreDistinct(t *testing.T) {
	all := []error{ErrExternalAmbiguous, ErrExternalAddressTaken, ErrExternalSyncedOnPremises, ErrExternalPrivileged,
		ErrExternalUnconfirmed, ErrExternalEnabled, ErrPassGone, ErrProviderUnavailable, ErrGroupNotManageable, ErrGroupUnconfirmed,
		ErrAccountNotManaged, ErrAccountPrivileged, ErrAccountNotFound, ErrUserGone, ErrInvalidActor, ErrAccountEnabled}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v), want every sentinel distinct", a, b)
			}
		}
	}
}
