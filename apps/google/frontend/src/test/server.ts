import type { GoogleConnection, GoogleConnectionTest, GoogleCredential, GoogleEvent } from "../api/connection";
import { jsonResponse, problemResponse } from "./api";
import { type StubbedFetch, stubFetch, TEST_SESSION } from "./fetch";

export const SCOPES = [
  "https://www.googleapis.com/auth/admin.directory.customer.readonly",
  "https://www.googleapis.com/auth/admin.directory.domain.readonly",
];

export const SERVICE_ACCOUNT = "vantigo@contoso-vantigo.iam.gserviceaccount.com";
export const NUMERIC_CLIENT_ID = "112233445566778899001";
export const KEY_ID = "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567";
export const PRIVATE_KEY = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----\n";
export const WEB_CLIENT_ID = "123456789012-abcdefg.apps.googleusercontent.com";
export const REDIRECT_URIS = [
  "https://vantigo.example/api/v1/google/oauth/callback",
  "https://vantigo.example/api/v1/identity/oidc/callback",
];

/** A service account key file's text, as Google downloads it. */
export const keyFile = (overrides: Record<string, string> = {}) =>
  JSON.stringify({
    type: "service_account",
    project_id: "contoso-vantigo",
    private_key_id: KEY_ID,
    private_key: PRIVATE_KEY,
    client_email: SERVICE_ACCOUNT,
    client_id: NUMERIC_CLIENT_ID,
    token_uri: "https://oauth2.googleapis.com/token",
    ...overrides,
  });

/** The connection as `GET /connection` answers it before anything is set. */
export const unconfigured = (overrides: Partial<GoogleConnection> = {}): GoogleConnection => ({
  status: "unconfigured",
  credentialKind: "service_account_key",
  domains: [],
  credentials: [],
  scopesToDelegate: [...SCOPES],
  scopesGranted: [],
  scopesMissing: [],
  scopesUnexpected: [],
  redirectUris: [...REDIRECT_URIS],
  version: 1,
  ...overrides,
});

export const key = (overrides: Partial<GoogleCredential> = {}): GoogleCredential => ({
  id: 1,
  kind: "service_account_key",
  clientEmail: SERVICE_ACCOUNT,
  clientId: NUMERIC_CLIENT_ID,
  privateKeyId: KEY_ID,
  projectId: "contoso-vantigo",
  createdAt: "2026-11-01T08:00:00Z",
  inUse: true,
  ...overrides,
});

/** Every step done: a key, the admin, tested with every scope, the web client with a secret. */
export const connected = (overrides: Partial<GoogleConnection> = {}): GoogleConnection => ({
  ...unconfigured(),
  status: "connected",
  adminSubject: "admin@contoso.no",
  credentials: [key(), { id: 2, kind: "oauth_client_secret", createdAt: "2026-11-02T08:00:00Z", inUse: true }],
  delegationClientId: NUMERIC_CLIENT_ID,
  organisationName: "Contoso AS",
  customerId: "C01abc234",
  primaryDomain: "contoso.no",
  domains: ["contoso.no", "contoso.com"],
  scopesGranted: [...SCOPES],
  lastCheckedAt: "2026-11-02T10:00:00Z",
  oauthClientId: WEB_CLIENT_ID,
  oauthClientSecretAddedAt: "2026-11-02T08:00:00Z",
  version: 5,
  ...overrides,
});

export interface GoogleServer {
  connection?: GoogleConnection;
  /** Whether the caller holds the Owner role; the session answers it. True by default. */
  owner?: boolean;
  /** What `POST /connection/test` answers; connected with every scope by default. */
  test?: GoogleConnectionTest;
  events?: GoogleEvent[];
  /** Answers a request instead of the fake's own; undefined falls through to it. */
  handle?: (method: string, path: string, body: unknown) => Response | undefined;
}

export type GoogleStub = StubbedFetch & { state: { connection: GoogleConnection } };

const googleProblem = (status: number, code: string) => problemResponse(status, `refused: ${code}`, { code });

/**
 * A fake of the module's API, kept as a connection row the writes change:
 * If-Match on `PUT /connection` (412 `version_mismatch`), a key parsed for its
 * four clear fields (400 on `json`, 422 `client_mismatch`), revocation, the
 * web client, and the test written back.
 */
