import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { BankTransaction } from "../api/bank";
import { jsonResponse, refusal } from "../test/api";
import { bankServer } from "../test/bank-server";
import { NO_BODY, requestTo } from "../test/document-server";
import { bankTransaction, CURRENT_USER_ID, listPage, OTHER_USER_ID, pageOf } from "../test/fixtures";
import { renderAtHost } from "../test/route-tree";
import { BankTransactionTable } from "./bank-transaction-table";

/** Mounts the table where the host would, and waits for the router to draw it. */
const table = async (lines: BankTransaction[], canAct = true) => {
  const { queryClient } = renderAtHost(
    <BankTransactionTable lines={lines} currency="NOK" canAct={canAct} currentUserId={CURRENT_USER_ID} />,
  );
  await screen.findByText(`Line ${lines[0].lineRef}`);
  return queryClient;
};

/** A line's row, found by its line reference. */
const row = (ref: string) => screen.getByText(`Line ${ref}`).closest("tr") as HTMLElement;

/** The answers of a write, one per call, the last repeated. */
const inTurn = (...answers: (() => Response)[]) => {
  let call = 0;
  return () => answers[Math.min(call++, answers.length - 1)]();
};

describe("BankTransactionTable", () => {
  it("BankTransactionTable_ShowsTextKidDebtorAppliedAndEvents", async () => {
    bankServer();
    await table(
      [
        bankTransaction({
          id: 2001,
          lineRef: "1/2",
          kid: "0010017",
          remittanceText: "Faktura 1000 og purregebyr",
          status: "resolved",
          reason: "exceeds_open",
          resolution: "applied",
          resolvedAt: "2026-09-12T09:00:00Z",
          resolvedBy: OTHER_USER_ID,
          resolutionNote: "Delvis",
          amount: 1250,
          unappliedAmount: 180,
          suggestions: undefined,
          applied: [
            { kind: "payment", id: 5001, invoiceId: 1001, number: 1000, amount: 1000, removed: false },
            { kind: "charge_payment", id: 6001, invoiceId: 1001, number: 1000, amount: 70, removed: false },
            { kind: "payment", id: 5000, invoiceId: 1001, number: 1000, amount: 1250, removed: true },
          ],
          events: [
            {
              id: 1,
              event: "queued",
              reason: "exceeds_open",
              note: "",
              by: CURRENT_USER_ID,
              at: "2026-09-12T08:00:00Z",
            },
            { id: 2, event: "applied", note: "Delvis", by: OTHER_USER_ID, at: "2026-09-12T09:00:00Z" },
          ],
        }),
        bankTransaction({
          id: 2002,
          lineRef: "1/3",
          kid: undefined,
          remittanceText: "Husleie september",
          status: "duplicate",
          reason: undefined,
          suggestions: undefined,
          unappliedAmount: 0,
          possibleDuplicateOf: {
            id: 1900,
            bankFileId: 1000,
            lineRef: "9/1",
            bookedOn: "2026-09-11",
            amount: 1250,
            status: "resolved",
            reversed: true,
            applied: [{ kind: "payment", id: 4001, invoiceId: 1001, number: 1000, amount: 1250, removed: true }],
          },
        }),
      ],
      false,
    );

    // The KID, and the text beside it; the debtor's name and account; the amounts.
    const applied = row("1/2");
    expect(within(applied).getByText("KID 0010017")).toBeInTheDocument();
    expect(within(applied).getByText("Faktura 1000 og purregebyr")).toBeInTheDocument();
    expect(within(applied).getByText("Acme AS")).toBeInTheDocument();
    expect(within(applied).getByText("1503.20.80119")).toBeInTheDocument();
    expect(within(applied).getByText(/^NOK\s?1,250\.00$/)).toBeInTheDocument();
    expect(within(applied).getByText(/^NOK\s?180\.00$/)).toBeInTheDocument();
    expect(within(applied).getByText("Resolved")).toBeInTheDocument();
    expect(within(applied).getByText("More than is owed")).toBeInTheDocument();
    expect(within(applied).getByText("Applied to invoices")).toBeInTheDocument();
    // What it applied, each linked to its invoice; a removed one struck through.
    expect(within(applied).getByRole("link", { name: /^Invoice 1000: NOK\s?1,000\.00$/ })).toHaveAttribute(
      "href",
      "/invoices/1001",
    );
    expect(within(applied).getByRole("link", { name: /^Invoice 1000, charges: NOK\s?70\.00$/ })).toBeInTheDocument();
    const removed = within(applied)
      .getByRole("link", { name: /^Invoice 1000: NOK\s?1,250\.00$/ })
      .closest("p");
    expect(removed).toHaveAttribute("data-removed", "true");
    expect(removed).toHaveStyle({ textDecoration: "line-through" });
    // A line without a KID shows its text alone.
    const text = row("1/3");
    expect(within(text).queryByText(/^KID/)).not.toBeInTheDocument();
    expect(within(text).getByText("Husleie september")).toBeInTheDocument();
    // Without invoices:payments, no action is offered.
    expect(
      screen.queryByRole("button", { name: /^(Reopen|Confirm duplicate|Treat as distinct)/ }),
    ).not.toBeInTheDocument();

    // The details: the resolution and its note, the events in order with who, the twin.
    await userEvent.click(within(applied).getByRole("button", { name: "Details of line 1/2" }));
    const details = await screen.findByTestId("bank-line-details-2001");
    expect(within(details).getByText(/^Applied to invoices .* by another user$/)).toBeInTheDocument();
    expect(within(details).getByText("Note: Delvis")).toBeInTheDocument();
    const events = within(details)
      .getAllByRole("listitem")
      .map((li) => li.textContent);
    expect(events[0]).toMatch(/^Queued .* by you — Reason: More than is owed$/);
    expect(events[1]).toMatch(/^Applied .* by another user — Note: Delvis$/);

    await userEvent.click(within(text).getByRole("button", { name: "Details of line 1/3" }));
    const twin = await screen.findByTestId("bank-line-details-2002");
    expect(
      within(twin).getByText(
        /^It may repeat line 9\/1 of file 1000, booked Sep 11, 2026, NOK\s?1,250\.00; that line is Resolved\./,
      ),
    ).toBeInTheDocument();
    expect(within(twin).getByTestId("twin-reversed")).toHaveTextContent(
      "The bank reversed a payment of that line, which is why its payment is gone.",
    );
    expect(within(twin).getByRole("link", { name: "Open file 1000" })).toHaveAttribute(
      "href",
      "/invoices/payments/files/1000",
    );
  });

  it("ApplyDialog_SplitsAcrossInvoicesAndCharges", async () => {
    const fetchMock = bankServer({
      answers: {
        "GET /api/v1/invoices": jsonResponse(200, listPage()),
        "POST /api/v1/invoices/bank-transactions/1001/apply": inTurn(
          () => refusal(409, "payment_exceeds_open", { invoiceId: 2102, openAmount: 100 }),
          () => refusal(409, "charge_payment_exceeds_outstanding", { chargesOutstanding: 70 }),
          () => refusal(409, "allocation_exceeds_transaction"),
          () => refusal(409, "paid_before_issue"),
          () => refusal(409, "allocation_not_an_invoice"),
          () => refusal(409, "bank_transaction_not_open"),
          () => refusal(409, "bank_transaction_reversed"),
          () => jsonResponse(200, bankTransaction({ status: "resolved", resolution: "applied" })),
        ),
      },
    });
    const queryClient = await table([
      bankTransaction({
        amount: 2500,
        unappliedAmount: 2500,
        suggestedInvoiceId: 2101,
        suggestions: [
          {
            invoiceId: 2101,
            number: 1001,
            customerId: 2001,
            buyerName: "Acme AS",
            openAmount: 1500,
            why: "number_in_text",
          },
          {
            invoiceId: 2102,
            number: 1002,
            customerId: 2001,
            buyerName: "Acme AS",
            openAmount: 500,
            why: "debtor_account",
          },
        ],
      }),
    ]);

    await userEvent.click(screen.getByRole("button", { name: "Apply: line NTF-0617/1/1" }));
    const dialog = await screen.findByRole("dialog", { name: "Apply line NTF-0617/1/1" });
    // The suggestions pre-filled, each up to its open amount, until the line runs out.
    expect(within(dialog).getByLabelText("Principal for invoice 1001")).toHaveValue("1500");
    expect(within(dialog).getByLabelText("Principal for invoice 1002")).toHaveValue("500");
    expect(within(dialog).getByTestId("apply-total")).toHaveTextContent(
      /^Applied NOK\s?2,000\.00 of NOK\s?2,500\.00; NOK\s?500\.00 stays unapplied\.$/,
    );

    // Charges on top: more than the line has left, refused in the form.
    const submit = within(dialog).getByRole("button", { name: "Apply" });
    await userEvent.type(within(dialog).getByLabelText("Charges for invoice 1001"), "{selectall}600");
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      /^That is NOK\s?100\.00 more than is left of this payment\. Lower the amounts\.$/,
    );
    expect(submit).toBeDisabled();

    // Less principal, written with a decimal comma; and an invoice added by its
    // number, paying its charges alone: the totals follow.
    await userEvent.clear(within(dialog).getByLabelText("Principal for invoice 1001"));
    await userEvent.type(within(dialog).getByLabelText("Principal for invoice 1001"), "1234,50");
    await userEvent.type(within(dialog).getByLabelText("Add an invoice by its number"), "1000");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(await within(dialog).findByLabelText("Principal for invoice 1000")).toHaveValue("0");
    await userEvent.type(within(dialog).getByLabelText("Charges for invoice 1000"), "{selectall}70");
    expect(within(dialog).getByTestId("apply-total")).toHaveTextContent(
      /^Applied NOK\s?2,404\.50 of NOK\s?2,500\.00; NOK\s?95\.50 stays unapplied\.$/,
    );
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
    expect(submit).toBeEnabled();

    // Each refusal in words, the dialog left open; the invoice over its open amount named.
    const said = [
      /^The principal for invoice 1002 is more than its open amount, NOK\s?100\.00\.$/,
      /^The payment is more than the reminder charges outstanding, NOK\s?70\.00\./,
      /^The allocations add up to more than is left of this bank line\.$/,
      /^The bank booked this line before the invoice was issued/,
      /^One of the allocations names a document that is not an issued invoice\.$/,
      /^This bank line is not in the queue as this needs it/,
      /^The bank reversed a payment of this bank line/,
    ];
    for (const words of said) {
      await userEvent.click(submit);
      expect(await within(dialog).findByText(words)).toBeInTheDocument();
    }
    expect(within(dialog).queryByText("The server's English.")).not.toBeInTheDocument();

    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    await userEvent.click(submit);
    expect(await screen.findByText("Applied to the invoices")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    // Every read of the module is read again: the queue, the files, the invoices.
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["invoices"] });
    // The decimals kept; charges only where there are some; an invoice paying
    // its charges alone sent with a principal of 0.
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/bank-transactions/1001/apply")).toEqual({
      allocations: [
        { invoiceId: 2101, amount: 1234.5, chargesAmount: 600 },
        { invoiceId: 2102, amount: 500 },
        { invoiceId: 1001, amount: 0, chargesAmount: 70 },
      ],
    });
  });

  it("ReversalDialog_PaymentsOrNoPayment", async () => {
    const reversal = bankTransaction({
      id: 3001,
      lineRef: "R/1",
      direction: "debit",
      reason: "reversal",
      kid: "0010017",
      bookedOn: "2026-09-12",
      unappliedAmount: 0,
      suggestions: undefined,
    });
    const matched = (id: number, amount: number, paymentId: number) =>
      bankTransaction({
        id,
        lineRef: `M/${id}`,
        status: "matched",
        reason: undefined,
        amount,
        bookedOn: "2026-09-10",
        unappliedAmount: 0,
        suggestions: undefined,
        applied: [{ kind: "payment", id: paymentId, invoiceId: 1001, number: 1000, amount, removed: false }],
      });
    // The matched lines of the reversal's account and amount over two pages:
    // the one it reverses is on the second.
    const asked: string[] = [];
    const removedOnly = {
      ...matched(900, 1250, 7000),
      applied: [{ kind: "payment" as const, id: 7000, invoiceId: 1001, number: 1000, amount: 1250, removed: true }],
    };
    const fetchMock = bankServer({
      lines: (query) => {
        asked.push(query.toString());
        if (query.get("status") !== "matched") return [];
        const pages = [[removedOnly, matched(902, 999, 7002)], [matched(901, 1250, 7001)]];
        const page = Number(query.get("page"));
        return {
          ...pageOf(pages[page - 1] ?? []),
          pagination: {
            page,
            pageSize: 100,
            totalCount: 3,
            totalPages: 2,
            hasNextPage: page < 2,
            hasPreviousPage: page > 1,
          },
        };
      },
      answers: {
        "POST /api/v1/invoices/bank-transactions/3001/handle-reversal": inTurn(
          () => jsonResponse(200, { ...reversal, status: "resolved", resolution: "reversal_handled" }),
          () => refusal(409, "reversal_payment_required"),
          () => jsonResponse(200, { ...reversal, status: "resolved", resolution: "reversal_handled" }),
        ),
      },
    });
    await table([reversal]);

    // A reversal is handled, never applied or dismissed.
    expect(screen.queryByRole("button", { name: "Apply: line R/1" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Not a customer payment: line R/1" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Handle reversal: line R/1" }));
    const dialog = await screen.findByRole("dialog", { name: "Handle the reversal on line R/1" });
    // The candidates: the payments of the same account and amount, nothing else.
    const candidate = await within(dialog).findByRole("checkbox", {
      name: /^Invoice 1000: NOK\s?1,250\.00, from line M\/901 booked Sep 10, 2026$/,
    });
    expect(within(dialog).queryByRole("checkbox", { name: /M\/90[02]/ })).not.toBeInTheDocument();
    // Read by the account, the amount and the day, every page of both statuses.
    expect(asked).toEqual(
      expect.arrayContaining([
        "status=matched&account=86011117947&amount=1250&to=2026-09-12&page=1&pageSize=100",
        "status=matched&account=86011117947&amount=1250&to=2026-09-12&page=2&pageSize=100",
        "status=resolved&account=86011117947&amount=1250&to=2026-09-12&page=1&pageSize=100",
      ]),
    );
    expect(within(dialog).getByText(/cannot be undone; register that payment again by hand/)).toBeInTheDocument();
    await userEvent.click(candidate);
    await userEvent.click(within(dialog).getByRole("button", { name: "Handle the reversal" }));
    expect(await screen.findByText("The reversal is handled")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/bank-transactions/3001/handle-reversal")).toEqual({
      removePayments: [{ invoiceId: 1001, paymentId: 7001 }],
    });

    // No payment, and no note: refused in words; with the note, handled.
    await userEvent.click(screen.getByRole("button", { name: "Handle reversal: line R/1" }));
    const again = await screen.findByRole("dialog", { name: "Handle the reversal on line R/1" });
    await userEvent.click(within(again).getByRole("radio", { name: "No payment is removed; the note says why" }));
    await userEvent.click(within(again).getByRole("button", { name: "Handle the reversal" }));
    expect(
      await within(again).findByText(
        "Name the payment the bank took back, or say in the note why no payment is removed.",
      ),
    ).toBeInTheDocument();
    await userEvent.type(within(again).getByLabelText("Note"), "Betalt tilbake utenfor Vantigo");
    await userEvent.click(within(again).getByRole("button", { name: "Handle the reversal" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const bodies = fetchMock.actualCalls
      .filter(([url]) => String(url).endsWith("/handle-reversal"))
      .map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies.slice(1)).toEqual([{ noPayment: true }, { noPayment: true, note: "Betalt tilbake utenfor Vantigo" }]);
  });

  it("ReversalDialog_SaysAFailedReadRatherThanNoPayment", async () => {
    bankServer({
      lines: (query) => (query.get("status") === "resolved" ? jsonResponse(500, { title: "Boom", status: 500 }) : []),
    });
    await table([
      bankTransaction({ id: 3001, lineRef: "R/1", direction: "debit", reason: "reversal", suggestions: undefined }),
    ]);

    await userEvent.click(screen.getByRole("button", { name: "Handle reversal: line R/1" }));
    const dialog = await screen.findByRole("dialog", { name: "Handle the reversal on line R/1" });
    expect(await within(dialog).findByText("Could not load the payments it may reverse")).toBeInTheDocument();
    expect(within(dialog).queryByText(/^No payment from a bank line/)).not.toBeInTheDocument();
  });

  it("QueueActions_DismissConfirmDistinctReopen", async () => {
    const fetchMock = bankServer({
      answers: {
        "POST /api/v1/invoices/bank-transactions/1001/dismiss": inTurn(
          () => refusal(409, "bank_transaction_not_open"),
          () => jsonResponse(200, bankTransaction({ status: "resolved" })),
        ),
        "POST /api/v1/invoices/bank-transactions/1002/confirm-duplicate": inTurn(
          () => refusal(409, "bank_transaction_not_applicable"),
          () => jsonResponse(200, bankTransaction({ id: 1002, status: "resolved" })),
        ),
        "POST /api/v1/invoices/bank-transactions/1002/treat-as-distinct": () =>
          refusal(409, "bank_transaction_reversed"),
        "POST /api/v1/invoices/bank-transactions/1003/reopen": inTurn(
          () => refusal(409, "bank_transaction_applied"),
          () => jsonResponse(200, bankTransaction({ id: 1003 })),
        ),
      },
    });
    await table([
      bankTransaction({ id: 1001, lineRef: "Q/1" }),
      bankTransaction({ id: 1002, lineRef: "Q/2", status: "duplicate", reason: undefined, suggestions: undefined }),
      bankTransaction({ id: 1003, lineRef: "Q/3", status: "resolved", resolution: "not_customer_payment" }),
      // A line the bank reversed a payment of: its money went back, never reopened.
      bankTransaction({
        id: 1004,
        lineRef: "Q/4",
        status: "resolved",
        resolution: "applied",
        events: [
          {
            id: 9,
            event: "reversed",
            note: "Reversed by the bank: line R/1",
            by: OTHER_USER_ID,
            at: "2026-09-12T10:00:00Z",
          },
        ],
      }),
    ]);

    // Each line offers what its state allows, and nothing else.
    const offered = (ref: string) =>
      within(row(ref))
        .queryAllByRole("button", { name: new RegExp(`: line ${ref}$`) })
        .map((b) => b.textContent);
    expect(offered("Q/1")).toEqual(["Apply", "Not a customer payment"]);
    expect(offered("Q/2")).toEqual(["Confirm duplicate", "Treat as distinct"]);
    expect(offered("Q/3")).toEqual(["Reopen"]);
    expect(offered("Q/4")).toEqual([]);

    // Dismiss: a note is required; a refusal in words, the dialog left open.
    await userEvent.click(screen.getByRole("button", { name: "Not a customer payment: line Q/1" }));
    const dismiss = await screen.findByRole("dialog", { name: "Line Q/1 is not a customer payment" });
    expect(within(dismiss).getByRole("button", { name: "Dismiss" })).toBeDisabled();
    await userEvent.type(within(dismiss).getByLabelText(/^Note/), "Vipps-utbetaling");
    await userEvent.click(within(dismiss).getByRole("button", { name: "Dismiss" }));
    expect(
      await within(dismiss).findByText(/^This bank line is not in the queue as this needs it/),
    ).toBeInTheDocument();
    await userEvent.click(within(dismiss).getByRole("button", { name: "Dismiss" }));
    expect(await screen.findByText("Dismissed as not a customer payment")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/bank-transactions/1001/dismiss")).toEqual({
      note: "Vipps-utbetaling",
    });

    // Confirm a duplicate: the note optional.
    await userEvent.click(screen.getByRole("button", { name: "Confirm duplicate: line Q/2" }));
    const confirm = await screen.findByRole("dialog", { name: "Confirm line Q/2 a duplicate" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Confirm duplicate" }));
    expect(await within(confirm).findByText(/^This cannot be done to this bank line/)).toBeInTheDocument();
    await userEvent.click(within(confirm).getByRole("button", { name: "Confirm duplicate" }));
    expect(await screen.findByText("Confirmed a duplicate")).toBeInTheDocument();
    // A JSON body even without a note.
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/bank-transactions/1002/confirm-duplicate")).toEqual({});

    // Treat as distinct and reopen act at once; their refusals in words.
    await userEvent.click(screen.getByRole("button", { name: "Treat as distinct: line Q/2" }));
    expect(
      await screen.findByText(/^The bank reversed a payment of this bank line: the money went back/),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reopen: line Q/3" }));
    expect(await screen.findByText(/^Payments registered from this bank line still stand/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reopen: line Q/3" }));
    expect(await screen.findByText("Back in the queue")).toBeInTheDocument();
    // The two that act at once send no body.
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/bank-transactions/1002/treat-as-distinct")).toBe(NO_BODY);
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/bank-transactions/1003/reopen")).toBe(NO_BODY);
  });
});
