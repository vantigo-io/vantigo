import type { EntraConnection } from "../api/connection";

/** The authority the sign-in form is pre-filled with: the tenant's v2.0 endpoint. */
export const entraAuthority = (tenantId: string) => `https://login.microsoftonline.com/${tenantId}/v2.0`;

/**
 * Each step's *Done* mark, as the connection answers it: 1 the tenant and the
 * client saved, 2 a secret in use, 3 the last test connected with every
 * permission, 4 the UPN domain set (the one rule without a default).
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
