import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createBrowserHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@vantigo/frontend-shell";
import { stationOperationList } from "@vantigo/manufacturing-ui";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AccountLanguagePreference } from "../components/account-language-preference";
import { publicPaths, sessionlessPaths } from "../lib/public-paths";
import { routeTree } from "../routeTree.gen";

const TOKEN = "operator-token-held-in-memory";
const STATION = "7d4a3f40-0b54-4b1f-9a4e-2a1d1f6c0a01";
const ADA = "0d1a5b8e-6f61-4c3a-8d2b-5b3f0b2a9c11";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const self = (operators: unknown[] = []) => ({
  id: STATION,
  name: "Press line 1",
  idleMinutes: 5,
  requirePinWithBadge: true,
  timeZone: "Europe/Oslo",
  operators,
});

const workOrder = {
  id: 40,
  orderId: 5,
  order: {
    id: 5,
    number: "MO-12",
    variant: { id: 1, sku: "TABLE", productName: "Oak table", unit: "pcs" },
    quantity: "10",
    produced: "0",
    remaining: "10",
    dueDate: "2026-11-20",
    priority: "normal",
    status: "confirmed",
    reservationStatus: "reserved",
  },
  sequence: 10,
  name: "Cut",
  workCenter: { id: 1, code: "SAW", name: "Saw", siteId: 1, workingState: "normal" },
  status: "ready",
  last: false,
  operators: [],
  checks: [],
};

/** Whether a request is one a station may make (manufacturing phase 2 M135). */
const allowed = (method: string, url: string) => {
  const { pathname } = new URL(url, "http://localhost");
  return stationOperationList.some(
    (operation) =>
      operation.method === method && new RegExp(`^${operation.path.replace(/\{[^}]+\}/g, "[^/]+")}$`).test(pathname),
  );
};

/** Every request the SPA makes, recorded, answered as a station's server would. */
const recordFetch = () => {
  const requests: { url: string; method: string; authorization: string | null }[] = [];
  let paired = false;
  let operators: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      requests.push({ url, method, authorization: new Headers(init?.headers).get("Authorization") });
      const path = new URL(url, "http://localhost").pathname;
      if (path === "/api/v1/identity/station" && method === "GET")
        return Promise.resolve(
          paired ? json(self(operators)) : json({ error: { code: "station_unknown", message: "Not paired." } }, 401),
        );
      if (path === "/api/v1/identity/station/pair" && method === "POST") {
        paired = true;
        return Promise.resolve(json(self()));
      }
      if (path === "/api/v1/identity/station/operators" && method === "GET")
        return Promise.resolve(
          json({
            items: [{ userId: ADA, displayName: "Ada Lovelace", initials: "AL", hasPin: true, hasBadge: false }],
          }),
        );
      if (path === "/api/v1/identity/station/operators" && method === "POST") {
        const session = {
          sessionId: "5b1f2c3d-0000-4000-8000-000000000001",
          userId: ADA,
          displayName: "Ada Lovelace",
          initials: "AL",
          startedAt: "2026-11-13T08:00:00Z",
          lastActivityAt: "2026-11-13T08:00:00Z",
        };
        operators = [session];
        return Promise.resolve(
          json({
            sessionId: session.sessionId,
            token: TOKEN,
            startedAt: session.startedAt,
            operator: { userId: ADA, displayName: "Ada Lovelace", initials: "AL" },
          }),
        );
      }
      if (path === "/api/v1/manufacturing/meta")
        return Promise.resolve(
          json({
            canManageBoms: false,
            canSeeCosts: false,
            canManageOrders: false,
            canWorkShopFloor: true,
            canManageQuality: false,
            canTraceLots: false,
            currency: "NOK",
          }),
        );
      if (path === "/api/v1/manufacturing/work-centers")
        return Promise.resolve(
          json({
            items: [
              {
                id: 1,
                code: "SAW",
                name: "Saw",
                siteId: 1,
                status: "active",
                workingState: "normal",
                capacity: 1,
                timeEfficiency: 100,
                setupMinutes: "0",
                cleanupMinutes: "0",
                createdAt: "2026-11-01T00:00:00Z",
                revision: 1,
              },
            ],
          }),
        );
      if (path === "/api/v1/manufacturing/work-centers/1/queue")
        return Promise.resolve(
          json({ workCenter: workOrder.workCenter, block: null, items: [workOrder], groupWork: [] }),
        );
      if (path === "/api/v1/manufacturing/work-orders/40")
        return Promise.resolve(
          json({ workOrder, instructions: "", components: [], openLogs: [], myOpenLog: null, block: null }),
        );
      return Promise.resolve(new Response(null, { status: 404 }));
    }),
  );
  return requests;
};

