import { clearCsrfToken, request } from "./request";
export interface Session {
  user: { id: string; displayName: string; email: string; roles: string[] };
}
export const sessionQueryKey = ["auth", "session"] as const;
export async function fetchSession(): Promise<Session | null> {
  try {
    const value = await request<Session>("/auth/session");
    return value?.user ? value : null;
  } catch (error) {
    return (error as { status?: number }).status === 401 || (error as { status?: number }).status === 404
      ? null
      : Promise.reject(error);
  }
}
export const fetchBootstrapStatus = () =>
  request<{ available: boolean }>("/auth/bootstrap-status", { handleUnauthorized: false });
export const signIn = async (email: string, password: string) => {
  const session = await request<Session>("/auth/login", {
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
  const session = await request<Session>("/auth/bootstrap", {
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
    return await request("/auth/logout", { method: "POST" });
  } finally {
    clearCsrfToken();
  }
};
