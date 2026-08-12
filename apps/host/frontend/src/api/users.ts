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
