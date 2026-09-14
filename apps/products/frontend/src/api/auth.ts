import { request } from "./request";

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
export const signIn = (email: string, password: string) =>
  request<Session>("/api/v1/identity/login", {
    method: "POST",
    handleUnauthorized: false,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
export const signOut = () => request<{ signedOut: true }>("/api/v1/identity/logout", { method: "POST" });
