import { request } from "./request";

export type UserRole = "User" | "Owner";

export interface OwnerUserResponse {
  id: string;
  displayName: string | null;
  email: string | null;
  role: UserRole;
  active: boolean;
  lockoutEnd: string | null;
  lockedOut: boolean;
  disabled: boolean;
  twoFactorEnabled: boolean;
  avatarUrl?: string | null;
  ssoEnabled: boolean;
  /** Staff created the user and they have not yet accepted their invitation; listed disabled and not active. */
  pending?: boolean;
  /** `staff` for a user Staff's provisioning manages; absent otherwise. */
  managedBy?: "staff";
  /** The employee number of a user Staff manages. */
  employeeNumber?: string;
  /** Who disabled the user; absent while enabled. */
  disabledBy?: "owner" | "staff";
  /** The user's single sign-on link; absent while OIDC is off. */
  ssoLink?: {
    state: "linked" | "expected" | "none";
    userPrincipalName?: string;
    objectId?: string;
    expectedAt?: string;
  };
}

export type ManagedUser = OwnerUserResponse;
export interface PasswordRecoveryResponse {
  accepted: boolean;
}

const json = (body: unknown): RequestInit => ({
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const listUsers = () => request<OwnerUserResponse[]>("/api/v1/identity/owner/users");
export const createUser = (body: { displayName: string; email: string; role: UserRole; password: string }) =>
  request<OwnerUserResponse>("/api/v1/identity/owner/users", json(body));
export const updateUser = (id: string, body: { displayName: string; email: string; role: UserRole }) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}`, { ...json(body), method: "PUT" });
export const sendPasswordReset = (id: string) =>
  request<PasswordRecoveryResponse>(`/api/v1/identity/owner/users/${id}/password-reset`, json({}));
export const setUserPassword = (id: string, password: string) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}/password`, json({ password }));
export const disableUser = (id: string) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}/disable`, json({}));
export const enableUser = (id: string) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}/enable`, json({}));
export const deleteUser = (id: string) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}`, { method: "DELETE" });
export const manageUser = (id: string, employeeNumber: string) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}/manage`, json({ module: "staff", employeeNumber }));
export const announceSsoLink = (id: string, body: { userPrincipalName?: string; objectId?: string }) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}/sso-link`, json(body));
export const removeSsoLink = (id: string) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}/sso-link`, { method: "DELETE" });
export const unmanageUser = (id: string) =>
  request<OwnerUserResponse>(`/api/v1/identity/owner/users/${id}/unmanage`, json({}));
