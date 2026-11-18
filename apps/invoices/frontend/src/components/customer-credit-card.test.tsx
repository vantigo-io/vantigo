import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { CustomerCredit } from "../api/credit";
import { jsonResponse, refusal } from "../test/api";
import { type Answer, requestTo } from "../test/document-server";
import { stubFetch } from "../test/fetch";
import { CURRENT_USER_ID, listPage, meta, reminderPolicy } from "../test/fixtures";
import { renderAtHost } from "../test/route-tree";
import { CustomerInvoicesPanel } from "./customer-invoices-panel";

const CREDIT = "/api/v1/invoices/customers/2001/credit?page=1";

/** Acme's credit: 200 kept from invoice 1000, then a refund of 50. */
const acmeCredit = (overrides: Partial<CustomerCredit> = {}): CustomerCredit => ({
  customerId: 2001,
  balances: [{ currency: "NOK", balance: 150 }],
  movements: [
    {
      id: 1002,
      kind: "refunded",
      currency: "NOK",
      amount: -50,
      creditDay: "2026-09-12",
      note: "",
      refundId: 1001,
      reversed: 0,
      registeredAt: "2026-09-12T09:00:00Z",
    },
    {
      id: 1001,
      kind: "overpayment",
      currency: "NOK",
      amount: 200,
      creditDay: "2026-09-10",
      note: "Betalte for mye",
      invoiceId: 1001,
      invoiceNumber: 1000,
      paymentId: 5001,
      reversed: 0,
      registeredAt: "2026-09-10T09:00:00Z",
    },
  ],
  refunds: [
    {
      id: 1001,
      refundedOn: "2026-09-12",
      amount: 50,
      currency: "NOK",
      bankAccount: "15032080119",
      reference: "",
      note: "",
      registeredAt: "2026-09-12T09:00:00Z",
      registeredByUserId: CURRENT_USER_ID,
    },
  ],
  pagination: { page: 1, pageSize: 25, totalCount: 2, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
  ...overrides,
});

/** The fetch fake: meta, the list, the policy, the credit as `credit` gives it, and each write by "METHOD url". */
const server = (credit: () => CustomerCredit = acmeCredit, answers: Record<string, Answer> = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`];
    if (answer) return typeof answer === "function" ? answer() : answer;
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url.startsWith("/api/v1/invoices?")) return jsonResponse(200, listPage());
    if (url.endsWith("/reminder-policy")) return jsonResponse(200, reminderPolicy());
    if (url === CREDIT && method === "GET") return jsonResponse(200, credit());
    return new Response(null, { status: 404 });
  });

const renderPanel = (canAct: boolean) =>
  renderAtHost(
    <CustomerInvoicesPanel
      customerId={2001}
      canCreate={false}
      canChangeReminderPolicy={canAct}
      currentUserId={CURRENT_USER_ID}
    />,
  );

describe("CustomerCreditCard", () => {
  it("shows the balance and every movement with what it came from, and nothing to change without invoices:payments", async () => {
    server();
    renderPanel(false);

    const card = await screen.findByTestId("customer-credit-card");
    expect(within(card).getByTestId("credit-balance-NOK")).toHaveTextContent(
      /The customer has NOK\s?150\.00 in credit\./,
    );
    expect(within(card).getByText("Overpayment of invoice 1000")).toBeInTheDocument();
    expect(within(card).getByText("Betalte for mye")).toBeInTheDocument();
    expect(within(card).getByText("Refunded")).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Record refund" })).not.toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Remove refund" })).not.toBeInTheDocument();
  });

  it("shows nothing for a customer with no credit ever", async () => {
    server(() => acmeCredit({ balances: [], movements: [], refunds: [] }));
    renderPanel(true);

    expect(await screen.findByTestId("reminder-policy-card")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByTestId("customer-credit-card")).not.toBeInTheDocument());
  });

  it("records a refund, and says credit_exceeds_balance with the balance", async () => {
    let calls = 0;
    const fetchMock = server(acmeCredit, {
      "POST /api/v1/invoices/customers/2001/credit/refunds": () =>
        calls++ === 0 ? refusal(409, "credit_exceeds_balance", { balance: 150 }) : jsonResponse(200, acmeCredit()),
    });
    renderPanel(true);

    const card = await screen.findByTestId("customer-credit-card");
    await userEvent.click(within(card).getByRole("button", { name: "Record refund" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Paid from account/ }), "1503.20.80119");
    await userEvent.click(within(dialog).getByRole("button", { name: "Record refund" }));
    expect(await screen.findByText(/That is more than the customer's credit, NOK\s?150\.00\./)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Record refund" }));
    expect(await screen.findByText("The refund is recorded")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/customers/2001/credit/refunds")).toEqual({
      refundedOn: "2026-09-12",
      amount: 150,
      currency: "NOK",
      bankAccount: "15032080119",
    });
  });

  it("removes a refund with a reason", async () => {
    const fetchMock = server(acmeCredit, {
      "POST /api/v1/invoices/customers/2001/credit/refunds/1001/remove": () => jsonResponse(200, acmeCredit()),
    });
    renderPanel(true);

    const card = await screen.findByTestId("customer-credit-card");
    await userEvent.click(within(card).getByRole("button", { name: "Remove refund" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Reason/ }), "Feil konto");
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove refund" }));
    expect(await screen.findByText("The refund is removed")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/customers/2001/credit/refunds/1001/remove")).toEqual({
      reason: "Feil konto",
    });
  });
});
