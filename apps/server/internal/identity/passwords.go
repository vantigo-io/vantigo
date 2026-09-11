package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP's minimum: 19 MiB, two passes, one lane) and
// the salt and key sizes every new hash uses. verifyPassword reads the
// parameters back from the stored PHC string, so raising them later keeps
// existing hashes verifiable.
const (
	argonMemoryKiB = 19456
	argonTime      = 2
	argonThreads   = 1
	argonSaltBytes = 16
	argonKeyBytes  = 32
)

// The bounds verifyPassword accepts from a stored hash before it runs
// Argon2, so a corrupted or hostile row cannot make one login spend
// gigabytes or minutes: passes 1..10, memory 8 KiB..256 MiB, lanes 1..16,
// and salt and key lengths of 8..64 bytes.
const (
	minArgonTime, maxArgonTime           = 1, 10
	minArgonMemoryKiB, maxArgonMemoryKiB = 8, 256 << 10
	minArgonThreads, maxArgonThreads     = 1, 16
	minArgonBytes, maxArgonBytes         = 8, 64
)

var errMalformedHash = errors.New("identity: malformed password hash")

// hashPassword returns pw's Argon2id hash as a PHC string:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>, both in unpadded base64.
func hashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("identity: password salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword reports whether pw matches hash, comparing in constant
// time. A wrong password is (false, nil); only a hash that is not an
// Argon2id PHC string identity wrote is an error. The string is parsed
// strictly (no trailing characters in any field) and its parameters must be
// inside the bounds above. Neither the password nor the hash appears in the
// error.
func verifyPassword(hash, pw string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errMalformedHash
	}
	version, ok := phcParam(parts[2], "v", argon2.Version, argon2.Version)
	if !ok || version != argon2.Version {
		return false, errMalformedHash
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, errMalformedHash
	}
	memory, okM := phcParam(params[0], "m", minArgonMemoryKiB, maxArgonMemoryKiB)
	passes, okT := phcParam(params[1], "t", minArgonTime, maxArgonTime)
	threads, okP := phcParam(params[2], "p", minArgonThreads, maxArgonThreads)
	if !okM || !okT || !okP {
		return false, errMalformedHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < minArgonBytes || len(salt) > maxArgonBytes {
		return false, errMalformedHash
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(want) < minArgonBytes || len(want) > maxArgonBytes {
		return false, errMalformedHash
	}
	// Every conversion is in range: phcParam and the length checks bounded each value.
	got := argon2.IDKey([]byte(pw), salt, uint32(passes), uint32(memory), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// phcParam parses one "name=value" field of a PHC string: exactly that
// name, a decimal value with nothing after it, within [lo, hi].
func phcParam(field, name string, lo, hi int) (int, bool) {
	digits, ok := strings.CutPrefix(field, name+"=")
	if !ok || digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return 0, false
	}
	v, err := strconv.Atoi(digits)
	if err != nil || v < lo || v > hi {
		return 0, false
	}
	return v, true
}

// Password policy failures, keyed and worded as ASP.NET Identity's
// IdentityErrorDescriber reports them. .NET's IdentityFailure groups a
// failed result's errors by code into the error body's fields
// (EA/AuthEndpoints.cs:540-546), so callers put this map there verbatim.
const (
	passwordTooShort            = "PasswordTooShort"
	passwordRequiresDigit       = "PasswordRequiresDigit"
	passwordRequiresUpper       = "PasswordRequiresUpper"
	passwordRequiresLower       = "PasswordRequiresLower"
	passwordRequiresUniqueChars = "PasswordRequiresUniqueChars"
)

// requiredUniqueChars is ASP.NET Identity's default
// PasswordOptions.RequiredUniqueChars, which .NET left unchanged: only an
// empty password has fewer distinct characters.
const requiredUniqueChars = 1

// validatePassword applies .NET's password policy
// (EA/AuthServiceCollectionExtensions.cs:26-37): at least 12 characters with
// an ASCII digit, upper-case and lower-case letter, and no symbol required.
// Development relaxes it to at least one character with nothing required. As
// in ASP.NET Identity's PasswordValidator, a blank password is always too
// short, length counts UTF-16 code units, and in every environment a
// password needs requiredUniqueChars distinct UTF-16 units. It returns nil
// when pw passes.
func validatePassword(pw string, dev bool) map[string][]string {
	minLength, requireClasses := 12, true
	if dev {
		minLength, requireClasses = 1, false
	}

	problems := map[string][]string{}
	if strings.TrimSpace(pw) == "" || utf16Length(pw) < minLength {
		problems[passwordTooShort] = []string{fmt.Sprintf("Passwords must be at least %d characters.", minLength)}
	}
	if requireClasses {
		if !strings.ContainsFunc(pw, func(r rune) bool { return r >= '0' && r <= '9' }) {
			problems[passwordRequiresDigit] = []string{"Passwords must have at least one digit ('0'-'9')."}
		}
		if !strings.ContainsFunc(pw, func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
			problems[passwordRequiresUpper] = []string{"Passwords must have at least one uppercase ('A'-'Z')."}
		}
		if !strings.ContainsFunc(pw, func(r rune) bool { return r >= 'a' && r <= 'z' }) {
			problems[passwordRequiresLower] = []string{"Passwords must have at least one lowercase ('a'-'z')."}
		}
	}
	if distinctUTF16Units(pw) < requiredUniqueChars {
		problems[passwordRequiresUniqueChars] = []string{fmt.Sprintf("Passwords must use at least %d different characters.", requiredUniqueChars)}
	}
	if len(problems) == 0 {
		return nil
	}
	return problems
}

// distinctUTF16Units is pw.Distinct().Count() as .NET counts it, over
// UTF-16 code units.
func distinctUTF16Units(pw string) int {
	seen := map[uint16]struct{}{}
	for _, u := range utf16.Encode([]rune(pw)) {
		seen[u] = struct{}{}
	}
	return len(seen)
}

// utf16Length is len(pw) as .NET's string.Length counts it.
func utf16Length(pw string) int {
	n := 0
	for _, r := range pw {
		n += utf16.RuneLen(r)
	}
	return n
}
