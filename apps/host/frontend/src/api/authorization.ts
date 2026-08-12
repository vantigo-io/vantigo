import { request } from "./request";

export interface PermissionDescriptor {
  key: string;
  module: string;
  category: string;
  displayName: string;
  description: string;
  sensitive: boolean;
  delegable: boolean;
}
export interface AuthorizationRole {
  id: string;
  name: string;
  normalizedName: string;
  displayName: string;
  description: string;
  isSystem: boolean;
  isBuiltIn: boolean;
  stewardUserId: string | null;
  version: string;
  permissions: string[];
}
export interface AuthorizationUser {
  id: string;
  displayName: string | null;
  email: string | null;
  roles?: string[];
}
export interface DelegationScope {
  id: string;
  canCreateRoles: boolean;
  grantablePermissionKeys: string[];
  stewardedRoleIds: string[];
  assignableRoleIds: string[];
}
export interface AdministrationScope {
  isOwner: boolean;
  delegationScopes: DelegationScope[];
}
export interface EffectiveAccess {
  id?: string;
  userId?: string;
  roles: string[];
  roleIds?: string[];
  permissions: string[];
  version: string;
  canManageAuthorization?: boolean;
  administrationScope?: AdministrationScope;
}
export interface Delegation {
  id: string;
  granteeUserId: string;
  expiresAt: string | null;
  revokedAt: string | null;
  version: string;
  canCreateRoles: boolean;
  permissionKeys: string[];
  stewardedRoleIds: string[];
}
export interface RoleInput {
  name: string;
  displayName: string;
  description: string;
  permissionKeys: string[];
  delegationId?: string;
  concurrencyStamp?: string;
}
export interface AssignmentInput {
  roleIds: string[];
  concurrencyStamp: string;
  delegationId?: string;
}
export interface DelegationInput {
  granteeUserId: string;
  expiresAt: string | null;
  permissionKeys: string[];
  stewardedRoleIds: string[];
  canCreateRoles: boolean;
  concurrencyStamp?: string;
}

const base = "/api/v1/identity/access";
const json = (body: unknown, method: string) => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});
export const getAuthorizationMe = () =>
  request<EffectiveAccess & { canManageAuthorization: boolean; administrationScope: AdministrationScope }>(
    `${base}/me`,
  );
export const listPermissionCatalog = () => request<PermissionDescriptor[]>(`${base}/catalog`);
export const listAuthorizationRoles = () => request<AuthorizationRole[]>(`${base}/roles`);
export const createAuthorizationRole = (input: RoleInput) =>
  request<AuthorizationRole>(`${base}/roles`, json(input, "POST"));
export const updateAuthorizationRole = (id: string, input: RoleInput) =>
  request<AuthorizationRole>(`${base}/roles/${id}`, json(input, "PUT"));
export const replaceRolePermissions = (id: string, input: RoleInput) =>
  request<AuthorizationRole>(`${base}/roles/${id}/permissions`, json(input, "PUT"));
export const deleteAuthorizationRole = (id: string, concurrencyStamp: string) =>
  request<void>(`${base}/roles/${id}`, json({ concurrencyStamp }, "DELETE"));
export const listAuthorizationUsers = () => request<AuthorizationUser[]>(`${base}/users`);
export const getUserAccess = (id: string) => request<EffectiveAccess>(`${base}/users/${id}`);
export const assignUserRoles = (id: string, input: AssignmentInput) =>
  request<EffectiveAccess>(`${base}/users/${id}/roles`, json(input, "PUT"));
export const listDelegations = () => request<Delegation[]>(`${base}/delegations`);
export const createDelegation = (input: DelegationInput) =>
  request<Delegation>(`${base}/delegations`, json(input, "POST"));
export const updateDelegation = (id: string, input: DelegationInput) =>
  request<Delegation>(`${base}/delegations/${id}`, json(input, "PUT"));
export const revokeDelegation = (id: string, concurrencyStamp: string) =>
  request<void>(`${base}/delegations/${id}/revoke`, json({ concurrencyStamp }, "POST"));
