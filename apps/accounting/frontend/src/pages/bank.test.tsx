import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { bankAccount, bankLine, meta, stubAccountingApi } from "../test/server";

describe("the bank accounts", () => {
  it("offers the seller's account from Invoices as the first, and adds it", async () => {
    const fetchMock = stubAccountingApi({ bankSuggested: true });
    renderRoute("/accounting/bank");
    expect(await screen.findByText("Add your account from Invoices")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Add it" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText(/Account number/)).toHaveValue("15030012345");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/bank\/accounts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/bank\/accounts$/)[0].body).toMatchObject({
      name: "Fjord Bygg AS",
      accountNumber: "15030012345",
      ledgerAccountId: 1002,
    });
  });

  it("lists each account with its source and its lines not matched", async () => {
    stubAccountingApi({
      bankAccounts: [
        bankAccount(),
        bankAccount({
          id: 7002,
          name: "Kort",
          accountNumber: "97100000001",
          source: "feed",
          feed: { status: "unauthorized", detail: "", lastSyncedAt: null, nextSyncAt: null },
          openLines: 0,
        }),
      ],
    });
    renderRoute("/accounting/bank");
    const table = await screen.findByTestId("bank-accounts");
    expect(within(table).getByText("Driftskonto")).toBeInTheDocument();
    expect(within(table).getByText(/Bank feed · the bank refused the key/)).toBeInTheDocument();
  });

  it("previews a CSV file with the mapping before it is saved", async () => {
    const fetchMock = stubAccountingApi({ bankAccounts: [bankAccount()] });
    renderRoute("/accounting/bank/7001/settings");
    const card = await screen.findByTestId("bank-mapping");
    const input = card.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await userEvent.upload(input, new File(["Dato;Tekst;Beløp\n"], "utskrift.csv", { type: "text/csv" }));
    await userEvent.click(within(card).getByRole("button", { name: "Preview" }));
    expect(await screen.findByTestId("bank-csv-preview")).toHaveTextContent("Innskudd");
    await userEvent.click(within(card).getByRole("button", { name: "Save the mapping" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/csv-mapping$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/csv-mapping$/)[0].body).toMatchObject({ delimiter: ";", encoding: "iso-8859-1" });
  });
});

describe("importing a statement", () => {
  it("says what was imported, what was there already and which accounts are not here", async () => {
    stubAccountingApi({ bankAccounts: [bankAccount()] });
    renderRoute("/accounting/bank/import");
    await screen.findByText("Statement file");
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await userEvent.upload(input, new File(["<Document/>"], "camt053.xml", { type: "application/xml" }));
    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    const result = await screen.findByTestId("bank-import-result");
    expect(within(result).getByText("Driftskonto")).toBeInTheDocument();
    expect(within(result).getByText("Gap before it")).toBeInTheDocument();
    expect(within(result).getByText(/30001234567/)).toBeInTheDocument();
  });
});

