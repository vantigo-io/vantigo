package identity

import (
	"cmp"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// The built-in roles (CT/Identity/AuthRoles.cs:5-7). The baseline migration
// inserts them with these fixed ids, and they cannot be renamed or deleted.
const (
	RoleSystemAdmin = "SystemAdmin"
	RoleOwner       = "Owner"
	RoleUser        = "User"
)

var (
	RoleSystemAdminID = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	RoleOwnerID       = uuid.MustParse("00000000-0000-4000-8000-000000000002")
	RoleUserID        = uuid.MustParse("00000000-0000-4000-8000-000000000003")
)

// orderRoles is how every response lists a user's roles
// (EA/AuthAccountState.cs:112-126): blank names dropped, duplicates removed,
// then SystemAdmin, Owner, User, then every other role in ordinal order.
func orderRoles(roles []string) []string {
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		if strings.TrimSpace(role) == "" || slices.Contains(out, role) {
			continue
		}
		out = append(out, role)
	}
	slices.SortFunc(out, func(a, b string) int {
		if c := cmp.Compare(roleRank(a), roleRank(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	return out
}

func roleRank(role string) int {
	switch role {
	case RoleSystemAdmin:
		return 0
	case RoleOwner:
		return 1
	case RoleUser:
		return 2
	default:
		return 3
	}
}

// managedRole is the one role owner user management shows and edits for a
// user: Owner if they hold it, else User (EA/AuthAccountState.cs:105-110).
func managedRole(roles []string) string {
	if slices.Contains(roles, RoleOwner) {
		return RoleOwner
	}
	return RoleUser
}
