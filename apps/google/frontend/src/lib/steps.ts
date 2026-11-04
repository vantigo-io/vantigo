import type { GoogleConnection, GoogleCredential } from "../api/connection";

/** A service account key that is not revoked. */
export const liveKeys = (connection: GoogleConnection): GoogleCredential[] =>
  connection.credentials.filter((credential) => credential.kind === "service_account_key" && !credential.revokedAt);

/**
 * Each step's *Done* mark, as the connection answers it: a live key uploaded,
 * the admin to impersonate named, the last test connected with every scope,
 * and the web client set with a secret. Creating the project and delegating
 * happen at Google and carry no mark of their own: the test is their proof.
 */
export const stepsDone = (connection: GoogleConnection) => ({
  key: liveKeys(connection).length > 0,
  admin: Boolean(connection.adminSubject),
  tested:
    connection.lastCheckedAt !== undefined &&
    connection.status === "connected" &&
    connection.scopesMissing.length === 0,
  oauthClient: Boolean(connection.oauthClientId && connection.oauthClientSecretAddedAt),
});

/**
 * Whether the page opens on *Connection* rather than *Status*: while nothing
 * is set up and no key is uploaded there is no status to show, so the host's
 * index route sends the reader to the first step (as Entra's page does).
 */
export const opensOnConnection = (connection: GoogleConnection): boolean =>
  connection.status === "unconfigured" && liveKeys(connection).length === 0;
