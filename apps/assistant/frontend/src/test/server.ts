import type {
  AssistantAgentRef,
  AssistantAvailability,
  AssistantBulkJob,
  AssistantBulkPlan,
  AssistantConversation,
  AssistantConversationDetail,
  AssistantMe,
  AssistantMessage,
  AssistantPendingAction,
  AssistantPermissions,
  AssistantPerson,
  AssistantSettings,
  AssistantUsage,
} from "../api/types";
import { jsonResponse, problemResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";

export const conversation = (overrides: Partial<AssistantConversation> = {}): AssistantConversation => ({
  id: "c1000000-0000-4000-8000-000000000001",
  title: "How do I add a metering point?",
  originApp: "energy",
  originRoute: "/energy/metering-points",
  pinned: false,
  archived: false,
  running: false,
  createdAt: "2026-11-01T08:00:00Z",
  lastActivityAt: "2026-11-01T08:05:00Z",
  ...overrides,
});

export const userMessage = (text: string, overrides: Partial<AssistantMessage> = {}): AssistantMessage => ({
  id: `m-user-${text.length}`,
  role: "user",
  parts: [{ type: "text", text }],
  createdAt: "2026-11-01T08:00:00Z",
  ...overrides,
});

export const assistantMessage = (text: string, overrides: Partial<AssistantMessage> = {}): AssistantMessage => ({
  id: `m-assistant-${text.length}`,
  role: "assistant",
  parts: [{ type: "text", text }],
  createdAt: "2026-11-01T08:00:05Z",
  ...overrides,
});

export const detailOf = (
  summary: AssistantConversation,
  messages: AssistantMessage[] = [],
  extra: Partial<AssistantConversationDetail> = {},
): AssistantConversationDetail => ({ conversation: summary, messages, actions: [], pendingActions: [], ...extra });

/** A change the assistant proposed and nobody has decided. */
export const card = (overrides: Partial<AssistantPendingAction> = {}): AssistantPendingAction => ({
  id: "p1000000-0000-4000-8000-000000000001",
  runId: "run-1",
  conversationId: "c1",
  operationId: "putCustomersById",
  method: "PUT",
  status: "pending",
  summary: "Update a customer",
  explain: "Set Ola Nordmann's payment terms to 30 days",
  fields: [{ name: "paymentTermsDays", current: "14", next: "30" }],
  target: { label: "Ola Nordmann", route: "/customers/42" },
  createdAt: "2026-11-02T08:00:00Z",
  expiresAt: "2026-11-03T08:00:00Z",
  ...overrides,
});

/** A bulk plan as its card shows it: ten projects found, nothing started. */
export const bulkPlan = (overrides: Partial<AssistantBulkPlan> = {}): AssistantBulkPlan => ({
  jobId: "j1000000-0000-4000-8000-000000000001",
  state: "previewed",
  total: 130,
  processed: 0,
  succeeded: 0,
  failed: 0,
  unknown: 0,
  skipped: 0,
  canarySize: 5,
  canary: [],
  sample: [
    {
      label: "Bulk project 000",
      route: "/projects/1",
      fields: [{ name: "description", current: "", next: "Reviewed in bulk" }],
    },
    {
      label: "Bulk project 001",
      route: "/projects/2",
      fields: [{ name: "description", current: "Old note", next: "Reviewed in bulk" }],
    },
  ],
  excluded: { count: 0, reasons: [] },
  ...overrides,
});

/** A bulk job as the polled endpoint answers it. */
export const bulkJob = (overrides: Partial<AssistantBulkJob> = {}): AssistantBulkJob => ({
  ...bulkPlan(),
  failures: [],
  ...overrides,
});

/** The card of a bulk plan, with its job where it stands. */
export const bulkCard = (
  plan: Partial<AssistantBulkPlan> = {},
  overrides: Partial<AssistantPendingAction> = {},
): AssistantPendingAction =>
  card({
    id: "p2000000-0000-4000-8000-000000000002",
    operationId: "putProjectsById",
    kind: "bulk",
    explain: "Add the note to every project",
    summary: "Update a project",
    fields: [{ name: "description", next: "Reviewed in bulk" }],
    target: undefined,
    bulk: bulkPlan(plan),
    status: plan.state && plan.state !== "previewed" ? "confirmed" : "pending",
    ...overrides,
  });

/** A part of an assistant message that stands for a card. */
export const cardPart = (actionId: string) => ({ type: "confirmation" as const, actionId });

export const settings = (overrides: Partial<AssistantSettings> = {}): AssistantSettings => ({
  mode: "all",
  retentionMonths: 12,
  sensitiveDefault: "off",
  version: 3,
  updatedAt: "2026-11-01T08:00:00Z",
  configured: true,
  local: true,
  provider: "openai-compatible",
  models: ["llama3.1"],
  dailyTokenBudget: null,
  ...overrides,
});

export const permissions = (overrides: Partial<AssistantPermissions> = {}): AssistantPermissions => ({
  version: 3,
  groups: [
    {
      module: "customers",
      category: "Customers",
      permissions: [
        {
          key: "customers:view",
          display: "View customers",
          description: "See the customer list.",
          sensitive: false,
          defaultLevel: "change",
          effectiveLevel: "change",
        },
        {
          key: "customers:contacts-view",
          display: "View contacts",
          sensitive: true,
          defaultLevel: "off",
          effectiveLevel: "off",
        },
      ],
    },
    {
      module: "invoices",
      category: "Invoices",
      permissions: [
        {
          key: "invoices:access",
          display: "Read invoices",
          sensitive: false,
          defaultLevel: "change",
          override: "read",
          effectiveLevel: "read",
        },
      ],
    },
  ],
  ...overrides,
});

export interface AssistantServer {
  /** The virtual employees `GET /agents` lists. */
  agents?: AssistantAgentRef[];
  availability?: AssistantAvailability;
  me?: AssistantMe;
  conversations?: AssistantConversation[];
  details?: Record<string, AssistantConversationDetail>;
  settings?: AssistantSettings;
  permissions?: AssistantPermissions;
  person?: AssistantPerson;
  usage?: AssistantUsage;
  /** What `POST /pending-actions/{id}/confirm|cancel` answers. */
  decide?: (decision: "confirm" | "cancel", id: string) => Response;
  /** What `POST /conversations/{id}/messages` answers. */
  stream?: (body: { text: string; context?: unknown }, id: string) => Response;
  /** Answers a request instead of the fake's own; undefined falls through. */
  handle?: (method: string, path: string, body: unknown, init: RequestInit | undefined) => Response | undefined;
}

export type AssistantStub = StubbedFetch & {
  state: { settings: AssistantSettings; permissions: AssistantPermissions };
};

/**
 * A fake of the assistant's API: availability, the person's last conversation,
 * conversations with their details, the Owner's settings and permissions with
 * If-Match (409 `version_conflict` when stale), and the person's switch.
 */
export const stubAssistantApi = (server: AssistantServer = {}): AssistantStub => {
  const state = {
    settings: server.settings ?? settings(),
    permissions: server.permissions ?? permissions(),
    person: server.person ?? { userId: "user-1", enabled: false, version: 0 },
  };
  const conversations = server.conversations ?? [];
  const details = server.details ?? {};

  const stale = (init: RequestInit | undefined, version: number) =>
    Number(new Headers(init?.headers).get("If-Match")?.replaceAll('"', "")) === version
      ? undefined
      : problemResponse(409, "stale", { code: "version_conflict" });

  const route = (method: string, path: string, init: RequestInit | undefined, body: unknown): Response => {
    const bare = path.split("?")[0];
    if (method === "GET" && bare === "/api/v1/assistant/availability")
      return jsonResponse(200, server.availability ?? { available: true, local: true });
    if (method === "GET" && bare === "/api/v1/assistant/me")
      return jsonResponse(200, server.me ?? { lastConversationId: null });
    if (method === "PUT" && bare === "/api/v1/assistant/me")
      return jsonResponse(200, {
        lastConversationId: (body as { lastConversationId: string | null }).lastConversationId,
      });
    if (method === "GET" && bare === "/api/v1/assistant/conversations") {
      const q = new URLSearchParams(path.split("?")[1] ?? "").get("q")?.toLowerCase();
      const items = conversations.filter((one) => !q || one.title.toLowerCase().includes(q));
      return jsonResponse(200, { items, nextCursor: null });
    }
    const one = bare.match(/^\/api\/v1\/assistant\/conversations\/([^/]+)$/);
    if (one && method === "GET") {
      const detail =
        details[one[1]] ?? (one[1] === "c-new" ? detailOf(conversation({ id: "c-new", title: "" })) : undefined);
      return detail ? jsonResponse(200, detail) : problemResponse(404, "not found", { code: "not_found" });
    }
    if (method === "GET" && bare === "/api/v1/assistant/agents")
      return jsonResponse(200, { items: server.agents ?? [] });
    if (method === "POST" && bare === "/api/v1/assistant/conversations") {
      const asked = body as { origin: { app: string }; agentId?: string };
      const agent = server.agents?.find((one) => one.id === asked.agentId);
      return jsonResponse(201, conversation({ id: "c-new", title: "", originApp: asked.origin.app, agent }));
    }
    const messages = bare.match(/^\/api\/v1\/assistant\/conversations\/([^/]+)\/messages$/);
    if (messages && method === "POST")
      return server.stream?.(body as { text: string }, messages[1]) ?? new Response(null, { status: 500 });
    if (method === "POST" && /\/runs\/[^/]+\/stop$/.test(bare)) return new Response(null, { status: 204 });
    const decision = bare.match(/^\/api\/v1\/assistant\/pending-actions\/([^/]+)\/(confirm|cancel)$/);
    if (decision && method === "POST")
      return server.decide?.(decision[2] as "confirm" | "cancel", decision[1]) ?? new Response(null, { status: 500 });
    if (method === "GET" && bare === "/api/v1/assistant/usage")
      return jsonResponse(200, server.usage ?? { from: "2026-10-04", to: "2026-11-02", rows: [] });
    if (one && method === "PATCH")
      return jsonResponse(200, {
        ...(conversations.find((c) => c.id === one[1]) ?? conversation()),
        ...(body as object),
      });
    if (one && method === "DELETE") return new Response(null, { status: 204 });

    if (method === "GET" && bare === "/api/v1/assistant/settings") return jsonResponse(200, state.settings);
    if (method === "PUT" && bare === "/api/v1/assistant/settings") {
      const refused = stale(init, state.settings.version);
      if (refused) return refused;
      state.settings = { ...state.settings, ...(body as object), version: state.settings.version + 1 };
      return jsonResponse(200, state.settings);
    }
    if (method === "GET" && bare === "/api/v1/assistant/settings/permissions")
      return jsonResponse(200, state.permissions);
    const permission = bare.match(/^\/api\/v1\/assistant\/settings\/permissions\/(.+)$/);
    if (method === "PUT" && permission) {
      const refused = stale(init, state.settings.version);
      if (refused) return refused;
      const key = decodeURIComponent(permission[1]);
      const level = (body as { level: "off" | "read" | "change" | null }).level;
      const found = state.permissions.groups.flatMap((group) => group.permissions).find((p) => p.key === key);
      if (!found) return problemResponse(404, "unknown", { code: "not_found" });
      let saved = found;
      saved = { ...saved, override: level ?? undefined, effectiveLevel: level ?? saved.defaultLevel };
      state.settings = { ...state.settings, version: state.settings.version + 1 };
      state.permissions = {
        version: state.settings.version,
        groups: state.permissions.groups.map((group) => ({
          ...group,
          permissions: group.permissions.map((p) => (p.key === key ? saved : p)),
        })),
      };
      return jsonResponse(200, { version: state.settings.version, permission: saved });
    }
    if (method === "GET" && bare.startsWith("/api/v1/assistant/people/")) return jsonResponse(200, state.person);
    if (method === "PUT" && bare.startsWith("/api/v1/assistant/people/")) {
      const refused = stale(init, state.person.version);
      if (refused) return refused;
      state.person = {
        ...state.person,
        enabled: (body as { enabled: boolean }).enabled,
        version: state.person.version + 1,
      };
      return jsonResponse(200, state.person);
    }
    return new Response(null, { status: 404 });
  };

  const stub = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const method = init?.method ?? "GET";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    return Promise.resolve(server.handle?.(method, path, body, init) ?? route(method, path, init, body));
  }) as AssistantStub;
  Object.defineProperty(stub, "state", { get: () => state });
  return stub;
};

const encoder = new TextEncoder();

/** One SSE event as the server writes it. */
export const sseEvent = (event: string, data: unknown) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;

/** A whole stream at once. */
export const sseResponse = (events: [string, unknown][]) =>
  new Response(
    new ReadableStream({
      start(controller) {
        for (const [event, data] of events) controller.enqueue(encoder.encode(sseEvent(event, data)));
        controller.close();
      },
    }),
    { status: 200, headers: { "Content-Type": "text/event-stream" } },
  );

/** A stream the test feeds by hand, to look at the screen between chunks. */
export const controlledStream = () => {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const response = new Response(
    new ReadableStream({
      start(c) {
        controller = c;
      },
    }),
    { status: 200, headers: { "Content-Type": "text/event-stream" } },
  );
  return {
    response,
    push: (event: string, data: unknown) => controller.enqueue(encoder.encode(sseEvent(event, data))),
    pushRaw: (text: string) => controller.enqueue(encoder.encode(text)),
    close: () => controller.close(),
  };
};
