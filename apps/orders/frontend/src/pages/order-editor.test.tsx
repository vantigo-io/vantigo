import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Order } from "../api/types";
import { type Call, commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { line, order, settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

const url = "/api/v1/orders/orders/2001";

const server = (
  current: Order = order(),
  writes: (call: Call) => Response | undefined = () => undefined,
  changes: unknown[] = [],
) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
    if (call.method === "GET" && call.url === `${url}/changes`) return jsonResponse(200, { items: changes });
    if (call.method !== "GET") return writes(call);
    return commonAnswers(call, settings());
  });

const confirmed = (overrides: Partial<Order> = {}) =>
  order({ status: "confirmed", confirmedAt: "2026-11-02T08:00:00Z", version: 3, ...overrides });

describe("the order editor", () => {
  it("confirms a draft order with the version it read", async () => {
    const fake = server(order(), (call) =>
      call.url === `${url}/confirm` ? jsonResponse(200, confirmed({ updatedAt: "2026-11-05T09:00:00Z" })) : undefined,
    );
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/confirm`)?.body).toEqual({
        version: 1,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
      }),
    );
    expect(await screen.findByText("The order is confirmed")).toBeInTheDocument();
    expect(await screen.findByTestId("order-status")).toHaveTextContent("Confirmed");
  });

  it("offers Confirm only with orders:sales-confirm", async () => {
    server();
    renderRoute("/orders/orders/2001", { canConfirmOrders: false });

    await screen.findByRole("heading", { name: /Order 5001/ });
    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
    expect(screen.getByText("Confirming needs the permission to confirm orders.")).toBeInTheDocument();
  });

  it("says why a confirm was refused, in the reader's words", async () => {
    server(order(), (call) => (call.url === `${url}/confirm` ? refusal(400, "order_empty") : undefined));
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Confirm" }));
    expect(await screen.findByText("The order has no product lines.")).toBeInTheDocument();
  });

  it("shows each line's delivered, invoiced and cancelled quantities on a confirmed order", async () => {
    server(
      confirmed({
        lines: [line({ quantity: 5, qtyDelivered: 2, qtyInvoiced: 1, qtyCancelled: 1 })],
        deliveryStatus: "partial",
      }),
    );
    renderRoute("/orders/orders/2001");

    const progress = await screen.findByTestId("line-progress");
    expect(progress).toHaveTextContent("2 of 5 delivered");
    expect(progress).toHaveTextContent("1 invoiced");
    expect(progress).toHaveTextContent("1 cancelled");
    expect(screen.getByTestId("delivery-status")).toHaveTextContent("Partly delivered");
    // A line with progress is never removed (O26).
    expect(screen.getByRole("button", { name: "Remove line 1" })).toBeDisabled();
  });

  it("registers delivered quantities per line, defaulting to what is left", async () => {
    const fake = server(confirmed({ lines: [line({ quantity: 5, qtyDelivered: 2 })] }), (call) =>
      call.url === `${url}/mark-delivered`
        ? jsonResponse(
            200,
            confirmed({
              version: 4,
              lines: [line({ quantity: 5, qtyDelivered: 5 })],
              deliveryStatus: "delivered",
              updatedAt: "2026-11-05T09:00:00Z",
            }),
          )
        : undefined,
    );
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Register delivered" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Quantity for Kabel 10 m")).toHaveValue("3");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register delivered" }));

    await waitFor(() =>
      expect(fake.sent("POST", `${url}/mark-delivered`)?.body).toEqual({
        version: 3,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
        lines: [{ lineId: 5001, quantity: 3 }],
      }),
    );
    expect(await screen.findByText("The delivery is registered")).toBeInTheDocument();
  });

  it("keeps the delivered dialog open with the refusal when it is too much", async () => {
    server(confirmed(), (call) => (call.url === `${url}/mark-delivered` ? refusal(400, "over_delivery") : undefined));
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Register delivered" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register delivered" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("That is more than is left to deliver.");
  });

  it("puts the order on hold with a reason, and shows it", async () => {
    const fake = server(confirmed(), (call) =>
      call.url === `${url}/hold`
        ? jsonResponse(
            200,
            confirmed({
              version: 4,
              onHold: true,
              holdReason: "Venter på betaling",
              updatedAt: "2026-11-05T09:00:00Z",
            }),
          )
        : undefined,
    );
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Put on hold" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Reason"), "Venter på betaling");
    await userEvent.click(within(dialog).getByRole("button", { name: "Put on hold" }));

    await waitFor(() =>
      expect(fake.sent("POST", `${url}/hold`)?.body).toEqual({
        version: 3,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
        reason: "Venter på betaling",
      }),
    );
    expect(await screen.findByText("Venter på betaling")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Release" })).toBeInTheDocument();
  });

  it("offers Cancel the rest instead of Cancel once something is delivered", async () => {
    const fake = server(confirmed({ lines: [line({ quantity: 5, qtyDelivered: 2 })] }), (call) =>
      call.url === `${url}/cancel-remaining`
        ? jsonResponse(
            200,
            confirmed({
              version: 4,
              lines: [line({ quantity: 5, qtyDelivered: 2, qtyCancelled: 3 })],
              updatedAt: "2026-11-05T09:00:00Z",
            }),
          )
        : undefined,
    );
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "More actions" }));
    expect(screen.queryByRole("menuitem", { name: "Cancel the order" })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Cancel the rest" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel the rest" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/cancel-remaining`)?.body).toEqual({
        version: 3,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
      }),
    );
    expect(await screen.findByText("The rest of the order is cancelled")).toBeInTheDocument();
  });

  it("locks a confirmed order, which then takes no edits", async () => {
    const fake = server(confirmed(), (call) =>
      call.url === `${url}/lock`
        ? jsonResponse(200, confirmed({ version: 4, locked: true, updatedAt: "2026-11-05T09:00:00Z" }))
        : undefined,
    );
    renderRoute("/orders/orders/2001");

    expect(await screen.findByLabelText("Quantity of line 1")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "More actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Lock" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/lock`)?.body).toEqual({ version: 3, expectedUpdatedAt: "2026-11-01T08:00:00Z" }),
    );
    expect(await screen.findByText(/The order is locked: it takes no edits/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Quantity of line 1")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });

  it("saves a confirmed order's changed quantity with its version", async () => {
    const fake = server(confirmed(), (call) =>
      call.method === "PUT"
        ? jsonResponse(200, confirmed({ version: 4, updatedAt: "2026-11-05T09:00:00Z" }))
        : undefined,
    );
    renderRoute("/orders/orders/2001");

    const quantity = await screen.findByLabelText("Quantity of line 1");
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "4");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    const body = fake.sent("PUT", url)?.body as { version: number; lines: Record<string, unknown>[] };
    expect(body.version).toBe(3);
    // An order line is never optional (O21): the flag is not sent.
    expect(body.lines[0]).not.toHaveProperty("optional");
    expect(body.lines[0]).toMatchObject({ id: 5001, quantity: 4 });
  });

  it("links back to the quote it was accepted from, and sends to the invoice e-mail when there is no contact", async () => {
    server(
      confirmed({
        sourceQuoteId: 1001,
        origin: { channel: "quote", sourceKind: "quote", sourceId: "1001@2" },
        defaultRecipient: "faktura@acme.no",
        customer: { ...order().customer, contact: {}, contactId: null },
      }),
    );
    renderRoute("/orders/orders/2001");

    expect(await screen.findByRole("link", { name: "Open the quote" })).toHaveAttribute("href", "/orders/quotes/1001");
    await userEvent.click(screen.getByRole("button", { name: /^Send/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText(/Recipient/)).toHaveValue("faktura@acme.no");
  });

  it("says a stale version as a conflict to reload", async () => {
    server(confirmed(), (call) => (call.method === "PUT" ? refusal(409, "version_conflict") : undefined));
    renderRoute("/orders/orders/2001");

    await userEvent.type(await screen.findByLabelText("Your reference"), "x");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByTestId("conflict-alert")).toBeInTheDocument();
  });

  it("shows the change log, newest first, in words", async () => {
    server(confirmed(), undefined, [
      {
        id: 1,
        version: 2,
        actor: { displayName: "Kari Selger" },
        at: "2026-11-02T09:00:00Z",
        actions: [{ kind: "held" }],
      },
      {
        id: 2,
        version: 3,
        actor: { displayName: "Per Lager" },
        at: "2026-11-03T09:00:00Z",
        actions: [{ kind: "quantity_changed", lineId: 5001, from: "3", to: "4" }],
      },
    ]);
    renderRoute("/orders/orders/2001");

    const log = await screen.findByTestId("change-log");
    await within(log).findByText("Per Lager");
    const text = log.textContent ?? "";
    expect(text.indexOf("Per Lager")).toBeLessThan(text.indexOf("Kari Selger"));
    expect(log).toHaveTextContent("Quantity changed");
    expect(log).toHaveTextContent("Kabel 10 m");
    expect(log).toHaveTextContent("3 → 4");
    expect(log).toHaveTextContent("Put on hold");
  });

  it("shows the done tag on an order delivered and invoiced", async () => {
    server(confirmed({ deliveryStatus: "delivered", invoiceStatus: "invoiced", done: true }));
    renderRoute("/orders/orders/2001");
    expect((await screen.findAllByText("Done")).length).toBeGreaterThan(0);
  });

  it("offers nothing to change to a reader", async () => {
    server(confirmed());
    renderRoute("/orders/orders/2001", { canManageOrders: false, canConfirmOrders: false });

    await screen.findByRole("heading", { name: /Order 5001/ });
    expect(screen.queryByTestId("order-actions")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Quantity of line 1")).not.toBeInTheDocument();
  });
});

describe("invoicing an order", () => {
  it("opens Invoices' From orders wizard with the order chosen", async () => {
    server(confirmed({ lines: [line({ quantity: 5, qtyInvoiced: 1, qtyDraftAllocated: 2 })] }));
    const { router } = renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "Invoice" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/new/from-orders"));
    expect(router.state.location.search).toEqual({ orderId: 2001 });
  });

  it("offers Invoice only with invoices:create, and not when nothing is left or the order is on hold", async () => {
    server(confirmed());
    renderRoute("/orders/orders/2001", { canCreateInvoices: false });
    await screen.findByRole("heading", { name: /Order 5001/ });
    expect(screen.queryByRole("button", { name: "Invoice" })).not.toBeInTheDocument();
  });

  it("holds Invoice back when everything is invoiced or on draft invoices", async () => {
    server(confirmed({ lines: [line({ quantity: 5, qtyInvoiced: 3, qtyDraftAllocated: 2 })] }));
    renderRoute("/orders/orders/2001");
    expect(await screen.findByRole("button", { name: "Invoice" })).toBeDisabled();
  });

  it("shows what draft invoices hold, and keeps that line's price and the line itself", async () => {
    server(confirmed({ lines: [line({ quantity: 5, qtyDraftAllocated: 2 })] }));
    renderRoute("/orders/orders/2001");

    expect(await screen.findByTestId("line-progress")).toHaveTextContent("2 on draft invoices");
    expect(screen.getByRole("button", { name: "Remove line 1" })).toBeDisabled();
    expect(screen.getByLabelText("Unit price of line 1")).toBeDisabled();
  });

  it("lists the invoices issued from the order, linking to Invoices", async () => {
    server(
      confirmed({
        invoiceStatus: "partial",
        invoices: [
          {
            invoiceId: 77,
            invoiceNumber: 10042,
            issueDate: "2026-11-04",
            amount: 1000,
            releasedAmount: 250,
            currency: "NOK",
            state: "invoiced",
          },
          {
            invoiceId: 78,
            invoiceNumber: 10043,
            issueDate: "2026-11-05",
            amount: 0,
            releasedAmount: 300,
            currency: "NOK",
            state: "released",
          },
        ],
      }),
    );
    renderRoute("/orders/orders/2001");

    const card = await screen.findByTestId("invoices-card");
    expect(within(card).getByRole("link", { name: "10042" })).toHaveAttribute("href", "/invoices/77");
    expect(card).toHaveTextContent("Credited");
    expect(card).toHaveTextContent(/250[.,]00 credited/);
  });

  it("names an invoice without a link for a caller who cannot open Invoices", async () => {
    server(
      confirmed({
        invoices: [
          {
            invoiceId: 77,
            invoiceNumber: 10042,
            issueDate: "2026-11-04",
            amount: 1250,
            releasedAmount: 0,
            currency: "NOK",
            state: "invoiced",
          },
        ],
      }),
    );
    renderRoute("/orders/orders/2001", { canViewInvoices: false });

    const card = await screen.findByTestId("invoices-card");
    expect(within(card).getByText("10042")).toBeInTheDocument();
    expect(within(card).queryByRole("link")).not.toBeInTheDocument();
  });

  it("releases the draft allocations after a warning", async () => {
    const fake = server(confirmed({ lines: [line({ quantity: 5, qtyDraftAllocated: 2 })] }), (call) =>
      call.url === `${url}/release-draft-allocations`
        ? jsonResponse(200, confirmed({ lines: [line({ quantity: 5 })], updatedAt: "2026-11-05T09:00:00Z" }))
        : undefined,
    );
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "More actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Release draft allocations" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("will refuse to issue");
    await userEvent.click(within(dialog).getByRole("button", { name: "Release draft allocations" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/release-draft-allocations`)?.body).toEqual({
        version: 3,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
      }),
    );
    expect(await screen.findByText("Released from the draft invoices")).toBeInTheDocument();
  });

  it("says why a cancel was refused while a draft invoice holds the order", async () => {
    server(confirmed({ lines: [line({ quantity: 5, qtyDraftAllocated: 2 })] }), (call) =>
      call.url === `${url}/cancel` ? refusal(409, "order_in_draft_invoice") : undefined,
    );
    renderRoute("/orders/orders/2001");

    await userEvent.click(await screen.findByRole("button", { name: "More actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Cancel the order" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel the order" }));
    expect(await screen.findByText(/Part of the order is on a draft invoice/)).toBeInTheDocument();
  });
});

describe("the new order", () => {
  it("creates the draft for the picked customer and opens it", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "POST" && call.url === "/api/v1/orders/orders") return jsonResponse(201, order({ id: 2077 }));
      if (call.method === "GET" && call.url === "/api/v1/orders/orders/2077")
        return jsonResponse(200, order({ id: 2077 }));
      return commonAnswers(call, settings());
    });
    const { router } = renderRoute("/orders/orders/new");

    await userEvent.click(await screen.findByRole("combobox", { name: "Customer" }));
    await userEvent.click(await screen.findByRole("option", { name: "Bygg AS (10002)" }));
    await userEvent.click(screen.getByRole("button", { name: "Create the order" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/orders/orders/2077"));
    expect(fake.sent("POST", "/api/v1/orders/orders")?.body).toMatchObject({ customerId: 43 });
  });

  it("waits for the product's variants before it lets the order be created", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url.startsWith("/api/v1/orders/products/7/grid"))
        return jsonResponse(200, {
          productId: 7,
          productName: "Chair",
          currency: "NOK",
          mode: "list",
          options: [],
          cells: [],
          variants: [{ variantId: 71, sku: "CH-1", unit: "stk", values: {}, price: 100 }],
        });
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/new?customerId=43&productId=7");

    // Before the variants are read the button is not to be pressed: the order would start without the line.
    expect(await screen.findByRole("button", { name: "Create the order" })).toBeDisabled();
    await waitFor(() => expect(screen.getByRole("button", { name: "Create the order" })).toBeEnabled());
  });

  it("says when the product could not be read, and starts the order empty", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url.startsWith("/api/v1/orders/products/7/grid")) return refusal(500, "failed");
      if (call.method === "POST" && call.url === "/api/v1/orders/orders") return jsonResponse(201, order({ id: 2081 }));
      if (call.method === "GET" && call.url === "/api/v1/orders/orders/2081")
        return jsonResponse(200, order({ id: 2081 }));
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/new?customerId=43&productId=7");

    expect(await screen.findByText("The product could not be read; the order starts empty.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Create the order" }));
    await waitFor(() => expect(fake.sent("POST", "/api/v1/orders/orders")).toBeDefined());
    expect(fake.sent("POST", "/api/v1/orders/orders")?.body).not.toHaveProperty("lines");
  });

  it("takes the only variant of the product a message showed", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url.startsWith("/api/v1/orders/products/7/grid"))
        return jsonResponse(200, {
          productId: 7,
          productName: "Chair",
          currency: "NOK",
          mode: "list",
          options: [],
          cells: [],
          variants: [{ variantId: 71, sku: "CH-1", unit: "stk", values: {}, price: 100 }],
        });
      if (call.method === "POST" && call.url === "/api/v1/orders/orders") return jsonResponse(201, order({ id: 2079 }));
      if (call.method === "GET" && call.url === "/api/v1/orders/orders/2079")
        return jsonResponse(200, order({ id: 2079 }));
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/new?customerId=43&productId=7");

    expect(await screen.findByText("The order starts with one line for Chair.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Create the order" }));
    await waitFor(() =>
      expect(fake.sent("POST", "/api/v1/orders/orders")?.body).toMatchObject({ lines: [{ variantId: 71 }] }),
    );
  });

  it("lets the person pick among the variants of the product a message showed", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url.startsWith("/api/v1/orders/products/7/grid"))
        return jsonResponse(200, {
          productId: 7,
          productName: "Chair",
          currency: "NOK",
          mode: "list",
          options: [],
          cells: [],
          variants: [
            { variantId: 71, sku: "CH-RED", unit: "stk", values: { Colour: "Red" }, price: 100 },
            { variantId: 72, sku: "CH-BLUE", unit: "stk", values: { Colour: "Blue" }, price: 100 },
          ],
        });
      if (call.method === "POST" && call.url === "/api/v1/orders/orders") return jsonResponse(201, order({ id: 2080 }));
      if (call.method === "GET" && call.url === "/api/v1/orders/orders/2080")
        return jsonResponse(200, order({ id: 2080 }));
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/new?customerId=43&productId=7");

    await userEvent.click(await screen.findByRole("combobox", { name: "Start with a variant of Chair" }));
    await userEvent.click(await screen.findByRole("option", { name: "CH-BLUE · Blue" }));
    await userEvent.click(screen.getByRole("button", { name: "Create the order" }));
    await waitFor(() =>
      expect(fake.sent("POST", "/api/v1/orders/orders")?.body).toMatchObject({ lines: [{ variantId: 72 }] }),
    );
  });
});

