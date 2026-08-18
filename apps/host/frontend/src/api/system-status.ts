import { request } from "./request";

export interface IdentitySystemStatus {
  total: number;
  active: number;
  disabled: number;
  pendingInvitations: number;
  staticOidcEnabled: boolean;
  staticOidcProvider: string | null;
  staticScimEnabled: boolean;
  lastStaticOidcSignInAtUtc: string | null;
  lastAuthenticatedScimRequestAtUtc: string | null;
}

/** Existing system-admin identity metrics endpoint. */
export const getIdentitySystemStatus = () => request<IdentitySystemStatus>("/api/v1/identity/owner/system-status");

export type SystemStatus = {
  maintenance: boolean;
  message: string | null;
};

export type SetMaintenanceRequest = {
  enabled: boolean;
  message?: string | null;
};

export const systemStatusQueryKey = ["system", "status"] as const;

export const fetchSystemStatus = () =>
  request<SystemStatus>("/api/v1/identity/system/status", { handleUnauthorized: false });

export const setMaintenance = (body: SetMaintenanceRequest) =>
  request<SystemStatus>("/api/v1/identity/system/maintenance", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export const shouldShowMaintenance = (status: SystemStatus | undefined, isSystemAdmin: boolean) =>
  status?.maintenance === true && !isSystemAdmin;
