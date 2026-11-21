import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ApiKey, IntegrationRequestLog } from "../../api/integrations";
import { renderRoute } from "../../test/render-route";
import { dayRows, IntegrationRequests, windowDays } from "./-api-access-requests";
import "../../i18n";

const api = vi.hoisted(() => ({ getIntegrationRequests: vi.fn(), listIntegrationConnections: vi.fn() }));

vi.mock("../../api/integrations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/integrations")>()),
  getIntegrationRequests: api.getIntegrationRequests,
}));

vi.mock("../../api/oauth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/oauth")>()),
  listIntegrationConnections: api.listIntegrationConnections,
}));

const key = (id: string, name: string): ApiKey => ({
  id,
  name,
  display: "vtg_4f2a…9XyZ",
  status: "active",
  readOnly: false,
  allowedNetworks: [],
  permissions: null,
  expiresAt: "2027-02-15T10:00:00Z",
  createdAt: "2026-11-17T10:00:00Z",
  createdBy: "me",
  revokedAt: null,
  rolledFrom: null,
  lastUsedAt: null,
  lastUsedIp: null,
});

const log: IntegrationRequestLog = {
  from: "2026-10-20T00:00:00Z",
  to: "2026-11-18T12:00:00Z",
  days: [
    { day: "2026-11-18", statusClass: 2, requests: 40 },
    { day: "2026-11-17", statusClass: 2, requests: 12 },
    { day: "2026-11-18", statusClass: 4, requests: 3 },
  ],
  operations: [
    { operationId: "getCustomers", requests: 50 },
    { operationId: "postOrders", requests: 5 },
  ],
  latest: [
    {
      at: "2026-11-18T11:59:00Z",
      keyId: "k-1",
      keyName: "Production",
      connectionId: null,
      clientName: null,
      method: "GET",
      operationId: "getCustomers",
      status: 429,
      reason: "rate_limited",
      durationMs: 2,
      clientIp: "192.0.2.10",
      traceId: "4bf92f3577b34da6a3ce929d0e0e4736",
    },
    {
      at: "2026-11-18T11:58:00Z",
      keyId: "k-gone",
      keyName: null,
      connectionId: null,
      clientName: null,
      method: "POST",
      operationId: "postOrders",
      status: 201,
      reason: null,
      durationMs: 31,
      clientIp: null,
      traceId: null,
    },
    {
      at: "2026-11-18T11:57:00Z",
      keyId: null,
      keyName: null,
      connectionId: "c-1",
      clientName: "Claude",
      method: "GET",
      operationId: "getCustomers",
      status: 200,
      reason: null,
      durationMs: 12,
      clientIp: null,
      traceId: null,
    },
  ],
};

const renderTab = async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute(
    "/workspace/api-access/i-1",
    () => <IntegrationRequests integrationId="i-1" keys={[key("k-1", "Production"), key("k-2", "Staging")]} />,
    queryClient,
  );
};

beforeEach(() => {
  api.getIntegrationRequests.mockReset();
  api.listIntegrationConnections.mockReset();
  api.listIntegrationConnections.mockResolvedValue([
    {
      id: "c-1",
      clientId: "https://claude.ai/oauth/mcp-oauth-client-metadata",
      clientKind: "metadata",
      clientName: "Claude",
      clientHost: "claude.ai",
      scopes: ["read"],
      authorizedBy: "u-1",
      authorizedByName: "Kari Owner",
      connectedAt: "2026-11-18T10:00:00Z",
      expiresAt: "2027-05-17T10:00:00Z",
      lastUsedAt: null,
      lastUsedIp: null,
      status: "active",
      inactiveReason: null,
    },
  ]);
});

