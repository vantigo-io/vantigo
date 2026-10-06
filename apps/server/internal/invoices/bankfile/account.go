package bankfile

import (
	"math/big"
	"strings"
)

// NormaliseAccount reads a Norwegian account number as a bank file or a
// person writes it (D3 step 4): its digits, once spaces and dots are
// dropped, when there are 11 of them; or a Norwegian IBAN — NO in either
// case, two check digits and the 11-digit BBAN — whose ISO 13616 mod-97
// check holds, read as that BBAN. Either way the BBAN's own MOD11 check
// digit must hold, the rule the seller's bank account is saved under
// (settings.go's validBankAccount), so a file can only name an account
// the settings could hold. Anything else is not an account here.
func NormaliseAccount(s string) (bban string, ok bool) {
	s = strings.ToUpper(strings.NewReplacer(" ", "", ".", "").Replace(s))
	if len(s) == 11 && allDigits(s) {
		if !accountCheckHolds(s) {
			return "", false
		}
		return s, true
	}
	if len(s) != 15 || !strings.HasPrefix(s, "NO") || !allDigits(s[2:]) {
		return "", false
	}
	// The BBAN, then the country's letters as numbers (A = 10 … Z = 35),
	// then the check digits: the whole is 1 mod 97.
	n, _ := new(big.Int).SetString(s[4:]+"2324"+s[2:4], 10)
	if new(big.Int).Mod(n, big.NewInt(97)).Int64() != 1 || !accountCheckHolds(s[4:]) {
		return "", false
	}
	return s[4:], true
}

// accountCheckHolds is the Norwegian kontonummer's MOD11 check over 11
// digits: the last is 11 less the sum of the first ten weighted 5 4 3 2 7 6
// 5 4 3 2, mod 11 (0 when that is 11); a check of 10 is no account, since
// no digit equals it.
func accountCheckHolds(digits string) bool {
	weights := [10]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	return int(digits[10]-'0') == (11-sum%11)%11
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
