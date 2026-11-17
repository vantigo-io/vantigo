import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { meta, rule, stubAccountingApi, supplier, supplierInvoice, unposted } from "../test/server";

// Delivery C's screens: suppliers with the Brønnøysund lookup, the supplier
// invoice form with its live totals and duplicate warning, Post, a payment
// and Reverse and copy, the expense posting rules and the two new reasons
// under Needs attention.

describe("suppliers", () => {
  it("lists them and adds one from Brønnøysundregisteret", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/suppliers");
    const table = await screen.findByTestId("suppliers");
    expect(within(table).getByRole("link", { name: "Rørlegger Hansen AS" })).toHaveAttribute(
      "href",
      "/accounting/suppliers/3001",
    );
    await userEvent.click(screen.getByRole("button", { name: "New supplier" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("combobox", { name: /Brønnøysundregisteret/ }), "923609016");
    await userEvent.click(await screen.findByRole("option", { name: "EQUINOR ASA (923609016)" }));
    await waitFor(() => expect(within(dialog).getByRole("textbox", { name: /^Name/ })).toHaveValue("EQUINOR ASA"));
    expect(within(dialog).getByRole("textbox", { name: "City" })).toHaveValue("STAVANGER");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/suppliers$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/suppliers$/)[0].body).toMatchObject({
      name: "EQUINOR ASA",
      organisationNumber: "923609016",
      postalCode: "4035",
      city: "STAVANGER",
      paymentTermsDays: 14,
    });
  });

  it("shows a person's retention", async () => {
    stubAccountingApi({ suppliers: [supplier({ isPerson: true, retainedUntil: "2031-12-31" })] });
    renderRoute("/accounting/suppliers/3001");
    expect(await screen.findByText("This supplier is a person")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export the supplier's data" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Anonymise" })).not.toBeInTheDocument();
  });

  it("anonymises a person once the retention has run out", async () => {
    const fetchMock = stubAccountingApi({ suppliers: [supplier({ isPerson: true, retainedUntil: "2020-12-31" })] });
    renderRoute("/accounting/suppliers/3001");
    await userEvent.click(await screen.findByRole("button", { name: "Anonymise" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Anonymised supplier 1/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Anonymise" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/suppliers\/3001\/anonymise$/)).toHaveLength(1));
    expect(await screen.findByText("The supplier is anonymised.")).toBeInTheDocument();
  });
});

describe("a supplier invoice", () => {
  it("computes the totals in øre, warns of a duplicate and saves the draft", async () => {
    const fetchMock = stubAccountingApi({
      supplierInvoices: [supplierInvoice({ id: 4002, state: "posted", invoiceNumber: "F-7" })],
    });
    renderRoute("/accounting/supplier-invoices/new");
    await userEvent.click(await screen.findByRole("combobox", { name: /^Supplier/ }));
    await userEvent.click(await screen.findByRole("option", { name: "1 Rørlegger Hansen AS" }));
    await userEvent.type(screen.getByRole("textbox", { name: /^Supplier's number/ }), "F-7");
    expect(await screen.findByText("This supplier's number is entered already")).toBeInTheDocument();
    await userEvent.clear(screen.getByRole("textbox", { name: /^Supplier's number/ }));
    await userEvent.type(screen.getByRole("textbox", { name: /^Supplier's number/ }), "F-8");
    await userEvent.type(screen.getByRole("textbox", { name: /^Total/ }), "99.99");
    await userEvent.click(screen.getByRole("combobox", { name: "Account, line 1" }));
    await userEvent.click(await screen.findByRole("option", { name: "6300 Leie av lokaler" }));
    await userEvent.click(screen.getByRole("combobox", { name: "Tax code, line 1" }));
    await userEvent.click(await screen.findByRole("option", { name: /^1 Fradragsberettiget/ }));
    await userEvent.type(screen.getByRole("textbox", { name: "Amount, line 1" }), "99.99");
    const totals = screen.getByTestId("supplier-invoice-totals");
    await waitFor(() => expect(within(totals).getByText(/VAT: 20[.,]00/)).toBeInTheDocument());
    expect(within(totals).queryByText(/left against the total/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save draft" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/supplier-invoices$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/supplier-invoices$/)[0].body).toMatchObject({
      supplierId: 3001,
      kind: "invoice",
      invoiceNumber: "F-8",
      currency: "NOK",
      total: "99.99",
      lines: [{ accountId: 1003, taxCodeId: 2001, amount: "99.99", entry: "gross" }],
    });
  });

  it("posts a draft that has its file", async () => {
    const fetchMock = stubAccountingApi({
      supplierInvoices: [
        supplierInvoice({
          files: [
            {
              id: 1,
              fileName: "f.pdf",
              contentType: "application/pdf",
              sizeBytes: 3,
              sha256: "0".repeat(64),
              uploadedAt: "2026-11-01T00:00:00Z",
            },
          ],
        }),
      ],
    });
    renderRoute("/accounting/supplier-invoices/4001");
    await userEvent.click(await screen.findByRole("button", { name: "Post" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/supplier-invoices\/4001\/post$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/supplier-invoices\/4001$/)).toHaveLength(1);
    expect(await screen.findByText("Voucher 2026-000020 is posted.")).toBeInTheDocument();
  });

  it.each([
    ["credit_note_exceeds_balance", "The credit note is more than is left on the invoice it credits."],
    ["credit_note_currency_mismatch", "A credit note is in the currency of the invoice it credits."],
    ["credit_note_invoice_not_posted", "The invoice this credit note credits is no longer posted."],
  ])("says why Post refused a credit note: %s", async (code, words) => {
    stubAccountingApi({
      supplierInvoices: [
        supplierInvoice({
          kind: "credit_note",
          creditsInvoiceId: 4000,
          files: [
            {
              id: 1,
              fileName: "k.pdf",
              contentType: "application/pdf",
              sizeBytes: 3,
              sha256: "0".repeat(64),
              uploadedAt: "2026-11-01T00:00:00Z",
            },
          ],
        }),
      ],
      refusals: { "POST /supplier-invoices/4001/post": { status: 409, code } },
    });
    renderRoute("/accounting/supplier-invoices/4001");
    await userEvent.click(await screen.findByRole("button", { name: "Post" }));
    expect(await screen.findByText(words)).toBeInTheDocument();
  });

  it("says why Reverse and copy was refused while a credit note stands on the invoice", async () => {
    stubAccountingApi({
      supplierInvoices: [
        supplierInvoice({ state: "posted", balance: "-1000.00", voucher: { id: 5010, display: "2026-000020" } }),
      ],
      refusals: {
        "POST /supplier-invoices/4001/reverse-and-copy": { status: 409, code: "supplier_invoice_item_in_use" },
      },
    });
    renderRoute("/accounting/supplier-invoices/4001");
    await userEvent.click(await screen.findByRole("button", { name: "Reverse and copy" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Reason/ }), "Wrong");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reverse and copy" }));
    expect(
      await screen.findByText(/A credit note, a payment or a settlement stands on this invoice/),
    ).toBeInTheDocument();
  });

  it("registers a payment in NOK with the share settled in the currency", async () => {
    const fetchMock = stubAccountingApi({
      supplierInvoices: [
        supplierInvoice({
          state: "posted",
          currency: "EUR",
          exchangeRate: "11.500000",
          total: "100.00",
          totalNok: "1150.00",
          balance: "-1150.00",
          balanceCurrency: "-100.00",
          voucher: { id: 5010, display: "2026-000020" },
        }),
      ],
    });
    renderRoute("/accounting/supplier-invoices/4001");
    await userEvent.click(await screen.findByRole("button", { name: "Register a payment" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: /^Share settled/ })).toHaveValue("100.00");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Amount in NOK/ }), "1160");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register a payment" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/supplier-invoices\/4001\/payments$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/payments$/)[0].body).toMatchObject({
      amountNok: "1160",
      amountCurrency: "100.00",
    });
  });

  it("reverses and copies a posted invoice", async () => {
    const fetchMock = stubAccountingApi({
      supplierInvoices: [
        supplierInvoice({ state: "posted", balance: "-1250.00", voucher: { id: 5010, display: "2026-000020" } }),
      ],
    });
    renderRoute("/accounting/supplier-invoices/4001");
    await userEvent.click(await screen.findByRole("button", { name: "Reverse and copy" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Reason/ }), "Wrong account");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reverse and copy" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/reverse-and-copy$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/reverse-and-copy$/)[0].body).toMatchObject({ reason: "Wrong account" });
  });
});

describe("expense rules and Needs attention", () => {
  it("adds an expense rule with its category and deductibility", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/posting-rules");
    await userEvent.click(await screen.findByRole("button", { name: "Add a rule" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Expenses" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Name/ }), "Meals");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Expense category" }));
    await userEvent.click(await screen.findByRole("option", { name: "Meals" }));
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /VAT deductible/ }));
    await userEvent.click(within(dialog).getByRole("combobox", { name: /^Account/ }));
    await userEvent.click(await screen.findByRole("option", { name: "6300 Leie av lokaler" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/posting-rules$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/posting-rules$/)[0].body).toEqual({
      name: "Meals",
      scope: "expense",
      match: "expense_category",
      expenseCategoryId: 1006,
      vatDeductible: false,
      accountId: 1003,
    });
  });

  it("names an expense rule's category", async () => {
    stubAccountingApi({
      rules: [
        rule({
          id: 1009,
          name: "Materialer",
          scope: "expense",
          match: "expense_category",
          taxCode: null,
          expenseCategoryId: 1001,
          vatDeductible: true,
        }),
      ],
    });
    renderRoute("/accounting/posting-rules");
    expect(await screen.findByText("Materials · VAT deductible")).toBeInTheDocument();
  });

  it("enters the NOK amount of an expense in another currency", async () => {
    const fetchMock = stubAccountingApi({
      meta: meta({ expensesEnabled: true }),
      unposted: [
        unposted({
          stream: "expenses.facts",
          source: { module: "expenses", kind: "entry.approved", id: 41, part: "3" },
          documentId: null,
          documentNumber: null,
          customerId: null,
          customerName: null,
          invoiceId: null,
          amount: null,
          reason: "amount_in_nok_needed",
          orderKey: "entry:41",
          expenseLines: [{ entryId: 41, kind: "outlay", currency: "EUR", gross: "80.00", amountNok: null }],
        }),
      ],
    });
    renderRoute("/accounting/needs-attention");
    const table = await screen.findByTestId("unposted");
    expect(within(table).getByText("In another currency: the amount in NOK is needed.")).toBeInTheDocument();
    await userEvent.click(within(table).getByRole("button", { name: "Enter the amount in NOK" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Expense 41/ }), "920");
    await userEvent.click(within(dialog).getByRole("button", { name: "Enter and post" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/unposted\/7001\/nok-amounts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /nok-amounts$/)[0].body).toEqual({ amounts: [{ entryId: 41, amountNok: "920" }] });
  });

  it("makes a supplier invoice from an expense of that kind, and acknowledges what is informational", async () => {
    const fetchMock = stubAccountingApi({
      unposted: [
        unposted({
          id: 7002,
          stream: "expenses.facts",
          source: { module: "expenses", kind: "entry.approved", id: 51, part: "4" },
          documentId: null,
          documentNumber: null,
          reason: "enter_as_supplier_invoice",
        }),
        unposted({
          id: 7003,
          stream: "expenses.facts",
          source: { module: "expenses", kind: "entry.approved", id: 52, part: "5" },
          documentId: null,
          documentNumber: null,
          reason: "vat_not_deducted",
          informational: true,
          postedAt: "2026-11-14T09:00:00Z",
          postedVoucher: { id: 5012, display: "2026-000022" },
        }),
      ],
    });
    const { router } = renderRoute("/accounting/needs-attention");
    const table = await screen.findByTestId("unposted");
    expect(within(table).getByText("For your information")).toBeInTheDocument();
    await userEvent.click(within(table).getByRole("button", { name: "Acknowledge" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/unposted\/7003\/acknowledge$/)).toHaveLength(1));
    await userEvent.click(within(table).getByRole("button", { name: "Make a supplier invoice" }));
    // The dialog asks whether a supplier made from the expense is a person.
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /A new supplier is a person/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Make a supplier invoice" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/accounting/supplier-invoices/4011"));
    expect(sent(fetchMock, "POST", /\/unposted\/7002\/supplier-invoice$/)[0].body).toEqual({ supplierIsPerson: true });
  });

  it("keeps the invoice of an unapproved expense in view, with a retry", async () => {
    const fetchMock = stubAccountingApi({
      unposted: [
        unposted({
          id: 7004,
          stream: "expenses.facts",
          source: { module: "expenses", kind: "entry.unapproved", id: 81, part: "6" },
          documentId: null,
          documentNumber: null,
          reason: "expense_invoice_unapproved",
          supplierInvoiceId: 4011,
        }),
      ],
    });
    renderRoute("/accounting/needs-attention");
    const table = await screen.findByTestId("unposted");
    expect(within(table).getByText(/was unapproved in Expenses/)).toBeInTheDocument();
    expect(within(table).getByRole("link", { name: "Open the supplier invoice" })).toBeInTheDocument();
    await userEvent.click(within(table).getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/unposted\/7004\/retry$/)).toHaveLength(1));
  });
});

describe("a supplier invoice matched in Purchasing", () => {
  it("shows the order as a badge linking to it, in the list and on the page, and nothing without a match", async () => {
    stubAccountingApi({
      supplierInvoices: [
        supplierInvoice({
          id: 4002,
          state: "posted",
          invoiceNumber: "F-7",
          purchaseMatch: { orders: [{ id: 77, number: "1001" }], differences: 2 },
        }),
        supplierInvoice({ id: 4003, state: "posted", invoiceNumber: "F-8" }),
      ],
    });
    renderRoute("/accounting/supplier-invoices");
    const badges = await screen.findAllByTestId("purchase-match");
    expect(badges).toHaveLength(1);
    expect(within(badges[0]).getByText("Purchase order 1001").closest("a")).toHaveAttribute(
      "href",
      "/purchasing/orders/77",
    );
    expect(within(badges[0]).getByText("2 differences")).toBeInTheDocument();
  });
});
