import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { PurchaseOrder, PurchasingInvoiceMatch, PurchasingInvoiceOrderMatches } from "../api/purchasing";
import { type Call, fakeServer, jsonResponse, refusal } from "../test/api";
import { commonAnswers, line, order } from "../test/fixtures";
import { renderRoute } from "../test/render";

const match = (overrides: Partial<PurchasingInvoiceMatch> = {}): PurchasingInvoiceMatch => ({
  id: 9001,
  supplierInvoiceId: 7001,
  invoiceNumber: "F-100",
  invoiceDate: "2026-11-15",
  currency: "NOK",
  kind: "invoice",
  state: "provisional",
  linePosition: 1,
  orderId: 501,
  orderNumber: 3001,
  orderLineId: 11,
  orderLinePosition: 1,
  name: "Skrue 10 mm",
  unit: "stk",
  quantity: "400",
  orderUnitNet: "0.4550",
  invoiceUnitNet: "0.5000",
  lineNet: "200.00",
  warnings: ["price_differs"],
  ...overrides,
});

const received = order({
  status: "partially_received",
  lines: [line({ received: "400", invoiced: "400", open: "600" })],
});

const serve = (read: PurchaseOrder, invoices: PurchasingInvoiceOrderMatches, write?: (call: Call) => Response) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === `/api/v1/purchasing/orders/${read.id}`) return jsonResponse(200, read);
    if (call.method === "GET" && call.url === `/api/v1/purchasing/orders/${read.id}/invoices`)
      return jsonResponse(200, invoices);
    if (call.method === "GET" && call.url === `/api/v1/purchasing/orders/${read.id}/receipts`)
      return jsonResponse(200, { items: [] });
    if (call.method !== "GET" && write) return write(call);
    return commonAnswers(call);
  });

describe("the supplier invoices on the purchase order page", () => {
  it("shows invoiced per line, and the matched invoices with their state, amounts and warnings", async () => {
    serve(received, { linesVisible: true, matches: [match()] });
    renderRoute("/purchasing/orders/501");
    expect(await screen.findByTestId("line-invoiced")).toHaveTextContent("400 / 1,000");
    const invoice = await screen.findByTestId("order-invoice");
    expect(invoice).toHaveTextContent("Invoice F-100");
    expect(invoice).toHaveTextContent("Provisional");
    expect(invoice).toHaveTextContent("Invoice price per unit");
    expect(within(invoice).getByTestId("match-warning")).toHaveTextContent("Price differs");
  });

  it("shows the matched quantities alone without accounting:read (A20)", async () => {
    serve(received, { linesVisible: false, matches: [match({ lineNet: null, invoiceUnitNet: null })] });
    renderRoute("/purchasing/orders/501");
    const invoice = await screen.findByTestId("order-invoice");
    expect(invoice).toHaveTextContent("400 stk");
    expect(invoice).not.toHaveTextContent("Invoice price per unit");
    expect(invoice).not.toHaveTextContent("Invoice line");
  });

  it("offers Register invoice only with purchasing:invoices-match and accounting:post", async () => {
    serve(received, { linesVisible: true, matches: [] });
    renderRoute("/purchasing/orders/501", { canRegisterInvoices: false });
    await screen.findByTestId("order-invoices");
    expect(screen.queryByRole("button", { name: "Register invoice" })).not.toBeInTheDocument();
  });

  it("registers the invoice, replaying the same request id after a refusal, and says what is left", async () => {
    const billable = order({ status: "received", lines: [line({ received: "1000", open: "0" })] });
    let first = true;
    const server = serve(billable, { linesVisible: true, matches: [] }, (call) => {
      if (call.url !== "/api/v1/purchasing/orders/501/register-invoice") return new Response(null, { status: 404 });
      if (first) {
        first = false;
        return refusal(409, "supplier_account_missing");
      }
      return jsonResponse(201, { supplierInvoiceId: 7002, lines: 200, linesLeft: 3, total: "455.00", currency: "NOK" });
    });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Register invoice" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Supplier's invoice number" }), "F-200");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register invoice" }));
    const error = await within(dialog).findByTestId("register-error");
    expect(error).toHaveTextContent("The supplier has no default account. Set it in Accounting first.");
    expect(within(error).getByRole("link", { name: "Open the supplier in Accounting" })).toBeInTheDocument();
    const firstBody = server.sent("POST", "/api/v1/purchasing/orders/501/register-invoice")?.body as {
      clientRequestId: string;
      invoiceNumber: string;
    };
    expect(firstBody.invoiceNumber).toBe("F-200");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register invoice" }));
    expect(await within(dialog).findByTestId("invoice-registered")).toHaveTextContent(
      "Invoiced in parts: 3 lines are left for another invoice.",
    );
    const second = server.sent("POST", "/api/v1/purchasing/orders/501/register-invoice")?.body as {
      clientRequestId: string;
    };
    expect(second.clientRequestId).toBe(firstBody.clientRequestId);
  });

  it("words Accounting's refusals by their codes", async () => {
    const billable = order({ status: "received", lines: [line({ received: "1000", open: "0" })] });
    serve(billable, { linesVisible: true, matches: [] }, () => refusal(403, "accounting_post_required"));
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Register invoice" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Supplier's invoice number" }), "F-1");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register invoice" }));
    await waitFor(() =>
      expect(within(dialog).getByTestId("register-error")).toHaveTextContent(
        "Registering a supplier invoice needs the right to post in Accounting.",
      ),
    );
  });
});
