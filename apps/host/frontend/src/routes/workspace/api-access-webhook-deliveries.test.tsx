import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WebhookDelivery, WebhookDeliveryDetail, WebhookEndpoint } from "../../api/webhooks";
import { renderRoute } from "../../test/render-route";
import { IntegrationWebhooks } from "./-api-access-webhooks";
import "../../i18n";

const api = vi.hoisted(() => ({
  listWebhooks: vi.fn(),
  listWebhookEvents: vi.fn(),
  listWebhookDeliveries: vi.fn(),
  getWebhookDelivery: vi.fn(),
  resendWebhookDelivery: vi.fn(),
}));

vi.mock("../../api/webhooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/webhooks")>()),
  ...api,
}));

const endpoint = (overrides: Partial<WebhookEndpoint> = {}): WebhookEndpoint => ({
  id: "w-1",
  integrationId: "i-1",
  url: "https://hooks.example.com/in",
  description: null,
  events: ["orders.sales_order.confirmed", "orders.sales_order.cancelled"],
  status: "enabled",
  disabledReason: null,
  disabledAt: null,
  failingSince: null,
  lastSuccessAt: null,
  previousSecretExpiresAt: null,
  createdAt: "2026-11-18T10:00:00Z",
  createdBy: "me",
  updatedAt: "2026-11-18T10:00:00Z",
  version: "v-1",
  ...overrides,
});

const delivery = (overrides: Partial<WebhookDelivery> = {}): WebhookDelivery => ({
  id: "d-1",
  messageId: "msg_d1",
  eventId: 42,
  eventName: "orders.sales_order.confirmed",
  kind: "event",
  state: "dead",
  stateReason: "attempts_exhausted",
  runAttempts: 8,
  maxRunAttempts: 8,
  totalAttempts: 8,
  nextAttemptAt: null,
  lastStatus: 500,
  lastError: "http_status",
  createdAt: "2026-11-18T10:00:00Z",
  completedAt: "2026-11-19T10:00:00Z",
  resendable: true,
  ...overrides,
});

const hostile = `<script>window.__pwned = true</script><img src=x onerror="window.__pwned = true">`;

const detail = (overrides: Partial<WebhookDeliveryDetail> = {}): WebhookDeliveryDetail => ({
  delivery: delivery(),
  body: '{"id":"evt_42","type":"orders.sales_order.confirmed"}',
  attempts: [
    {
      at: "2026-11-19T10:00:00Z",
      statusCode: 500,
      durationMs: 120,
      error: "http_status",
      excerpt: hostile,
      secretUsed: "both",
    },
  ],
  ...overrides,
});

const renderLog = async (options: { archived?: boolean; endpoint?: WebhookEndpoint } = {}) => {
  api.listWebhooks.mockResolvedValue([options.endpoint ?? endpoint()]);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute(
    "/workspace/api-access/i-1",
    () => <IntegrationWebhooks integrationId="i-1" archived={options.archived ?? false} />,
    queryClient,
  );
  fireEvent.click(await screen.findByRole("button", { name: "Deliveries" }));
};

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  api.listWebhookEvents.mockResolvedValue([]);
  delete (window as { __pwned?: boolean }).__pwned;
});

