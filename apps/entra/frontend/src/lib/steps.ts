import { CALENDAR_PERMISSION, type EntraConnection } from "../api/connection";

/** The authority the sign-in form is pre-filled with: the tenant's v2.0 endpoint. */
export const entraAuthority = (tenantId: string) => `https://login.microsoftonline.com/${tenantId}/v2.0`;

/**
 * Each step's *Done* mark, as the connection answers it: 1 the tenant and the
 * client saved, 2 a secret in use, 3 the last test connected with every
 * permission the module needs, 4 the UPN domain set (the one rule without a
 * default). Calendars.ReadWrite does not count: it is advisory, since a grant
 * made with Exchange RBAC for Applications never appears in the token
 * (calendar Outlook sync design O2, as built).
 */
export const stepsDone = (connection: EntraConnection) => ({
  register: Boolean(connection.tenantId && connection.clientId),
  secret: connection.credentials.some((credential) => credential.inUse && !credential.revokedAt),
  permissions:
    connection.lastCheckedAt !== undefined &&
    connection.status === "connected" &&
    connection.permissionsMissing.every((name) => name === CALENDAR_PERMISSION),
  accounts: Boolean(connection.settings.upnDomain),
});
