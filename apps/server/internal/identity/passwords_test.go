package identity

import (
	"encoding/base64"
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestPasswordHashRoundTrip proves the PHC string's exact shape and
// parameters, that the password verifies, that a wrong one does not, and
// that every hash gets its own salt.
func TestPasswordHashRoundTrip(t *testing.T) {
	const pw = "LongEnough1234"
	hash, err := hashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "$argon2id$v=19$m=19456,t=2,p=1$"
	if !strings.HasPrefix(hash, prefix) {
		t.Fatalf("hash = %q, want prefix %q", hash, prefix)
	}
	parts := strings.Split(strings.TrimPrefix(hash, prefix), "$")
	if len(parts) != 2 {
		t.Fatalf("hash = %q, want <salt>$<key> after the parameters", hash)
	}
	if salt, err := base64.RawStdEncoding.DecodeString(parts[0]); err != nil || len(salt) != 16 {
		t.Errorf("salt %q: %d bytes, err %v; want 16 bytes of unpadded base64", parts[0], len(salt), err)
	}
	if key, err := base64.RawStdEncoding.DecodeString(parts[1]); err != nil || len(key) != 32 {
		t.Errorf("key %q: %d bytes, err %v; want 32 bytes of unpadded base64", parts[1], len(key), err)
	}

	if ok, err := verifyPassword(hash, pw); err != nil || !ok {
		t.Errorf("verifyPassword(right) = %v, %v; want true", ok, err)
	}
	if ok, err := verifyPassword(hash, "LongEnough1235"); err != nil || ok {
		t.Errorf("verifyPassword(wrong) = %v, %v; want false, nil", ok, err)
	}

	again, err := hashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if again == hash {
		t.Error("two hashes of one password are equal; the salt is not random")
	}
}

// TestVerifyPasswordRejectsMalformedHashes proves anything but an Argon2id
// PHC string identity could have written is an error, not a mismatch, and
// that the error never carries the password.
func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	const pw = "LongEnough1234"
	for _, hash := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=16$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=0,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=99999999,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=19456,t=2,p=1$not base64$a2V5",
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$",
	} {
		ok, err := verifyPassword(hash, pw)
		if err == nil || ok {
			t.Errorf("verifyPassword(%q) = %v, %v; want an error", hash, ok, err)
			continue
		}
		if strings.Contains(err.Error(), pw) {
			t.Errorf("error %q carries the password", err)
		}
	}
}

// TestPasswordPolicy pins .NET's policy (at least 12 characters with an
// ASCII digit, upper- and lower-case letter) and its development relaxation
// (at least one character), with ASP.NET Identity's error codes as keys.
func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		pw   string
		dev  bool
		want []string // failing codes, sorted; nil when the password passes
	}{
		{"short1A", false, []string{"PasswordTooShort"}},
		{"nouppercase1234", false, []string{"PasswordRequiresUpper"}},
		{"NOLOWERCASE1234", false, []string{"PasswordRequiresLower"}},
		{"NoDigitsAtAllHere", false, []string{"PasswordRequiresDigit"}},
		{"LongEnough1234", false, nil},
		{"            ", false, []string{"PasswordRequiresDigit", "PasswordRequiresLower", "PasswordRequiresUpper", "PasswordTooShort"}},
		{"x", true, nil},
		{"", true, []string{"PasswordTooShort"}},
		{" ", true, []string{"PasswordTooShort"}},
	}
	for _, c := range cases {
		got := validatePassword(c.pw, c.dev)
		if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, c.want) {
			t.Errorf("validatePassword(%q, dev=%v) codes = %v, want %v", c.pw, c.dev, keys, c.want)
		}
		if c.want == nil && got != nil {
			t.Errorf("validatePassword(%q, dev=%v) = %#v, want nil", c.pw, c.dev, got)
		}
	}
	if got := validatePassword("short1A", false)["PasswordTooShort"]; !slices.Equal(got, []string{"Passwords must be at least 12 characters."}) {
		t.Errorf("PasswordTooShort message = %v", got)
	}
}

// TestPasswordLengthCountsUTF16Units proves length is counted as .NET's
// string.Length counts it: a character outside the BMP is two units.
func TestPasswordLengthCountsUTF16Units(t *testing.T) {
	// Five 🔒 (two UTF-16 units each) plus "aA1": 13 units, 8 runes.
	if got := validatePassword("🔒🔒🔒🔒🔒aA1", false); got != nil {
		t.Errorf("validatePassword = %v, want it to pass at 13 UTF-16 units", got)
	}
}