describe("reconciling", () => {
  it("accepts a suggestion by its key, and shows what it would post first", async () => {
    const fetchMock = stubAccountingApi({ bankAccounts: [bankAccount()], bankLines: [bankLine()] });
    renderRoute("/accounting/bank/7001");
    expect(await screen.findByText("The bank rule Bankgebyr: to account 7770.")).toBeInTheDocument();
    expect(screen.getByTestId("bank-proposed")).toHaveTextContent("7770");
    expect(screen.getByTestId("bank-foot")).toHaveTextContent("Difference");
    await userEvent.click(screen.getByRole("button", { name: "Accept: A bank rule" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/bank\/lines\/8101\/accept$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/bank\/lines\/8101\/accept$/)[0].body).toEqual({ key: "rule:1:1" });
  });

  it("codes a line in øre, balanced against the bank's amount", async () => {
    const fetchMock = stubAccountingApi({
      bankAccounts: [bankAccount()],
      bankLines: [bankLine({ id: 8102, amount: "-349.00", text: "ADOBE", suggestions: [] })],
    });
    renderRoute("/accounting/bank/7001");
    await userEvent.click(await screen.findByRole("button", { name: "Code it" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Balanced")).toBeInTheDocument();
    await userEvent.click(within(dialog).getAllByLabelText("Account")[0]);
    await userEvent.click(await screen.findByRole("option", { name: "6300 Leie av lokaler" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Post" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/bank\/lines\/8102\/code$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/bank\/lines\/8102\/code$/)[0].body).toMatchObject({
      entry: "gross",
      lines: [{ accountId: 1003, amount: "349.00", taxCodeId: null }],
    });
  });

  it("points a customer payment to Invoices and offers no coding", async () => {
    stubAccountingApi({
      bankAccounts: [bankAccount()],
      bankLines: [
        bankLine({
          id: 8103,
          amount: "2000.00",
          kid: "0000104016",
          customerPayment: true,
          suggestions: [
            {
              key: "invoices",
              kind: "invoices",
              certainty: "likely",
              exact: false,
              reason: "A customer payment that Invoices has not registered.",
              postings: [],
              lines: [],
              supplierInvoiceId: null,
              otherLineId: null,
              bankRuleId: null,
              currency: null,
              amountCurrency: null,
              needsAmountCurrency: false,
              fee: null,
              feeMax: null,
            },
          ],
        }),
      ],
    });
    renderRoute("/accounting/bank/7001");
    expect(await screen.findByRole("link", { name: "Open Invoices" })).toHaveAttribute("href", "/invoices/payments");
    expect(screen.queryByRole("button", { name: "Code it" })).not.toBeInTheDocument();
  });

  it("undoes a match after a confirmation, and accepts the exact ones together", async () => {
    const matched = bankLine({
      id: 8104,
      state: "matched",
      suggestions: [],
      match: {
        id: 1,
        kind: "rule",
        reason: "The bank rule Bankgebyr",
        voucherId: 5002,
        matchedAt: "2026-11-14T09:00:00Z",
        matchedByUserId: "11111111-1111-1111-1111-111111111111",
        postings: [],
      },
    });
    const exact = bankLine({
      id: 8105,
      suggestions: [
        { ...bankLine().suggestions[0], key: "posting:1", kind: "posting", certainty: "exact", exact: true },
      ],
    });
    const fetchMock = stubAccountingApi({ bankAccounts: [bankAccount()], bankLines: [matched, exact] });
    renderRoute("/accounting/bank/7001");
    await userEvent.click(await screen.findByRole("button", { name: "Undo" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Undo" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/bank\/lines\/8104\/undo$/)).toHaveLength(1));
    await userEvent.click(screen.getByRole("button", { name: "Accept the 1 exact ones" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Accept them" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/bank\/lines\/accept-exact$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/bank\/lines\/accept-exact$/)[0].body).toEqual({ lineIds: [8105] });
  });
});

describe("what the bank does not say", () => {
  const suggestion = bankLine().suggestions[0];

  it("asks a Vipps payout's fee from the settlement report", async () => {
    const fetchMock = stubAccountingApi({
      bankAccounts: [bankAccount()],
      bankLines: [
        bankLine({
          id: 8201,
          amount: "1234.00",
          text: "Utbetaling Vipps",
          suggestions: [
            {
              ...suggestion,
              key: "vipps:nofee",
              kind: "vipps_payout",
              certainty: "possible",
              reason: "No run of settlement days' sales comes to it.",
              fee: "0.00",
              feeMax: "61.70",
              bankRuleId: null,
            },
          ],
        }),
      ],
    });
    renderRoute("/accounting/bank/7001");
    await userEvent.click(await screen.findByRole("button", { name: "Accept: A Vipps payout" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/At most/)).toHaveTextContent("61.70");
    const fee = within(dialog).getByLabelText("Vipps' fee");
    await userEvent.clear(fee);
    await userEvent.type(fee, "16");
    await userEvent.click(within(dialog).getByRole("button", { name: "Accept" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/bank\/lines\/8201\/accept$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/bank\/lines\/8201\/accept$/)[0].body).toEqual({
      key: "vipps:nofee",
      fee: "16.00",
    });
  });

  it("asks the amount in the invoice's currency a part payment settles", async () => {
    const fetchMock = stubAccountingApi({
      bankAccounts: [bankAccount()],
      bankLines: [
        bankLine({
          id: 8202,
          amount: "-470.00",
          text: "Cloud GmbH INV-77",
          suggestions: [
            {
              ...suggestion,
              key: "supplier:1001",
              kind: "supplier_invoice",
              certainty: "possible",
              reason: "Needs the amount in EUR the payment settles.",
              currency: "EUR",
              amountCurrency: "40.87",
              needsAmountCurrency: true,
              supplierInvoiceId: 1001,
              bankRuleId: null,
            },
          ],
        }),
      ],
    });
    renderRoute("/accounting/bank/7001");
    await userEvent.click(await screen.findByRole("button", { name: "Accept: Pay a supplier invoice" }));
    const dialog = await screen.findByRole("dialog");
    const amount = within(dialog).getByLabelText("Amount settled in EUR");
    await userEvent.clear(amount);
    await userEvent.type(amount, "40");
    await userEvent.click(within(dialog).getByRole("button", { name: "Accept" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/bank\/lines\/8202\/accept$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/bank\/lines\/8202\/accept$/)[0].body).toEqual({
      key: "supplier:1001",
      amountCurrency: "40.00",
    });
  });

  it("sets a possible duplicate aside, offering it no match", async () => {
    const fetchMock = stubAccountingApi({
      bankAccounts: [bankAccount()],
      bankLines: [bankLine({ id: 8203, possibleDuplicateOf: 8101, source: "csv", suggestions: [] })],
    });
    renderRoute("/accounting/bank/7001");
    const card = await screen.findByTestId("bank-possible-duplicate");
    expect(screen.queryByRole("button", { name: "Code it" })).not.toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Set aside as a duplicate" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/bank\/lines\/8203\/duplicate$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/bank\/lines\/8203\/duplicate$/)[0].body).toEqual({ duplicate: true });
  });

  it("says when an import holds possible duplicates", async () => {
    stubAccountingApi({ bankAccounts: [bankAccount()] });
    renderRoute("/accounting/bank/import");
    await screen.findByText("Statement file");
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await userEvent.upload(input, new File(["<Document/>"], "camt053.xml", { type: "application/xml" }));
    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    const result = await screen.findByTestId("bank-import-result");
    expect(within(result).getByText(/2 lines may repeat lines already imported/)).toBeInTheDocument();
  });
});

describe("locking a month the bank does not reconcile", () => {
  it("warns, and locks only when confirmed", async () => {
    const fetchMock = stubAccountingApi({ bankUnreconciled: true, meta: meta() });
    renderRoute("/accounting/settings");
    await userEvent.type(await screen.findByLabelText(/Lock through the end of/), "2026-10");
    await userEvent.click(screen.getByRole("button", { name: "Lock" }));
    expect(await screen.findByText("The bank is not reconciled")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Lock anyway" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/lock$/)).toHaveLength(2));
    expect(sent(fetchMock, "PUT", /\/lock$/)[1].body).toEqual({
      lockedThrough: "2026-10-31",
      reason: null,
      confirmUnreconciled: true,
    });
  });
});
