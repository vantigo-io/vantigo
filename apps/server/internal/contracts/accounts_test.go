package contracts

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// TestSameEmployeeNumber pins identity's comparison, the same
// NormalizeEmployeeNumber key: trimmed, NFC-normalised, upper-cased by Go,
// and an empty number the same as no other — not
// even another empty one, so an unnumbered record never matches an
// unnumbered user.
func TestSameEmployeeNumber(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"E-1", "E-1", true},
		{"e-1", "E-1", true},
		{" E-1\t", "E-1", true},
		{"æ-1", "Æ-1", true},
		{"E-1", "E-2", false},
		{"E-1", "E-10", false},
		{"", "", false},
		{"  ", "", false},
		{"E-1", "", false},
		{"\u00c5-17", "A\u030a-17", true}, // Å precomposed and decomposed: one number once NFC
		{"\u00e5-17", "A\u030a-17", true},
		{"\u212a-1", "k-1", true}, // the Kelvin sign is K under NFC, as identity stores it
		{"ß-1", "ẞ-1", false},     // Go keeps ß, where an en_US upper() would make it ẞ
		{"ǅ-1", "ǆ-1", true},
	} {
		if got := SameEmployeeNumber(tc.a, tc.b); got != tc.want {
			t.Errorf("SameEmployeeNumber(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestNormalizeEmployeeNumber pins the key itself: what identity stores in
// users.employee_number_key and its unique index is on.
func TestNormalizeEmployeeNumber(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{" e-1\t", "E-1"},
		{"æ-1", "Æ-1"},
		{"a\u030a-1", "\u00c5-1"},
		{"ß-1", "ß-1"},
		{"ǆ-1", "Ǆ-1"},
		{"\u212a-1", "K-1"},
		{"  ", ""},
	} {
		if got := NormalizeEmployeeNumber(tc.in); got != tc.want {
			t.Errorf("NormalizeEmployeeNumber(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestActor_Valid pins the actor a provider accepts before any write: exactly
// one of a user and a system, and a system only a known one.
func TestActor_Valid(t *testing.T) {
	user := uuid.New()
	for _, tc := range []struct {
		name  string
		actor Actor
		want  bool
	}{
		{"a user", Actor{UserID: &user}, true},
		{"the accounts worker", Actor{System: SystemActorStaffAccounts}, true},
		{"nobody", Actor{}, false},
		{"both", Actor{UserID: &user, System: SystemActorStaffAccounts}, false},
		{"an unknown system", Actor{System: "system:someone"}, false},
		{"the nil user", Actor{UserID: &uuid.Nil}, false},
	} {
		if got := tc.actor.Valid(); got != tc.want {
			t.Errorf("%s: Valid() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestAccountConstants pins the wire-stable values a caller records: the
// states, who disabled a user (a directory's deactivation its own value), the
// modes and the invitation states.
func TestAccountConstants(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{AccountModeLocal, "local"}, {AccountModeFederated, "federated"},
		{AccountStatePending, "pending"}, {AccountStateActive, "active"}, {AccountStateDisabled, "disabled"},
		{DisabledByOwner, "owner"}, {DisabledByStaff, "staff"}, {DisabledByDirectory, "directory"},
		{InvitationNone, "none"}, {InvitationActive, "active"}, {InvitationExpired, "expired"},
		{InvitationRevokedByOwner, "revoked_by_owner"}, {InvitationExhausted, "exhausted"},
		{SystemActorStaffAccounts, "system:staff-accounts"},
	} {
		if c.got != c.want {
			t.Errorf("constant = %q, want %q", c.got, c.want)
		}
	}
}

// TestAccountErrors_AreDistinct proves each refusal is its own sentinel, so a
// caller's errors.Is tells them apart — ErrEmployeeNumberTaken from
// ErrAccountEmailTaken above all.
func TestAccountErrors_AreDistinct(t *testing.T) {
	all := []error{ErrAccountNotManaged, ErrAccountPrivileged, ErrBlockedByOwner, ErrAccountEmailTaken,
		ErrEmployeeNumberTaken, ErrInvalidActor, ErrEmployeeRoleMissing, ErrEmployeeRoleUnsafe, ErrAccountNotFound, ErrUserGone, ErrModeMismatch, ErrLastOwner}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v), want every sentinel distinct", a, b)
			}
		}
	}
}

// The contract's shape: Account takes the address the caller would set and
// answers whether identity's differs, so the identity address never crosses
// into the caller (the field it once had is gone).
var _ = func(p AccountProvisioning) (bool, error) {
	ref, err := p.Account(context.Background(), uuid.New(), "kari@example.test")
	if ref == nil {
		return false, err
	}
	return AccountRef{EmailDiffers: ref.EmailDiffers}.EmailDiffers, err
}
