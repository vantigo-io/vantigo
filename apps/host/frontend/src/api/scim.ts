import { request } from "./request";

export type ScimProvisioningMode = "Authoritative" | "Additive";
export interface ScimConnection {
  id: string;
  federationConnectionId: string;
  mode: ScimProvisioningMode;
  isEnabled: boolean;
  tokenVersion: number;
  createdAt: string;
  updatedAt: string;
  lastRotatedAt: string | null;
  lastRevokedAt: string | null;
  concurrencyStamp: string;
}
export interface ScimTokenResult {
  id: string;
  mode?: ScimProvisioningMode;
  token: string;
  tokenVersion: number;
  concurrencyStamp: string;
}
export interface ScimUserMapping {
  id: string;
  scimConnectionId: string;
  userId: string;
  resourceId: string;
  externalId: string;
  userName: string;
  upstreamActive: boolean;
  lifecycleOverride: "ForceEnable" | "ForceDisable" | null;
  lifecycleOverrideReason: string | null;
  sourceProfileJson: string | null;
  lastSynchronizedAt: string;
  version: number;
  etag: string;
  createdAt: string;
  updatedAt: string;
}
const base = "/api/v1/identity/access/scim";
const post = <T>(url: string, body: unknown) =>
  request<T>(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
export const listScimConnections = () => request<ScimConnection[]>(base);
export const createScimConnection = (federationConnectionId: string, mode: ScimProvisioningMode) =>
  post<ScimTokenResult>(base, { federationConnectionId, mode });
export const setScimEnabled = (id: string, concurrencyStamp: string, enabled: boolean) =>
  post<Pick<ScimConnection, "id" | "isEnabled" | "concurrencyStamp">>(
    `${base}/${id}/${enabled ? "enable" : "disable"}`,
    { concurrencyStamp },
  );
export const rotateScimToken = (id: string, concurrencyStamp: string) =>
  post<ScimTokenResult>(`${base}/${id}/rotate`, { concurrencyStamp });
export const revokeScimToken = (id: string, concurrencyStamp: string) =>
  post<void>(`${base}/${id}/revoke`, { concurrencyStamp });
export const listScimUsers = (id: string) => request<ScimUserMapping[]>(`${base}/${id}/users`);
export const setScimUserOverride = (
  connectionId: string,
  userId: string,
  body: { override: "ForceEnable" | "ForceDisable" | null; reason: string; etag: string },
) =>
  request<ScimUserMapping>(`${base}/${connectionId}/users/${userId}/override`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
