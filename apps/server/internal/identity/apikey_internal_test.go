package identity

import (
	"bytes"
	"math/big"
	"strings"
	"testing"
)

// TestAPIKey_Format holds a minted key to K11: vtg_, 43 base62 characters of
// the random part, 6 of its checksum, 53 in all, stored as hashToken of the 32
// random bytes, and shown as vtg_, four characters, an ellipsis and the last
// four.
func TestAPIKey_Format(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 200 {
		key, hash := newAPIKey()
		if len(key) != 53 || !strings.HasPrefix(key, "vtg_") {
			t.Fatalf("key %q: want vtg_ and 53 characters", key)
		}
		for _, c := range key[4:] {
			if !strings.ContainsRune(base62Alphabet, c) {
				t.Fatalf("key %q: %q is not base62", key, c)
			}
		}
		if seen[key] {
			t.Fatalf("key %q minted twice", key)
		}
		seen[key] = true
		parsed, ok := parseAPIKey(key)
		if !ok || !bytes.Equal(parsed, hash) {
			t.Fatalf("parseAPIKey(%q) = %x, %v, want the minted hash %x", key, parsed, ok, hash)
		}
		if d := apiKeyDisplay(key); d != key[:8]+"…"+key[49:] {
			t.Fatalf("display %q of %q", d, key)
		}
	}
}

// TestAPIKey_Padding is the left padding: a random part of all zero bytes is
// 43 zeros, and a small one keeps its width, as a checksum of zero would.
func TestAPIKey_Padding(t *testing.T) {
	t.Parallel()
	if got := base62(big.NewInt(0), 43); got != strings.Repeat("0", 43) {
		t.Errorf("base62(0, 43) = %q", got)
	}
	if got := base62(big.NewInt(61), 6); got != "00000z" {
		t.Errorf("base62(61, 6) = %q", got)
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	if got := base62(max, 43); len(got) != 43 {
		t.Errorf("base62(2^256-1) = %q, %d characters, want 43", got, len(got))
	}
	random := strings.Repeat("0", 43)
	key := "vtg_" + random + apiKeyChecksum(random)
	hash, ok := parseAPIKey(key)
	if !ok || !bytes.Equal(hash, hashToken(make([]byte, 32))) {
		t.Errorf("parseAPIKey of the zero key = %x, %v", hash, ok)
	}
}

// TestAPIKey_LookAlikes is what parseAPIKey refuses without a database read:
// any change to a character, the wrong prefix, the wrong length, a character
// outside base62, and a random part past 256 bits.
func TestAPIKey_LookAlikes(t *testing.T) {
	t.Parallel()
	key, _ := newAPIKey()
	flip := func(i int) string {
		b := []byte(key)
		if b[i] == 'a' {
			b[i] = 'b'
		} else {
			b[i] = 'a'
		}
		return string(b)
	}
	tooBig := strings.Repeat("z", 43)
	for name, bad := range map[string]string{
		"a random character changed":   flip(10),
		"a checksum character changed": flip(50),
		"the prefix":                   "vtk_" + key[4:],
		"upper-case prefix":            "VTG_" + key[4:],
		"one short":                    key[:52],
		"one long":                     key + "0",
		"not base62":                   key[:20] + "-" + key[21:],
		"past 256 bits":                "vtg_" + tooBig + apiKeyChecksum(tooBig),
		"empty":                        "",
	} {
		if _, ok := parseAPIKey(bad); ok {
			t.Errorf("%s: parseAPIKey(%q) accepted", name, bad)
		}
	}
}
