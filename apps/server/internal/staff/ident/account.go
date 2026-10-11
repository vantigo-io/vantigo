package ident

import (
	"errors"
	"fmt"
	"strings"
)

// AccountKind is the kind of a bank account number, as the contract and the
// people table spell it.
type AccountKind string

const (
	// BBAN is a Norwegian kontonummer: eleven digits.
	BBAN AccountKind = "bban"
	// IBAN is an international bank account number, Norway's included.
	IBAN AccountKind = "iban"
)

// ErrInvalidBankAccount is every refusal of ValidateBankAccount, wrapped with
// a reason that names no digit. Callers answer it as bank_account_invalid.
var ErrInvalidBankAccount = errors.New("not a valid bank account number")

// ValidateBankAccount checks raw as an account of kind and returns it
// normalised, the form it is sealed in (ported from invoices/bankfile's
// NormaliseAccount, split by kind).
//
// A BBAN is eleven digits once spaces and full stops are dropped, its last
// digit the MOD11 check over the first ten. An IBAN is read without spaces and
// upper-cased: two letters, two check digits and alphanumerics, 15 to 34
// characters in all, whose ISO 13616 mod-97 check holds; a Norwegian one must
// moreover be 15 characters with an eleven-digit BBAN whose own MOD11 holds,
// the rule a Norwegian account is held to whichever way it is written. No
// other country's length is checked (staff pre-flight answer 5): mod-97 already
// refuses a mistyped number, and a table of lengths is one more thing to keep
// current for no refusal mod-97 misses.
func ValidateBankAccount(kind AccountKind, raw string) (string, error) {
	switch kind {
	case BBAN:
		s := strings.NewReplacer(" ", "", ".", "").Replace(raw)
		if len(s) != 11 || !allDigits(s) {
			return "", fmt.Errorf("%w: a Norwegian account is eleven digits", ErrInvalidBankAccount)
		}
		if !accountCheckHolds(s) {
			return "", fmt.Errorf("%w: the control digit does not hold", ErrInvalidBankAccount)
		}
		return s, nil
	case IBAN:
		s := strings.ToUpper(strings.ReplaceAll(raw, " ", ""))
		if !ibanShaped(s) {
			return "", fmt.Errorf("%w: not the structure of an IBAN", ErrInvalidBankAccount)
		}
		if !ibanCheckHolds(s) {
			return "", fmt.Errorf("%w: the IBAN check digits do not hold", ErrInvalidBankAccount)
		}
		if s[:2] == "NO" && (len(s) != 15 || !allDigits(s[4:]) || !accountCheckHolds(s[4:])) {
			return "", fmt.Errorf("%w: a Norwegian IBAN carries an eleven-digit account whose control digit holds", ErrInvalidBankAccount)
		}
		return s, nil
	default:
		return "", fmt.Errorf("%w: an unknown kind", ErrInvalidBankAccount)
	}
}

// Last4 is the last four characters of a normalised account — what the person
// page shows in clear — or the whole value when it is shorter.
func Last4(normalised string) string {
	if len(normalised) <= 4 {
		return normalised
	}
	return normalised[len(normalised)-4:]
}

// accountCheckHolds is the Norwegian kontonummer's MOD11 check over 11 digits:
// the last is 11 less the sum of the first ten weighted 5 4 3 2 7 6 5 4 3 2,
// mod 11 (0 when that is 11); a check of 10 is no account, since no digit
// equals it (ported from invoices/bankfile).
func accountCheckHolds(digits string) bool {
	weights := [10]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	return int(digits[10]-'0') == (11-sum%11)%11
}

// ibanShaped is ISO 13616's structure: two letters, two digits, then
// alphanumerics, 15 characters (Norway's, the shortest) to 34 in all.
func ibanShaped(s string) bool {
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	if !isUpperLetter(s[0]) || !isUpperLetter(s[1]) || !isDigit(s[2]) || !isDigit(s[3]) {
		return false
	}
	for i := 4; i < len(s); i++ {
		if !isUpperLetter(s[i]) && !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// ibanCheckHolds is ISO 13616's mod-97 over a shaped IBAN: the BBAN, then the
// country and check digits, letters read as numbers (A = 10 … Z = 35), is 1
// mod 97. The check digits 00, 01 and 99 are never issued — 01 and 99 equal 98
// and 02 mod 97, so mod-97 alone would let them stand in for those — and are
// refused first.
func ibanCheckHolds(s string) bool {
	if check := s[2:4]; check == "00" || check == "01" || check == "99" {
		return false
	}
	rearranged := s[4:] + s[:4]
	r := 0
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		if isDigit(c) {
			r = (r*10 + int(c-'0')) % 97
		} else {
			r = (r*100 + int(c-'A') + 10) % 97
		}
	}
	return r == 1
}
