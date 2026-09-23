package customers

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// actor is who a timeline write is attributed to: the three values every
// timeline entry and revision row carries (actor_kind, actor_display,
// actor_user_id — customers foundation design D1), resolved once per request
// rather than reconstructed at each of the many insert sites. UserID is nil
// for anything not attributed to a specific user (an unattributed manual
// entry, a generated system event, or a user the directory no longer knows).
type actor struct {
	Kind    string
	Display string
	UserID  *uuid.UUID
}

// unknownUserDisplay is what a user the directory no longer knows is called,
// wherever this module has to name one: the actor on a timeline entry written
// by an account since deleted (actorFor below), and a customer's owner in the
// same state (owner.go's customerDecoration.owner). One constant rather than
// two literals, because it is one fact about one directory — and because the
// two places must never disagree about it in the same response.
const unknownUserDisplay = "Unknown user"

// manualFallbackActor and generatedFallbackActor are actorFor's fallback
// when ctx carries no attributable user principal: today's pre-D1 constant
// values, unattributed for a manual entry, system for a generated one.
//
// No caller in this module reaches the fallback today: every operation that
// can write a timeline entry sits behind module.Router's authentication,
// which always plants a Principal with a nonzero UserID, and the SCIM bearer
// token authenticates only identity's own SCIM endpoints, never this
// module's. The fallback stays, and every caller still passes one, so a
// future caller that *can* run unauthenticated (or under SCIM) does not have
// to relearn D1's answer for "no user to attribute to" — and the
// manual/generated distinction pre-D1 code relied on keeps working
// unchanged if that day comes. actor_test.go exercises the fallback branch
// directly, since no HTTP path can drive it today.
var (
	manualFallbackActor    = actor{Kind: "unattributed", Display: "Unattributed"}
	generatedFallbackActor = actor{Kind: "system", Display: "System"}
)

// actorFor resolves ctx's authenticated principal, if any, into the actor a
// timeline write should be attributed to, or fallback if there is none
// (customers foundation design D1). It must be called before a transaction
// opens: the directory lookup it can make (s.deps.Users.User) is an
// out-of-process call this module never wants to make while holding a
// database lock.
//
//   - No principal in ctx, or a SCIM-authenticated caller, or a
//     Principal.UserID that is the zero UUID: fallback, verbatim.
//   - Otherwise, the principal's user is looked up in the directory. A
//     lookup error is returned unchanged: a timeline entry that guesses at
//     who wrote it is worse than a 500. A user the directory no longer knows
//     about ((nil, nil) — contracts.UserDirectory's "does not exist" shape)
//     is not an error: the write still happens, attributed to "Unknown
//     user" so the record exists even though the writer's own account is
//     gone.
func (s *server) actorFor(ctx context.Context, fallback actor) (actor, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.SCIM || p.UserID == uuid.Nil {
		return fallback, nil
	}

	user, err := s.deps.Users.User(ctx, p.UserID)
	if err != nil {
		return actor{}, fmt.Errorf("customers: resolve actor: %w", err)
	}
	userID := p.UserID
	if user == nil {
		return actor{Kind: "user", Display: unknownUserDisplay, UserID: &userID}, nil
	}
	return actor{Kind: "user", Display: user.DisplayName, UserID: &userID}, nil
}
