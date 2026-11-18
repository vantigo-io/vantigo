package identity

import (
	"crypto/rand"
	"hash/crc32"
	"math/big"
	"strings"
)

// An API key (API access design K11) is vtg_, then its 32 random bytes in
// base62 left-padded to 43 characters, then the CRC32 (IEEE) of those 43
// characters in base62 left-padded to 6: 53 characters in all. The checksum
// lets a secret scanner, and Check, reject a mistyped or look-alike string
// without a database read; it is not a security control. Only hashToken of
// the 32 bytes is stored, as for every other token identity mints.
const (
	apiKeyPrefix      = "vtg_"
	apiKeyRandomChars = 43
	apiKeyCheckChars  = 6
	apiKeyLength      = len(apiKeyPrefix) + apiKeyRandomChars + apiKeyCheckChars
)

// base62Alphabet is the digits, then the upper case, then the lower case
// letters: the order secret scanners expect of base62.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var big62 = big.NewInt(62)

// base62 is n in base62, left-padded with zeros to width.
func base62(n *big.Int, width int) string {
	var digits []byte
	v := new(big.Int).Set(n)
	mod := new(big.Int)
	for v.Sign() > 0 {
		v.DivMod(v, big62, mod)
		digits = append(digits, base62Alphabet[mod.Int64()])
	}
	for len(digits) < width {
		digits = append(digits, '0')
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// fromBase62 is the number s spells in base62, and false for a character
// outside the alphabet.
func fromBase62(s string) (*big.Int, bool) {
	n := new(big.Int)
	for i := 0; i < len(s); i++ {
		d := strings.IndexByte(base62Alphabet, s[i])
		if d < 0 {
			return nil, false
		}
		n.Mul(n, big62)
		n.Add(n, big.NewInt(int64(d)))
	}
	return n, true
}

// apiKeyChecksum is the six checksum characters of a key's random part.
func apiKeyChecksum(random string) string {
	return base62(new(big.Int).SetUint64(uint64(crc32.ChecksumIEEE([]byte(random)))), apiKeyCheckChars)
}

// newAPIKey mints a key: the text handed to the Owner once and the hash to
// store.
func newAPIKey() (key string, hash []byte) {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
	random := base62(new(big.Int).SetBytes(b), apiKeyRandomChars)
	return apiKeyPrefix + random + apiKeyChecksum(random), hashToken(b)
}

// parseAPIKey checks a presented key's shape — the prefix, the length, the
// alphabet and the checksum — and returns the hash it is stored under. It
// reports false for anything newAPIKey cannot have produced, so garbage never
// costs a database lookup; looking the hash up through the unique index is
// the comparison, as parseToken's is.
func parseAPIKey(s string) (hash []byte, ok bool) {
	if len(s) != apiKeyLength || !strings.HasPrefix(s, apiKeyPrefix) {
		return nil, false
	}
	random := s[len(apiKeyPrefix) : len(apiKeyPrefix)+apiKeyRandomChars]
	if s[len(apiKeyPrefix)+apiKeyRandomChars:] != apiKeyChecksum(random) {
		return nil, false
	}
	n, ok := fromBase62(random)
	if !ok || n.BitLen() > tokenBytes*8 {
		return nil, false
	}
	return hashToken(n.FillBytes(make([]byte, tokenBytes))), true
}

// apiKeyDisplay is what a key is shown as once its secret is gone: vtg_, the
// first four characters of the random part, an ellipsis, and the key's last
// four characters.
func apiKeyDisplay(key string) string {
	return apiKeyPrefix + key[len(apiKeyPrefix):len(apiKeyPrefix)+4] + "…" + key[len(key)-4:]
}

// MintAPIKey mints an API key as the integrations service does and answers
// the key, the hash it is stored under and how it is displayed. It is for
// the development seed, which writes its demo integration's key itself and
// prints the key once; nothing else outside this package mints one.
func MintAPIKey() (key string, hash []byte, display string) {
	key, hash = newAPIKey()
	return key, hash, apiKeyDisplay(key)
}
