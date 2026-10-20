package entra

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the address rule (Entra design D13, research §3.1): the user
// principal name the module gives a user it creates, and its mailNickname.
// The person's work email, lower-cased, when its domain is one of the
// tenant's verified domains and its local part is in the UPN's alphabet once
// folded; else the connection's pattern (given.family, g.family, given) over
// the folded names at upn_domain, with 2, 3, … 99 appended on a conflict.
// The local part is the mailNickname, at most 64 characters. Folding is the
// explicit table first (the three Norwegian letters, and the few Latin
// letters NFD does not decompose), then NFD with the combining marks
// stripped, then anything outside the alphabet dropped.

// The UPN's bounds.
const (
	// maxNickname is mailNickname's length, and so the local part's.
	maxNickname = 64
	// maxSuffix is the last number appended to a pattern's address before
	// the address is answered taken.
	maxSuffix = 99
	// patternBase is the longest local part a pattern makes before its
	// suffix, so the suffix always fits within maxNickname.
	patternBase = maxNickname - 2
)

// The patterns upn_pattern admits.
const (
	patternGivenFamily = "given.family"
	patternInitial     = "g.family"
	patternGiven       = "given"
)

// upnAlphabet is what a UPN's local part may hold (research §3.1).
var upnAlphabet = regexp.MustCompile(`^[A-Za-z0-9'.\-_!#^~]+$`)

// errNoDomain: the connection names no UPN domain and the tenant verified
// none, so no address can be made.
var errNoDomain = errors.New("entra: no UPN domain: set one in the account rules, or test the connection")

// foldTable is the explicit folding: the Norwegian letters, which NFD
// leaves whole, and the Latin letters NFD does not decompose either.
var foldTable = map[rune]string{
	'Ø': "O", 'ø': "o", 'Æ': "AE", 'æ': "ae", 'Å': "A", 'å': "a",
	'ß': "ss", 'Ł': "L", 'ł': "l", 'Đ': "D", 'đ': "d", 'Þ': "Th", 'þ': "th", 'Œ': "OE", 'œ': "oe",
}

// fold is s with the table's letters replaced and every other letter
// decomposed (NFD) and its combining marks stripped. Nothing is dropped.
func fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if f, ok := foldTable[r]; ok {
			b.WriteString(f)
			continue
		}
		b.WriteRune(r)
	}
	var out strings.Builder
	for _, r := range norm.NFD.String(b.String()) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// inAlphabet reports whether r may stand in a UPN's local part.
func inAlphabet(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("'.-_!#^~", r)
}

// cleanName is a name folded with everything outside the alphabet dropped —
// spaces and dots too, so a part of a pattern never holds a dot of its own.
func cleanName(s string) string {
	var b strings.Builder
	for _, r := range fold(s) {
		if inAlphabet(r) && r != '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// address is the address a create tries: a local part and a domain, and
// whether the local part is the person's work email's, which is tried once.
type address struct {
	local     string
	domain    string
	fromEmail bool
}

// attempt is the n-th address to try, from 1: the local part as it is, then
// with n appended (2 to maxSuffix) when a pattern made it. It answers the
// UPN and the mailNickname, and false when there is no n-th.
func (a address) attempt(n int) (upn, nickname string, ok bool) {
	switch {
	case n == 1:
		return a.local + "@" + a.domain, a.local, true
	case a.fromEmail || n < 1 || n > maxSuffix:
		return "", "", false
	}
	local := a.local + strconv.Itoa(n)
	return local + "@" + a.domain, local, true
}

// addressFor is the address rule for spec: its work email when the domain
// is one of verified and the local part folds into the alphabet, else
// pattern over the folded names at domain (nil: the first verified domain
// that is not the tenant's initial one, else the first). errNoDomain when
// there is no domain to use.
func addressFor(spec contracts.ExternalSpec, pattern string, domain *string, verified []string) (address, error) {
	if local, host, ok := strings.Cut(strings.ToLower(strings.TrimSpace(spec.WorkEmail)), "@"); ok {
		folded := fold(local)
		if folded != "" && len(folded) <= maxNickname && upnAlphabet.MatchString(folded) &&
			!strings.HasPrefix(folded, ".") && !strings.HasSuffix(folded, ".") && !strings.Contains(folded, "..") &&
			slices.ContainsFunc(verified, func(d string) bool { return strings.EqualFold(d, host) }) {
			return address{local: folded, domain: host, fromEmail: true}, nil
		}
	}
	at := ""
	if domain != nil {
		at = strings.ToLower(strings.TrimSpace(*domain))
	}
	if at == "" {
		for _, d := range verified {
			if !strings.HasSuffix(strings.ToLower(d), ".onmicrosoft.com") {
				at = strings.ToLower(d)
				break
			}
		}
	}
	if at == "" && len(verified) > 0 {
		at = strings.ToLower(verified[0])
	}
	if at == "" {
		return address{}, errNoDomain
	}
	given, family := strings.ToLower(cleanName(spec.GivenName)), strings.ToLower(cleanName(spec.FamilyName))
	var parts []string
	switch pattern {
	case patternGiven:
		parts = []string{given}
	case patternInitial:
		initial := ""
		if given != "" {
			initial = given[:1]
		}
		parts = []string{initial, family}
	default:
		parts = []string{given, family}
	}
	parts = slices.DeleteFunc(parts, func(p string) bool { return p == "" })
	local := strings.Join(parts, ".")
	if len(local) > patternBase {
		local = strings.TrimRight(local[:patternBase], ".")
	}
	if local == "" {
		local = "user"
	}
	return address{local: local, domain: at}, nil
}

// The create's password (the plan's "Exact values"): 32 characters of
// passwordAlphabet with one of each class at least. Graph asks for one; the
// person never uses it — the pass replaces it — and it is never stored or
// logged.
const (
	passwordLength   = 32
	passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#%+-"
)

// passwordClasses are the four classes a password holds one of each.
var passwordClasses = []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz", "0123456789", "!#%+-"}

// temporaryPassword is a fresh random password from crypto/rand.
func temporaryPassword() (string, error) {
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for {
		b := make([]byte, passwordLength)
		for i := range b {
			n, err := rand.Int(rand.Reader, limit)
			if err != nil {
				return "", err
			}
			b[i] = passwordAlphabet[n.Int64()]
		}
		p := string(b)
		if !slices.ContainsFunc(passwordClasses, func(class string) bool { return !strings.ContainsAny(p, class) }) {
			return p, nil
		}
	}
}
