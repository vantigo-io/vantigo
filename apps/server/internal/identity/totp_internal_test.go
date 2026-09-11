package identity

import (
	"bytes"
	"encoding/base32"
	"regexp"
	"testing"
	"time"
)

// rfc6238Secret is RFC 6238's SHA-1 test key, "12345678901234567890", in
// base32.
var rfc6238Secret = base32.StdEncoding.EncodeToString([]byte("12345678901234567890"))

// TestTotpStep_AcceptsOneStepEitherSideAndReportsTheStep checks the window
// against RFC 6238's vector: at 59 s (step 1) the code is 287082 (the last
// six digits of 94287082). It is accepted from step 0 to step 2 and
// reported as step 1 each time; two steps away it is not.
func TestTotpStep_AcceptsOneStepEitherSideAndReportsTheStep(t *testing.T) {
	for _, at := range []int64{29, 30, 59, 60, 89} {
		step, ok := totpStep(rfc6238Secret, "287082", time.Unix(at, 0))
		if !ok || step != 1 {
			t.Errorf("at %d s: step %d ok %v, want step 1", at, step, ok)
		}
	}
	if _, ok := totpStep(rfc6238Secret, "287082", time.Unix(90, 0)); ok {
		t.Errorf("accepted two steps after its step")
	}
	if step, ok := totpStep(rfc6238Secret, "081804", time.Unix(1111111109, 0)); !ok || step != 1111111109/30 {
		t.Errorf("RFC 6238 at 1111111109: step %d ok %v", step, ok)
	}
	for _, code := range []string{"287083", "28708", "2870820", "28708a", ""} {
		if _, ok := totpStep(rfc6238Secret, code, time.Unix(59, 0)); ok {
			t.Errorf("accepted %q", code)
		}
	}
}

// TestNewTOTPSecret_Is20RandomBytesInUnpaddedBase32 checks the secret's
// shape and that two secrets differ.
func TestNewTOTPSecret_Is20RandomBytesInUnpaddedBase32(t *testing.T) {
	a, b := newTOTPSecret(), newTOTPSecret()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(a)
	if err != nil || len(raw) != 20 || len(a) != 32 {
		t.Errorf("secret %q: %d bytes (%v), want 20 bytes as 32 base32 characters", a, len(raw), err)
	}
	if a == b {
		t.Errorf("two secrets are equal")
	}
}

// TestIsTOTPCode checks the routing rule: exactly six ASCII digits.
func TestIsTOTPCode(t *testing.T) {
	for code, want := range map[string]bool{
		"123456": true, "000000": true, "12345": false, "1234567": false,
		"12345a": false, "ABCDE-FGHJK": false, "１２３４５６": false, "": false,
	} {
		if got := isTOTPCode(code); got != want {
			t.Errorf("isTOTPCode(%q) = %v, want %v", code, got, want)
		}
	}
}

// TestHashRecoveryCode_IgnoresCaseAndTheDash checks the comparison is
// case-insensitive and ignores the dash, and that no non-ASCII character
// stands in for a letter of the alphabet.
func TestHashRecoveryCode_IgnoresCaseAndTheDash(t *testing.T) {
	want := hashRecoveryCode("ABCDE-FGH23")
	for _, same := range []string{"abcde-fgh23", "ABCDEFGH23", "aBcDeFgH23", "-ABCDE-FGH23-"} {
		if !bytes.Equal(hashRecoveryCode(same), want) {
			t.Errorf("%q hashes differently from ABCDE-FGH23", same)
		}
	}
	// U+017F LATIN SMALL LETTER LONG S upper-cases to S in Unicode.
	if bytes.Equal(hashRecoveryCode("ABCDE-FGHS2"), hashRecoveryCode("ABCDE-FGHſ2")) {
		t.Errorf("a non-ASCII letter matched an ASCII one")
	}
}

// TestNewRecoveryCodes_AreTenDistinctCodesWithTheirHashes checks the set:
// ten distinct codes of the shape XXXXX-XXXXX from the alphabet, each with
// its hash.
func TestNewRecoveryCodes_AreTenDistinctCodesWithTheirHashes(t *testing.T) {
	shape := regexp.MustCompile(`^[` + recoveryCodeAlphabet + `]{5}-[` + recoveryCodeAlphabet + `]{5}$`)
	codes, hashes := newRecoveryCodes()
	if len(codes) != 10 || len(hashes) != 10 {
		t.Fatalf("%d codes and %d hashes, want 10 of each", len(codes), len(hashes))
	}
	seen := map[string]bool{}
	for i, code := range codes {
		if !shape.MatchString(code) {
			t.Errorf("code %q has the wrong shape", code)
		}
		if seen[code] {
			t.Errorf("code %q repeats", code)
		}
		seen[code] = true
		if !bytes.Equal(hashes[i], hashRecoveryCode(code)) {
			t.Errorf("hash %d is not the hash of %q", i, code)
		}
	}
	if len(recoveryCodeAlphabet) != 32 {
		t.Errorf("the alphabet has %d letters; the byte-to-letter pick needs exactly 32", len(recoveryCodeAlphabet))
	}
}
