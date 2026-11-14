import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Delivery, Order } from "../api/types";
import { commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { line, meta, order, settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

// A kit line's child lines (manufacturing phase 2 M145, M146): shown under
// their kit, never edited, never sent.

const url = "/api/v1/orders/orders/2001";

const kitOrder = (): Order =>
  order({
    status: "confirmed",
    confirmedAt: "2026-11-02T08:00:00Z",
    version: 3,
    lines: [
      line({ id: 5001, sku: "SETT-1", name: "Verktøysett", quantity: 2, deliveryMethod: "delivery", available: 3 }),
      line({
        id: 5002,
        position: 2,
        variantId: 302,
        sku: "HYL-1",
        name: "Hylle",
        quantity: 4,
        unitPrice: 0,
        displayUnitPrice: 0,
        lineGross: 0,
        lineNet: 0,
        deliveryMethod: "delivery",
        qtyReserved: 4,
        parentLineId: 5001,
        perKit: 2,
      }),
    ],
  });

describe("an order's kit lines", () => {
  it("shows the kit's children under it and saves the kit alone", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, kitOrder());
      if (call.method === "GET" && call.url === `${url}/changes`) return jsonResponse(200, { items: [] });
      if (call.method === "PUT") return jsonResponse(200, kitOrder());
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/2001");

    const child = await screen.findByTestId("kit-child-row");
    expect(child).toHaveTextContent("HYL-1 · Hylle");
    expect(child).toHaveTextContent("4 stk · 2 per kit");
    expect(within(child).getByTestId("line-stock")).toHaveTextContent("Reserved 4");
    expect(screen.getAllByTestId("line-row")).toHaveLength(1);
    expect(screen.getByText("Available 3")).toBeInTheDocument();

    const quantity = screen.getByLabelText("Quantity of line 1");
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "3");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    const body = fake.sent("PUT", url)?.body as { lines: Record<string, unknown>[] };
    expect(body.lines).toHaveLength(1);
    expect(body.lines[0]).toMatchObject({ id: 5001, quantity: 3 });
  });

  it("offers no Reserve now for a kit line, whose children hold the reservations", async () => {
    const atSite: Order = { ...kitOrder(), siteId: 1 };
    atSite.lines = atSite.lines.map((l) => (l.id === 5001 ? { ...l, qtyReserved: 0 } : l));
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, atSite);
      if (call.method === "GET" && call.url === `${url}/changes`) return jsonResponse(200, { items: [] });
      if (call.method === "GET" && call.url === "/api/v1/orders/sites")
        return jsonResponse(200, { items: [{ id: 1, code: "OSL", name: "Oslo", fulfilsOrders: true, active: true }] });
      if (call.method === "GET" && call.url.startsWith("/api/v1/orders/deliveries?"))
        return jsonResponse(200, { items: [], total: 0 });
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/orders/2001");

    await screen.findByTestId("kit-child-row");
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Site" })).toHaveValue("OSL · Oslo"));
    expect(screen.queryByRole("button", { name: "Reserve now" })).not.toBeInTheDocument();
  });

  it("offers a kit delivered by delivery documents as its children in Register delivered", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, kitOrder());
      if (call.method === "GET" && call.url === `${url}/changes`) return jsonResponse(200, { items: [] });
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Register delivered" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Quantity for Hylle in Verktøysett")).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Quantity for Verktøysett")).not.toBeInTheDocument();
  });

  it("offers a kit marked by hand without its children in Register delivered", async () => {
    const byHand = kitOrder();
    byHand.lines = byHand.lines.map((l) => ({ ...l, deliveryMethod: "manual" as const }));
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, byHand);
      if (call.method === "GET" && call.url === `${url}/changes`) return jsonResponse(200, { items: [] });
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Register delivered" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Quantity for Verktøysett")).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Quantity for Hylle")).not.toBeInTheDocument();
  });

  it("names a kit's short child on the kit's row when the confirm is short", async () => {
    const draft: Order = { ...kitOrder(), status: "draft", confirmedAt: null, siteId: 1 };
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, draft);
      if (call.method === "GET" && call.url === `${url}/changes`) return jsonResponse(200, { items: [] });
      if (call.method === "GET" && call.url === "/api/v1/orders/sites")
        return jsonResponse(200, { items: [{ id: 1, code: "OSL", name: "Oslo", fulfilsOrders: true, active: true }] });
      if (call.method === "POST" && call.url === `${url}/confirm`)
        return refusal(409, "insufficient_available", {
          lines: [{ index: 0, code: "insufficient_available", detail: "1 available." }],
        });
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Confirm" }));
    const dialog = await screen.findByRole("dialog", { name: "Not enough available" });
    expect(within(dialog).getByTestId("shortfall-line")).toHaveTextContent("Line 1: Verktøysett (available 3)");
  });

  it("tells a kit's component from a line of the same product on its own", async () => {
    const both = kitOrder();
    both.lines = [
      ...both.lines,
      line({
        id: 5003,
        position: 3,
        variantId: 302,
        sku: "HYL-1",
        name: "Hylle",
        quantity: 1,
        deliveryMethod: "delivery",
      }),
    ];
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, both);
      if (call.method === "GET" && call.url === `${url}/changes`) return jsonResponse(200, { items: [] });
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Register delivered" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Quantity for Hylle in Verktøysett")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Quantity for Hylle")).toBeInTheDocument();
    const children = within(dialog).getAllByTestId("kit-child-row");
    expect(children).toHaveLength(1);
    expect(children[0]).toHaveTextContent("in Verktøysett");
  });

  it("shows a delivery's kit component under its kit's name", async () => {
    const both = kitOrder();
    both.lines = [
      ...both.lines,
      line({
        id: 5003,
        position: 3,
        variantId: 302,
        sku: "HYL-1",
        name: "Hylle",
        quantity: 1,
        deliveryMethod: "delivery",
      }),
    ];
    const deliveryLine = (id: number, orderLineId: number): Delivery["lines"][number] => ({
      id,
      position: id,
      orderLineId,
      variantId: 302,
      sku: "HYL-1",
      name: "Hylle",
      unit: "stk",
      quantity: 1,
      ordered: 1,
      open: 0,
      fromLocationId: 11,
      fromLocationCode: "OSL-A-01",
      locations: [],
      tracked: false,
      lots: [],
      lotChoices: [],
      short: 0,
    });
    const posted: Delivery = {
      id: 7001,
      number: 12,
      orderId: 2001,
      orderNumber: 5001,
      customerId: 42,
      customerName: "Acme AS",
      orderOnHold: false,
      siteId: 1,
      status: "posted",
      postedAt: "2026-11-05T09:00:00Z",
      packingSlipStored: false,
      version: 2,
      createdAt: "2026-11-05T08:00:00Z",
      updatedAt: "2026-11-05T09:00:00Z",
      deliveryAddress: { name: "Acme lager", line1: "Lagerveien 2", postalCode: "2000", city: "Lillestrøm" },
      lines: [deliveryLine(1, 5002), deliveryLine(2, 5003)],
    };
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, both);
      if (call.method === "GET" && call.url === "/api/v1/orders/deliveries/7001") return jsonResponse(200, posted);
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/deliveries/7001");

    const of = await screen.findByTestId("kit-child-of");
    expect(of).toHaveTextContent("in Verktøysett");
    expect(screen.getAllByTestId("kit-child-of")).toHaveLength(1);
    expect(screen.getAllByText("HYL-1 Hylle", { exact: false })).toHaveLength(2);
  });
});
