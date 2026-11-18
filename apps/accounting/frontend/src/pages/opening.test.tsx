import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubAccountingApi } from "../test/server";

describe("The opening balances", () => {
  it("offers the customers' credit in Invoices as credit lines on their credit items", async () => {
    const fetchMock = stubAccountingApi({
      opening: {
        booksStart: "2026-01-01",
        voucher: null,
        canEnter: true,
        locked: false,
        invoicesAvailable: true,
        openInvoices: [],
        openCredits: [{ customerId: 501, customerName: "Acme AS", currency: "NOK", amount: "-200.00" }],
      },
    });
    renderRoute("/accounting/opening");
    await userEvent.click(await screen.findByRole("button", { name: "Add the customers' credit" }));
    await userEvent.click(screen.getByRole("button", { name: "Post the opening balances" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/opening$/)).toHaveLength(1));
    const lines = (sent(fetchMock, "POST", /\/opening$/)[0].body as { lines: Record<string, unknown>[] }).lines;
    expect(lines).toContainEqual(
      expect.objectContaining({
        amount: "-200.00",
        customerId: 501,
        customerCredit: true,
        documentNumber: null,
        sourceDocumentId: null,
      }),
    );
  });
});
