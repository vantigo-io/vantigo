import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { PurchaseOrder, PurchasingReceipt } from "../api/purchasing";
import { type Call, fakeServer, jsonResponse, refusal } from "../test/api";
import { commonAnswers, line, order, receipt, receiptLine } from "../test/fixtures";
import { renderRoute } from "../test/render";

const serve = (read: PurchaseOrder, receipts: PurchasingReceipt[], write?: (call: Call) => Response) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === `/api/v1/purchasing/orders/${read.id}`) return jsonResponse(200, read);
    if (call.method === "GET" && call.url === `/api/v1/purchasing/orders/${read.id}/receipts`)
      return jsonResponse(200, { items: receipts });
    if (call.method !== "GET" && write) return write(call);
    return commonAnswers(call);
  });

const textLine = line({
  id: 12,
  position: 2,
  kind: "text",
  variantId: null,
  sku: null,
  name: "",
  description: "Frakt",
  productType: null,
  packagingId: null,
  packagingName: null,
  packagingQuantity: null,
  open: "0",
});

describe("receiving on the purchase order page", () => {
  it("offers Receive on a sent order with an open goods line, to a holder of purchasing:receive", async () => {
    serve(order(), []);
    const { router } = renderRoute("/purchasing/orders/501");
    const receive = await screen.findByRole("link", { name: "Receive" });
    await userEvent.click(receive);
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/orders/501/receive"));
  });

  it("offers no Receive without the permission, or when no goods line is open", async () => {
    serve(order(), []);
    renderRoute("/purchasing/orders/501", { canReceive: false });
    await screen.findByTestId("order-actions");
    expect(screen.queryByRole("link", { name: "Receive" })).not.toBeInTheDocument();
  });

  it("hides Receive once every goods line is received", async () => {
    serve(order({ status: "received", lines: [line({ received: "1000", open: "0" })] }), []);
    renderRoute("/purchasing/orders/501");
    await screen.findByTestId("order-actions");
    expect(screen.queryByRole("link", { name: "Receive" })).not.toBeInTheDocument();
  });

  it("lists the receipts with who, the delivery note, the lines, the lot and the movement", async () => {
    serve(order({ status: "partially_received" }), [
      receipt(),
      receipt({
        id: 701,
        number: 9000,
        status: "undone",
        undoneAt: "2026-11-16T12:00:00Z",
        undoneBy: { id: "00000000-0000-4000-8000-000000000002", displayName: "Lars Lager" },
        undoNote: "Feil parti",
        reversalMovementId: 4401,
        lines: [
          receiptLine({
            id: 802,
            inPackaging: false,
            quantity: "5",
            enteredQuantity: "5",
            lotNumber: "LOT-42",
            expiry: "2027-05-31",
          }),
        ],
      }),
    ]);
    renderRoute("/purchasing/orders/501");
    const box = within(await screen.findByTestId("order-receipts"));
    const [posted, undone] = box.getAllByTestId("receipt");
    expect(within(posted).getByText("Receipt 9001")).toBeInTheDocument();
    expect(within(posted).getByText("Posted")).toBeInTheDocument();
    expect(within(posted).getByText(/Lars Lager · Delivery note FS-123/)).toBeInTheDocument();
    expect(within(posted).getByText("2 Eske (200 stk)")).toBeInTheDocument();
    expect(within(posted).getByText("HL-A-01")).toBeInTheDocument();
    expect(within(posted).getByRole("link", { name: "Stock movement" })).toHaveAttribute(
      "href",
      "/inventory/movements/4400",
    );
    expect(within(posted).getByRole("button", { name: "Undo" })).toBeInTheDocument();
    expect(within(undone).getByText("Undone")).toBeInTheDocument();
    expect(within(undone).getByText("Lot LOT-42")).toBeInTheDocument();
    expect(within(undone).getByText(/^Expires/)).toBeInTheDocument();
    expect(within(undone).getByText(/Feil parti/)).toBeInTheDocument();
    expect(within(undone).getByRole("link", { name: "Undo movement" })).toHaveAttribute(
      "href",
      "/inventory/movements/4401",
    );
    expect(within(undone).queryByRole("button", { name: "Undo" })).not.toBeInTheDocument();
  });

  it("links no movement when Inventory cannot be opened, and offers no Undo without purchasing:receive", async () => {
    serve(order({ status: "partially_received" }), [receipt()]);
    renderRoute("/purchasing/orders/501", { canReceive: false, canOpenInventoryMovements: false });
    const box = within(await screen.findByTestId("order-receipts"));
    expect(box.getByText("Receipt 9001")).toBeInTheDocument();
    expect(box.queryByRole("link", { name: "Stock movement" })).not.toBeInTheDocument();
    expect(box.queryByRole("button", { name: "Undo" })).not.toBeInTheDocument();
  });

  it("undoes a receipt with a note", async () => {
    const fake = serve(order({ status: "partially_received" }), [receipt()], () =>
      jsonResponse(200, { order: order(), receipt: receipt({ status: "undone" }) }),
    );
    renderRoute("/purchasing/orders/501");
    const box = within(await screen.findByTestId("order-receipts"));
    await userEvent.click(box.getByRole("button", { name: "Undo" }));
    const dialog = await screen.findByRole("dialog", { name: "Undo receipt 9001" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Feil parti");
    await userEvent.click(within(dialog).getByRole("button", { name: "Undo" }));
    expect(await screen.findByText("Receipt 9001 is undone.")).toBeInTheDocument();
    expect(fake.sent("POST", "/api/v1/purchasing/receipts/700/undo")?.body).toEqual({ note: "Feil parti" });
  });

  it("says in words why an undo is refused", async () => {
    serve(order({ status: "partially_received" }), [receipt()], () => refusal(409, "receipt_invoiced"));
    renderRoute("/purchasing/orders/501");
    const box = within(await screen.findByTestId("order-receipts"));
    await userEvent.click(box.getByRole("button", { name: "Undo" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Undo" }));
    expect(await screen.findByText("Part of the receipt is invoiced, so it cannot be undone.")).toBeInTheDocument();
  });

  it("marks a text line delivered on the order as read, and shows when", async () => {
    const fake = serve(order({ lines: [line(), textLine] }), [], () => jsonResponse(200, order()));
    renderRoute("/purchasing/orders/501");
    const lines = within(await screen.findByTestId("order-lines"));
    await userEvent.click(lines.getByRole("button", { name: "Mark delivered" }));
    expect(await screen.findByText("The line is marked delivered.")).toBeInTheDocument();
    expect(fake.sent("POST", "/api/v1/purchasing/orders/501/lines/12/delivered")?.body).toEqual({
      delivered: true,
      expectedUpdatedAt: "2026-11-16T09:00:00Z",
    });
  });

  it("takes the delivered mark off a delivered line", async () => {
    const fake = serve(
      order({
        status: "received",
        lines: [line({ received: "1000", open: "0" }), { ...textLine, deliveredAt: "2026-11-16T10:00:00Z" }],
      }),
      [],
      () => jsonResponse(200, order()),
    );
    renderRoute("/purchasing/orders/501");
    const lines = within(await screen.findByTestId("order-lines"));
    expect(lines.getByTestId("line-delivered")).toHaveTextContent(/^Delivered/);
    await userEvent.click(lines.getByRole("button", { name: "Not delivered" }));
    await waitFor(() =>
      expect(fake.sent("POST", "/api/v1/purchasing/orders/501/lines/12/delivered")?.body).toMatchObject({
        delivered: false,
      }),
    );
  });
});
