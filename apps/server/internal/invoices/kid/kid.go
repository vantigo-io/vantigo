// Package kid is the KID — kundeidentifikasjon, the Norwegian payment
// reference — under a seller's bank agreement (EHF and KID design D3): the
// invoice number zero-padded to the agreed length less one, then a check
// digit by the agreed algorithm, MOD10 (Luhn) or MOD11. A leaf: it imports
// nothing of the module, and the invoices and EHF packages both use it.
package kid

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The two algorithms a bank agrees to, as the settings and the document
// store them.
const (
	Mod10 = "mod10"
	Mod11 = "mod11"
)

// ErrLengthExceeded is a number with more digits than the agreed length
// leaves room for: only possible after the agreement was shortened.
var ErrLengthExceeded = errors.New("kid: the number does not fit the agreed length")

// CheckMod10 is the Luhn check digit over digits: weights 2 and 1
// alternating from the right, each product's digits summed, and
// (10 − sum mod 10) mod 10.
func CheckMod10(digits string) byte {
	sum := 0
	for i := 0; i < len(digits); i++ {
		d := int(digits[len(digits)-1-i] - '0')
		if i%2 == 0 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return byte('0' + (10-sum%10)%10)
}

// CheckMod11 is the MOD11 check digit over digits: weights 2 to 7 repeating
// from the right, and 11 − (sum mod 11) — 0 when the remainder is 0, and '-'
// when it is 1, as the specification prescribes.
func CheckMod11(digits string) byte {
	sum := 0
	for i := 0; i < len(digits); i++ {
		sum += int(digits[len(digits)-1-i]-'0') * (2 + i%6)
	}
	switch r := sum % 11; r {
	case 0:
		return '0'
	case 1:
		return '-'
	default:
		return byte('0' + 11 - r)
	}
}

// check is algorithm's check digit over digits; ok is false for an
// algorithm this package does not know.
func check(digits, algorithm string) (byte, bool) {
	switch algorithm {
	case Mod10:
		return CheckMod10(digits), true
	case Mod11:
		return CheckMod11(digits), true
	}
	return 0, false
}

// Compute is number's KID of length characters under algorithm. A number of
// more than length − 1 digits is ErrLengthExceeded.
func Compute(number int64, length int, algorithm string) (string, error) {
	if number < 1 {
		return "", fmt.Errorf("kid: %d is not an invoice number", number)
	}
	digits := strconv.FormatInt(number, 10)
	if len(digits) > length-1 {
		return "", ErrLengthExceeded
	}
	body := strings.Repeat("0", length-1-len(digits)) + digits
	c, ok := check(body, algorithm)
	if !ok {
		return "", fmt.Errorf("kid: unknown algorithm %q", algorithm)
	}
	return body + string(c), nil
}

// Verify reports whether kid is number's KID under algorithm, at whatever
// length it was issued with: its digits but the last read as number, and its
// last character the check over them.
func Verify(kid, algorithm string, number int64) bool {
	if len(kid) < 2 {
		return false
	}
	body := kid[:len(kid)-1]
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseInt(body, 10, 64)
	if err != nil || n != number {
		return false
	}
	c, ok := check(body, algorithm)
	return ok && c == kid[len(kid)-1]
}

// Fits is the settings' rule for an agreement of length characters (D3):
// whether next, the next number to be issued, fits in length − 1 digits, and
// whether fewer than two digits of headroom are left — a hundredfold growth.
func Fits(next int64, length int) (fits, headroomLow bool) {
	digits := len(strconv.FormatInt(next, 10))
	room := length - 1 - digits
	if room < 0 {
		return false, false
	}
	return true, room < 2
}

// Parse reads a KID as a bank line carries it (payments and reminders design
// D4): 2 to 25 characters, every one but the last a digit, the last a digit
// or MOD11's '-'. body is the digits before the check character, as
// written; number is their value with the leading zeros dropped, and fits is
// false — number 0 — when it exceeds int64 (a 25-digit KID of another
// agreement). ok is false for anything else. Parse does not judge the check
// character; Verify does, under the algorithm a document was issued with.
func Parse(s string) (body string, number int64, fits bool, ok bool) {
	if len(s) < 2 || len(s) > 25 {
		return "", 0, false, false
	}
	body, last := s[:len(s)-1], s[len(s)-1]
	if last != '-' && (last < '0' || last > '9') {
		return "", 0, false, false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return "", 0, false, false
		}
	}
	n, err := strconv.ParseInt(body, 10, 64)
	if err != nil {
		// Only a range error is left: every character is a digit.
		return body, 0, false, true
	}
	return body, n, true, true
}