describe("a webhook endpoint's delivery log", () => {
  it("lists the deliveries with their state, the test events marked, and loads more by cursor", async () => {
    api.listWebhookDeliveries
      .mockResolvedValueOnce({
        deliveries: [
          delivery(),
          delivery({
            id: "d-2",
            eventName: "webhook.ping",
            kind: "ping",
            eventId: null,
            state: "succeeded",
            stateReason: null,
            runAttempts: 1,
            maxRunAttempts: 1,
            totalAttempts: 1,
            lastStatus: 204,
            lastError: null,
            resendable: false,
          }),
        ],
        nextCursor: "c-1",
      })
      .mockResolvedValueOnce({
        deliveries: [delivery({ id: "d-3", state: "skipped", stateReason: "permission_not_held" })],
      });
    await renderLog();

    const table = await screen.findByTestId("webhook-deliveries");
    expect(api.listWebhookDeliveries).toHaveBeenCalledWith("i-1", "w-1", {}, undefined);
    const dead = table.querySelector('[data-delivery="d-1"]') as HTMLElement;
    expect(within(dead).getByText("Failed").closest("[data-state]")).toHaveAttribute("data-state", "dead");
    expect(within(dead).getByText("Every attempt failed")).toBeInTheDocument();
    expect(within(dead).getByText("Error status")).toBeInTheDocument();
    expect(within(dead).getByText("8 of 8 (8 in all)")).toBeInTheDocument();
    const ping = table.querySelector('[data-delivery="d-2"]') as HTMLElement;
    expect(within(ping).getByText("Test")).toBeInTheDocument();
    expect(within(ping).queryByRole("button", { name: "Resend" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(table.querySelector('[data-delivery="d-3"]')).not.toBeNull());
    expect(api.listWebhookDeliveries).toHaveBeenLastCalledWith("i-1", "w-1", {}, "c-1");
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Back to the endpoints" }));
    expect(await screen.findByTestId("webhook-endpoints")).toBeInTheDocument();
  });

  it("filters by state and event, and says when nothing matches", async () => {
    api.listWebhookDeliveries.mockResolvedValueOnce({ deliveries: [delivery()] });
    api.listWebhookDeliveries.mockResolvedValue({ deliveries: [] });
    await renderLog();
    await screen.findByTestId("webhook-deliveries");

    const filters = screen.getByTestId("delivery-filters");
    fireEvent.click(within(filters).getByRole("combobox", { name: "Status" }));
    fireEvent.click(await screen.findByRole("option", { name: "Failed" }));
    await waitFor(() =>
      expect(api.listWebhookDeliveries).toHaveBeenLastCalledWith("i-1", "w-1", { state: "dead" }, undefined),
    );
    fireEvent.click(within(filters).getByRole("combobox", { name: "Event" }));
    fireEvent.click(await screen.findByRole("option", { name: "webhook.ping" }));
    await waitFor(() =>
      expect(api.listWebhookDeliveries).toHaveBeenLastCalledWith(
        "i-1",
        "w-1",
        { state: "dead", event: "webhook.ping" },
        undefined,
      ),
    );
    expect(await screen.findByText("No deliveries match the filters")).toBeInTheDocument();
  });

  it("says when the endpoint has had no deliveries yet", async () => {
    api.listWebhookDeliveries.mockResolvedValue({ deliveries: [] });
    await renderLog();
    expect(await screen.findByText("No deliveries yet")).toBeInTheDocument();
  });

  it("shows a delivery's body and attempts, the receiver's answer as literal text only", async () => {
    api.listWebhookDeliveries.mockResolvedValue({ deliveries: [delivery()] });
    api.getWebhookDelivery.mockResolvedValue(detail());
    await renderLog();

    fireEvent.click(await screen.findByRole("button", { name: "View" }));
    const drawer = await screen.findByTestId("webhook-delivery-detail");
    expect(api.getWebhookDelivery).toHaveBeenCalledWith("i-1", "w-1", "d-1");
    expect(within(drawer).getByText("msg_d1")).toBeInTheDocument();
    expect(within(drawer).getByTestId("webhook-delivery-body")).toHaveTextContent(
      '{"id":"evt_42","type":"orders.sales_order.confirmed"}',
    );
    expect(within(drawer).getByText("Signed with the current and the previous secret")).toBeInTheDocument();

    // The excerpt is the receiver's text: shown as it is, never parsed as HTML.
    const excerpt = within(drawer).getByTestId("webhook-attempt-excerpt");
    expect(excerpt.textContent).toBe(hostile);
    expect(excerpt.children).toHaveLength(0);
    expect(document.querySelector("script")?.textContent ?? "").not.toContain("__pwned");
    expect(drawer.querySelector("img")).toBeNull();
    expect(drawer.querySelector("a")).toBeNull();
    expect((window as { __pwned?: boolean }).__pwned).toBeUndefined();
  });

  it("resends after asking, showing only that row as busy", async () => {
    api.listWebhookDeliveries.mockResolvedValue({
      deliveries: [delivery(), delivery({ id: "d-2", state: "succeeded", stateReason: null })],
    });
    api.resendWebhookDelivery.mockReturnValue(new Promise(() => {}));
    await renderLog();

    const table = await screen.findByTestId("webhook-deliveries");
    const first = table.querySelector('[data-delivery="d-1"]') as HTMLElement;
    const second = table.querySelector('[data-delivery="d-2"]') as HTMLElement;
    fireEvent.click(within(first).getByRole("button", { name: "Resend" }));
    expect(await screen.findByText("Resend this delivery?")).toBeInTheDocument();
    expect(api.resendWebhookDelivery).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Resend" }));
    await waitFor(() => expect(api.resendWebhookDelivery).toHaveBeenCalledWith("i-1", "w-1", "d-1"));
    await waitFor(() =>
      expect(within(first).getByRole("button", { name: "Resend" })).toHaveAttribute("data-loading", "true"),
    );
    expect(within(second).getByRole("button", { name: "Resend" })).not.toHaveAttribute("data-loading");
  });

  it("explains a resend refused for the limit", async () => {
    api.listWebhookDeliveries.mockResolvedValue({ deliveries: [delivery()] });
    api.resendWebhookDelivery.mockRejectedValue(Object.assign(new Error("Too many"), { code: "rate_limited" }));
    await renderLog();

    fireEvent.click(await screen.findByRole("button", { name: "Resend" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Resend" }));
    expect(await screen.findByText(/at most 3 times a minute/)).toBeInTheDocument();
  });

  it("offers no resend for a disabled endpoint or an archived integration", async () => {
    api.listWebhookDeliveries.mockResolvedValue({ deliveries: [delivery()] });
    await renderLog({
      endpoint: endpoint({ status: "disabled", disabledReason: "manual", disabledAt: "2026-11-19T10:00:00Z" }),
    });
    await screen.findByTestId("webhook-deliveries");
    expect(screen.queryByRole("button", { name: "Resend" })).not.toBeInTheDocument();
    expect(screen.getByTestId("webhook-no-resend")).toHaveTextContent(/enable it to resend/);
  });
});