export const stubGoogleApi = (server: GoogleServer = {}): GoogleStub => {
  const state = { connection: server.connection ?? unconfigured() };
  let nextCredential = 100;
  const owner = server.owner ?? true;
  const session = { user: { ...TEST_SESSION.user, roles: owner ? ["user", "Owner"] : ["user"] } };

  const route = (method: string, path: string, init: RequestInit | undefined, body: unknown): Response => {
    const c = state.connection;
    if (method === "GET" && path === "/api/v1/google/connection") return jsonResponse(200, c);
    if (method === "GET" && path.startsWith("/api/v1/google/events")) return jsonResponse(200, server.events ?? []);
    if (method === "PUT" && path === "/api/v1/google/connection") {
      const supplied = new Headers(init?.headers).get("If-Match");
      if (Number(supplied?.replaceAll('"', "")) !== c.version) return googleProblem(412, "version_mismatch");
      const request = body as { adminSubject?: string };
      state.connection = {
        ...c,
        ...(request.adminSubject ? { adminSubject: request.adminSubject } : {}),
        version: c.version + 1,
      };
      return jsonResponse(200, state.connection);
    }
    if (method === "POST" && path === "/api/v1/google/connection/credentials") {
      let file: Record<string, string>;
      try {
        file = JSON.parse((body as { json: string }).json);
      } catch {
        return problemResponse(400, "Validation failed", { errors: { json: ["The key is not JSON."] } });
      }
      if (file.type !== "service_account")
        return problemResponse(400, "Validation failed", { errors: { json: ["The key is not a service account's."] } });
      const live = c.credentials.filter((one) => one.kind === "service_account_key" && !one.revokedAt);
      if (live.some((one) => one.clientId !== file.client_id)) return googleProblem(422, "client_mismatch");
      const added: GoogleCredential = {
        id: nextCredential++,
        kind: "service_account_key",
        clientEmail: file.client_email,
        clientId: file.client_id,
        privateKeyId: file.private_key_id,
        projectId: file.project_id,
        createdAt: "2026-11-03T08:00:00Z",
        inUse: true,
      };
      state.connection = {
        ...c,
        delegationClientId: file.client_id,
        credentials: [added, ...c.credentials.map((one) => (one.kind === added.kind ? { ...one, inUse: false } : one))],
      };
      return jsonResponse(201, added);
    }
    const revoke = method === "DELETE" && path.match(/^\/api\/v1\/google\/connection\/credentials\/(\d+)$/);
    if (revoke) {
      const id = Number(revoke[1]);
      const target = c.credentials.find((one) => one.id === id);
      if (!target) return new Response(null, { status: 404 });
      const revoked = { ...target, inUse: false, revokedAt: "2026-11-03T09:00:00Z" };
      state.connection = { ...c, credentials: c.credentials.map((one) => (one.id === id ? revoked : one)) };
      return jsonResponse(200, revoked);
    }
    if (method === "PUT" && path === "/api/v1/google/connection/oauth-client") {
      const request = body as { clientId: string; clientSecret?: string };
      state.connection = {
        ...c,
        oauthClientId: request.clientId,
        ...(request.clientSecret ? { oauthClientSecretAddedAt: "2026-11-03T08:00:00Z" } : {}),
      };
      return jsonResponse(200, state.connection);
    }
    if (method === "POST" && path === "/api/v1/google/connection/test") {
      const result: GoogleConnectionTest = server.test ?? {
        status: "connected",
        customer: { id: "C01abc234", organisationName: "Contoso AS", primaryDomain: "contoso.no" },
        domains: ["contoso.com", "contoso.no"],
        granted: [...SCOPES],
        missing: [],
        unexpected: [],
      };
      state.connection = {
        ...c,
        status: result.status,
        statusReason: result.reason,
        scopesGranted: result.granted,
        scopesMissing: result.missing,
        scopesUnexpected: result.unexpected,
        lastCheckedAt: "2026-11-03T10:00:00Z",
      };
      return jsonResponse(200, result);
    }
    return new Response(null, { status: 404 });
  };

  const stub = stubFetch(
    (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? "GET";
      const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
      return Promise.resolve(server.handle?.(method, path, body) ?? route(method, path, init, body));
    },
    { session },
  ) as GoogleStub;
  stub.state = state;
  return stub;
};
