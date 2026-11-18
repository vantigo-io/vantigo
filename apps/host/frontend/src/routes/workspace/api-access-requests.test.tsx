import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ApiKey, IntegrationRequestLog } from "../../api/integrations";
import { renderRoute } from "../../test/render-route";
import { dayRows, IntegrationRequests } from "./-api-access-requests";
import "../../i18n";

const api = vi.hoisted(() => ({ getIntegrationRequests: vi.fn() }));

vi.mock("../../api/integrations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/integrations")>()),
  getIntegrationRequests: api.getIntegrationRequests,
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
      method: "POST",
      operationId: "postOrders",
      status: 201,
      reason: null,
      durationMs: 31,
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
});

describe("an integration's request log", () => {
  it("shows the days by status class, the operations called most and the latest requests", async () => {
    api.getIntegrationRequests.mockResolvedValue(log);
    await renderTab();

    const days = await screen.findByTestId("request-days");
    const rows = within(days).getAllByRole("row");
    expect(rows.map((row) => row.textContent)).toEqual([
      "Day2xx success3xx redirect4xx refused5xx server error",
      "2026-11-1712000",
      "2026-11-1840030",
    ]);
    expect(within(screen.getByTestId("request-operations")).getByText("getCustomers")).toBeTruthy();

    const latest = within(screen.getByTestId("request-latest"));
    expect(latest.getByText("Over the rate limit")).toBeTruthy();
    expect(latest.getByText("Deleted key")).toBeTruthy();
    expect(latest.getByText("4bf92f3577b34da6a3ce929d0e0e4736")).toBeTruthy();
    expect(api.getIntegrationRequests).toHaveBeenCalledWith("i-1", {});
  });

  it("reads the log again under each filter", async () => {
    api.getIntegrationRequests.mockResolvedValue(log);
    await renderTab();
    await screen.findByTestId("request-latest");

    fireEvent.click(screen.getByRole("combobox", { name: "Refused because" }));
    fireEvent.click(await screen.findByRole("option", { name: "Over the rate limit" }));
    await waitFor(() => expect(api.getIntegrationRequests).toHaveBeenLastCalledWith("i-1", { reason: "rate_limited" }));

    fireEvent.click(screen.getByRole("combobox", { name: "Key" }));
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

  it("says so when nothing matches", async () => {
    api.getIntegrationRequests.mockResolvedValue({ ...log, days: [], operations: [], latest: [] });
    await renderTab();
    expect(await screen.findByText("No requests")).toBeTruthy();
  });

  it("orders the chart's days oldest first, a count per status class", () => {
    expect(dayRows(log)).toEqual([
      { day: "2026-11-17", class2: 12 },
      { day: "2026-11-18", class2: 40, class4: 3 },
    ]);
  });
});
