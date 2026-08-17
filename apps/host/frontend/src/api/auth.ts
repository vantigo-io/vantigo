import { clearCsrfToken, ensureCsrfToken, request } from "./request";

export interface User {
  id: string;
  displayName: string;
  email: string;
  roles: string[];
}
export interface Session {
  user: User;
  twoFactorEnabled?: boolean;
  mfaEnrollmentRequired?: boolean;
  mfaAuthenticated?: boolean;
  isSystemAdmin: boolean;
  tenants?: Tenant[];
  activeTenantId?: string | null;
}
export interface Tenant {
  id: string;
  name: string;
  slug: string;
  /** Supplied by deployments that expose tenant availability. */
  status?: string;
}

export const sessionQueryKey = ["auth", "session"] as const;
export const fetchSession = async (): Promise<Session | null> => {
  try {
    const session = await request<Session>("/api/v1/identity/session", { handleUnauthorized: false });
    return session && typeof session === "object" && "user" in session ? session : null;
  } catch (error) {
    const status = (error as { status?: number }).status;
    if (status === 401 || status === 404) return null;
    throw error;
  }
};
export const switchTenant = async (tenantId: string): Promise<Session> => {
  await ensureCsrfToken();
  const session = await request<Session>("/api/v1/identity/session/tenant", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tenantId }),
  });
  clearCsrfToken();
  await ensureCsrfToken();
  return session;
};
export const signIn = async (email: string, password: string) => {
  await ensureCsrfToken();
  const session = await request<Session>("/api/v1/identity/login", {
    method: "POST",
    handleUnauthorized: false,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  clearCsrfToken();
  await ensureCsrfToken();
  return session;
};
export const beginPasskeyLogin = (email: string) =>
  request<{ ceremonyId: string; options: unknown }>("/api/v1/identity/passkeys/login/begin", {
    method: "POST",
    handleUnauthorized: false,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email }),
  });
export const completePasskeyLogin = async (ceremonyId: string, credentialJson: string) => {
  const session = await request<Session>("/api/v1/identity/passkeys/login/complete", {
    method: "POST",
    handleUnauthorized: false,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ceremonyId, credentialJson }),
  });
  clearCsrfToken();
  await ensureCsrfToken();
  return session;
};
export const signOut = async () => {
  try {
    return await request<{ signedOut: true }>("/api/v1/identity/logout", { method: "POST" });
  } finally {
    clearCsrfToken();
  }
};
