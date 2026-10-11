package ident_test

import (
	"errors"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/ident"
)

// Ported from customers' TestValidNorwegianOrgNumber: nine digits whose ninth
// is the mod-11 check over the first eight weighted 3 2 7 6 5 4 3 2, a
// remainder that would need the check digit 10 refused outright.
func TestValidateOrgNumber(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, in, want string // want "": refused
	}{
		{"Equinor ASA is accepted", "923609016", "923609016"},
		{"Brønnøysundregistrene is accepted", "974760673", "974760673"},
		{"a number written in groups is read as its digits", " 923 609 016 ", "923609016"},
		{"Equinor's number with the check digit flipped is refused", "923609017", ""},
		{"eight digits are refused", "92360901", ""},
		{"ten digits are refused", "9236090166", ""},
		{"a letter where a digit belongs is refused", "92360901a", ""},
		{"a weighted sum that gives check digit 10 is refused", "912345678", ""},
		{"empty is refused", "", ""},
	} {
		got, err := ident.ValidateOrgNumber(c.in)
		if c.want == "" {
			if !errors.Is(err, ident.ErrInvalidOrgNumber) || got != "" {
				t.Errorf("%s: ValidateOrgNumber(%q) = %q, %v; want ErrInvalidOrgNumber", c.name, c.in, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: ValidateOrgNumber(%q) = %q, %v; want %q", c.name, c.in, got, err, c.want)
		}
	}
}
