package staff

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is who holds a permission by right (delivery C, Task 5; the
// owner's answers 3 and 4, B's open question 9): the active users identity
// names as holding a key — through a role of their own, a role mapped to an
// access group they are a member of, or as an Owner, who holds every key —
// read through contracts.UserDirectory.PermissionHolders. Two rules read
// them:
//
//   - the absence mails' fallback (notifications.go): a request of a person
//     with no manager-decider mails the holders of byRightKeys, and a request
//     of an HR-only type the other holders of staff:personal-data;
//   - the sole holder's own HR-only request (deciders.go): the one active
//     holder of staff:personal-data decides their own.
//
// The holders are read before any transaction — never inside one, the lock
// rule — and once per request and key (the caller's memo). A read that
// fails is logged by its caller and degrades: no holder is mailed, and the
// caller is not the sole holder, so the rule fails toward waiting. The
// holders are judged before the decision's transaction takes the person's
// lock, so a holder added or removed between the judgment and the write is
// not seen by that write — a window of one request, accepted as B's
// manager-deciders accept it (TOCTOU).

// byRightKeys are the keys whose holders decide every person's absence of a
// type that is not HR-only: the holders the request's mail falls back to.
var byRightKeys = []string{permApprove, permManage}

// holdersRead is one key's answer, kept for the rest of the request.
type holdersRead struct {
	holders []contracts.Holder
	err     error
}

// holders is the active holders of key, read once per request: the
// directory's answer, or its error, is memoised on caller.
func (s *server) holders(ctx context.Context, caller *callerContext, key string) ([]contracts.Holder, error) {
	if r, ok := caller.holders[key]; ok {
		return r.holders, r.err
	}
	hs, err := s.usersPermissionHolders(ctx, key)
	if caller.holders == nil {
		caller.holders = map[string]holdersRead{}
	}
	caller.holders[key] = holdersRead{holders: hs, err: err}
	return hs, err
}

// solePersonalData reports whether the caller is the one active holder of
// staff:personal-data, who decides their own HR-only request: they hold it,
// they are linked to a person, and identity names exactly one holder — them.
// It fails closed: a directory that cannot answer makes nobody sole.
func (s *server) solePersonalData(ctx context.Context, caller *callerContext) bool {
	if !caller.PersonalData || caller.Person == nil {
		return false
	}
	hs, err := s.holders(ctx, caller, permPersonalData)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "staff: the holders of staff:personal-data could not be read; nobody is their sole holder",
			"user_id", caller.UserID.String(), "error", err.Error())
		return false
	}
	return len(hs) == 1 && hs[0].ID == caller.UserID
}
