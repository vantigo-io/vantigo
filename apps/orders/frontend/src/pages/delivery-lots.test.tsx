import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Delivery, DeliveryLine } from "../api/types";
import { type Call, commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { line, meta, order, settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

// Layer O (manufacturing phase 2 M77–M79): a tracked delivery line's lots —
// proposed, short, changed, refused per lot — and the order's deliveries
// naming their lots.

const deliveryUrl = "/api/v1/orders/deliveries/7002";

const trackedLine = (overrides: Partial<DeliveryLine> = {}): DeliveryLine => ({
  id: 2,
  position: 1,
  orderLineId: 5002,
  variantId: 401,
  sku: "MILK-1",
  name: "Melk",
  unit: "l",
  quantity: 6,
  ordered: 6,
  open: 6,
  fromLocationId: null,
  locations: [],
  tracked: true,
  lots: [
    {
      lotId: 21,
      lotNumber: "B2",
      expiry: "2026-11-20",
      locationId: 12,
      locationCode: "OSL-A-02",
      quantity: 4,
      allowExpired: false,
      chosen: false,
    },
  ],
  lotChoices: [
    {
      lotId: 23,
      lotNumber: "X0",
      expiry: "2026-11-01",
      bestBefore: null,
      locationId: 11,
      fullCode: "OSL-A-01",
      quantity: 9,
      status: "available",
      expired: true,
      serial: false,
    },
    {
      lotId: 21,
      lotNumber: "B2",
      expiry: "2026-11-20",
      bestBefore: null,
      locationId: 12,
      fullCode: "OSL-A-02",
      quantity: 4,
      status: "available",
      expired: false,
      serial: false,
    },
    {
      lotId: 24,
      lotNumber: "Q4",
      expiry: "2026-11-25",
      bestBefore: null,
      locationId: 11,
      fullCode: "OSL-A-01",
      quantity: 10,
      status: "quarantine",
      expired: false,
      serial: false,
    },
    {
      lotId: 22,
      lotNumber: "A1",
      expiry: null,
      bestBefore: null,
      locationId: 11,
      fullCode: "OSL-A-01",
      quantity: 5,
      status: "available",
      expired: false,
      serial: false,
    },
  ],
  short: 2,
  ...overrides,
});

const delivery = (overrides: Partial<Delivery> = {}): Delivery => ({
  id: 7002,
  number: 13,
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
  lines: [trackedLine()],
  ...overrides,
});

const server = (
  current: Delivery,
  writes: (call: Call) => Response | undefined = () => undefined,
  metaOverrides: Record<string, unknown> = {},
) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === deliveryUrl) return jsonResponse(200, current);
    if (call.method !== "GET") return writes(call);
    return commonAnswers(call, settings(), meta({ stockKept: true, ...metaOverrides }));
  });