/** A WebSocket that never connects: the station's live socket, out of the test's way. */
class QuietSocket {
  readyState = 0;
  onopen = null;
  onclose = null;
  onmessage = null;
  onerror = null;
  send() {}
  close() {}
  addEventListener() {}
  removeEventListener() {}
}

const renderSpaAt = (address: string) => {
  window.history.replaceState(null, "", address);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({ routeTree, context: { queryClient }, history: createBrowserHistory() });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <I18nProvider>
          <AccountLanguagePreference />
          <ModalsProvider>
            <RouterProvider router={router} />
          </ModalsProvider>
        </I18nProvider>
      </QueryClientProvider>
    </MantineProvider>,
  );
};

afterEach(() => {
  window.history.replaceState(null, "", "/");
  window.sessionStorage.clear();
  window.localStorage.clear();
});

describe("the /station route", () => {
  it("is public and asks for no session", () => {
    expect(publicPaths.has("/station")).toBe(true);
    expect(sessionlessPaths.has("/station")).toBe(true);
  });

  it("pairs, switches an operator in and makes only requests open to stations", async () => {
    vi.stubGlobal("WebSocket", QuietSocket);
    const requests = recordFetch();
    renderSpaAt("/station");

    await userEvent.type(await screen.findByLabelText("Pairing code"), "ABCD-EFGH");
    await userEvent.click(screen.getByRole("button", { name: "Pair" }));
    await userEvent.click(await screen.findByRole("button", { name: "Switch in" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(await within(dialog).findByRole("button", { name: /Ada Lovelace/ }));
    const pinField = await vi.waitFor(() => {
      const field = dialog.querySelector<HTMLInputElement>("input");
      if (!field) throw new Error("no PIN field yet");
      return field;
    });
    await userEvent.type(pinField, "482915");
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    await userEvent.click(await screen.findByRole("button", { name: "MO-12 · Cut" }));
    expect(await screen.findByRole("button", { name: "Start" })).toBeInTheDocument();
    await new Promise((resolve) => setTimeout(resolve, 50));

    const refused = requests.filter((request) => !allowed(request.method, request.url));
    expect(refused.map((request) => `${request.method} ${request.url}`)).toEqual([]);
    for (const request of requests) {
      // The station's own endpoints go with its cookie alone; every module request as the operator.
      const expected = request.url.startsWith("/api/v1/manufacturing/") ? `Station ${TOKEN}` : null;
      expect(`${request.method} ${request.url}: ${request.authorization}`).toBe(
        `${request.method} ${request.url}: ${expected}`,
      );
    }
    expect(requests.some((request) => request.url.startsWith("/api/v1/manufacturing/work-orders/40"))).toBe(true);
    // The token is held in memory only.
    const stored = [
      ...Object.keys(localStorage).map((key) => localStorage.getItem(key) ?? ""),
      ...Object.keys(sessionStorage).map((key) => sessionStorage.getItem(key) ?? ""),
      document.cookie,
    ];
    expect(stored.some((value) => value.includes(TOKEN))).toBe(false);
  });
});
