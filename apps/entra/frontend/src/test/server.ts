import type {
  EntraAccount,
  EntraConnection,
  EntraConnectionTest,
  EntraCredential,
  EntraEvent,
  EntraGroup,
  EntraSettings,
  EntraSku,
} from "../api/connection";
import { requiredPermissions } from "../api/connection";
import type { EntraDirectoryMatches, EntraDirectoryUser } from "../api/directory";
import { DIRECTORY_SEARCH_LIMIT } from "../api/directory";
import type { EntraOutlookComplete, EntraOutlookConnection } from "../api/outlook";
import { jsonResponse, problemResponse } from "./api";
import { type StubbedFetch, stubFetch, TEST_SESSION } from "./fetch";

export const TENANT_ID = "11111111-1111-1111-1111-111111111111";
export const CLIENT_ID = "22222222-2222-2222-2222-222222222222";

export const defaultSettings = (): EntraSettings => ({
  upnPattern: "given.family",
  defaultUsageLocation: "NO",
  leaverStripLicences: true,
  leaverRemoveGroups: "all",
  leaverRevokeSessions: true,
  leaverWriteLeaveDate: true,
  writeHireDate: true,
  passLifetimeMinutes: 480,
  passUsableOnce: false,
  passDelivery: "hr",
});

/** The connection as `GET /connection` answers it before anything is set. */
export const unconfigured = (): EntraConnection => ({
  status: "unconfigured",
  verifiedDomains: [],
  permissionsPresent: [],
  permissionsMissing: [...requiredPermissions],
  permissionsUnexpected: [],
  settings: defaultSettings(),
  credentials: [],
  accounts: { count: 0, enabled: 0, disabled: 0, deleted: 0 },
  groups: { count: 0 },
  outlookConnect: {
    enabled: false,
    status: "off",
    connected: 0,
    reconnect: 0,
    redirectUri: "https://vantigo.example/api/v1/entra/outlook/callback",
    notifications: { status: "not_configured", subscriptions: 0 },
  },
  mailChannels: { enabled: false, status: "off" },
  version: 1,
});

/** Microsoft's authorization endpoint, as `POST /outlook/connect` answers it. */
export const AUTHORIZE_URL = `https://login.microsoftonline.com/${TENANT_ID}/oauth2/v2.0/authorize?client_id=${CLIENT_ID}`;

/** The caller's Outlook connection: offered, Vantigo knowing them as kari@contoso.no, not connected. */
export const outlookConnection = (overrides: Partial<EntraOutlookConnection> = {}): EntraOutlookConnection => ({
  offered: true,
  state: "not_connected",
  account: "kari@contoso.no",
  scopes: [],
  features: [],
  ...overrides,
});

/** The caller's Outlook connected for the calendar since 2026-10-29. */
export const activeOutlook = (overrides: Partial<EntraOutlookConnection> = {}): EntraOutlookConnection =>
  outlookConnection({
    state: "active",
    connectedAt: "2026-10-29T09:00:00Z",
    scopes: ["calendars.readwrite", "offline_access", "openid", "profile"],
    features: ["calendar"],
    ...overrides,
  });

export const credential = (overrides: Partial<EntraCredential> = {}): EntraCredential => ({
  id: 1,
  kind: "client_secret",
  label: "Vantigo 2026",
  createdAt: "2026-10-01T08:00:00Z",
  expiresAt: "2028-10-01T08:00:00Z",
  lastUsedAt: "2026-10-19T09:00:00Z",
  inUse: true,
  ...overrides,
});

/** Every step done: registered, a secret in use, tested with every permission, the UPN domain set. */
export const connected = (overrides: Partial<EntraConnection> = {}): EntraConnection => ({
  status: "connected",
  tenantId: TENANT_ID,
  clientId: CLIENT_ID,
  organisationName: "Contoso AS",
  verifiedDomains: ["contoso.no", "contoso.onmicrosoft.com"],
  permissionsPresent: [...requiredPermissions],
  permissionsMissing: [],
  permissionsUnexpected: [],
  lastCheckedAt: "2026-10-19T10:00:00Z",
  settings: { ...defaultSettings(), upnDomain: "contoso.no" },
  credentials: [credential()],
  accounts: { count: 3, enabled: 2, disabled: 1, deleted: 0 },
  groups: { count: 0 },
  outlookConnect: {
    enabled: false,
    status: "off",
    connected: 0,
    reconnect: 0,
    redirectUri: "https://vantigo.example/api/v1/entra/outlook/callback",
    notifications: { status: "not_configured", subscriptions: 0 },
  },
  mailChannels: { enabled: false, status: "off" },
  version: 4,
  ...overrides,
});

