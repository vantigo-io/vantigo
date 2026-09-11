package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
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

// maxArgonMemoryKiB bounds the memory a stored hash may ask verifyPassword
// to spend, so a corrupted row cannot make one login allocate gigabytes.
const maxArgonMemoryKiB = 1 << 20 // 1 GiB

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
// Argon2id PHC string identity wrote is an error. Neither the password nor
// the hash appears in the error.
func verifyPassword(hash, pw string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errMalformedHash
	}
	var memory, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil ||
		memory == 0 || memory > maxArgonMemoryKiB || passes == 0 || threads == 0 {
		return false, errMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, errMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errMalformedHash
	}
	got := argon2.IDKey([]byte(pw), salt, passes, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// Password policy failures, keyed and worded as ASP.NET Identity's
// IdentityErrorDescriber reports them. .NET's IdentityFailure groups a
// failed result's errors by code into the error body's fields
// (EA/AuthEndpoints.cs:540-546), so callers put this map there verbatim.
const (
	passwordTooShort      = "PasswordTooShort"
	passwordRequiresDigit = "PasswordRequiresDigit"
	passwordRequiresUpper = "PasswordRequiresUpper"
	passwordRequiresLower = "PasswordRequiresLower"
)

// validatePassword applies .NET's password policy
// (EA/AuthServiceCollectionExtensions.cs:26-37): at least 12 characters with
// an ASCII digit, upper-case and lower-case letter, and no symbol required.
// Development relaxes it to at least one character with nothing required. As
// in ASP.NET Identity's PasswordValidator, a blank password is always too
// short and length counts UTF-16 code units. It returns nil when pw passes.
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
	if len(problems) == 0 {
		return nil
	}
	return problems
}

// utf16Length is len(pw) as .NET's string.Length counts it.
func utf16Length(pw string) int {
	n := 0
	for _, r := range pw {
		n += utf16.RuneLen(r)
	}
	return n
}
