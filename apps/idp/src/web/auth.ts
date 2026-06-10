import { createAuthClient } from "better-auth/react";

export const authClient = createAuthClient({
  // Point to the relative path prefix on the same origin (e.g. /idp/api/auth)
  baseURL: "/idp",
});
