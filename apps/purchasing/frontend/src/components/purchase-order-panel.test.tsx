import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { PurchasingInvoiceMatch, PurchasingInvoicePanel } from "../api/purchasing";
import { type Call, fakeServer, jsonResponse, refusal } from "../test/api";
import { commonAnswers, line, order } from "../test/fixtures";
import { renderInvoicePanel } from "../test/render";

const matched: PurchasingInvoiceMatch = {
  id: 9001,
  supplierInvoiceId: 7001,
  invoiceNumber: "F-100",
  invoiceDate: "2026-11-15",
  currency: "NOK",
  kind: "invoice",
  state: "posted",
  linePosition: 1,
  orderId: 501,
  orderNumber: 3001,
  orderLineId: 11,
  orderLinePosition: 1,
  name: "Skrue 10 mm",
  unit: "stk",
  quantity: "400",
  orderUnitNet: "0.4550",
  invoiceUnitNet: "0.4550",
  lineNet: "182.00",
  warnings: ["invoiced_over_received"],
};

const panel = (overrides: Partial<PurchasingInvoicePanel> = {}): PurchasingInvoicePanel => ({
  invoiceId: 7001,
  supplierId: 40,
  supplierName: "Skruer AS",
  kind: "invoice",
  number: "F-100",
  invoiceDate: "2026-11-15",
  currency: "NOK",
  state: "draft",
  reference: "PO 3001",
  linesVisible: true,
  lines: [
    { position: 1, text: "Skruer", net: "182.00" },
    { position: 2, text: "Frakt", net: "50.00" },
  ],
  matches: [],
  suggestions: [{ orderId: 501, number: 3001, status: "received", currency: "NOK", netTotal: "455.00", toInvoice: 1 }],
  differences: 0,
  ...overrides,
});

const base = "/api/v1/purchasing/supplier-invoices/7001";

/** A fake whose panel read answers what the last successful write answered, as the server would. */
const serve = (read: PurchasingInvoicePanel, write?: (call: Call) => Response) => {
  let current = read;
  return fakeServer(async (call) => {
    if (call.method === "GET" && call.url === `${base}/match`) return jsonResponse(200, current);
    if (call.method === "GET" && call.url === "/api/v1/purchasing/orders/501")
      return jsonResponse(200, order({ status: "received", lines: [line({ received: "1000", open: "0" })] }));
    if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/orders?"))
      return jsonResponse(200, { items: [], total: 0, page: 1, pageSize: 20 });
    if (call.method !== "GET" && write) {
      const answer = write(call);
      if (answer.ok) current = (await answer.clone().json()) as PurchasingInvoicePanel;
      return answer;
    }
    return commonAnswers(call) ?? new Response(null, { status: 404 });
  });
};

describe("the Purchase order panel on a supplier invoice", () => {
  it("suggests the orders the reference names, and matches all received of one", async () => {
    const server = serve(panel(), () => jsonResponse(200, panel({ matches: [matched], differences: 1 })));
    renderInvoicePanel(7001);
    const suggestions = await screen.findByTestId("panel-suggestions");
    expect(suggestions).toHaveTextContent("Purchase order 3001");
    expect(suggestions).toHaveTextContent("1 line to invoice");
    await userEvent.click(within(suggestions).getByRole("button", { name: "Match all received" }));
    await waitFor(() => expect(server.sent("POST", `${base}/match-all`)?.body).toEqual({ orderId: 501 }));
    expect(await screen.findByTestId("panel-matches")).toHaveTextContent("1. Skruer");
    expect(screen.getByTestId("panel-differences")).toHaveTextContent("1 difference against the order.");
    expect(screen.getByTestId("match-warning")).toHaveTextContent("Invoiced more than received");
  });

  it("unmatches one line", async () => {
    const server = serve(panel({ matches: [matched] }), () => jsonResponse(200, panel()));
    renderInvoicePanel(7001);
    const matches = await screen.findByTestId("panel-matches");
    await userEvent.click(within(matches).getByRole("button", { name: "Unmatch" }));
    await waitFor(() => expect(server.sent("POST", `${base}/unmatch`)?.body).toEqual({ linePosition: 1 }));
  });

  it("allocates an invoice line to an order line, asking before it bills more than is received", async () => {
    const server = serve(panel(), (call) => {
      const body = call.body as { acceptOver?: boolean };
      return body.acceptOver ? jsonResponse(200, panel({ matches: [matched] })) : refusal(409, "over_invoiced");
    });
    renderInvoicePanel(7001);
    const suggestions = await screen.findByTestId("panel-suggestions");
    await userEvent.click(within(suggestions).getByRole("button", { name: "Allocate…" }));
    const allocation = await screen.findByTestId("panel-allocation");
    await userEvent.click(within(allocation).getByRole("combobox", { name: "Order line for invoice line 1" }));
    await userEvent.click(await screen.findByRole("option", { name: /Skrue 10 mm/ }));
    await userEvent.type(within(allocation).getByRole("textbox", { name: "Quantity for invoice line 1" }), "400");
    await userEvent.click(within(allocation).getByRole("button", { name: "Match" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("More than is received");
    await userEvent.click(within(dialog).getByRole("button", { name: "Match anyway" }));
    await waitFor(() => expect(server.count("POST", `${base}/matches`)).toBe(2));
    expect(server.sent("POST", `${base}/matches`)?.body).toEqual({
      orderId: 501,
      lines: [{ linePosition: 1, orderLineId: 11, quantity: "400" }],
      acceptOver: true,
    });
  });

  it("shows only positions and quantities without accounting:read, and no actions without purchasing:invoices-match", async () => {
    serve(
      panel({
        linesVisible: false,
        reference: null,
        lines: [{ position: 1 }, { position: 2 }],
        matches: [{ ...matched, lineNet: null, invoiceUnitNet: null }],
      }),
    );
    renderInvoicePanel(7001, false);
    const matches = await screen.findByTestId("panel-matches");
    expect(matches).toHaveTextContent("400 stk");
    expect(matches).not.toHaveTextContent("Skruer");
    expect(matches).not.toHaveTextContent("Invoice line net");
    expect(screen.queryByRole("button", { name: "Unmatch" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Match all received" })).not.toBeInTheDocument();
  });
});
