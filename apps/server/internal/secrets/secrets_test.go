package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// testAppSecret is a fixture value only: 32 bytes, never a real secret.
var testAppSecret = []byte(strings.Repeat("s", 32))

func TestNew_RejectsShortSecret(t *testing.T) {
	_, err := New(bytes.Repeat([]byte("s"), 31))
	if err == nil {
		t.Fatal("New(31 bytes) = nil error, want an error")
	}
}

func TestNew_AcceptsMinimumSecret(t *testing.T) {
	if _, err := New(testAppSecret); err != nil {
		t.Fatalf("New(32 bytes) = %v, want nil", err)
	}
}

func TestSealOpen_RoundTrip(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plaintext := []byte("super secret totp seed")
	sealed, err := box.Seal("identity/totp", plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	opened, err := box.Open("identity/totp", sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Errorf("Open = %q, want %q", opened, plaintext)
	}
}

func TestOpen_WrongPurposeFails(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sealed, err := box.Seal("identity/totp", []byte("data"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	_, err = box.Open("identity/oidc-state", sealed)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Open(wrong purpose) error = %v, want ErrInvalid", err)
	}
}

func TestOpen_TamperedByteFails(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sealed, err := box.Seal("identity/totp", []byte("data"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0xFF

	_, err = box.Open("identity/totp", tampered)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Open(tampered) error = %v, want ErrInvalid", err)
	}
}

func TestSeal_NonceDiffersBetweenCalls(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plaintext := []byte("data")
	a, err := box.Seal("identity/totp", plaintext)
	if err != nil {
		t.Fatalf("Seal (1): %v", err)
	}
	b, err := box.Seal("identity/totp", plaintext)
	if err != nil {
		t.Fatalf("Seal (2): %v", err)
	}

	if bytes.Equal(a, b) {
		t.Error("two seals of the same plaintext are identical, want distinct nonces")
	}
}

func TestSeal_FormatIsKeyIDNonceCiphertext(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plaintext := []byte("data")
	sealed, err := box.Seal("identity/totp", plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	const nonceSize = 12
	const gcmTagSize = 16
	wantLen := 1 + nonceSize + len(plaintext) + gcmTagSize
	if len(sealed) != wantLen {
		t.Fatalf("len(sealed) = %d, want %d", len(sealed), wantLen)
	}
	if sealed[0] != 0x01 {
		t.Errorf("sealed[0] = %#x, want 0x01 (key id 1)", sealed[0])
	}
}

func TestOpen_UnknownKeyIDFails(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sealed, err := box.Seal("identity/totp", []byte("data"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	sealed[0] = 0x02

	_, err = box.Open("identity/totp", sealed)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Open(unknown key id) error = %v, want ErrInvalid", err)
	}
}

func TestOpen_ShortInputFails(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := box.Open("identity/totp", []byte{0x01}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Open(short input) error = %v, want ErrInvalid", err)
	}
	if _, err := box.Open("identity/totp", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("Open(nil) error = %v, want ErrInvalid", err)
	}
}

// TestSeal_RefusesPlaintextAboveTheMaximum proves the documented bound: a
// plaintext of maxPlaintextBytes still seals and opens, one byte more is
// ErrTooLarge and nothing is allocated for it. The bound is what makes
// Seal's output-size computation provably safe.
//
// The bound is lowered for the duration, because what is under test is the
// boundary and not the number: at the real 64 MiB this one test allocates a
// quarter of a gigabyte, which a small runner should not be asked for. This
// test therefore must not call t.Parallel — no other test in the package does
// either.
func TestSeal_RefusesPlaintextAboveTheMaximum(t *testing.T) {
	realMaximum := maxPlaintextBytes
	maxPlaintextBytes = 4096
	t.Cleanup(func() { maxPlaintextBytes = realMaximum })

	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sealed, err := box.Seal("identity/totp", make([]byte, maxPlaintextBytes))
	if err != nil {
		t.Fatalf("Seal(maxPlaintextBytes) = %v, want nil", err)
	}
	opened, err := box.Open("identity/totp", sealed)
	if err != nil || len(opened) != maxPlaintextBytes {
		t.Fatalf("Open(a sealed maximum) = %d bytes, %v; want %d bytes, nil", len(opened), err, maxPlaintextBytes)
	}

	over, err := box.Seal("identity/totp", make([]byte, maxPlaintextBytes+1))
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("Seal(maxPlaintextBytes+1) error = %v, want ErrTooLarge", err)
	}
	if over != nil {
		t.Errorf("Seal(maxPlaintextBytes+1) returned %d bytes, want none", len(over))
	}
	if _, err := box.SealString("identity/totp", strings.Repeat("s", maxPlaintextBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("SealString(maxPlaintextBytes+1) error = %v, want ErrTooLarge", err)
	}
}

func TestSealStringOpenString_RoundTrip(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	s, err := box.SealString("identity/oidc-state", "state=xyz")
	if err != nil {
		t.Fatalf("SealString: %v", err)
	}
	if strings.ContainsAny(s, "+/=") {
		t.Errorf("SealString result %q contains standard-base64 or padding characters, want base64url without padding", s)
	}

	opened, err := box.OpenString("identity/oidc-state", s)
	if err != nil {
		t.Fatalf("OpenString: %v", err)
	}
	if opened != "state=xyz" {
		t.Errorf("OpenString = %q, want %q", opened, "state=xyz")
	}
}

func TestOpenString_InvalidBase64Fails(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := box.OpenString("identity/oidc-state", "not base64url!!"); !errors.Is(err, ErrInvalid) {
		t.Errorf("OpenString(invalid base64) error = %v, want ErrInvalid", err)
	}
}

func TestKeyDerivation_CachedPerPurpose(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Sealing repeatedly with the same purpose must keep working, which
	// exercises the cached-key path rather than a distinct derivation per
	// call.
	for i := 0; i < 3; i++ {
		sealed, err := box.Seal("identity/totp", []byte("data"))
		if err != nil {
			t.Fatalf("Seal (iteration %d): %v", i, err)
		}
		if _, err := box.Open("identity/totp", sealed); err != nil {
			t.Fatalf("Open (iteration %d): %v", i, err)
		}
	}
}

func TestBox_NeverPrintsTheSecret(t *testing.T) {
	// Build a Box from a recognizable secret: 32 bytes of 'Z'.
	recognizableSecret := bytes.Repeat([]byte("Z"), 32)
	box, err := New(recognizableSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Patterns that must not appear in any fmt output.
	forbiddenPatterns := []string{
		"ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ", // Secret string as-is
		"90 90 90",                         // Decimal byte value of 'Z' (90) repeated
		"5a5a5a",                           // Hex value of 'Z' (5a) repeated
		"5A5A5A",                           // Hex uppercase variant
	}

	testCases := []struct {
		name string
		verb string
		fmt  string
	}{
		{"pointer %v", "v", "%v"},
		{"pointer %+v", "+v", "%+v"},
		{"pointer %#v", "#v", "%#v"},
		{"pointer %s", "s", "%s"},
		{"pointer %d", "d", "%d"},
		{"pointer %x", "x", "%x"},
	}

	// Test pointer formatting.
	for _, tc := range testCases {
		output := fmt.Sprintf(tc.fmt, box)
		for _, pattern := range forbiddenPatterns {
			if strings.Contains(output, pattern) {
				t.Errorf("%s: output contains secret pattern %q: %q", tc.name, pattern, output)
			}
		}
		// Verify the redaction message is present.
		if !strings.Contains(output, "redacted") {
			t.Errorf("%s: output does not contain redaction message: %q", tc.name, output)
		}
	}

	// Test slog text handler.
	var textBuf bytes.Buffer
	textHandler := slog.NewTextHandler(&textBuf, nil)
	textLogger := slog.New(textHandler)
	textLogger.Info("test", slog.Any("box", box))
	textOutput := textBuf.String()
	for _, pattern := range forbiddenPatterns {
		if strings.Contains(textOutput, pattern) {
			t.Errorf("slog text handler: output contains secret pattern %q: %q", pattern, textOutput)
		}
	}
	if !strings.Contains(textOutput, "redacted") {
		t.Errorf("slog text handler: output does not contain redaction message: %q", textOutput)
	}

	// Test slog JSON handler.
	var jsonBuf bytes.Buffer
	jsonHandler := slog.NewJSONHandler(&jsonBuf, nil)
	jsonLogger := slog.New(jsonHandler)
	jsonLogger.Info("test", slog.Any("box", box))
	jsonOutput := jsonBuf.String()
	for _, pattern := range forbiddenPatterns {
		if strings.Contains(jsonOutput, pattern) {
			t.Errorf("slog JSON handler: output contains secret pattern %q: %q", pattern, jsonOutput)
		}
	}
	if !strings.Contains(jsonOutput, "redacted") {
		t.Errorf("slog JSON handler: output does not contain redaction message: %q", jsonOutput)
	}
}
