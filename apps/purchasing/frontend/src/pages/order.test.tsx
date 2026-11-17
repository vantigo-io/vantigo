import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { PurchaseOrder, PurchasingMeta } from "../api/purchasing";
import { type Call, fakeServer, jsonResponse, refusal } from "../test/api";
import { commonAnswers, draft, line, order } from "../test/fixtures";
import { renderRoute } from "../test/render";

const serve = (
  read: PurchaseOrder,
  { metaOverrides = {}, write }: { metaOverrides?: Partial<PurchasingMeta>; write?: (call: Call) => Response } = {},
) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === `/api/v1/purchasing/orders/${read.id}`) return jsonResponse(200, read);
    if (call.method !== "GET" && write) return write(call);
    return commonAnswers(call, metaOverrides);
  });

const actions = async () => within(await screen.findByTestId("order-actions"));
const names = (box: ReturnType<typeof within>) => box.getAllByRole("button").map((b: HTMLElement) => b.textContent);

describe("the purchase order page", () => {
  it("offers a draft Edit, Send, Mark as sent, Copy, the PDF preview and Delete draft", async () => {
    serve(draft());
    renderRoute("/purchasing/orders/501");
    const box = await actions();
    await waitFor(() => expect(box.getByRole("button", { name: "Send" })).toBeInTheDocument());
    expect(names(box)).toEqual(["Edit", "Send", "Mark as sent", "Copy", "Preview PDF", "Delete draft"]);
    expect(screen.getByRole("heading", { name: "Draft purchase order" })).toBeInTheDocument();
  });

  it("offers Mark as sent but not Send when sending is not set up", async () => {
    serve(draft(), { metaOverrides: { sendingAvailable: false } });
    renderRoute("/purchasing/orders/501");
    const box = await actions();
    await screen.findByText("Hovedlager");
    expect(box.getByRole("button", { name: "Mark as sent" })).toBeInTheDocument();
    expect(box.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
  });

  it("offers a sent order's actions, and Cancel only while nothing is received", async () => {
    serve(order());
    renderRoute("/purchasing/orders/501");
    const box = await actions();
    expect(names(box)).toEqual([
      "Edit",
      "Record confirmation",
      "Send again",
      "Close",
      "Cancel order",
      "Copy",
      "Download PDF",
    ]);
    expect(within(screen.getByTestId("order-lines")).getByRole("button", { name: "Close line" })).toBeInTheDocument();
  });

  it("hides Cancel once something is received, and offers Reopen on a closed order", async () => {
    serve(order({ status: "partially_received", lines: [line({ received: "100" })] }));
    renderRoute("/purchasing/orders/501");
    const box = await actions();
    expect(box.queryByRole("button", { name: "Cancel order" })).not.toBeInTheDocument();
  });

  it("offers Reopen and Copy on a closed order", async () => {
    serve(order({ status: "closed", lines: [line({ closedAt: "2026-11-16T10:00:00Z" })] }));
    renderRoute("/purchasing/orders/501");
    const box = await actions();
    expect(names(box)).toEqual(["Reopen", "Copy", "Download PDF"]);
  });

  it("shows only the PDF to a reader without the order permissions", async () => {
    serve(order());
    renderRoute("/purchasing/orders/501", { canManageOrders: false, canSendOrders: false });
    const box = await actions();
    expect(names(box)).toEqual(["Download PDF"]);
    expect(screen.queryByRole("button", { name: "Close line" })).not.toBeInTheDocument();
  });

  it("sends a draft to the supplier's address with a request id and the order as read", async () => {
    const sent = order();
    const fake = serve(draft(), { write: () => jsonResponse(200, { order: sent }) });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Send" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: /^To/ })).toHaveValue("ordre@skruer.no");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Copy to/ }), "lager@firma.no");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    await screen.findByText("The order is sent");
    const body = fake.sent("POST", "/api/v1/purchasing/orders/501/send")?.body as Record<string, unknown>;
    expect(body).toMatchObject({
      recipient: "ordre@skruer.no",
      cc: ["lager@firma.no"],
      expectedUpdatedAt: "2026-11-16T09:00:00Z",
    });
    expect(body.clientRequestId).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("says a refused mail in the reader's words, the order left sent", async () => {
    serve(draft(), { write: () => refusal(503, "mail_failed") });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Send" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Send" }));
    expect(
      await screen.findByText("The order is sent, but the mail provider did not take the e-mail. It is retried."),
    ).toBeInTheDocument();
  });

  it("cancels a mailed order with the supplier told, unless the tick is taken off", async () => {
    const fake = serve(order(), { write: () => jsonResponse(200, { order: order({ status: "cancelled" }) }) });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Cancel order" }));
    const dialog = await screen.findByRole("dialog");
    const tick = within(dialog).getByRole("checkbox", { name: "Send the supplier the cancellation" });
    expect(tick).toBeChecked();
    await userEvent.click(tick);
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel order" }));
    await screen.findByText("The order is cancelled");
    expect(fake.sent("POST", "/api/v1/purchasing/orders/501/cancel")?.body).toEqual({
      expectedUpdatedAt: "2026-11-16T09:00:00Z",
      notifySupplier: false,
    });
  });

  it("closes one line as the order was read", async () => {
    const fake = serve(order(), { write: () => jsonResponse(200, order()) });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(within(await screen.findByTestId("order-lines")).getByRole("button", { name: "Close line" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Close line" }));
    await screen.findByText("The line is closed");
    expect(fake.sent("POST", "/api/v1/purchasing/orders/501/lines/11/close")?.body).toEqual({
      expectedUpdatedAt: "2026-11-16T09:00:00Z",
    });
  });

  it("records the supplier's reference and confirmed day", async () => {
    const fake = serve(order(), { write: () => jsonResponse(200, order({ supplierReference: "SO-9" })) });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Record confirmation" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "The supplier's reference" }), "SO-9");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await screen.findByText("The confirmation is recorded");
    expect(fake.sent("PUT", "/api/v1/purchasing/orders/501/confirmation")?.body).toEqual({
      expectedUpdatedAt: "2026-11-16T09:00:00Z",
      supplierReference: "SO-9",
      confirmedOn: null,
      lines: [{ lineId: 11, confirmedOn: null }],
    });
  });

  it("copies an order into a new draft and opens it", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "POST" && call.url === "/api/v1/purchasing/orders/501/copy")
        return jsonResponse(201, draft({ id: 777 }));
      if (call.url === "/api/v1/purchasing/orders/501") return jsonResponse(200, order());
      if (call.url === "/api/v1/purchasing/orders/777") return jsonResponse(200, draft({ id: 777 }));
      return commonAnswers(call);
    });
    const { router } = renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Copy" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/orders/777"));
    expect(fake.count("POST", "/api/v1/purchasing/orders/501/copy")).toBe(1);
  });

  it("lists the history: the changes, the stored PDFs and the mailings", async () => {
    fakeServer((call) => {
      if (call.url === "/api/v1/purchasing/orders/501") return jsonResponse(200, order());
      if (call.url === "/api/v1/purchasing/orders/501/history")
        return jsonResponse(200, {
          changes: [
            {
              id: 1,
              at: "2026-11-16T10:00:00Z",
              actor: { displayName: "Kari Innkjøper" },
              actions: [{ action: "updated", field: "expectedOn", from: "2026-11-20", to: "2026-11-22" }],
            },
          ],
          documents: [
            { id: 9, kind: "order", version: 1, sizeBytes: 1000, createdAt: "2026-11-16T09:00:00Z", erased: false },
          ],
          mailings: [
            {
              id: 4,
              kind: "order",
              version: 1,
              recipient: "ordre@skruer.no",
              cc: [],
              subject: "Innkjøpsordre 3001",
              status: "sent",
              attempts: 1,
              createdAt: "2026-11-16T09:00:00Z",
              sentAt: "2026-11-16T09:00:01Z",
            },
          ],
        });
      return commonAnswers(call);
    });
    renderRoute("/purchasing/orders/501");
    expect(await screen.findByText(/Expected day: 2026-11-20 → 2026-11-22/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "PDFs" }));
    expect(within(await screen.findByTestId("order-documents")).getByText("Version 1")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "Mailings" }));
    expect(within(await screen.findByTestId("order-mailings")).getByText("ordre@skruer.no")).toBeInTheDocument();
  });
});