describe("an integration's request log", () => {
  it("shows the days by status class, the operations called most and the latest requests", async () => {
    api.getIntegrationRequests.mockResolvedValue(log);
    await renderTab();

    const days = await screen.findByTestId("request-days");
    const rows = within(days).getAllByRole("row");
    expect(rows.map((row) => row.textContent)).toEqual([
      "Day2xx success3xx redirect4xx refused5xx server error",
      "Nov 17, 202612000",
      "Nov 18, 202640030",
    ]);
    expect(within(screen.getByTestId("request-operations")).getByText("getCustomers")).toBeTruthy();

    const latest = within(screen.getByTestId("request-latest"));
    expect(latest.getByText("Over the rate limit")).toBeTruthy();
    expect(latest.getByText("Deleted key")).toBeTruthy();
    expect(latest.getByText("(connected app)")).toBeTruthy();
    expect(latest.getByText(/Claude/)).toBeTruthy();
    expect(latest.getByText("4bf92f3577b34da6a3ce929d0e0e4736")).toBeTruthy();
    expect(api.getIntegrationRequests).toHaveBeenCalledWith("i-1", {});
  });

  it("reads the log again under each filter", async () => {
    api.getIntegrationRequests.mockResolvedValue(log);
    await renderTab();
    await screen.findByTestId("request-latest");

    fireEvent.click(screen.getByRole("combobox", { name: "Reason" }));
    fireEvent.click(await screen.findByRole("option", { name: "Over the rate limit" }));
    await waitFor(() => expect(api.getIntegrationRequests).toHaveBeenLastCalledWith("i-1", { reason: "rate_limited" }));

    fireEvent.click(screen.getByRole("combobox", { name: "Key or app" }));
    fireEvent.click(await screen.findByRole("option", { name: "Staging" }));
    await waitFor(() =>
      expect(api.getIntegrationRequests).toHaveBeenLastCalledWith("i-1", { reason: "rate_limited", key: "k-2" }),
    );

    fireEvent.click(screen.getByRole("combobox", { name: "Status" }));
    fireEvent.click(await screen.findByRole("option", { name: "4xx refused" }));
    await waitFor(() =>
      expect(api.getIntegrationRequests).toHaveBeenLastCalledWith("i-1", {
        reason: "rate_limited",
        key: "k-2",
        statusClass: 4,
      }),
    );
  });

  it("filters by a connected app instead of a key", async () => {
    api.getIntegrationRequests.mockResolvedValue(log);
    await renderTab();
    await screen.findByTestId("request-latest");

    fireEvent.click(screen.getByRole("combobox", { name: "Key or app" }));
    fireEvent.click(await screen.findByRole("option", { name: "Staging" }));
    await waitFor(() => expect(api.getIntegrationRequests).toHaveBeenLastCalledWith("i-1", { key: "k-2" }));

    fireEvent.click(screen.getByRole("combobox", { name: "Key or app" }));
    fireEvent.click(await screen.findByRole("option", { name: "Claude · claude.ai" }));
    await waitFor(() => expect(api.getIntegrationRequests).toHaveBeenLastCalledWith("i-1", { connection: "c-1" }));
    const call = api.getIntegrationRequests.mock.lastCall?.[1] as Record<string, unknown>;
    expect(call.key).toBeUndefined();
  });

  it("offers no connected apps while OAuth is switched off", async () => {
    api.getIntegrationRequests.mockResolvedValue(log);
    api.listIntegrationConnections.mockRejectedValue(Object.assign(new Error("Not Found"), { status: 404 }));
    await renderTab();
    await screen.findByTestId("request-latest");
    fireEvent.click(screen.getByRole("combobox", { name: "Key or app" }));
    expect(await screen.findByRole("option", { name: "Staging" })).toBeTruthy();
    expect(screen.queryByRole("option", { name: "Claude · claude.ai" })).toBeNull();
  });

  it("says so when nothing matches", async () => {
    api.getIntegrationRequests.mockResolvedValue({ ...log, days: [], operations: [], latest: [] });
    await renderTab();
    expect(await screen.findByText("No requests")).toBeTruthy();
  });

  it("offers the days of the server's window, whatever the browser's clock", async () => {
    api.getIntegrationRequests.mockResolvedValue(log);
    await renderTab();
    await screen.findByTestId("request-latest");
    fireEvent.click(screen.getByRole("combobox", { name: "Day" }));
    const options = await screen.findAllByRole("option");
    expect(options).toHaveLength(30);
    expect(options[0]?.textContent).toBe("Nov 18, 2026");
    expect(options[29]?.textContent).toBe("Oct 20, 2026");
  });

  it("counts the window's UTC days, newest first", () => {
    expect(windowDays("2026-10-30T00:00:00Z", "2026-11-01T23:59:00Z")).toEqual([
      "2026-11-01",
      "2026-10-31",
      "2026-10-30",
    ]);
  });

  it("orders the chart's days oldest first, a count per status class", () => {
    expect(dayRows(log)).toEqual([
      { day: "2026-11-17", class2: 12 },
      { day: "2026-11-18", class2: 40, class4: 3 },
    ]);
  });
});
