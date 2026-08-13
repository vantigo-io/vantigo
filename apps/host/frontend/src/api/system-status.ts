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

export const getIdentitySystemStatus = () => request<IdentitySystemStatus>("/api/v1/identity/owner/system-status");
