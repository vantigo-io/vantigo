package identity

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Every gen.StrictServerInterface operation not yet implemented answers 501
// through module.ResponseError. Generated once from the interface; each area
// moves the operations it implements into its own file and deletes them
// here, so the build keeps proving the interface is complete. The test
// package's pendingOperations lists exactly these operations.

func (*server) DeleteIdentityAccessGroupsByGroupIdMembersByUserId(context.Context, gen.DeleteIdentityAccessGroupsByGroupIdMembersByUserIdRequestObject) (gen.DeleteIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleId(context.Context, gen.DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdRequestObject) (gen.DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityAccessGroupsById(context.Context, gen.DeleteIdentityAccessGroupsByIdRequestObject) (gen.DeleteIdentityAccessGroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityAccessRolesById(context.Context, gen.DeleteIdentityAccessRolesByIdRequestObject) (gen.DeleteIdentityAccessRolesByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityAccountPasskeysByCredentialId(context.Context, gen.DeleteIdentityAccountPasskeysByCredentialIdRequestObject) (gen.DeleteIdentityAccountPasskeysByCredentialIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityScimV2GroupsById(context.Context, gen.DeleteIdentityScimV2GroupsByIdRequestObject) (gen.DeleteIdentityScimV2GroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityScimV2UsersById(context.Context, gen.DeleteIdentityScimV2UsersByIdRequestObject) (gen.DeleteIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessAudit(context.Context, gen.GetIdentityAccessAuditRequestObject) (gen.GetIdentityAccessAuditResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessCatalog(context.Context, gen.GetIdentityAccessCatalogRequestObject) (gen.GetIdentityAccessCatalogResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessDelegations(context.Context, gen.GetIdentityAccessDelegationsRequestObject) (gen.GetIdentityAccessDelegationsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessGroups(context.Context, gen.GetIdentityAccessGroupsRequestObject) (gen.GetIdentityAccessGroupsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessGroupsById(context.Context, gen.GetIdentityAccessGroupsByIdRequestObject) (gen.GetIdentityAccessGroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessMe(context.Context, gen.GetIdentityAccessMeRequestObject) (gen.GetIdentityAccessMeResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessRoles(context.Context, gen.GetIdentityAccessRolesRequestObject) (gen.GetIdentityAccessRolesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessUsers(context.Context, gen.GetIdentityAccessUsersRequestObject) (gen.GetIdentityAccessUsersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccessUsersById(context.Context, gen.GetIdentityAccessUsersByIdRequestObject) (gen.GetIdentityAccessUsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccountPasskeys(context.Context, gen.GetIdentityAccountPasskeysRequestObject) (gen.GetIdentityAccountPasskeysResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOidcCallback(context.Context, gen.GetIdentityOidcCallbackRequestObject) (gen.GetIdentityOidcCallbackResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOidcChallenge(context.Context, gen.GetIdentityOidcChallengeRequestObject) (gen.GetIdentityOidcChallengeResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOidcComplete(context.Context, gen.GetIdentityOidcCompleteRequestObject) (gen.GetIdentityOidcCompleteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOwnerSystemStatus(context.Context, gen.GetIdentityOwnerSystemStatusRequestObject) (gen.GetIdentityOwnerSystemStatusResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2Groups(context.Context, gen.GetIdentityScimV2GroupsRequestObject) (gen.GetIdentityScimV2GroupsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2GroupsById(context.Context, gen.GetIdentityScimV2GroupsByIdRequestObject) (gen.GetIdentityScimV2GroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2ResourceTypes(context.Context, gen.GetIdentityScimV2ResourceTypesRequestObject) (gen.GetIdentityScimV2ResourceTypesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2Schemas(context.Context, gen.GetIdentityScimV2SchemasRequestObject) (gen.GetIdentityScimV2SchemasResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2ServiceProviderConfig(context.Context, gen.GetIdentityScimV2ServiceProviderConfigRequestObject) (gen.GetIdentityScimV2ServiceProviderConfigResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2Users(context.Context, gen.GetIdentityScimV2UsersRequestObject) (gen.GetIdentityScimV2UsersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2UsersById(context.Context, gen.GetIdentityScimV2UsersByIdRequestObject) (gen.GetIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentitySystemStatus(context.Context, gen.GetIdentitySystemStatusRequestObject) (gen.GetIdentitySystemStatusResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PatchIdentityScimV2GroupsById(context.Context, gen.PatchIdentityScimV2GroupsByIdRequestObject) (gen.PatchIdentityScimV2GroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PatchIdentityScimV2UsersById(context.Context, gen.PatchIdentityScimV2UsersByIdRequestObject) (gen.PatchIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccessDelegations(context.Context, gen.PostIdentityAccessDelegationsRequestObject) (gen.PostIdentityAccessDelegationsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccessDelegationsByIdRevoke(context.Context, gen.PostIdentityAccessDelegationsByIdRevokeRequestObject) (gen.PostIdentityAccessDelegationsByIdRevokeResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccessGroups(context.Context, gen.PostIdentityAccessGroupsRequestObject) (gen.PostIdentityAccessGroupsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccessGroupsByGroupIdMembersByUserId(context.Context, gen.PostIdentityAccessGroupsByGroupIdMembersByUserIdRequestObject) (gen.PostIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleId(context.Context, gen.PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdRequestObject) (gen.PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccessRoles(context.Context, gen.PostIdentityAccessRolesRequestObject) (gen.PostIdentityAccessRolesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountPasskeysBegin(context.Context, gen.PostIdentityAccountPasskeysBeginRequestObject) (gen.PostIdentityAccountPasskeysBeginResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountPasskeysComplete(context.Context, gen.PostIdentityAccountPasskeysCompleteRequestObject) (gen.PostIdentityAccountPasskeysCompleteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOidcCallback(context.Context, gen.PostIdentityOidcCallbackRequestObject) (gen.PostIdentityOidcCallbackResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityPasskeysLoginBegin(context.Context, gen.PostIdentityPasskeysLoginBeginRequestObject) (gen.PostIdentityPasskeysLoginBeginResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityPasskeysLoginComplete(context.Context, gen.PostIdentityPasskeysLoginCompleteRequestObject) (gen.PostIdentityPasskeysLoginCompleteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityScimV2Groups(context.Context, gen.PostIdentityScimV2GroupsRequestObject) (gen.PostIdentityScimV2GroupsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityScimV2Users(context.Context, gen.PostIdentityScimV2UsersRequestObject) (gen.PostIdentityScimV2UsersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccessDelegationsById(context.Context, gen.PutIdentityAccessDelegationsByIdRequestObject) (gen.PutIdentityAccessDelegationsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccessGroupsByGroupIdMembersByUserId(context.Context, gen.PutIdentityAccessGroupsByGroupIdMembersByUserIdRequestObject) (gen.PutIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleId(context.Context, gen.PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdRequestObject) (gen.PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccessGroupsById(context.Context, gen.PutIdentityAccessGroupsByIdRequestObject) (gen.PutIdentityAccessGroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccessRolesById(context.Context, gen.PutIdentityAccessRolesByIdRequestObject) (gen.PutIdentityAccessRolesByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccessRolesByIdPermissions(context.Context, gen.PutIdentityAccessRolesByIdPermissionsRequestObject) (gen.PutIdentityAccessRolesByIdPermissionsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccessUsersByIdRoles(context.Context, gen.PutIdentityAccessUsersByIdRolesRequestObject) (gen.PutIdentityAccessUsersByIdRolesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityScimV2UsersById(context.Context, gen.PutIdentityScimV2UsersByIdRequestObject) (gen.PutIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentitySystemMaintenance(context.Context, gen.PutIdentitySystemMaintenanceRequestObject) (gen.PutIdentitySystemMaintenanceResponseObject, error) {
	return nil, module.ErrNotImplemented
}
