package identity_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// pendingOperations are the identity operations still stubbed in
// unimplemented.go (TestPendingOperationsAreExactlyTheStubs keeps the two
// lists equal). The coverage gate excuses them; each area removes the
// operations it implements, and an entry a successful exchange exercised
// fails the run. The list was generated from the contract's operationIds;
// it goes away once every operation is implemented.
var pendingOperations = []string{
	"deleteIdentityAccessGroupsByGroupIdMembersByUserId",
	"deleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleId",
	"deleteIdentityAccessGroupsById",
	"deleteIdentityScimV2GroupsById",
	"deleteIdentityScimV2UsersById",
	"getIdentityAccessDelegations",
	"getIdentityAccessGroups",
	"getIdentityAccessGroupsById",
	"getIdentityOidcCallback",
	"getIdentityOidcChallenge",
	"getIdentityOidcComplete",
	"getIdentityScimV2Groups",
	"getIdentityScimV2GroupsById",
	"getIdentityScimV2ResourceTypes",
	"getIdentityScimV2Schemas",
	"getIdentityScimV2ServiceProviderConfig",
	"getIdentityScimV2Users",
	"getIdentityScimV2UsersById",
	"patchIdentityScimV2GroupsById",
	"patchIdentityScimV2UsersById",
	"postIdentityAccessDelegations",
	"postIdentityAccessDelegationsByIdRevoke",
	"postIdentityAccessGroups",
	"postIdentityAccessGroupsByGroupIdMembersByUserId",
	"postIdentityAccessGroupsByGroupIdRoleMappingsByRoleId",
	"postIdentityOidcCallback",
	"postIdentityScimV2Groups",
	"postIdentityScimV2Users",
	"putIdentityAccessDelegationsById",
	"putIdentityAccessGroupsByGroupIdMembersByUserId",
	"putIdentityAccessGroupsByGroupIdRoleMappingsByRoleId",
	"putIdentityAccessGroupsById",
	"putIdentityScimV2UsersById",
}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
