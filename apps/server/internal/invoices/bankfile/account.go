package bankfile

import (
	"math/big"
	"strings"
)

// NormaliseAccount reads a Norwegian account number as a bank file or a
// person writes it (D3 step 4): its digits, once spaces and dots are
// dropped, when there are 11 of them; or a Norwegian IBAN — NO, two check
// digits and the 11-digit BBAN — whose ISO 13616 mod-97 check holds, read
// as that BBAN. Anything else is not an account here.
func NormaliseAccount(s string) (bban string, ok bool) {
	s = strings.NewReplacer(" ", "", ".", "").Replace(s)
	if len(s) == 11 && allDigits(s) {
		return s, true
	}
	if len(s) != 15 || !strings.HasPrefix(s, "NO") || !allDigits(s[2:]) {
		return "", false
	}
	// The BBAN, then the country's letters as numbers (A = 10 … Z = 35),
	// then the check digits: the whole is 1 mod 97.
	n, _ := new(big.Int).SetString(s[4:]+"2324"+s[2:4], 10)
	if new(big.Int).Mod(n, big.NewInt(97)).Int64() != 1 {
		return "", false
	}
	return s[4:], true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
