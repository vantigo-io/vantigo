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
    await waitFor(() => expect(fake.sent("POST", `${url}/confirm`)?.body).toEqual({ version: 1 }));
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
      expect(fake.sent("POST", `${url}/hold`)?.body).toEqual({ version: 3, reason: "Venter på betaling" }),
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
    await waitFor(() => expect(fake.sent("POST", `${url}/cancel-remaining`)?.body).toEqual({ version: 3 }));
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
    await waitFor(() => expect(fake.sent("POST", `${url}/lock`)?.body).toEqual({ version: 3 }));
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
});
