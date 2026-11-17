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

  it("closes the dialog on a queued mail and says the order is sent, the mail queued", async () => {
    serve(draft(), { write: () => refusal(503, "mail_failed") });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Send" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Send" }));
    expect(
      await screen.findByText(
        "The order is sent, but its e-mail did not go out yet. It is queued and sent automatically.",
      ),
    ).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("retries a send whose answer was lost under the same request id", async () => {
    let attempts = 0;
    const fake = serve(draft(), {
      write: () => (++attempts === 1 ? jsonResponse(500, {}) : jsonResponse(200, { order: order() })),
    });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Send" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    await waitFor(() => expect(fake.count("POST", "/api/v1/purchasing/orders/501/send")).toBe(1));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Send" }));
    await screen.findByText("The order is sent");
    const sends = fake.calls.filter((c) => c.method === "POST" && c.url === "/api/v1/purchasing/orders/501/send");
    expect(sends).toHaveLength(2);
    const ids = sends.map((c) => (c.body as { clientRequestId: string }).clientRequestId);
    expect(ids[0]).toMatch(/^[0-9a-f-]{36}$/);
    expect(ids[1]).toBe(ids[0]);
  });

  it("marks a draft as sent with a request id, the same one on a retry", async () => {
    let attempts = 0;
    const fake = serve(draft(), {
      write: () => (++attempts === 1 ? jsonResponse(500, {}) : jsonResponse(200, { order: order() })),
    });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Mark as sent" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Mark as sent" }));
    await waitFor(() => expect(fake.count("POST", "/api/v1/purchasing/orders/501/mark-sent")).toBe(1));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Mark as sent" }));
    await screen.findByText("The order is marked as sent");
    const marks = fake.calls.filter((c) => c.url === "/api/v1/purchasing/orders/501/mark-sent");
    expect(marks.map((c) => (c.body as { expectedUpdatedAt: string }).expectedUpdatedAt)).toEqual([
      "2026-11-16T09:00:00Z",
      "2026-11-16T09:00:00Z",
    ]);
    expect((marks[1].body as { clientRequestId: string }).clientRequestId).toBe(
      (marks[0].body as { clientRequestId: string }).clientRequestId,
    );
  });

  it("sends a sent order again, revised, or only stores it with the tick taken off", async () => {
    const fake = serve(order(), { write: () => jsonResponse(200, { order: order({ version: 2 }) }) });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Send again" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "Mail the revised order to the supplier" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Send again" }));
    await screen.findByText("A revised order is stored");
    expect(fake.sent("POST", "/api/v1/purchasing/orders/501/send-again")?.body).toEqual({
      mail: false,
      expectedUpdatedAt: "2026-11-16T09:00:00Z",
    });
  });

  it("reopens a closed order as it was read", async () => {
    const closed = order({ status: "closed", lines: [line({ closedAt: "2026-11-16T10:00:00Z" })] });
    const fake = serve(closed, { write: () => jsonResponse(200, order()) });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Reopen" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Reopen" }));
    await screen.findByText("The order is open again");
    expect(fake.sent("POST", "/api/v1/purchasing/orders/501/reopen")?.body).toEqual({
      expectedUpdatedAt: "2026-11-16T09:00:00Z",
    });
  });

  it("deletes a draft as it was read and goes back to the orders", async () => {
    const fake = serve(draft(), { write: () => new Response(null, { status: 204 }) });
    const { router } = renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Delete draft" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Delete draft" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/orders"));
    expect(fake.sent("DELETE", /\/api\/v1\/purchasing\/orders\/501\?expectedUpdatedAt=/)).toBeDefined();
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

  it("says a failed mailing in the reader's words, the provider's detail beside it", async () => {
    fakeServer((call) => {
      if (call.url === "/api/v1/purchasing/orders/501") return jsonResponse(200, order());
      if (call.url === "/api/v1/purchasing/orders/501/history")
        return jsonResponse(200, {
          changes: [],
          documents: [],
          mailings: [
            {
              id: 4,
              kind: "order",
              version: 1,
              recipient: "ordre@skruer.no",
              cc: [],
              subject: "Innkjøpsordre 3001",
              status: "failed",
              attempts: 8,
              lastError: "The mail provider did not take the e-mail. 550 mailbox full",
              createdAt: "2026-11-16T09:00:00Z",
            },
          ],
        });
      return commonAnswers(call);
    });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("tab", { name: "Mailings" }));
    const box = within(await screen.findByTestId("order-mailings"));
    expect(
      box.getByText("The e-mail could not be sent. Send the order again, or download the PDF."),
    ).toBeInTheDocument();
    expect(box.getByText("The mail provider did not take the e-mail. 550 mailbox full")).toBeInTheDocument();
  });
});
