import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse } from "../test/api";
import { documentServer, requestTo } from "../test/document-server";
import { partlyPaid } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const CREDIT = "GET /api/v1/invoices/customers/2001/credit?page=1";

/** Acme's credit of 30 in NOK. */
const credit = (balance = 30) =>
  jsonResponse(200, {
    customerId: 2001,
    balances: [{ currency: "NOK", balance }],
    movements: [],
    refunds: [],
    pagination: { page: 1, pageSize: 25, totalCount: 0, totalPages: 0, hasNextPage: false, hasPreviousPage: false },
  });

describe("the customer's credit on an invoice", () => {
  it("says the credit on an invoice with something open and applies it", async () => {
    const fetchMock = documentServer(() => partlyPaid(), {
      [CREDIT]: () => credit(),
      "POST /api/v1/invoices/1001/apply-credit": () => jsonResponse(200, partlyPaid({ openAmount: 44.99 })),
    });
    renderRoute("/invoices/1001");

    const banner = await screen.findByTestId("credit-banner");
    expect(banner).toHaveTextContent(/The customer has NOK\s?30\.00 in credit\./);
    await userEvent.click(within(banner).getByRole("button", { name: "Use credit" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: "Amount" })).toHaveValue("30");
    await userEvent.click(within(dialog).getByRole("button", { name: "Use credit" }));
    expect(await screen.findByText("The credit is applied to the invoice")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/apply-credit")).toEqual({
      amount: 30,
      paidOn: "2026-09-12",
    });
  });

  it("says nothing of the credit on a paid invoice", async () => {
    documentServer(() => partlyPaid({ state: "paid", paidAmount: 124.99, openAmount: 0 }), {
      [CREDIT]: () => credit(),
    });
    renderRoute("/invoices/1001");

    expect(await screen.findByTestId("payments-card")).toBeInTheDocument();
    expect(screen.queryByTestId("credit-banner")).not.toBeInTheDocument();
  });

  it("offers to keep the rest as credit when the amount is above what is open", async () => {
    const fetchMock = documentServer(() => partlyPaid(), {
      "POST /api/v1/invoices/1001/payments": () => jsonResponse(200, partlyPaid({ state: "paid", openAmount: 0 })),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register payment" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("checkbox")).not.toBeInTheDocument();
    const amount = within(dialog).getByRole("textbox", { name: "Amount" });
    await userEvent.clear(amount);
    await userEvent.type(amount, "100");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /^Keep the rest as credit \(NOK\s?25\.01\)/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));
    await waitFor(() =>
      expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payments")).toEqual({
        paidOn: "2026-09-12",
        amount: 100,
        keepRestAsCredit: true,
      }),
    );
  });
});
