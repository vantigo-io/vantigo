// Package directoryaddress is the address rule a directory provider gives an
// account it creates (Entra design D13, Google Workspace design G18): the
// person's work email, lower-cased, when its domain is one of the
// directory's verified domains and its local part is in the rules' alphabet
// once folded; else a pattern (given.family, g.family, given) over the folded
// names at the provider's domain, with 2, 3, … 99 appended on a conflict. The
// local part is at most 64 characters. Folding is the explicit table first
// (the three Norwegian letters, and the few Latin letters NFD does not
// decompose), then NFD with the combining marks stripped, then anything
// outside the alphabet dropped.
//
// It is pure: no context, no network, no database. Entra and Google call it,
// each with its own alphabet (Rules) and its own choice of the domain.
package directoryaddress

import (
	"crypto/rand"
	"errors"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The address's bounds.
const (
	// MaxLocal is the longest local part an address may have.
	MaxLocal = 64
	// MaxSuffix is the last number appended to a pattern's address before
	// the address is answered taken.
	MaxSuffix = 99
	// patternBase is the longest local part a pattern makes before its
	// suffix, so the suffix always fits within MaxLocal.
	patternBase = MaxLocal - 2
)

// The patterns a provider's account rules admit.
const (
	PatternGivenFamily = "given.family"
	PatternInitial     = "g.family"
	PatternGiven       = "given"
)

// ErrNoDomain: the provider named no domain to make the address at.
var ErrNoDomain = errors.New("directoryaddress: no domain to make the address at")

// Rules are a directory's alphabet for a local part: the ASCII letters and
// digits, and Specials — the dot among them.
type Rules struct {
	Specials string
}

// UPN is a Microsoft Entra ID user principal name's local part (research
// §3.1 of the Entra design).
var UPN = Rules{Specials: "'.-_!#^~"}

// Mailbox is a mailbox's local part as Google Workspace admits it: letters,
// digits, the apostrophe, the dot, the hyphen and the underscore.
var Mailbox = Rules{Specials: "'.-_"}

// foldTable is the explicit folding: the Norwegian letters, which NFD
// leaves whole, and the Latin letters NFD does not decompose either.
var foldTable = map[rune]string{
	'Ø': "O", 'ø': "o", 'Æ': "AE", 'æ': "ae", 'Å': "A", 'å': "a",
	'ß': "ss", 'Ł': "L", 'ł': "l", 'Đ': "D", 'đ': "d", 'Þ': "Th", 'þ': "th", 'Œ': "OE", 'œ': "oe",
}

// Fold is s with the table's letters replaced and every other letter
// decomposed (NFD) and its combining marks stripped. Nothing is dropped.
func Fold(s string) string {
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

// inAlphabet reports whether r may stand in a local part under ru.
func (ru Rules) inAlphabet(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune(ru.Specials, r)
}

// matches reports whether every rune of s is in ru's alphabet.
func (ru Rules) matches(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !ru.inAlphabet(r) {
			return false
		}
	}
	return true
}

// CleanName is a name folded with everything outside ru's alphabet dropped
// — spaces and dots too, so a part of a pattern never holds a dot of its
// own.
func (ru Rules) CleanName(s string) string {
	var b strings.Builder
	for _, r := range Fold(s) {
		if ru.inAlphabet(r) && r != '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Address is the address a create tries: a local part and a domain, and
// whether the local part is the person's work email's, which is tried once.
type Address struct {
	Local     string
	Domain    string
	FromEmail bool
}

// Attempt is the n-th address to try, from 1: the local part as it is, then
// with n appended (2 to MaxSuffix) when a pattern made it. It answers the
// address and its local part, and false when there is no n-th.
func (a Address) Attempt(n int) (address, local string, ok bool) {
	switch {
	case n == 1:
		return a.Local + "@" + a.Domain, a.Local, true
	case a.FromEmail || n < 1 || n > MaxSuffix:
		return "", "", false
	}
	local = a.Local + strconv.Itoa(n)
	return local + "@" + a.Domain, local, true
}

// For is the address rule for spec under ru: its work email when the domain
// is one of verified and the local part folds into the alphabet, else
// pattern over the folded names at domain, which the caller has resolved.
// ErrNoDomain when the work email cannot be used and domain is "".
func (ru Rules) For(spec contracts.ExternalSpec, pattern, domain string, verified []string) (Address, error) {
	if local, host, ok := strings.Cut(strings.ToLower(strings.TrimSpace(spec.WorkEmail)), "@"); ok {
		folded := Fold(local)
		if len(folded) <= MaxLocal && ru.matches(folded) &&
			!strings.HasPrefix(folded, ".") && !strings.HasSuffix(folded, ".") && !strings.Contains(folded, "..") &&
			slices.ContainsFunc(verified, func(d string) bool { return strings.EqualFold(d, host) }) {
			return Address{Local: folded, Domain: host, FromEmail: true}, nil
		}
	}
	at := strings.ToLower(strings.TrimSpace(domain))
	if at == "" {
		return Address{}, ErrNoDomain
	}
	given, family := strings.ToLower(ru.CleanName(spec.GivenName)), strings.ToLower(ru.CleanName(spec.FamilyName))
	var parts []string
	switch pattern {
	case PatternGiven:
		parts = []string{given}
	case PatternInitial:
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
	return Address{Local: local, Domain: at}, nil
}

// The create's password: 32 characters of passwordAlphabet with one of each
// class at least. A directory asks for one at the create; the person never
// uses it, and it is never stored or logged.
const (
	passwordLength   = 32
	passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#%+-"
)

// passwordClasses are the four classes a password holds one of each.
var passwordClasses = []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz", "0123456789", "!#%+-"}

// TemporaryPassword is a fresh random password from crypto/rand.
func TemporaryPassword() (string, error) {
	return randomPassword(passwordLength, passwordAlphabet, passwordClasses)
}

// randomPassword is length characters of alphabet holding one of each class.
func randomPassword(length int, alphabet string, classes []string) (string, error) {
	limit := big.NewInt(int64(len(alphabet)))
	for {
		b := make([]byte, length)
		for i := range b {
			n, err := rand.Int(rand.Reader, limit)
			if err != nil {
				return "", err
			}
			b[i] = alphabet[n.Int64()]
		}
		p := string(b)
		if !slices.ContainsFunc(classes, func(class string) bool { return !strings.ContainsAny(p, class) }) {
			return p, nil
		}
	}
}