export interface EntraServer {
  connection?: EntraConnection;
  /** Whether the caller holds the Owner role; the session answers it. True by default. */
  owner?: boolean;
  /** What `POST /connection/test` answers; connected with every permission by default. */
  test?: EntraConnectionTest;
  skus?: EntraSku[] | Response;
  events?: EntraEvent[];
  accounts?: EntraAccount[];
  /** What `GET /groups` answers: the module's groups, deleted ones included. None by default. */
  groups?: EntraGroup[];
  /** The tenant's accounts `GET /directory/users` searches (a case-insensitive substring of the name, mail or principal name), or what it answers instead. */
  directory?: EntraDirectoryUser[] | Response;
  /** What `GET /directory/matches` answers; no proposal by default. */
  matches?: EntraDirectoryMatches | Response;
  /** The caller's own Outlook connection, as `GET /outlook/connection` answers it; offered and not connected by default. */
  outlook?: EntraOutlookConnection;
  /** What `POST /outlook/connect` answers; Microsoft's authorization URL by default. */
  connect?: Response;
  /** What `POST /outlook/complete` answers; the connection made active, returning to the Connections page, by default. */
  complete?: EntraOutlookComplete | Response;
  /** Answers a request instead of the fake's own; undefined falls through to it. */
  handle?: (method: string, path: string, body: unknown) => Response | undefined;
}

export interface EntraState {
  connection: EntraConnection;
  outlook: EntraOutlookConnection;
  accounts: EntraAccount[];
  groups: EntraGroup[];
}

export type EntraStub = StubbedFetch & { state: EntraState };

const entraProblem = (status: number, code: string, extra: Record<string, unknown> = {}) =>
  problemResponse(status, `refused: ${code}`, { code, ...extra });

const guid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/**
 * A fake of the module's API, kept as a connection row the writes change:
 * If-Match on both PUTs (412 `version_mismatch`), `connection_in_use` while
 * accounts exist, `credential_in_use` for the only valid secret, the test
 * written back, field errors in the `EntraProblem` shape, an adopted
 * account's confirmation (404 unknown, 409 `adoption_confirmed`), and an
 * adopted group's (404 unknown, idempotent).
 */
