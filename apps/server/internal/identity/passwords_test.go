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
// that every hash gets its own salt. It hashes at productionArgonParams by
// name — what every installation hashes with — because this binary is a
// test binary and so hashPassword itself runs at the cheaper test cost
// (see activeArgonParams); TestArgonCostSeam covers that choice.
func TestPasswordHashRoundTrip(t *testing.T) {
	const pw = "LongEnough1234"
	hash, err := hashPasswordWith(pw, productionArgonParams)
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

	again, err := hashPasswordWith(pw, productionArgonParams)
	if err != nil {
		t.Fatal(err)
	}
	if again == hash {
		t.Error("two hashes of one password are equal; the salt is not random")
	}
}

// TestArgonCostSeam pins the cost a server hashes with, proves a test binary
// is the only thing that lowers it, and proves hashes written at either cost
// verify — verifyPassword reads the parameters back from the PHC string, so
// the two costs coexist in one database.
func TestArgonCostSeam(t *testing.T) {
	const pw = "LongEnough1234"
	if want := (argonParams{memoryKiB: 19456, time: 2, threads: 1}); productionArgonParams != want {
		t.Errorf("productionArgonParams = %+v, want %+v (OWASP's minimum)", productionArgonParams, want)
	}
	if !testing.Testing() {
		t.Fatal("testing.Testing() is false inside a test binary")
	}
	if activeArgonParams != testArgonParams {
		t.Errorf("a test binary hashes at %+v, want the test cost %+v", activeArgonParams, testArgonParams)
	}
	for name, params := range map[string]argonParams{"test": testArgonParams, "production": productionArgonParams} {
		hash, err := hashPasswordWith(pw, params)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ok, err := verifyPassword(hash, pw); err != nil || !ok {
			t.Errorf("%s cost: verifyPassword(%q) = %v, %v; want true", name, hash, ok, err)
		}
		if ok, err := verifyPassword(hash, "WrongPassword1"); err != nil || ok {
			t.Errorf("%s cost: verifyPassword(wrong) = %v, %v; want false, nil", name, ok, err)
		}
	}
}

// TestVerifyPasswordRejectsMalformedHashes proves anything but an Argon2id
// PHC string identity could have written is an error, not a mismatch: a
// field with trailing characters, a parameter outside its bounds, or a salt
// or key of the wrong size is refused before Argon2 runs. The error never
// carries the password. Each case breaks one field of a hash that verifies.
func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	const pw = "LongEnough1234"
	good, err := hashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := verifyPassword(good, pw); !ok || err != nil {
		t.Fatalf("the unbroken hash does not verify: %v, %v", ok, err)
	}
	fields := strings.Split(good, "$") // "", "argon2id", "v=19", "m=…,t=…,p=…", salt, key
	with := func(i int, v string) string {
		f := slices.Clone(fields)
		f[i] = v
		return strings.Join(f, "$")
	}
	b64 := func(n int) string { return base64.RawStdEncoding.EncodeToString(make([]byte, n)) }

	cases := map[string]string{
		"empty":                  "",
		"not PHC":                "plaintext",
		"argon2i":                with(1, "argon2i"),
		"old version":            with(2, "v=16"),
		"version junk":           with(2, "v=19junk"),
		"version sign":           with(2, "v=+19"),
		"lanes junk":             with(3, "m=19456,t=2,p=1junk"),
		"passes junk":            with(3, "m=19456,t=2x,p=1"),
		"extra parameter":        with(3, "m=19456,t=2,p=1,x=1"),
		"reordered parameters":   with(3, "t=2,m=19456,p=1"),
		"memory zero":            with(3, "m=0,t=2,p=1"),
		"memory below 8 KiB":     with(3, "m=7,t=2,p=1"),
		"memory above 256 MiB":   with(3, "m=262145,t=2,p=1"),
		"passes zero":            with(3, "m=19456,t=0,p=1"),
		"passes above 10":        with(3, "m=19456,t=11,p=1"),
		"lanes zero":             with(3, "m=19456,t=2,p=0"),
		"lanes above 16":         with(3, "m=19456,t=2,p=17"),
		"salt not base64":        with(4, "not base64"),
		"salt padded":            with(4, fields[4]+"=="),
		"salt of 7 bytes":        with(4, b64(7)),
		"salt of 65 bytes":       with(4, b64(65)),
		"key missing":            with(5, ""),
		"key of 7 bytes":         with(5, b64(7)),
		"key of 65 bytes":        with(5, b64(65)),
		"trailing field":         good + "$",
		"memory digits overflow": with(3, "m=99999999999999999999,t=2,p=1"),
		// Values that fit the field's own parse but not the type it is
		// converted to for Argon2: 2^32 as memory or passes (a uint32), 2^8 as
		// lanes (a uint8). A truncating conversion would turn each into a
		// plausible in-range cost and run Argon2 at it.
		"memory above uint32": with(3, "m=4294967296,t=2,p=1"),
		"passes above uint32": with(3, "m=19456,t=4294967296,p=1"),
		"lanes above uint8":   with(3, "m=19456,t=2,p=256"),
	}
	for name, hash := range cases {
		ok, err := verifyPassword(hash, pw)
		if err == nil || ok {
			t.Errorf("%s: verifyPassword(%q) = %v, %v; want an error", name, hash, ok, err)
			continue
		}
		if strings.Contains(err.Error(), pw) {
			t.Errorf("%s: error %q carries the password", name, err)
		}
	}

	// The bounds themselves are accepted: a hash at each edge verifies as a
	// mismatch, not an error.
	for name, hash := range map[string]string{
		"smallest": "$argon2id$v=19$m=8,t=1,p=1$" + b64(8) + "$" + b64(8),
		"largest":  "$argon2id$v=19$m=8,t=10,p=16$" + b64(64) + "$" + b64(64),
	} {
		if ok, err := verifyPassword(hash, pw); ok || err != nil {
			t.Errorf("%s bounds: verifyPassword = %v, %v; want false, nil", name, ok, err)
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
		// ASP.NET's default RequiredUniqueChars=1 fails only an empty password.
		{"", true, []string{"PasswordRequiresUniqueChars", "PasswordTooShort"}},
		{"", false, []string{"PasswordRequiresDigit", "PasswordRequiresLower", "PasswordRequiresUniqueChars", "PasswordRequiresUpper", "PasswordTooShort"}},
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
	if got := validatePassword("", true)["PasswordRequiresUniqueChars"]; !slices.Equal(got, []string{"Passwords must use at least 1 different characters."}) {
		t.Errorf("PasswordRequiresUniqueChars message = %v", got)
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
