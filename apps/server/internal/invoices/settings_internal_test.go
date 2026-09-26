package invoices

import "testing"

// The three check-digit rules of the seller record (D2), against numbers
// whose validity is published: Brønnøysundregistrene's own organisation
// number, DNB's sample account and its IBAN, and a Swedish IBAN.
func TestSellerNumbers_TheCheckDigitRules(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		number string
		want   bool
	}{
		{"974760673", true}, {"923609016", true}, {"974760674", false}, {"97476067", false}, {"97476067a", false},
	} {
		if got := validOrganisationNumber(c.number); got != c.want {
			t.Errorf("validOrganisationNumber(%q) = %v, want %v", c.number, got, c.want)
		}
	}
	for _, c := range []struct {
		number string
		want   bool
	}{
		{"86011117947", true}, {"12345678903", true}, {"86011117948", false}, {"8601111794", false},
	} {
		if got := validBankAccount(c.number); got != c.want {
			t.Errorf("validBankAccount(%q) = %v, want %v", c.number, got, c.want)
		}
	}
	for _, c := range []struct {
		iban string
		want bool
	}{
		{"NO9386011117947", true}, {"SE4550000000058398257466", true}, {"GB82WEST12345698765432", true},
		{"NO9386011117948", false}, {"NO93860111179", false}, {"9O9386011117947", false},
	} {
		if got := validIBAN(c.iban); got != c.want {
			t.Errorf("validIBAN(%q) = %v, want %v", c.iban, got, c.want)
		}
	}
}
