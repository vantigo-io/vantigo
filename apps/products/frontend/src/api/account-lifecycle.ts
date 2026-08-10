import { clearCsrfToken, ensureCsrfToken, request } from "./request";

export interface LifecycleSession {
  user: { id: string; displayName: string; email: string; roles: string[] };
  twoFactorEnabled?: boolean;
  mfaEnrollmentRequired?: boolean;
  mfaAuthenticated?: boolean;
}
export interface Invitation {
  id: string;
  email: string;
  role: "User" | "Owner";
  displayName: string | null;
  createdAt: string;
  expiresAt: string;
  revokedAt: string | null;
  acceptedAt: string | null;
}
const json = (method: string, body: unknown) => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});
export const lifecycleRequest = <T>(url: string, init?: RequestInit) => request<T>(url, init);
export const listInvitations = () => request<Invitation[]>("/api/v1/identity/owner/invitations");
export const createInvitation = (body: { email: string; displayName?: string; role: "User" | "Owner" }) =>
  request<Invitation>("/api/v1/identity/owner/invitations", json("POST", body));
export const invitationAction = (id: string, action: "revoke" | "resend") =>
  request<Invitation>(`/api/v1/identity/owner/invitations/${id}/${action}`, { method: "POST" });
export const mfaStatus = () =>
  request<{ twoFactorEnabled: boolean; mfaEnrollmentRequired: boolean }>("/api/v1/identity/owner/mfa");
export const mfaSetup = () =>
  request<{ sharedKey: string | null; authenticatorUri: string | null; initialized: boolean }>(
    "/api/v1/identity/owner/mfa/setup",
  );
export const initializeMfa = () => request("/api/v1/identity/owner/mfa/setup", { method: "POST" });
export const enableMfa = (code: string) =>
  request<{ twoFactorEnabled: boolean; recoveryCodes: string[] }>(
    "/api/v1/identity/owner/mfa/enable",
    json("POST", { code }),
  );
export const regenerateRecoveryCodes = (code: string) =>
  request<{ recoveryCodes: string[] }>("/api/v1/identity/owner/mfa/recovery-codes", json("POST", { code }));
export const disableMfa = (password: string) =>
  request("/api/v1/identity/owner/mfa/disable", json("POST", { password }));
export const validateInvitation = (token: string) =>
  request<{ valid: boolean; email?: string; role?: string; expiresAt?: string }>(
    `/api/v1/identity/invitations/validate?token=${encodeURIComponent(token)}`,
  );
export const acceptInvitation = async (body: { token: string; displayName?: string; password: string }) => {
  const result = await request<LifecycleSession>("/api/v1/identity/invitations/accept", json("POST", body));
  clearCsrfToken();
  await ensureCsrfToken();
  return result;
};
export const requestPasswordRecovery = async (email: string) =>
  request<{ accepted: boolean }>("/api/v1/identity/password-recovery/request", json("POST", { email }));
export const resetPassword = (body: { email: string; token: string; newPassword: string }) =>
  request<{ success: boolean }>("/api/v1/identity/password-recovery/reset", json("POST", body));
export const fetchOidcProvider = () => request<{ oidc: { displayName: string } | null }>("/api/v1/identity/providers");
export const completeTwoFactor = async (code: string, rememberMe = false) => {
  const result = await request<{
    user: LifecycleSession["user"] | null;
    requiresTwoFactor: boolean;
    mfaEnrollmentRequired: boolean;
  }>("/api/v1/identity/login/2fa", json("POST", { code, rememberMe }));
  clearCsrfToken();
  await ensureCsrfToken();
  return result;
};
export const fetchBootstrapStatus = () =>
  request<{ available: boolean }>("/api/v1/identity/bootstrap-status", { handleUnauthorized: false });
export const bootstrapAccount = async (body: {
  secret: string;
  email: string;
  displayName: string;
  password: string;
}) => {
  const result = await request<LifecycleSession>("/api/v1/identity/bootstrap", json("POST", body));
  clearCsrfToken();
  await ensureCsrfToken();
  return result;
};
