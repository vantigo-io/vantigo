package google

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Token is the delegated token for scopes acting as subject, from the seam
// every call takes its token from (token.go), over the module built on d:
// the external tests reach a subject other than the admin here, as a later
// delivery's per-person calls will.
func Token(ctx context.Context, d module.Deps, scopes []string, subject string) (string, error) {
	return newKeyring(store.New(d.Pool), newGoogleClient(d)).tokenSource(scopes, subject).token(ctx)
}

// SubjectInvalid reports whether err is Google refusing the subject rather
// than the key.
func SubjectInvalid(err error) bool { return errors.Is(err, errSubjectInvalid) }

// CachedScopes are the scope sets the process holds a token for, signed
// with the key sealed as sealed, sorted.
func CachedScopes(sealed []byte) []string {
	digest := sha256.Sum256(sealed)
	processTokens.mu.Lock()
	defer processTokens.mu.Unlock()
	var out []string
	for k := range processTokens.tokens {
		if k.sealed == digest {
			out = append(out, k.scopes)
		}
	}
	slices.Sort(out)
	return out
}
