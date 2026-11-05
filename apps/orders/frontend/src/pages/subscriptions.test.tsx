import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Subscription } from "../api/types";
import { type Call, commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { line, period, quote, settings, subscription } from "../test/fixtures";
import { renderRoute } from "../test/render";

const base = "/api/v1/orders/subscriptions";
const url = `${base}/3001`;

const server = (
  current: Subscription = subscription(),
  writes: (call: Call) => Response | undefined = () => undefined,
) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
    if (call.method !== "GET") return writes(call);
    return commonAnswers(call, settings());
  });

const active = (overrides: Partial<Subscription> = {}) =>
  subscription({
    status: "active",
    version: 2,
    activatedAt: "2026-10-01T08:00:00Z",
    billedThrough: "2026-10-31",
    nextChangeFrom: "2026-11-01",
    periods: [
      period({ state: "invoiced", invoiceNumbers: [10042] }),
      period({ start: "2026-11-01", end: "2026-11-30", state: "due" }),
      period({ start: "2026-12-01", end: "2026-12-31", state: "upcoming" }),
    ],
    ...overrides,
  });

describe("the subscriptions", () => {
  it("lists them with their plan, billed through and value per period", async () => {
    const fake = fakeServer((call) => {
      if (call.url.startsWith(`${base}?`) || call.url === base)
        return jsonResponse(200, {
          items: [
            {
              id: 3001,
              number: 7001,
              status: "active",
              interval: "quarterly",
              billing: "in_arrears",
              startDate: "2026-10-01",
              billedThrough: "2026-12-31",
              customerId: 42,
              customerName: "Acme AS",
              currency: "NOK",
              net: 4500,
              gross: 5625,
              updatedAt: "2026-11-01T08:00:00Z",
            },
          ],
          total: 1,
        });
      return commonAnswers(call);
    });
    renderRoute("/orders/subscriptions");

    const table = await screen.findByTestId("subscription-table");
    expect(within(table).getByRole("link", { name: "7001" })).toHaveAttribute("href", "/orders/subscriptions/3001");
    expect(table).toHaveTextContent("Quarterly · In arrears");
    expect(table).toHaveTextContent("NOK 4,500.00");
    expect(screen.getByTestId("subscription-status")).toHaveTextContent("Active");

    await userEvent.click(screen.getByRole("checkbox", { name: "Paused" }));
    await waitFor(() => expect(fake.calls.some((c) => c.url === `${base}?status=paused&pageSize=25`)).toBe(true));
  });

  it("creates a subscription from its customer and plan, and opens it", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "POST" && call.url === base) return jsonResponse(201, subscription());
      if (call.method === "GET" && call.url === url) return jsonResponse(200, subscription());
      return commonAnswers(call, settings());
    });
    const { router } = renderRoute("/orders/subscriptions/new?customerId=42");

    await userEvent.click(await screen.findByRole("button", { name: "Create subscription" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orders/subscriptions/3001"));
    const body = fake.sent("POST", base)?.body as Record<string, unknown>;
    expect(body).toMatchObject({ customerId: 42, interval: "monthly", billing: "in_advance", lines: [] });
    expect(String(body.startDate)).toMatch(/^\d{4}-\d{2}-01$/);
  });

  it("refuses New subscription without the key", async () => {
    fakeServer((call) => commonAnswers(call));
    renderRoute("/orders/subscriptions/new", { canManageSubscriptions: false });

    expect(
      await screen.findByText("Creating subscriptions needs the permission to manage subscriptions."),
    ).toBeInTheDocument();
  });

  it("activates a draft with the version it read", async () => {
    const fake = server(subscription(), (call) =>
      call.url === `${url}/activate` ? jsonResponse(200, active({ updatedAt: "2026-11-05T09:00:00Z" })) : undefined,
    );
    renderRoute("/orders/subscriptions/3001");

    await userEvent.click(await screen.findByRole("button", { name: "Activate" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/activate`)?.body).toEqual({
        version: 1,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
      }),
    );
    expect(await screen.findByText("The subscription is active")).toBeInTheDocument();
    expect(await screen.findByTestId("subscription-status")).toHaveTextContent("Active");
  });

  it("says why an activate was refused, in the reader's words", async () => {
    server(subscription(), (call) =>
      call.url === `${url}/activate` ? refusal(400, "subscription_goods_not_allowed") : undefined,
    );
    renderRoute("/orders/subscriptions/3001");

    await userEvent.click(await screen.findByRole("button", { name: "Activate" }));
    expect(
      await screen.findByText("A subscription sells services only. Remove the goods line, or sell it on an order."),
    ).toBeInTheDocument();
  });

  it("shows each period's state, billed through and the lines a change ended", async () => {
    server(
      active({
        earlierLines: [line({ id: 5000, name: "Kabel gammel", validTo: "2026-10-31" })],
        lines: [line({ validFrom: "2026-11-01" })],
      }),
    );
    renderRoute("/orders/subscriptions/3001");

    const periods = await screen.findByTestId("periods");
    expect(
      within(periods)
        .getAllByTestId("period-state")
        .map((b) => b.getAttribute("data-state")),
    ).toEqual(["invoiced", "due", "upcoming"]);
    expect(periods).toHaveTextContent("10042");
    expect(screen.getByTestId("billed-through")).toHaveTextContent("Oct 31, 2026");
    expect(screen.getByTestId("earlier-lines")).toHaveTextContent("Kabel gammel");
  });

  it("saves a change of an active subscription's lines from the next unbilled period", async () => {
    const fake = server(active(), (call) =>
      call.method === "PUT" && call.url === url
        ? jsonResponse(200, active({ version: 3, updatedAt: "2026-11-05T09:00:00Z" }))
        : undefined,
    );
    renderRoute("/orders/subscriptions/3001");

    const quantity = await screen.findByRole("textbox", { name: "Quantity of line 1" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "4");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    const body = fake.sent("PUT", url)?.body as Record<string, unknown>;
    expect(body).toMatchObject({ version: 2, effectiveFrom: "2026-11-01", noticeMonths: 1 });
    expect(body).not.toHaveProperty("deliveryDate");
    expect(body).not.toHaveProperty("interval");
    expect((body.lines as { quantity: number }[])[0].quantity).toBe(4);
    expect(await screen.findByText("The subscription is saved")).toBeInTheDocument();
  });

  it("pauses from the period asked, and resumes from a later one", async () => {
    const fake = server(active(), (call) =>
      call.url === `${url}/pause`
        ? jsonResponse(200, active({ status: "paused", pausedFrom: "2026-12-01", version: 3 }))
        : undefined,
    );
    renderRoute("/orders/subscriptions/3001");

    await userEvent.click(await screen.findByRole("button", { name: "Pause" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Pause" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/pause`)?.body).toEqual({
        version: 2,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
      }),
    );
    expect(await screen.findByText("The subscription is paused")).toBeInTheDocument();
  });

  it("ends with the final period asked, pre-selected from the subscription's own", async () => {
    const fake = server(active({ finalPeriod: "prorated" }), (call) =>
      call.url === `${url}/end`
        ? jsonResponse(200, active({ status: "ended", endDate: "2026-12-15", finalPeriod: "full", version: 3 }))
        : undefined,
    );
    renderRoute("/orders/subscriptions/3001");

    await userEvent.click(await screen.findByRole("button", { name: "End" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: "Pro-rated to the end date" })).toBeChecked();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "End" }), "15 Dec 2026");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Full period" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "End" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/end`)?.body).toEqual({
        version: 2,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
        endDate: "2026-12-15",
        finalPeriod: "full",
      }),
    );
    expect(await screen.findByText("The subscription has its end")).toBeInTheDocument();
  });

  it("says why a pro-rated final period was refused", async () => {
    server(active(), (call) => (call.url === `${url}/end` ? refusal(409, "final_period_billed") : undefined));
    renderRoute("/orders/subscriptions/3001");

    await userEvent.click(await screen.findByRole("button", { name: "End" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "End" }), "15 Nov 2026");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Pro-rated to the end date" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "End" }));
    expect(
      await screen.findByText(
        "The final period is already on an invoice or a draft invoice and cannot be pro-rated. Bill it in full, or credit it first.",
      ),
    ).toBeInTheDocument();
  });

  it("saves an active subscription's final period with its plan", async () => {
    const fake = server(active(), (call) =>
      call.method === "PUT" && call.url === url
        ? jsonResponse(200, active({ finalPeriod: "prorated", version: 3, updatedAt: "2026-11-05T09:00:00Z" }))
        : undefined,
    );
    renderRoute("/orders/subscriptions/3001");

    await userEvent.click(await screen.findByRole("combobox", { name: "Final period when a subscription ends" }));
    await userEvent.click(await screen.findByRole("option", { name: "Pro-rated to the end date" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    expect(fake.sent("PUT", url)?.body).toMatchObject({ version: 2, finalPeriod: "prorated" });
  });

  it("offers no steps to a reader", async () => {
    server(active());
    renderRoute("/orders/subscriptions/3001", { canManageSubscriptions: false });

    await screen.findByRole("heading", { name: /Subscription 7001/ });
    expect(screen.queryByTestId("subscription-actions")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });

  it("accepts a quote as a subscription on its plan, and opens it", async () => {
    const quoteUrl = "/api/v1/orders/quotes/1001";
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === quoteUrl) return jsonResponse(200, quote({ status: "sent" }));
      if (call.url === `${quoteUrl}/accept`)
        return jsonResponse(200, { quote: quote({ status: "accepted" }), subscription: subscription() });
      if (call.method === "GET" && call.url === url) return jsonResponse(200, subscription());
      return commonAnswers(call, settings());
    });
    const { router } = renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Accept" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("radio", { name: /A subscription/ }));
    expect(within(dialog).getByRole("button", { name: "Accept as subscription" })).toBeDisabled();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Start" }), "01.01.2027");
    await userEvent.click(within(dialog).getByRole("button", { name: "Accept as subscription" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/orders/subscriptions/3001"));
    expect(fake.sent("POST", `${quoteUrl}/accept`)?.body).toMatchObject({
      into: "subscription",
      plan: { interval: "monthly", billing: "in_advance", startDate: "2027-01-01" },
    });
  });

  it("offers accepting as a subscription only with the key", async () => {
    server();
    fakeServer((call) =>
      call.method === "GET" && call.url === "/api/v1/orders/quotes/1001"
        ? jsonResponse(200, quote({ status: "sent" }))
        : commonAnswers(call, settings()),
    );
    renderRoute("/orders/quotes/1001", { canManageSubscriptions: false });

    await userEvent.click(await screen.findByRole("button", { name: "Accept" }));
    expect(within(await screen.findByRole("dialog")).getByRole("radio", { name: /A subscription/ })).toBeDisabled();
  });
});
