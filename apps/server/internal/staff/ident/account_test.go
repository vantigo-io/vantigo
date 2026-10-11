package ident_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/ident"
)

// Ported from invoices/bankfile's TestNormaliseAccount, split by kind: a BBAN
// is 11 digits however it is punctuated with its MOD11 check digit holding; a
// Norwegian IBAN is 15 characters whose mod-97 check and embedded BBAN's MOD11
// both hold; any other IBAN is judged by its structure and mod-97 alone.
func TestValidateBankAccount(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		kind ident.AccountKind
		in   string
		want string // "": refused
	}{
		{"a BBAN is accepted", ident.BBAN, "12345678903", "12345678903"},
		{"a BBAN with dots is read as its digits", ident.BBAN, "1234.56.78903", "12345678903"},
		{"a BBAN with spaces is read as its digits", ident.BBAN, " 1234 56 78903 ", "12345678903"},
		{"a BBAN whose sum is 0 checks to 0", ident.BBAN, "00000000000", "00000000000"},
		{"a BBAN with the wrong check digit is refused", ident.BBAN, "12345678904", ""},
		{"another BBAN with the wrong check digit is refused", ident.BBAN, "86011117948", ""},
		{"a BBAN whose check would be 10 is refused", ident.BBAN, "12345678920", ""},
		{"a BBAN of ten digits is refused", ident.BBAN, "1234567890", ""},
		{"a BBAN of twelve digits is refused", ident.BBAN, "123456789012", ""},
		{"a BBAN with a letter is refused", ident.BBAN, "1234567890A", ""},
		{"a BBAN with hyphens is refused", ident.BBAN, "1234-56-78903", ""},
		{"an empty BBAN is refused", ident.BBAN, "", ""},
		{"a Norwegian IBAN given as a BBAN is refused", ident.BBAN, "NO9386011117947", ""},
		{"a Norwegian IBAN is accepted", ident.IBAN, "NO9386011117947", "NO9386011117947"},
		{"a Norwegian IBAN in groups and lower case is normalised", ident.IBAN, "no93 8601 1117 947", "NO9386011117947"},
		{"a Norwegian IBAN whose embedded BBAN fails MOD11 is refused", ident.IBAN, "NO4412345678904", ""},
		{"a Norwegian IBAN with the wrong check digits is refused", ident.IBAN, "NO9486011117947", ""},
		{"a Norwegian IBAN of 14 characters is refused", ident.IBAN, "NO938601111794", ""},
		{"a Norwegian IBAN of 16 characters is refused", ident.IBAN, "NO93860111179470", ""},
		{"check digits 99 are refused although 99 is 2 mod 97", ident.IBAN, "NO9986010000108", ""},
		{"check digits 01 are refused although 01 is 98 mod 97", ident.IBAN, "NO0186010001678", ""},
		{"the true check digits 02 of that account are accepted", ident.IBAN, "NO0286010000108", "NO0286010000108"},
		{"a German IBAN is accepted by mod-97", ident.IBAN, "DE89 3704 0044 0532 0130 00", "DE89370400440532013000"},
		{"a British IBAN with letters in its BBAN is accepted", ident.IBAN, "GB29NWBK60161331926819", "GB29NWBK60161331926819"},
		{"a Swedish IBAN is accepted", ident.IBAN, "SE4550000000058398257466", "SE4550000000058398257466"},
		{"a Danish IBAN is accepted", ident.IBAN, "DK5000400440116243", "DK5000400440116243"},
		{"a German IBAN with the wrong check digits is refused", ident.IBAN, "DE88370400440532013000", ""},
		{"a British IBAN with one character changed is refused", ident.IBAN, "GB29NWBK60161331926818", ""},
		{"an IBAN of 14 characters is refused", ident.IBAN, "DE893704004405", ""},
		{"an IBAN of 35 characters is refused", ident.IBAN, "DE89" + strings.Repeat("0", 31), ""},
		{"an IBAN with a digit for a country letter is refused", ident.IBAN, "D189370400440532013000", ""},
		{"an IBAN with a letter for a check digit is refused", ident.IBAN, "DEX9370400440532013000", ""},
		{"an IBAN with punctuation is refused", ident.IBAN, "DE89-3704-0044-0532-0130-00", ""},
		{"a BBAN given as an IBAN is refused", ident.IBAN, "12345678903", ""},
		{"an unknown kind is refused", ident.AccountKind("giro"), "12345678903", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := ident.ValidateBankAccount(c.kind, c.in)
			if c.want == "" {
				if !errors.Is(err, ident.ErrInvalidBankAccount) || got != "" {
					t.Fatalf("ValidateBankAccount(%s, %q) = %q, %v; want ErrInvalidBankAccount", c.kind, c.in, got, err)
				}
				if strings.ContainsAny(err.Error(), "0123456789") {
					t.Errorf("error %q carries a digit; a refusal names no digit", err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("ValidateBankAccount(%s, %q) = %q, %v; want %q", c.kind, c.in, got, err, c.want)
			}
		})
	}
}

func TestLast4(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, in, want string }{
		{"a BBAN shows its last four digits", "12345678903", "8903"},
		{"an IBAN shows its last four characters", "GB29NWBK60161331926819", "6819"},
		{"a value of four is itself", "1234", "1234"},
		{"a value shorter than four is itself", "12", "12"},
		{"empty is empty", "", ""},
	} {
		if got := ident.Last4(c.in); got != c.want {
			t.Errorf("%s: Last4(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
