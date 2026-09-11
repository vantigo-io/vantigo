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

func (*server) DeleteIdentityAccountAvatar(context.Context, gen.DeleteIdentityAccountAvatarRequestObject) (gen.DeleteIdentityAccountAvatarResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityAccountPasskeysByCredentialId(context.Context, gen.DeleteIdentityAccountPasskeysByCredentialIdRequestObject) (gen.DeleteIdentityAccountPasskeysByCredentialIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityOwnerUsersById(context.Context, gen.DeleteIdentityOwnerUsersByIdRequestObject) (gen.DeleteIdentityOwnerUsersByIdResponseObject, error) {
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

func (*server) GetIdentityAccount(context.Context, gen.GetIdentityAccountRequestObject) (gen.GetIdentityAccountResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccountAvatar(context.Context, gen.GetIdentityAccountAvatarRequestObject) (gen.GetIdentityAccountAvatarResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccountMfa(context.Context, gen.GetIdentityAccountMfaRequestObject) (gen.GetIdentityAccountMfaResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccountMfaSetup(context.Context, gen.GetIdentityAccountMfaSetupRequestObject) (gen.GetIdentityAccountMfaSetupResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityAccountPasskeys(context.Context, gen.GetIdentityAccountPasskeysRequestObject) (gen.GetIdentityAccountPasskeysResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityBootstrapStatus(context.Context, gen.GetIdentityBootstrapStatusRequestObject) (gen.GetIdentityBootstrapStatusResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityInvitationsValidate(context.Context, gen.GetIdentityInvitationsValidateRequestObject) (gen.GetIdentityInvitationsValidateResponseObject, error) {
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

func (*server) GetIdentityOwnerInvitations(context.Context, gen.GetIdentityOwnerInvitationsRequestObject) (gen.GetIdentityOwnerInvitationsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOwnerMfa(context.Context, gen.GetIdentityOwnerMfaRequestObject) (gen.GetIdentityOwnerMfaResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOwnerMfaSetup(context.Context, gen.GetIdentityOwnerMfaSetupRequestObject) (gen.GetIdentityOwnerMfaSetupResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOwnerSystemStatus(context.Context, gen.GetIdentityOwnerSystemStatusRequestObject) (gen.GetIdentityOwnerSystemStatusResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOwnerUsers(context.Context, gen.GetIdentityOwnerUsersRequestObject) (gen.GetIdentityOwnerUsersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityOwnerUsersByIdAvatar(context.Context, gen.GetIdentityOwnerUsersByIdAvatarRequestObject) (gen.GetIdentityOwnerUsersByIdAvatarResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityProviders(context.Context, gen.GetIdentityProvidersRequestObject) (gen.GetIdentityProvidersResponseObject, error) {
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

func (*server) GetIdentitySession(context.Context, gen.GetIdentitySessionRequestObject) (gen.GetIdentitySessionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentitySystemStatus(context.Context, gen.GetIdentitySystemStatusRequestObject) (gen.GetIdentitySystemStatusResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PatchIdentityAccountProfile(context.Context, gen.PatchIdentityAccountProfileRequestObject) (gen.PatchIdentityAccountProfileResponseObject, error) {
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

func (*server) PostIdentityAccountAvatar(context.Context, gen.PostIdentityAccountAvatarRequestObject) (gen.PostIdentityAccountAvatarResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountMfaDisable(context.Context, gen.PostIdentityAccountMfaDisableRequestObject) (gen.PostIdentityAccountMfaDisableResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountMfaEnable(context.Context, gen.PostIdentityAccountMfaEnableRequestObject) (gen.PostIdentityAccountMfaEnableResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountMfaRecoveryCodes(context.Context, gen.PostIdentityAccountMfaRecoveryCodesRequestObject) (gen.PostIdentityAccountMfaRecoveryCodesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountMfaSetup(context.Context, gen.PostIdentityAccountMfaSetupRequestObject) (gen.PostIdentityAccountMfaSetupResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountPasskeysBegin(context.Context, gen.PostIdentityAccountPasskeysBeginRequestObject) (gen.PostIdentityAccountPasskeysBeginResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountPasskeysComplete(context.Context, gen.PostIdentityAccountPasskeysCompleteRequestObject) (gen.PostIdentityAccountPasskeysCompleteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountPassword(context.Context, gen.PostIdentityAccountPasswordRequestObject) (gen.PostIdentityAccountPasswordResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityAccountSessionsRevoke(context.Context, gen.PostIdentityAccountSessionsRevokeRequestObject) (gen.PostIdentityAccountSessionsRevokeResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityBootstrap(context.Context, gen.PostIdentityBootstrapRequestObject) (gen.PostIdentityBootstrapResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityInvitationsAccept(context.Context, gen.PostIdentityInvitationsAcceptRequestObject) (gen.PostIdentityInvitationsAcceptResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityLogin(context.Context, gen.PostIdentityLoginRequestObject) (gen.PostIdentityLoginResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityLogin2fa(context.Context, gen.PostIdentityLogin2faRequestObject) (gen.PostIdentityLogin2faResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityLogout(context.Context, gen.PostIdentityLogoutRequestObject) (gen.PostIdentityLogoutResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOidcCallback(context.Context, gen.PostIdentityOidcCallbackRequestObject) (gen.PostIdentityOidcCallbackResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerInvitations(context.Context, gen.PostIdentityOwnerInvitationsRequestObject) (gen.PostIdentityOwnerInvitationsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerInvitationsByIdResend(context.Context, gen.PostIdentityOwnerInvitationsByIdResendRequestObject) (gen.PostIdentityOwnerInvitationsByIdResendResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerInvitationsByIdRevoke(context.Context, gen.PostIdentityOwnerInvitationsByIdRevokeRequestObject) (gen.PostIdentityOwnerInvitationsByIdRevokeResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerMfaDisable(context.Context, gen.PostIdentityOwnerMfaDisableRequestObject) (gen.PostIdentityOwnerMfaDisableResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerMfaEnable(context.Context, gen.PostIdentityOwnerMfaEnableRequestObject) (gen.PostIdentityOwnerMfaEnableResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerMfaRecoveryCodes(context.Context, gen.PostIdentityOwnerMfaRecoveryCodesRequestObject) (gen.PostIdentityOwnerMfaRecoveryCodesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerMfaResetByUserId(context.Context, gen.PostIdentityOwnerMfaResetByUserIdRequestObject) (gen.PostIdentityOwnerMfaResetByUserIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerMfaSetup(context.Context, gen.PostIdentityOwnerMfaSetupRequestObject) (gen.PostIdentityOwnerMfaSetupResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerUsers(context.Context, gen.PostIdentityOwnerUsersRequestObject) (gen.PostIdentityOwnerUsersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerUsersByIdDisable(context.Context, gen.PostIdentityOwnerUsersByIdDisableRequestObject) (gen.PostIdentityOwnerUsersByIdDisableResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerUsersByIdEnable(context.Context, gen.PostIdentityOwnerUsersByIdEnableRequestObject) (gen.PostIdentityOwnerUsersByIdEnableResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerUsersByIdPassword(context.Context, gen.PostIdentityOwnerUsersByIdPasswordRequestObject) (gen.PostIdentityOwnerUsersByIdPasswordResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityOwnerUsersByIdPasswordReset(context.Context, gen.PostIdentityOwnerUsersByIdPasswordResetRequestObject) (gen.PostIdentityOwnerUsersByIdPasswordResetResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityPasskeysLoginBegin(context.Context, gen.PostIdentityPasskeysLoginBeginRequestObject) (gen.PostIdentityPasskeysLoginBeginResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityPasskeysLoginComplete(context.Context, gen.PostIdentityPasskeysLoginCompleteRequestObject) (gen.PostIdentityPasskeysLoginCompleteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityPasswordRecoveryRequest(context.Context, gen.PostIdentityPasswordRecoveryRequestRequestObject) (gen.PostIdentityPasswordRecoveryRequestResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityPasswordRecoveryReset(context.Context, gen.PostIdentityPasswordRecoveryResetRequestObject) (gen.PostIdentityPasswordRecoveryResetResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityScimV2Groups(context.Context, gen.PostIdentityScimV2GroupsRequestObject) (gen.PostIdentityScimV2GroupsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityScimV2Users(context.Context, gen.PostIdentityScimV2UsersRequestObject) (gen.PostIdentityScimV2UsersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentitySystemUsersByUserIdSessionsRevoke(context.Context, gen.PostIdentitySystemUsersByUserIdSessionsRevokeRequestObject) (gen.PostIdentitySystemUsersByUserIdSessionsRevokeResponseObject, error) {
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

func (*server) PutIdentityAccount(context.Context, gen.PutIdentityAccountRequestObject) (gen.PutIdentityAccountResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccountAvatar(context.Context, gen.PutIdentityAccountAvatarRequestObject) (gen.PutIdentityAccountAvatarResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityAccountProfile(context.Context, gen.PutIdentityAccountProfileRequestObject) (gen.PutIdentityAccountProfileResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityOwnerUsersById(context.Context, gen.PutIdentityOwnerUsersByIdRequestObject) (gen.PutIdentityOwnerUsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityScimV2UsersById(context.Context, gen.PutIdentityScimV2UsersByIdRequestObject) (gen.PutIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentitySystemMaintenance(context.Context, gen.PutIdentitySystemMaintenanceRequestObject) (gen.PutIdentitySystemMaintenanceResponseObject, error) {
	return nil, module.ErrNotImplemented
}
