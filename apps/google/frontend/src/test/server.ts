import type {
  GoogleAccount,
  GoogleConnection,
  GoogleConnectionTest,
  GoogleCredential,
  GoogleEvent,
  GoogleGroup,
  GoogleSettingsRequest,
  GoogleSku,
} from "../api/connection";
import type { GooglePersonalComplete, GooglePersonalConnection, GooglePersonalSwitch } from "../api/personal";
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
export const PERSONAL_REDIRECT_URI = "https://vantigo.example/api/v1/google/connect/callback";
export const AUTHORIZE_URL = "https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=y";

/** Connection for people, as the connection answers it: off, nobody connected. */
export const personalSwitch = (overrides: Partial<GooglePersonalSwitch> = {}): GooglePersonalSwitch => ({
  enabled: false,
  status: "off",
  connected: 0,
  reconnect: 0,
  redirectUri: PERSONAL_REDIRECT_URI,
  ...overrides,
});

/** The caller's own connection as `GET /connection/me` answers it: offered, not connected. */
export const personalConnection = (overrides: Partial<GooglePersonalConnection> = {}): GooglePersonalConnection => ({
  offered: true,
  state: "not_connected",
  features: [],
  account: "kari.nordmann@contoso.no",
  ...overrides,
});

/** A live connection for the calendar. */
export const activePersonal = (overrides: Partial<GooglePersonalConnection> = {}): GooglePersonalConnection =>
  personalConnection({ state: "active", features: ["calendar"], connectedAt: "2026-11-03T08:00:00Z", ...overrides });

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
  addressPattern: "given.family",
  orgUnitPath: "/",
  passLifetimeDays: 7,
  passDelivery: "hr",
  leaverRemoveLicences: true,
  leaverRemoveGroups: "managed",
  leaverSignOut: true,
  skus: [],
  personalConnection: personalSwitch(),
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

export const sku = (overrides: Partial<GoogleSku> = {}): GoogleSku => ({
  productId: "Google-Apps",
  skuId: "1010020027",
  value: "Google-Apps/1010020027",
  name: "Google Workspace Business Starter",
  assigned: 12,
  ...overrides,
});

/** A user the module made for a person in Staff, active, with one licence. */
export const googleAccount = (overrides: Partial<GoogleAccount> = {}): GoogleAccount => ({
  userId: "100000000000000000001",
  employeeNumber: "1001",
  primaryEmail: "kari.nordmann@contoso.no",
  state: "enabled",
  createdByUs: true,
  licences: ["Google-Apps/1010020027"],
  reported: [],
  passWaiting: false,
  lastSyncedAt: "2026-11-03T08:00:00Z",
  createdAt: "2026-11-01T08:00:00Z",
  ...overrides,
});

/** A group the module made for a Staff group. */
export const googleGroup = (overrides: Partial<GoogleGroup> = {}): GoogleGroup => ({
  groupId: "03abc123def456",
  staffGroupId: "42",
  email: "engineering@contoso.no",
  createdByUs: true,
  createdAt: "2026-11-01T08:00:00Z",
  ...overrides,
});

export interface GoogleServer {
  connection?: GoogleConnection;
  /** Whether the caller holds the Owner role; the session answers it. True by default. */
  owner?: boolean;
  /** What `POST /connection/test` answers; connected with every scope by default. */
  test?: GoogleConnectionTest;
  events?: GoogleEvent[];
  accounts?: GoogleAccount[];
  groups?: GoogleGroup[];
  /** What `GET /connection/skus` reads from Google, kept on the connection; the connection's own by default. */
  skus?: GoogleSku[];
  /** What `GET /connection/me` answers; offered and not connected by default. */
  personal?: GooglePersonalConnection;
  /** What `POST /connect` answers: Google's address by default, or a refusal. */
  connect?: Response;
  /** What `POST /complete` answers: an active connection returning to the Connections page by default, or a refusal. */
  complete?: GooglePersonalComplete | Response;
  /** Answers a request instead of the fake's own; undefined falls through to it. */
  handle?: (method: string, path: string, body: unknown) => Response | undefined;
}

export type GoogleStub = StubbedFetch & {
  state: {
    connection: GoogleConnection;
    accounts: GoogleAccount[];
    groups: GoogleGroup[];
    personal: GooglePersonalConnection;
  };
};

const googleProblem = (status: number, code: string) => problemResponse(status, `refused: ${code}`, { code });

