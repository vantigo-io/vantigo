package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// TestNewTokenIsThirtyTwoRandomBytesHashedRaw proves the token format: 32
// random bytes as unpadded base64url, stored as SHA-256 of the bytes (not
// of the text), and that parseToken recovers the same hash.
func TestNewTokenIsThirtyTwoRandomBytesHashedRaw(t *testing.T) {
	raw, hash := newToken()
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != 32 {
		t.Fatalf("token %q decodes to %d bytes (err %v), want 32", raw, len(b), err)
	}
	if sum := sha256.Sum256(b); !bytes.Equal(hash, sum[:]) {
		t.Error("hash is not SHA-256 of the token's bytes")
	}
	if got, ok := parseToken(raw); !ok || !bytes.Equal(got, hash) {
		t.Errorf("parseToken(token) = %x, %v; want the stored hash", got, ok)
	}
	if other, _ := newToken(); other == raw {
		t.Error("two tokens are equal")
	}
}

// TestParseTokenRejectsWhatNewTokenCannotProduce proves garbage, padding and
// the wrong length never reach a lookup.
func TestParseTokenRejectsWhatNewTokenCannotProduce(t *testing.T) {
	valid, _ := newToken()
	for _, raw := range []string{
		"",
		"not a token",
		valid + "=",          // padded
		valid[:len(valid)-2], // 31 bytes' worth
		base64.RawURLEncoding.EncodeToString(make([]byte, 33)),
		base64.StdEncoding.EncodeToString(make([]byte, 32)), // standard alphabet, padded
	} {
		if _, ok := parseToken(raw); ok {
			t.Errorf("parseToken(%q) accepted it", raw)
		}
	}
}
