import { clearCsrfToken, request } from "./request";
export interface Session {
  user: { id: string; displayName: string; email: string; roles: string[] };
}
export const sessionQueryKey = ["auth", "session"] as const;
export async function fetchSession(): Promise<Session | null> {
  try {
    const value = await request<Session>("/api/v1/identity/session");
    return value?.user ? value : null;
  } catch (error) {
    return (error as { status?: number }).status === 401 || (error as { status?: number }).status === 404
      ? null
      : Promise.reject(error);
  }
}
export const fetchBootstrapStatus = () =>
  request<{ available: boolean }>("/api/v1/identity/bootstrap-status", { handleUnauthorized: false });
export const signIn = async (email: string, password: string) => {
  const session = await request<Session>("/api/v1/identity/login", {
    method: "POST",
    handleUnauthorized: false,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  clearCsrfToken();
  return session;
};
export const bootstrapAccount = async (values: {
  secret: string;
  email: string;
  displayName: string;
  password: string;
}) => {
  const session = await request<Session>("/api/v1/identity/bootstrap", {
    method: "POST",
    handleUnauthorized: false,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(values),
  });
  clearCsrfToken();
  return session;
};
export const signOut = async () => {
  try {
    return await request("/api/v1/identity/logout", { method: "POST" });
  } finally {
    clearCsrfToken();
  }
};