export const stubEntraApi = (server: EntraServer = {}): EntraStub => {
  const state: EntraState = {
    connection: server.connection ?? unconfigured(),
    outlook: server.outlook ?? outlookConnection(),
    accounts: server.accounts ?? [],
    groups: server.groups ?? [],
  };
  let nextCredential = 100;
  const owner = server.owner ?? true;
  const session = { user: { ...TEST_SESSION.user, roles: owner ? ["user", "Owner"] : ["user"] } };

  const stale = (init: RequestInit | undefined) => {
    const supplied = new Headers(init?.headers).get("If-Match");
    return Number(supplied?.replaceAll('"', "")) === state.connection.version
      ? undefined
      : entraProblem(412, "version_mismatch");
  };

  const route = (method: string, path: string, init: RequestInit | undefined, body: unknown): Response => {
    const c = state.connection;
    if (method === "GET" && path === "/api/v1/entra/connection") return jsonResponse(200, c);
    if (method === "GET" && path === "/api/v1/entra/outlook/connection") return jsonResponse(200, state.outlook);
    if (method === "POST" && path === "/api/v1/entra/outlook/connect") {
      if (server.connect) return server.connect.clone();
      return jsonResponse(200, { authorizeUrl: AUTHORIZE_URL });
    }
    if (method === "POST" && path === "/api/v1/entra/outlook/complete") {
      if (server.complete instanceof Response) return server.complete.clone();
      const answer = server.complete ?? {
        connection: activeOutlook(),
        returnTo: "/settings/connections",
      };
      state.outlook = answer.connection;
      return jsonResponse(200, answer);
    }
    if (method === "DELETE" && path === "/api/v1/entra/outlook/connection") {
      state.outlook = { ...state.outlook, state: "not_connected", scopes: [], features: [], connectedAt: undefined };
      return new Response(null, { status: 204 });
    }
    if (method === "GET" && path.startsWith("/api/v1/entra/events")) return jsonResponse(200, server.events ?? []);
    if (method === "GET" && path === "/api/v1/entra/accounts") return jsonResponse(200, state.accounts);
    if (method === "GET" && path === "/api/v1/entra/groups") return jsonResponse(200, state.groups);
    if (method === "GET" && path.startsWith("/api/v1/entra/directory/users?")) {
      if (server.directory instanceof Response) return server.directory.clone();
      const q = (new URLSearchParams(path.split("?")[1]).get("q") ?? "").trim().toLowerCase();
      if (q.length < 2 || q.length > 100)
        return problemResponse(400, "Validation failed", {
          code: "invalid_request",
          errors: { q: ["The search must be 2 to 100 characters."] },
        });
      const hits = (server.directory ?? [])
        .filter((one) =>
          [one.displayName, one.mail ?? "", one.userPrincipalName].some((value) => value.toLowerCase().includes(q)),
        )
        .slice(0, DIRECTORY_SEARCH_LIMIT);
      return jsonResponse(200, { users: hits });
    }
    if (method === "GET" && path === "/api/v1/entra/directory/matches") {
      if (server.matches instanceof Response) return server.matches.clone();
      return jsonResponse(200, server.matches ?? { matches: [], candidates: 0, ambiguous: 0, truncated: false });
    }
    const confirmGroup = method === "POST" && path.match(/^\/api\/v1\/entra\/groups\/([^/]+)\/confirm$/);
    if (confirmGroup) {
      const target = state.groups.find((one) => one.objectId === confirmGroup[1]);
      if (!target) return new Response(null, { status: 404 });
      if (target.createdByUs || target.adoptionConfirmedAt) return jsonResponse(200, target);
      const confirmed = { ...target, adoptionConfirmedAt: "2026-10-20T12:00:00Z" };
      state.groups = state.groups.map((one) => (one.objectId === confirmed.objectId ? confirmed : one));
      return jsonResponse(200, confirmed);
    }
    const confirm = method === "POST" && path.match(/^\/api\/v1\/entra\/accounts\/([^/]+)\/confirm$/);
    if (confirm) {
      const target = state.accounts.find((one) => one.objectId === confirm[1]);
      if (!target) return new Response(null, { status: 404 });
      if (target.createdByUs || target.adoptionConfirmed) return entraProblem(409, "adoption_confirmed");
      const confirmed = {
        ...target,
        adoptionConfirmed: true,
        reported: target.reported.filter((code) => code !== "adoption_unconfirmed"),
      };
      state.accounts = state.accounts.map((one) => (one.objectId === confirmed.objectId ? confirmed : one));
      return jsonResponse(200, confirmed);
    }
    if (method === "GET" && path === "/api/v1/entra/connection/skus") {
      if (server.skus instanceof Response) return server.skus;
      return jsonResponse(200, server.skus ?? []);
    }
    if (method === "PUT" && path === "/api/v1/entra/connection") {
      const refused = stale(init);
      if (refused) return refused;
      const request = body as { tenantId: string; clientId: string; confirmChange?: boolean };
      const errors: Record<string, string[]> = {};
      if (!guid.test(request.tenantId)) errors.tenantId = ["The tenant id must be a GUID."];
      if (!guid.test(request.clientId)) errors.clientId = ["The client id must be a GUID."];
      if (Object.keys(errors).length > 0) return problemResponse(400, "Validation failed", { errors });
      const changes = request.tenantId !== c.tenantId || request.clientId !== c.clientId;
      if (changes && c.accounts.count > 0 && !request.confirmChange) return entraProblem(409, "connection_in_use");
      state.connection = {
        ...c,
        tenantId: request.tenantId,
        clientId: request.clientId,
        ...(changes ? { status: "unconfigured", lastCheckedAt: undefined } : {}),
        version: c.version + 1,
      };
      return jsonResponse(200, state.connection);
    }
    if (method === "POST" && path === "/api/v1/entra/connection/credentials") {
      const request = body as { label?: string; secret: string; expiresAt: string };
      const added: EntraCredential = {
        id: nextCredential++,
        kind: "client_secret",
        ...(request.label ? { label: request.label } : {}),
        createdAt: "2026-10-20T08:00:00Z",
        expiresAt: request.expiresAt,
        inUse: true,
      };
      state.connection = {
        ...c,
        credentials: [added, ...c.credentials.map((one) => ({ ...one, inUse: false }))],
      };
      return jsonResponse(201, added);
    }
    const revoke = method === "DELETE" && path.match(/^\/api\/v1\/entra\/connection\/credentials\/(\d+)$/);
    if (revoke) {
      const id = Number(revoke[1]);
      const target = c.credentials.find((one) => one.id === id);
      if (!target) return new Response(null, { status: 404 });
      const valid = c.credentials.filter((one) => !one.revokedAt);
      if (valid.length === 1 && valid[0].id === id && c.accounts.count > 0)
        return entraProblem(409, "credential_in_use");
      const revoked = { ...target, inUse: false, revokedAt: "2026-10-20T09:00:00Z" };
      state.connection = { ...c, credentials: c.credentials.map((one) => (one.id === id ? revoked : one)) };
      return jsonResponse(200, revoked);
    }
    if (method === "POST" && path === "/api/v1/entra/connection/test") {
      const result: EntraConnectionTest = server.test ?? {
        status: "connected",
        organisationName: "Contoso AS",
        verifiedDomains: ["contoso.no"],
        permissionsPresent: [...requiredPermissions],
        permissionsMissing: [],
        permissionsUnexpected: [],
        roleKnown: false,
      };
      state.connection = {
        ...c,
        status: result.status,
        statusReason: result.reason,
        organisationName: result.organisationName,
        verifiedDomains: result.verifiedDomains,
        permissionsPresent: result.permissionsPresent,
        permissionsMissing: result.permissionsMissing,
        lastCheckedAt: "2026-10-20T10:00:00Z",
      };
      return jsonResponse(200, result);
    }
    if (method === "PUT" && path === "/api/v1/entra/connection/settings") {
      const refused = stale(init);
      if (refused) return refused;
      const { outlookConnect, ...request } = body as Partial<EntraSettings> & { outlookConnect?: boolean };
      if (request.upnDomain && c.verifiedDomains.length > 0 && !c.verifiedDomains.includes(request.upnDomain))
        return problemResponse(400, "Validation failed", {
          errors: { upnDomain: ["The domain must be one of the tenant's verified domains."] },
        });
      const sync = outlookConnect ?? c.outlookConnect.enabled;
      state.connection = {
        ...c,
        settings: { ...c.settings, ...request },
        outlookConnect: {
          ...(!sync
            ? { enabled: false, status: "off" as const }
            : c.status !== "connected"
              ? { enabled: true, status: "unavailable" as const, ...(c.statusReason ? { reason: c.statusReason } : {}) }
              : { enabled: true, status: "untested" as const }),
          connected: c.outlookConnect.connected,
          reconnect: c.outlookConnect.reconnect,
          redirectUri: c.outlookConnect.redirectUri,
          notifications: c.outlookConnect.notifications,
        },
        version: c.version + 1,
      };
      return jsonResponse(200, state.connection);
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
  ) as EntraStub;
  stub.state = state;
  return stub;
};

/** One group the module keeps, a security group it made, present, its members reconciled. */
export const entraGroup = (overrides: Partial<EntraGroup> = {}): EntraGroup => ({
  objectId: "33333333-3333-4333-8333-333333333333",
  groupId: "1001",
  key: "engineering",
  displayName: "Engineering",
  mailNickname: "engineering",
  type: "security",
  team: false,
  state: "present",
  createdByUs: true,
  lastMembersAt: "2026-10-20T06:00:00Z",
  createdAt: "2026-10-19T08:00:00Z",
  ...overrides,
});

/** One account in the tenant, enabled and held by nobody. */
export const directoryUser = (overrides: Partial<EntraDirectoryUser> = {}): EntraDirectoryUser => ({
  objectId: "44444444-4444-4444-8444-444444444444",
  displayName: "Kari Nordmann",
  mail: "kari@contoso.no",
  userPrincipalName: "kari@contoso.no",
  accountEnabled: true,
  ...overrides,
});
