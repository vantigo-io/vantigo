package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// tokenBytes is the entropy of every bearer token identity mints: session
// cookies, login tickets, invitations and password resets (256 bits).
const tokenBytes = 32

// hashToken is what identity stores for a token: SHA-256 of its raw bytes,
// never the token itself. As .NET's InvitationTokenService does
// (SV/InvitationTokenService.cs:22-44), the hash is over the decoded bytes,
// not the base64url text.
func hashToken(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}

// newToken returns a fresh token as the base64url text handed to the client
// and the hash to store.
func newToken() (raw string, hash []byte) {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
	return base64.RawURLEncoding.EncodeToString(b), hashToken(b)
}

// parseToken decodes a client-presented token and returns its hash. It
// reports false for anything newToken cannot have produced, so garbage never
// costs a database lookup. Looking the hash up through a unique index is
// the comparison: the client cannot choose which stored hash it is compared
// against, so there is no timing oracle on the secret.
func parseToken(raw string) (hash []byte, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != tokenBytes {
		return nil, false
	}
	return hashToken(b), true
}