/**
 * A fake of the module's API, kept as a connection row the writes change:
 * If-Match on `PUT /connection` and `PUT /connection/settings` (412
 * `version_mismatch`), a key parsed for its four clear fields (400 on `json`,
 * 422 `client_mismatch`), revocation, the web client, the test written back,
 * the licences read, and the accounts and groups with their confirmation
 * (409 `adoption_confirmed` for an account with nothing to confirm).
 */
export const stubGoogleApi = (server: GoogleServer = {}): GoogleStub => {
  const state = {
    connection: server.connection ?? unconfigured(),
    accounts: server.accounts ?? [],
    groups: server.groups ?? [],
    personal: server.personal ?? personalConnection(),
  };
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
    if (method === "PUT" && path === "/api/v1/google/connection/settings") {
      const supplied = new Headers(init?.headers).get("If-Match");
      if (Number(supplied?.replaceAll('"', "")) !== c.version) return googleProblem(412, "version_mismatch");
      const { addressDomain, ...rest } = body as GoogleSettingsRequest;
      state.connection = {
        ...c,
        ...rest,
        addressDomain: addressDomain === undefined ? c.addressDomain : addressDomain || undefined,
        version: c.version + 1,
      };
      return jsonResponse(200, state.connection);
    }
    if (method === "GET" && path === "/api/v1/google/connection/skus") {
      const skus = server.skus ?? c.skus;
      state.connection = { ...c, skus };
      return jsonResponse(200, skus);
    }
    if (method === "GET" && path === "/api/v1/google/connection/me") return jsonResponse(200, state.personal);
    if (method === "DELETE" && path === "/api/v1/google/connection/me") {
      state.personal = { ...state.personal, state: "not_connected", features: [], connectedAt: undefined };
      return new Response(null, { status: 204 });
    }
    if (method === "POST" && path === "/api/v1/google/connect")
      return server.connect?.clone() ?? jsonResponse(200, { authorizeUrl: AUTHORIZE_URL });
    if (method === "POST" && path === "/api/v1/google/complete") {
      if (server.complete instanceof Response) return server.complete.clone();
      const answer = server.complete ?? { connection: activePersonal(), returnTo: "/settings/connections" };
      state.personal = answer.connection;
      return jsonResponse(200, answer);
    }
    if (method === "PUT" && path === "/api/v1/google/connection/personal") {
      const { enabled } = body as { enabled: boolean };
      state.connection = {
        ...c,
        personalConnection: enabled
          ? { ...c.personalConnection, enabled, status: "ready" }
          : { ...c.personalConnection, enabled, status: "off", connected: 0, reconnect: 0 },
      };
      return jsonResponse(200, state.connection);
    }
    if (method === "GET" && path === "/api/v1/google/accounts") return jsonResponse(200, state.accounts);
    if (method === "GET" && path === "/api/v1/google/groups") return jsonResponse(200, state.groups);
    const confirmAccount = method === "POST" && path.match(/^\/api\/v1\/google\/accounts\/([^/]+)\/confirm$/);
    if (confirmAccount) {
      const id = decodeURIComponent(confirmAccount[1]);
      const target = state.accounts.find((one) => one.userId === id);
      if (!target) return new Response(null, { status: 404 });
      if (target.createdByUs || target.adoptionConfirmedAt) return googleProblem(409, "adoption_confirmed");
      const confirmed: GoogleAccount = {
        ...target,
        adoptionConfirmedAt: "2026-11-04T08:00:00Z",
        reported: target.reported.filter((code) => code !== "adoption_unconfirmed"),
      };
      state.accounts = state.accounts.map((one) => (one.userId === id ? confirmed : one));
      return jsonResponse(200, confirmed);
    }
    const confirmGroup = method === "POST" && path.match(/^\/api\/v1\/google\/groups\/([^/]+)\/confirm$/);
    if (confirmGroup) {
      const id = decodeURIComponent(confirmGroup[1]);
      const target = state.groups.find((one) => one.groupId === id);
      if (!target) return new Response(null, { status: 404 });
      const confirmed: GoogleGroup =
        target.createdByUs || target.adoptionConfirmedAt
          ? target
          : { ...target, adoptionConfirmedAt: "2026-11-04T08:00:00Z" };
      state.groups = state.groups.map((one) => (one.groupId === id ? confirmed : one));
      return jsonResponse(200, confirmed);
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
