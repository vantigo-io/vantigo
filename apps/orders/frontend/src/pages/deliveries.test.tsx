import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Delivery, Order } from "../api/types";
import { type Call, commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { line, meta, order, settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

// Delivery C (orders design O45–O48): the site, reservations at confirm,
// Reserve now, the order's deliveries, the delivery editor and the list.

const orderUrl = "/api/v1/orders/orders/2001";
const deliveryUrl = "/api/v1/orders/deliveries/7001";
const sites = {
  items: [
    { id: 1, code: "OSL", name: "Oslo", fulfilsOrders: true, active: true },
    { id: 3, code: "SHOP", name: "Butikk", fulfilsOrders: false, active: true },
  ],
};

const atSite = (overrides: Partial<Order> = {}) =>
  order({
    siteId: 1,
    lines: [line({ deliveryMethod: "delivery", invoicePolicy: "delivery", qtyReserved: 0, available: 1 })],
    ...overrides,
  });

const delivery = (overrides: Partial<Delivery> = {}): Delivery => ({
  id: 7001,
  number: 12,
  orderId: 2001,
  orderNumber: 5001,
  customerId: 42,
  customerName: "Acme AS",
  orderOnHold: false,
  siteId: 1,
  status: "draft",
  packingSlipStored: false,
  version: 1,
  createdAt: "2026-11-05T08:00:00Z",
  updatedAt: "2026-11-05T08:00:00Z",
  deliveryAddress: { name: "Acme lager", line1: "Lagerveien 2", postalCode: "2000", city: "Lillestrøm" },
  lines: [
    {
      id: 1,
      position: 1,
      orderLineId: 5001,
      variantId: 301,
      sku: "KAB-10",
      name: "Kabel 10 m",
      unit: "stk",
      quantity: 2,
      ordered: 3,
      open: 3,
      fromLocationId: null,
      locations: [
        { locationId: 11, fullCode: "OSL-A-01", quantity: 5 },
        { locationId: 12, fullCode: "OSL-A-02", quantity: 1 },
      ],
      tracked: false,
      lots: [],
      lotChoices: [],
      short: 0,
    },
  ],
  ...overrides,
});

const server = (current: Order, writes: (call: Call) => Response | undefined = () => undefined) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === orderUrl) return jsonResponse(200, current);
    if (call.method === "GET" && call.url === "/api/v1/orders/sites") return jsonResponse(200, sites);
    if (call.method === "GET" && call.url.startsWith("/api/v1/orders/deliveries?"))
      return jsonResponse(200, { items: [], total: 0 });
    if (call.method === "GET" && call.url === deliveryUrl) return jsonResponse(200, delivery());
    if (call.method !== "GET") return writes(call);
    return commonAnswers(call, settings(), meta({ stockKept: true }));
  });

describe("deliveries and stock", () => {
  it("offers Confirm without reserving when the site is short, naming the short line", async () => {
    const fake = server(atSite(), (call) => {
      if (call.url !== `${orderUrl}/confirm`) return undefined;
      const body = call.body as { reserve?: boolean };
      return body.reserve === false
        ? jsonResponse(200, atSite({ status: "confirmed", updatedAt: "2026-11-05T09:00:00Z" }))
        : refusal(409, "insufficient_available", {
            lines: [{ index: 0, code: "insufficient_available", detail: "1 available." }],
          });
    });
    renderRoute("/orders/orders/2001");

    const site = await screen.findByRole("combobox", { name: "Site" });
    await waitFor(() => expect(site).toHaveValue("OSL · Oslo"));
    await userEvent.click(await screen.findByRole("button", { name: "Confirm" }));
    const dialog = await screen.findByRole("dialog", { name: "Not enough available" });
    expect(within(dialog).getByTestId("shortfall-line")).toHaveTextContent("Line 1: Kabel 10 m (available 1)");
    await userEvent.click(within(dialog).getByRole("button", { name: "Confirm without reserving" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${orderUrl}/confirm`)?.body).toEqual({
        version: 1,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
        reserve: false,
      }),
    );
    expect(await screen.findByText("Order confirmed without reserving")).toBeInTheDocument();
  });

  it("shows what is reserved and available, reserves now, and makes a delivery", async () => {
    const confirmed = atSite({
      status: "confirmed",
      lines: [line({ deliveryMethod: "delivery", invoicePolicy: "delivery", qtyReserved: 1, available: 4 })],
    });
    const fake = server(confirmed, (call) => {
      if (call.url === `${orderUrl}/reserve`) return jsonResponse(200, confirmed);
      if (call.url === "/api/v1/orders/deliveries") return jsonResponse(201, delivery());
      return undefined;
    });
    renderRoute("/orders/orders/2001");

    const stock = await screen.findByTestId("line-stock");
    expect(stock).toHaveTextContent("Reserved 1");
    expect(stock).toHaveTextContent("Available 4");
    await userEvent.click(await screen.findByRole("button", { name: "Reserve now" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${orderUrl}/reserve`)?.body).toEqual({
        version: 1,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
      }),
    );

    await userEvent.click(
      within(screen.getByTestId("deliveries-card")).getByRole("button", { name: "Create delivery" }),
    );
    await waitFor(() => expect(fake.sent("POST", "/api/v1/orders/deliveries")?.body).toEqual({ orderId: 2001 }));
    expect(await screen.findByRole("heading", { name: /Delivery 12/ })).toBeInTheDocument();
  });

  it("picks a draft delivery's locations and saves its lines, posting only once every line is picked", async () => {
    const fake = server(atSite(), (call) =>
      call.method === "PUT" && call.url === deliveryUrl
        ? jsonResponse(200, delivery({ version: 2, updatedAt: "2026-11-05T08:30:00Z" }))
        : undefined,
    );
    renderRoute("/orders/deliveries/7001");

    expect(await screen.findByRole("heading", { name: /Delivery 12/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Post delivery" })).toBeDisabled();
    await userEvent.click(screen.getByRole("combobox", { name: "Location of line 1" }));
    await userEvent.click(await screen.findByRole("option", { name: "OSL-A-01 (5)" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(fake.sent("PUT", deliveryUrl)?.body).toEqual({
        version: 1,
        expectedUpdatedAt: "2026-11-05T08:00:00Z",
        lines: [{ orderLineId: 5001, quantity: 2, fromLocationId: 11 }],
      }),
    );
    expect(await screen.findByText("Delivery saved")).toBeInTheDocument();
  });

  it("lists the orders still to deliver", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url.startsWith("/api/v1/orders/orders?"))
        return jsonResponse(200, {
          items: [
            {
              id: 2001,
              number: 5001,
              status: "confirmed",
              onHold: false,
              locked: false,
              deliveryStatus: "partial",
              invoiceStatus: "none",
              done: false,
              customerId: 42,
              customerName: "Acme AS",
              currency: "NOK",
              net: 1500,
              gross: 1875,
              updatedAt: "2026-11-01T08:00:00Z",
            },
          ],
          total: 1,
        });
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/deliveries");

    expect(await screen.findByText("Acme AS")).toBeInTheDocument();
    expect(fake.sent("GET", /\/api\/v1\/orders\/orders\?/)?.url).toContain("toDeliver=true");
  });
});
