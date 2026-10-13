import { runtimeBase } from "@vantigo/frontend-shell";

/**
 * The paths that render without a session: the root asks for none on them, the
 * MFA gate and the 401 handler leave them alone. Exact paths, never prefixes.
 */
export const publicPaths = new Set([
  "/sign-in",
  "/setup",
  "/forgot-password",
  "/reset-password",
  "/password-reset",
  "/accept-invitation",
  "/invitations/accept",
  "/session-expired",
  // The pay page (D15): anonymous, and the only page the public host serves.
  "/pay",
]);

/** A browser pathname with the runtime base path taken off: `/vantigo/pay` is `/pay` under `/vantigo/`. */
export const appPathname = (pathname: string): string => {
  const base = runtimeBase().replace(/\/$/, "");
  if (!base || (pathname !== base && !pathname.startsWith(`${base}/`))) return pathname;
  return pathname.slice(base.length) || "/";
};

/**
 * The paths the public host serves (D14): there not even the account's
 * language is asked for, since every request must be on the host's allowlist.
 */
export const sessionlessPaths = new Set(["/pay"]);