describe("a goods line's manufacturing (manufacturing design M45, M48a)", () => {
  const goods = (overrides: Partial<Order["lines"][number]> = {}) =>
    line({ deliveryMethod: "delivery", qtyReserved: 0, ...overrides });

  it("offers Create manufacturing order on a line with a BoM and no order, to orders-manage", async () => {
    server(confirmed({ siteId: 1, lines: [goods({ hasBom: true, production: [] })] }));
    const { router } = renderRoute("/orders/orders/2001");

    const create = await screen.findByRole("link", { name: "Create manufacturing order" });
    expect(create).toHaveAttribute("href", "/manufacturing/orders/new?salesOrderLineId=5001");
    await userEvent.click(create);
    await waitFor(() => expect(router.state.location.pathname).toBe("/manufacturing/orders/new"));
  });

  it("does not offer it without manufacturing:orders-manage", async () => {
    server(confirmed({ siteId: 1, lines: [goods({ hasBom: true, production: [] })] }));
    renderRoute("/orders/orders/2001", { canCreateManufacturingOrders: false });

    await screen.findByRole("heading", { name: /Order 5001/ });
    expect(screen.queryByRole("link", { name: "Create manufacturing order" })).not.toBeInTheDocument();
  });

  it("shows the orders made for the line, linking to them, and that the goods are ready to reserve", async () => {
    server(
      confirmed({
        siteId: 1,
        lines: [
          goods({
            hasBom: true,
            production: [
              {
                manufacturingOrderId: 12,
                number: "MO-12",
                status: "done",
                quantity: 3,
                produced: 3,
                siteId: 1,
                plannedFinish: null,
                dueDate: "2026-12-01",
              },
            ],
            readyToReserve: { manufacturingOrderId: 12, produced: 3, siteId: 1, otherSite: false },
          }),
        ],
      }),
    );
    renderRoute("/orders/orders/2001");

    const production = await screen.findByTestId("line-production");
    expect(within(production).getByRole("link", { name: "MO-12" })).toHaveAttribute("href", "/manufacturing/orders/12");
    expect(production).toHaveTextContent("Done");
    expect(production).toHaveTextContent("3 of 3 produced");
    expect(screen.getByTestId("line-ready")).toHaveTextContent("ready to reserve");
    expect(screen.queryByRole("link", { name: "Create manufacturing order" })).not.toBeInTheDocument();
  });

  it("says when the goods were made at another site", async () => {
    server(
      confirmed({
        siteId: 1,
        lines: [goods({ readyToReserve: { manufacturingOrderId: 12, produced: 2, siteId: 2, otherSite: true } })],
      }),
    );
    renderRoute("/orders/orders/2001", { canViewManufacturing: false });

    expect(await screen.findByTestId("line-ready")).toHaveTextContent("another site");
  });
});
