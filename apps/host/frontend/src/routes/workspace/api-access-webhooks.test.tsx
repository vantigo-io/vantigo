import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WebhookEndpoint, WebhookEvent } from "../../api/webhooks";
import { renderRoute } from "../../test/render-route";
import { IntegrationWebhooks } from "./-api-access-webhooks";
import "../../i18n";

const api = vi.hoisted(() => ({
  listWebhooks: vi.fn(),
  listWebhookEvents: vi.fn(),
  createWebhook: vi.fn(),
  updateWebhook: vi.fn(),
  rollWebhookSecret: vi.fn(),
  disableWebhook: vi.fn(),
  enableWebhook: vi.fn(),
  testWebhook: vi.fn(),
}));

vi.mock("../../api/webhooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/webhooks")>()),
  ...api,
}));

const event = (name: string, permitted: boolean): WebhookEvent => {
  const module = name.split(".")[0];
  return {
    name,
    module,
    resource: name.split(".")[1],
    path: `/api/v1/${module}/x/{id}`,
    permissions: [`${module}:access`],
    permitted,
  };
};

const catalogue = [
  event("orders.sales_order.confirmed", true),
  event("orders.quote.sent", true),
  event("invoices.invoice.overdue", false),
];

const endpoint = (overrides: Partial<WebhookEndpoint> = {}): WebhookEndpoint => ({
  id: "w-1",
  integrationId: "i-1",
  url: "https://hooks.example.com/catch/abc?token=xyz",
  description: "To the ERP",
  events: ["orders.sales_order.confirmed"],
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

const secret = "whsec_c2VjcmV0LXNlY3JldC1zZWNyZXQtc2VjcmV0LXNlY3I=";

const renderTab = async (archived = false) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute(
    "/workspace/api-access/i-1",
    () => <IntegrationWebhooks integrationId="i-1" archived={archived} />,
    queryClient,
  );
};

/** Whether value is anywhere in the browser's storage. */
const stored = (value: string) =>
  [localStorage, sessionStorage].some((storage) =>
    Array.from({ length: storage.length }, (_, i) => storage.getItem(storage.key(i) ?? "") ?? "").some((item) =>
      item.includes(value),
    ),
  );

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  api.listWebhookEvents.mockResolvedValue(catalogue);
});

