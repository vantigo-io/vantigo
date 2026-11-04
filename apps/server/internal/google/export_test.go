package google

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
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

// Throttled reports whether err is the client's errThrottled: Google's usage
// limit, never a refusal.
func Throttled(err error) bool { return errors.Is(err, errThrottled) }

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

// PersonalToken is the access token of the person's own connection gid for
// feature (delivery D, personal_token.go), as the calendar (E) and personal
// sending (G) ask for it; removal asks as a removal would.
func PersonalToken(ctx context.Context, d module.Deps, gid, feature string, removal bool) (string, error) {
	return newPersonalTokens(d).source(gid, feature, removal).token(ctx)
}

// ConnectionChanges are the personal connections whose state moved after
// since, as the calendar's Connections answers them (delivery E).
func ConnectionChanges(ctx context.Context, d module.Deps, since time.Time) ([]contracts.ConnectionChange, error) {
	return connectionChanges(ctx, store.New(d.Pool), since)
}
