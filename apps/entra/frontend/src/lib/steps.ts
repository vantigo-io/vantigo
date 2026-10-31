import type { EntraConnection } from "../api/connection";

/** The authority the sign-in form is pre-filled with: the tenant's v2.0 endpoint. */
export const entraAuthority = (tenantId: string) => `https://login.microsoftonline.com/${tenantId}/v2.0`;

/**
 * Each step's *Done* mark, as the connection answers it: 1 the tenant and the
 * client saved, 2 a secret in use, 3 the last test connected with every
 * permission the module needs, 4 the UPN domain set (the one rule without a
 * default).
 */
export const stepsDone = (connection: EntraConnection) => ({
  register: Boolean(connection.tenantId && connection.clientId),
  secret: connection.credentials.some((credential) => credential.inUse && !credential.revokedAt),
  permissions:
    connection.lastCheckedAt !== undefined &&
    connection.status === "connected" &&
    connection.permissionsMissing.length === 0,
  accounts: Boolean(connection.settings.upnDomain),
});

/** How many of the four steps that carry a mark are done (steps 5 and 6 are optional and carry none). */
export const requiredStepsDone = (connection: EntraConnection) => {
  const done = stepsDone(connection);
  return [done.register, done.secret, done.permissions, done.accounts].filter(Boolean).length;
};

/** The tab holding the first required step not done: steps 1 to 3 are *Connection*'s, step 4 *Accounts*'; none when all are done. */
export const nextStepTab = (connection: EntraConnection): "connection" | "accounts" | undefined => {
  const done = stepsDone(connection);
  if (!done.register || !done.secret || !done.permissions) return "connection";
  return done.accounts ? undefined : "accounts";
};