describe("an integration's webhook endpoints", () => {
  it("lists each endpoint with its full URL, its events and why it is disabled", async () => {
    api.listWebhooks.mockResolvedValue([
      endpoint(),
      endpoint({
        id: "w-2",
        url: "https://other.example.com/in",
        description: null,
        status: "disabled",
        disabledReason: "gone",
        events: ["orders.sales_order.confirmed", "orders.quote.sent", "a.b.c", "d.e.f", "g.h.i"],
        previousSecretExpiresAt: "2026-11-19T10:00:00Z",
      }),
    ]);
    await renderTab();

    const table = await screen.findByTestId("webhook-endpoints");
    expect(within(table).getByText("https://hooks.example.com/catch/abc?token=xyz")).toBeInTheDocument();
    expect(within(table).getByText("Disabled: it answered 410 Gone")).toBeInTheDocument();
    expect(within(table).getByText("+2 more")).toBeInTheDocument();
    expect(within(table).getByText(/The previous secret signs too until/)).toBeInTheDocument();
  });

  it("offers only the events the roles allow, grouped by module, and shows the secret once", async () => {
    api.listWebhooks.mockResolvedValue([]);
    api.createWebhook.mockResolvedValue({ endpoint: endpoint(), secret });
    await renderTab();

    expect(await screen.findByText("No endpoints yet")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "New endpoint" }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText("orders.sales_order.confirmed");
    expect(within(dialog).queryByText("invoices.invoice.overdue")).not.toBeInTheDocument();
    expect(dialog.querySelector('[data-group="Orders"]')).not.toBeNull();

    fireEvent.change(within(dialog).getByRole("textbox", { name: /^URL/ }), {
      target: { value: " https://hooks.example.com/in " },
    });
    fireEvent.click(within(dialog).getByRole("checkbox", { name: "orders.sales_order.confirmed" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Create endpoint" }));

    expect(await screen.findByText(secret)).toBeInTheDocument();
    expect(api.createWebhook).toHaveBeenCalledWith("i-1", {
      url: "https://hooks.example.com/in",
      events: ["orders.sales_order.confirmed"],
    });
    expect(screen.getByText("This is the only time the secret is shown.", { exact: false })).toBeInTheDocument();
    expect(stored(secret)).toBe(false);

    // Only "I have copied the secret" closes it: no close button, and not Escape.
    const shown = screen.getByRole("dialog");
    expect(shown.querySelector(".mantine-Modal-close")).toBeNull();
    fireEvent.keyDown(shown, { key: "Escape" });
    expect(screen.getByText(secret)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "I have copied the secret" }));
    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());
    expect(stored(secret)).toBe(false);
  });

  it("edits under the version read, keeping an event no longer permitted visible to take off", async () => {
    api.listWebhooks.mockResolvedValue([
      endpoint({ events: ["orders.sales_order.confirmed", "invoices.invoice.overdue"] }),
    ]);
    api.updateWebhook.mockResolvedValue(endpoint());
    await renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Actions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Edit endpoint" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("No longer permitted")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /invoices\.invoice\.overdue/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(api.updateWebhook).toHaveBeenCalledWith("i-1", "w-1", "v-1", {
        url: "https://hooks.example.com/catch/abc?token=xyz",
        description: "To the ERP",
        events: ["orders.sales_order.confirmed"],
      }),
    );
  });

  it("rolls the secret, warning when an overlap still runs, and shows the new one once", async () => {
    api.listWebhooks.mockResolvedValue([endpoint({ previousSecretExpiresAt: "2026-11-19T10:00:00Z" })]);
    api.rollWebhookSecret.mockResolvedValue({ endpoint: endpoint(), secret });
    await renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Actions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Roll secret" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/rolling again ends the older secret at once/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Roll secret" }));

    expect(await screen.findByText(secret)).toBeInTheDocument();
    expect(api.rollWebhookSecret).toHaveBeenCalledWith("i-1", "w-1", 24);
    fireEvent.click(screen.getByRole("button", { name: "I have copied the secret" }));
    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());
    expect(stored(secret)).toBe(false);
  });

  it("waits for the catalogue before offering events, and says when it could not be read", async () => {
    api.listWebhooks.mockResolvedValue([]);
    let fail: (error: Error) => void = () => {};
    api.listWebhookEvents.mockReturnValue(
      new Promise((_, reject) => {
        fail = reject;
      }),
    );
    await renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "New endpoint" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(within(dialog).queryByTestId("webhook-no-events")).not.toBeInTheDocument();

    fail(new Error("Service unavailable"));
    expect(await within(dialog).findByTestId("webhook-events-error")).toHaveTextContent(
      "The events could not be loaded",
    );
    expect(within(dialog).getByText("Service unavailable")).toBeInTheDocument();
    expect(within(dialog).queryByTestId("webhook-no-events")).not.toBeInTheDocument();
  });

  it("rolls without the overlap warning when no overlap runs", async () => {
    api.listWebhooks.mockResolvedValue([endpoint()]);
    await renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Actions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Roll secret" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/A new secret is made/)).toBeInTheDocument();
    expect(within(dialog).queryByText(/rolling again ends the older secret at once/)).not.toBeInTheDocument();
  });

  it("asks before disabling an endpoint", async () => {
    api.listWebhooks.mockResolvedValue([endpoint()]);
    api.disableWebhook.mockResolvedValue(endpoint({ status: "disabled", disabledReason: "manual" }));
    await renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Actions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Disable" }));
    fireEvent.click(await screen.findByRole("button", { name: "Disable" }));
    await waitFor(() => expect(api.disableWebhook).toHaveBeenCalledWith("i-1", "w-1"));
  });

  it("offers nothing but deletion on an archived integration", async () => {
    api.listWebhooks.mockResolvedValue([endpoint({ status: "disabled", disabledReason: "integration_archived" })]);
    await renderTab(true);

    expect(screen.queryByRole("button", { name: "New endpoint" })).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole("button", { name: "Actions" }));
    const items = await screen.findAllByRole("menuitem");
    expect(items.map((item) => item.textContent)).toEqual(["Delete"]);
  });

  it("sends a test event to an enabled endpoint, and offers none to a disabled one", async () => {
    api.listWebhooks.mockResolvedValue([
      endpoint(),
      endpoint({ id: "w-2", url: "https://off.example.com/in", status: "disabled", disabledReason: "manual" }),
    ]);
    api.testWebhook.mockResolvedValue({ deliveryId: "d-1" });
    await renderTab();

    const [enabled, disabled] = await screen.findAllByRole("button", { name: "Actions" });
    fireEvent.click(enabled);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Send test event" }));
    await waitFor(() => expect(api.testWebhook).toHaveBeenCalledWith("i-1", "w-1"));
    expect(await screen.findByText("Test event queued")).toBeInTheDocument();

    fireEvent.click(disabled);
    await screen.findByRole("menuitem", { name: "Enable" });
    expect(screen.queryByRole("menuitem", { name: "Send test event" })).not.toBeInTheDocument();
  });

  it("marks a failing endpoint, and explains one the sender disabled with a way to enable it", async () => {
    api.listWebhooks.mockResolvedValue([
      endpoint({ failingSince: "2026-11-17T08:00:00Z", lastSuccessAt: "2026-11-16T08:00:00Z" }),
      endpoint({
        id: "w-2",
        url: "https://gone.example.com/catch/secret-path",
        status: "disabled",
        disabledReason: "gone",
        disabledAt: "2026-11-18T09:00:00Z",
      }),
      endpoint({
        id: "w-3",
        url: "https://failing.example.com/in",
        status: "disabled",
        disabledReason: "failing",
        disabledAt: "2026-11-18T09:00:00Z",
      }),
      endpoint({ id: "w-4", url: "https://manual.example.com/in", status: "disabled", disabledReason: "manual" }),
    ]);
    api.enableWebhook.mockResolvedValue(endpoint({ id: "w-2" }));
    await renderTab();

    const table = await screen.findByTestId("webhook-endpoints");
    const failing = table.querySelector('[data-endpoint="w-1"]') as HTMLElement;
    expect(within(failing).getByText("Failing").closest("[data-status]")).toHaveAttribute("data-status", "failing");
    expect(within(failing).getByText(/Failing since/)).toBeInTheDocument();
    expect(within(failing).getByText(/Last delivered/)).toBeInTheDocument();

    // A banner for each endpoint the sender disabled, none for an Owner's.
    const banners = screen.getAllByTestId("webhook-disabled-banner");
    expect(banners.map((banner) => banner.getAttribute("data-reason"))).toEqual(["gone", "failing"]);
    expect(within(banners[0]).getByText("Endpoint disabled: gone.example.com")).toBeInTheDocument();
    expect(within(banners[0]).queryByText(/secret-path/)).not.toBeInTheDocument();
    expect(within(banners[0]).getByText(/answered 410 Gone/)).toBeInTheDocument();
    expect(within(banners[1]).getByText(/failed for 5 days/)).toBeInTheDocument();

    fireEvent.click(within(banners[0]).getByRole("button", { name: "Enable" }));
    await waitFor(() => expect(api.enableWebhook).toHaveBeenCalledWith("i-1", "w-2"));
  });

  it("offers no way to enable from a banner on an archived integration", async () => {
    api.listWebhooks.mockResolvedValue([endpoint({ status: "disabled", disabledReason: "gone" })]);
    await renderTab(true);
    const banner = await screen.findByTestId("webhook-disabled-banner");
    expect(within(banner).queryByRole("button")).not.toBeInTheDocument();
  });
});
