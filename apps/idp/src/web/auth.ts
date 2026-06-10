import { createAuthClient } from "better-auth/react";

export const authClient = createAuthClient({
  // Point to the full URL path on the same origin (e.g. http://localhost:8787/idp)
  baseURL:
    typeof window !== "undefined" ? `${window.location.origin}/idp` : undefined,
});
