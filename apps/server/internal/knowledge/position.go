package knowledge

import (
	"errors"
	"strings"
)

// Positions are fractional-index keys (K3): strings over positionDigits,
// ordered bytewise — the C collation of articles.position and
// favourites.position — so a key can always be made between any two others
// and a move never renumbers its siblings. A key never ends in the lowest
// digit, which is what keeps a key between two neighbours always possible.
const positionDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// errPositionOrder is keyBetween's answer to bounds that are not in order or
// not keys.
var errPositionOrder = errors.New("knowledge: position keys out of order")

// keyBetween answers a key strictly between a and b, where "" for a is the
// start and "" for b the end, so keyBetween("", "") is a first key,
// keyBetween(last, "") one after the last and keyBetween("", first) one
// before the first.
func keyBetween(a, b string) (string, error) {
	if !validKey(a) || !validKey(b) || (a != "" && b != "" && a >= b) {
		return "", errPositionOrder
	}
	return midpoint(a, b, b != ""), nil
}

// validKey reports whether k is "" or a key: digits only, not ending in the
// lowest one.
func validKey(k string) bool {
	if k == "" {
		return true
	}
	for i := 0; i < len(k); i++ {
		if strings.IndexByte(positionDigits, k[i]) < 0 {
			return false
		}
	}
	return k[len(k)-1] != positionDigits[0]
}

// midpoint is the key between a and b (b unbounded when !hasB), with a < b.
func midpoint(a, b string, hasB bool) string {
	if hasB {
		// The common prefix, a padded with the lowest digit, is kept.
		n := 0
		for n < len(b) && digitAt(a, n) == b[n] {
			n++
		}
		if n > 0 {
			rest := ""
			if n < len(a) {
				rest = a[n:]
			}
			return b[:n] + midpoint(rest, b[n:], true)
		}
	}
	lo := 0
	if a != "" {
		lo = strings.IndexByte(positionDigits, a[0])
	}
	hi := len(positionDigits)
	if hasB {
		hi = strings.IndexByte(positionDigits, b[0])
	}
	if hi-lo > 1 {
		return string(positionDigits[(lo+hi+1)/2])
	}
	// The first digits are neighbours.
	if hasB && len(b) > 1 {
		return b[:1]
	}
	rest := ""
	if len(a) > 1 {
		rest = a[1:]
	}
	return string(positionDigits[lo]) + midpoint(rest, "", false)
}

// digitAt is a's digit at i, the lowest digit beyond its end.
func digitAt(a string, i int) byte {
	if i < len(a) {
		return a[i]
	}
	return positionDigits[0]
}