describe("a delivery's lots", () => {
  it("shows the proposed lots and what they leave short, and blocks the post until they add up", async () => {
    server(delivery());
    renderRoute("/orders/deliveries/7002");

    const lots = await screen.findByTestId("delivery-lots-1");
    expect(within(lots).getByText("B2")).toBeInTheDocument();
    expect(within(lots).getByText("OSL-A-02")).toBeInTheDocument();
    expect(within(lots).getByTestId("lots-short")).toHaveTextContent("Short by 2");
    expect(screen.getByText("Picked by lot")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Location of line 1" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Post delivery" })).toBeDisabled();
    expect(screen.getByText("Make every line's lots add up to its quantity first")).toBeInTheDocument();
  });

  it("links each lot to its page and its trace in Inventory only for a caller who may read stock (M94)", async () => {
    server(delivery(), undefined, { canTraceLots: true });
    renderRoute("/orders/deliveries/7002");
    const lots = await screen.findByTestId("delivery-lots-1");
    expect(await within(lots).findByRole("link", { name: "B2" })).toHaveAttribute("href", "/inventory/lots/21");
    expect(within(lots).getByRole("link", { name: "Trace lot B2" })).toHaveAttribute(
      "href",
      "/inventory/trace?lotId=21",
    );
  });

  it("names the lots without links for a caller who may not read stock", async () => {
    server(delivery());
    renderRoute("/orders/deliveries/7002");
    const lots = await screen.findByTestId("delivery-lots-1");
    expect(within(lots).getByText("B2")).toBeInTheDocument();
    expect(within(lots).queryByRole("link")).not.toBeInTheDocument();
  });

  it("changes a lot, offering held and expired lots only as the rules allow, and saves the lots chosen", async () => {
    const fake = server(delivery(), (call) =>
      call.method === "PUT" && call.url === deliveryUrl
        ? jsonResponse(200, delivery({ version: 2, updatedAt: "2026-11-05T08:30:00Z" }))
        : undefined,
    );
    renderRoute("/orders/deliveries/7002");

    const lots = await screen.findByTestId("delivery-lots-1");
    expect(within(lots).queryByRole("checkbox", { name: "Use expired lot" })).not.toBeInTheDocument();
    await userEvent.click(within(lots).getByRole("combobox", { name: "Change lot of line 1" }));
    expect(await screen.findByRole("option", { name: /^X0 .*Expired/ })).toHaveAttribute(
      "data-combobox-disabled",
      "true",
    );
    expect(screen.getByRole("option", { name: /^Q4 .*In quarantine/ })).toHaveAttribute(
      "data-combobox-disabled",
      "true",
    );
    await userEvent.click(screen.getByRole("option", { name: /^A1 / }));
    expect(within(lots).queryByTestId("lots-short")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(fake.sent("PUT", deliveryUrl)?.body).toEqual({
        version: 1,
        expectedUpdatedAt: "2026-11-05T08:00:00Z",
        lines: [
          {
            orderLineId: 5002,
            quantity: 6,
            fromLocationId: null,
            lots: [
              { lotId: 21, locationId: 12, quantity: 4 },
              { lotId: 22, locationId: 11, quantity: 2 },
            ],
          },
        ],
      }),
    );
  });

  it("offers Use expired lot to a holder of the override, and sends the expired lot allowed", async () => {
    const fake = server(
      delivery(),
      (call) =>
        call.method === "PUT" && call.url === deliveryUrl
          ? jsonResponse(200, delivery({ version: 2, updatedAt: "2026-11-05T08:30:00Z" }))
          : undefined,
      { canOverrideExpired: true },
    );
    renderRoute("/orders/deliveries/7002");

    const lots = await screen.findByTestId("delivery-lots-1");
    await userEvent.click(await within(lots).findByRole("checkbox", { name: "Use expired lot" }));
    await userEvent.click(within(lots).getByRole("combobox", { name: "Change lot of line 1" }));
    await userEvent.click(await screen.findByRole("option", { name: /^X0 / }));
    expect(within(lots).getByText("Expired")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      const body = fake.sent("PUT", deliveryUrl)?.body as { lines: { lots: unknown[] }[] } | undefined;
      expect(body?.lines.at(0)?.lots).toEqual([
        { lotId: 21, locationId: 12, quantity: 4 },
        { lotId: 23, locationId: 11, quantity: 2, allowExpired: true },
      ]);
    });
  });

  it("proposes a line's lots again, saving the line with Propose again and no lots", async () => {
    const fake = server(delivery(), (call) =>
      call.method === "PUT" && call.url === deliveryUrl
        ? jsonResponse(200, delivery({ version: 2, updatedAt: "2026-11-05T08:30:00Z" }))
        : undefined,
    );
    renderRoute("/orders/deliveries/7002");

    await userEvent.click(await screen.findByRole("button", { name: "Propose the lots of line 1 again" }));
    await waitFor(() =>
      expect(fake.sent("PUT", deliveryUrl)?.body).toEqual({
        version: 1,
        expectedUpdatedAt: "2026-11-05T08:00:00Z",
        lines: [{ orderLineId: 5002, quantity: 6, fromLocationId: null, proposeAgain: true }],
      }),
    );
  });

  it("names a save's refused lot on the row it was sent from, past a lot left at 0", async () => {
    server(delivery(), (call) =>
      call.method === "PUT" && call.url === deliveryUrl
        ? refusal(400, "lot_unknown", {
            lines: [{ index: 0, code: "lot_unknown", detail: "Lot 22 holds none.", lotIndex: 0, lotId: 22 }],
          })
        : undefined,
    );
    renderRoute("/orders/deliveries/7002");

    const lots = await screen.findByTestId("delivery-lots-1");
    await userEvent.click(within(lots).getByRole("combobox", { name: "Change lot of line 1" }));
    await userEvent.click(await screen.findByRole("option", { name: /^A1 / }));
    const b2 = within(lots).getByRole("textbox", { name: "Quantity of lot B2 on line 1" });
    await userEvent.clear(b2);
    await userEvent.type(b2, "0");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    const alert = await within(lots).findByRole("alert");
    expect(alert).toHaveTextContent("That lot does not hold the item at that location.");
    expect(alert.closest("tr")).toHaveTextContent("A1");
  });

  it("names a refused lot on its row", async () => {
    const covered = delivery({ lines: [trackedLine({ quantity: 4, ordered: 4, open: 4, short: 0 })] });
    server(covered, (call) =>
      call.url === `${deliveryUrl}/post`
        ? refusal(409, "lot_blocked", {
            lines: [{ index: 0, code: "lot_blocked", detail: "Lot B2 at OSL-A-02: on hold.", lotIndex: 0, lotId: 21 }],
          })
        : undefined,
    );
    renderRoute("/orders/deliveries/7002");

    await userEvent.click(await screen.findByRole("button", { name: "Post delivery" }));
    const lots = await screen.findByTestId("delivery-lots-1");
    expect(await within(lots).findByRole("alert")).toHaveTextContent("The lot is on hold.");
  });

  it("lists an order's deliveries with their lots", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === "/api/v1/orders/orders/2001")
        return jsonResponse(
          200,
          order({
            siteId: 1,
            status: "confirmed",
            lines: [line({ deliveryMethod: "delivery", invoicePolicy: "delivery", qtyReserved: 0, available: 1 })],
          }),
        );
      if (call.method === "GET" && call.url === "/api/v1/orders/sites") return jsonResponse(200, { items: [] });
      if (call.method === "GET" && call.url.startsWith("/api/v1/orders/deliveries?"))
        return jsonResponse(200, {
          items: [
            {
              id: 7002,
              number: 13,
              orderId: 2001,
              orderNumber: 5001,
              customerId: 42,
              customerName: "Acme AS",
              siteId: 1,
              status: "posted",
              lines: 1,
              lots: ["B2", "A1"],
              postedAt: "2026-11-05T09:00:00Z",
              updatedAt: "2026-11-05T09:00:00Z",
            },
          ],
          total: 1,
        });
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/orders/2001");

    const card = await screen.findByTestId("deliveries-card");
    expect(await within(card).findByText("B2, A1")).toBeInTheDocument();
    expect(within(card).getByText("Lots")).toBeInTheDocument();
  });
});
