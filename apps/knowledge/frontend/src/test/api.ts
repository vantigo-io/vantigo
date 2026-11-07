import { vi } from "vitest";
import type { KnowledgeArticle, KnowledgeTreeNode } from "../api/articles";

export const jsonResponse = (status: number, body: unknown) =>
  new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

/** A coded refusal as the Knowledge API answers it (KnowledgeProblem), with the server's English, which the page never shows. */
export const refusal = (status: number, code: string, extra: Record<string, unknown> = {}) =>
  new Response(
    JSON.stringify({ type: "about:blank", title: "Refused", status, code, detail: "The server's English.", ...extra }),
    { status, headers: { "Content-Type": "application/problem+json" } },
  );

export interface Call {
  method: string;
  url: string;
  body: unknown;
}

type Handler = (call: Call) => Response | undefined | Promise<Response | undefined>;

/**
 * A fetch fake: each request is recorded and handed to the handler, which
 * answers it; what it leaves unanswered gets the empty answers of
 * `emptyAnswers`, else a 404.
 */
export const fakeServer = (handler: Handler) => {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const call: Call = {
        method: init?.method ?? "GET",
        url: String(input),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      };
      calls.push(call);
      return (await handler(call)) ?? emptyAnswers(call) ?? new Response(null, { status: 404 });
    }),
  );
  const matching = (method: string, url: string | RegExp) =>
    calls.filter((c) => c.method === method && (typeof url === "string" ? c.url === url : url.test(c.url)));
  return {
    calls,
    sent: (method: string, url: string | RegExp) => matching(method, url).at(-1),
    count: (method: string, url: string | RegExp) => matching(method, url).length,
  };
};

/** The sidebar's reads, empty, so a test answers only what it is about. */
const emptyAnswers = (call: Call): Response | undefined => {
  if (call.method !== "GET") return undefined;
  if (call.url.startsWith("/api/v1/knowledge/tree")) return jsonResponse(200, { items: [] });
  if (call.url === "/api/v1/knowledge/favourites") return jsonResponse(200, { items: [] });
  return undefined;
};

export const ids = {
  routines: "00000000-0000-4000-8000-000000000001",
  onboarding: "00000000-0000-4000-8000-000000000002",
  notes: "00000000-0000-4000-8000-000000000003",
  shared: "00000000-0000-4000-8000-000000000004",
  kari: "00000000-0000-4000-8000-0000000000aa",
  ola: "00000000-0000-4000-8000-0000000000bb",
};

export const node = (overrides: Partial<KnowledgeTreeNode> & { id: string; title: string }): KnowledgeTreeNode => ({
  parentId: null,
  position: "a0",
  icon: null,
  hasChildren: false,
  locked: false,
  section: "workspace",
  permission: "write",
  ...overrides,
});

export const article = (overrides: Partial<KnowledgeArticle> = {}): KnowledgeArticle => ({
  id: ids.routines,
  parentId: null,
  position: "a0",
  title: "Routines",
  icon: "📘",
  content: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "Lock the door." }] }] },
  version: 3,
  locked: false,
  aiHidden: false,
  aiHiddenInherited: false,
  permission: "write",
  section: "workspace",
  path: [],
  createdBy: { userId: ids.kari, displayName: "Kari Nordmann" },
  createdAt: "2026-11-01T10:00:00Z",
  updatedBy: { userId: ids.kari, displayName: "Kari Nordmann" },
  updatedAt: "2026-11-01T10:00:00Z",
  ...overrides,
});
