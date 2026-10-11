package ident

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ErrInvalidOrgNumber is every refusal of ValidateOrgNumber. Callers answer it
// as org_number_invalid.
var ErrInvalidOrgNumber = errors.New("not a valid organisation number")

// ValidateOrgNumber checks raw as a Norwegian organisasjonsnummer and returns
// its nine digits (ported from customers' validNorwegianOrgNumber). Whitespace
// is dropped first, as Brønnøysundregistrene writes it in groups of three.
// The ninth digit is the mod-11 check over the first eight weighted
// 3 2 7 6 5 4 3 2; a remainder that would need the check digit 10 is invalid
// outright, since nine digits cannot encode it.
func ValidateOrgNumber(raw string) (string, error) {
	digits := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, raw)
	if len(digits) != 9 || !allDigits(digits) {
		return "", fmt.Errorf("%w: not nine digits", ErrInvalidOrgNumber)
	}
	weights := [8]int{3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	check := 0
	if r := sum % 11; r != 0 {
		check = 11 - r
		if check == 10 {
			return "", fmt.Errorf("%w: no control digit exists for the first eight", ErrInvalidOrgNumber)
		}
	}
	if int(digits[8]-'0') != check {
		return "", fmt.Errorf("%w: the control digit does not hold", ErrInvalidOrgNumber)
	}
	return digits, nil
}
