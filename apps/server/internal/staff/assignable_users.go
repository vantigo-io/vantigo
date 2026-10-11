package staff

import (
	"context"
	"fmt"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// assignableUsersLimit is how many users the picker answers at most (plan
// reading 9, customers' assignable-users precedent).
const assignableUsersLimit = 20

// GetStaffAssignableUsers Search the users a person may be linked to
// (GET /api/v1/staff/assignable-users)
//
// The picker HR links a person to a user with: the user directory's search,
// which answers active users, capped at twenty here whatever the directory's
// own bound — the cap is this module's promise, enforced rather than assumed
// of a collaborator. A user already linked elsewhere is listed; linking them
// answers why not. Identity's own user list needs the authorization
// administrator's policy, which HR need not hold, so the module asks the
// directory, as customers' owner picker does.
func (s *server) GetStaffAssignableUsers(ctx context.Context, req gen.GetStaffAssignableUsersRequestObject) (gen.GetStaffAssignableUsersResponseObject, error) {
	query := ""
	if req.Params.Q != nil {
		query = strings.TrimSpace(*req.Params.Q)
	}
	found, err := s.usersSearch(ctx, query, assignableUsersLimit)
	if err != nil {
		return nil, fmt.Errorf("staff: search the users a person may be linked to: %w", err)
	}
	out := make([]gen.StaffAssignableUser, 0, len(found))
	for _, u := range found {
		if !u.Active {
			continue
		}
		out = append(out, gen.StaffAssignableUser{Id: u.ID, DisplayName: u.DisplayName})
		if len(out) == assignableUsersLimit {
			break
		}
	}
	return gen.GetStaffAssignableUsers200JSONResponse(out), nil
}
