package secrets

import (
	"bytes"
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
	if err != ErrInvalid {
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
	if err != ErrInvalid {
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
	if err != ErrInvalid {
		t.Errorf("Open(unknown key id) error = %v, want ErrInvalid", err)
	}
}

func TestOpen_ShortInputFails(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := box.Open("identity/totp", []byte{0x01}); err != ErrInvalid {
		t.Errorf("Open(short input) error = %v, want ErrInvalid", err)
	}
	if _, err := box.Open("identity/totp", nil); err != ErrInvalid {
		t.Errorf("Open(nil) error = %v, want ErrInvalid", err)
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

	if _, err := box.OpenString("identity/oidc-state", "not base64url!!"); err != ErrInvalid {
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
