import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { invoicesCatalog } from "../i18n";
import { jsonResponse, refusal } from "../test/api";
import { NO_BODY, pendingResponse, readsOf, requestTo, documentServer as server } from "../test/document-server";
import { OTHER_USER_ID, partlyPaid, reminded, reminderLetter } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

/** The answer of a hold, a lift, a hand-off or its withdrawal: the invoice, and the letters left. */
const holdResult = (lettersLeft: unknown[] = []) => jsonResponse(200, { invoice: reminded(), lettersLeft });

/** A letter's row in the Reminders card, by its number. */
/** The invoice's own read: a write is followed by reading it again. */
const INVOICE = "/api/v1/invoices/1001";

const letterRow = async (sequence: number) => {
  const card = await screen.findByTestId("reminders-card");
  return card.querySelector(`[data-letter="${sequence}"]`) as HTMLElement;
};

// jsdom has no object URLs: a PDF and the CSV make and revoke one.
const { createObjectURL, revokeObjectURL } = URL;
beforeEach(() => {
  URL.createObjectURL = vi.fn(() => "blob:file");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => {
  URL.createObjectURL = createObjectURL;
  URL.revokeObjectURL = revokeObjectURL;
});

describe("RemindersCard_LettersPdfWithdrawRetry", () => {
  it("lists each letter with its status in words and why it waits, failed or was withdrawn", async () => {
    server(() => reminded());
    renderRoute("/invoices/1001");

    const sent = await letterRow(1);
    expect(within(sent).getByText("Letter 1")).toBeInTheDocument();
    expect(within(sent).getByText("Reminder")).toBeInTheDocument();
    expect(within(sent).getByText("Sent")).toBeInTheDocument();
    expect(within(sent).getByText("purring@acme.no")).toBeInTheDocument();
    expect(within(sent).getByText(/NOK\s?111\.19/)).toBeInTheDocument();
    expect(within(sent).getByText(/Fee NOK\s?35\.00\./)).toBeInTheDocument();
    expect(within(sent).getByText("Oct 29, 2026")).toBeInTheDocument();

    const printed = await letterRow(2);
    expect(within(printed).getByText("Printed, not confirmed posted")).toBeInTheDocument();
    expect(within(printed).getByText("In print batch 7")).toBeInTheDocument();
    expect(within(printed).getByText("Paper")).toBeInTheDocument();

    expect(within(await letterRow(3)).getByText("Queued")).toBeInTheDocument();
    expect(screen.getByTestId("letter-status-3")).toHaveTextContent(
      "Waiting: a collection rate it needs has no row for the half-year.",
    );
    expect(within(await letterRow(4)).getByText("Failed")).toBeInTheDocument();
    expect(screen.getByTestId("letter-status-4")).toHaveTextContent(
      "The mail server refused it after 12 attempts: 550 mailbox unavailable",
    );
    // Withdrawn by the module: its code in words. By a person: who, and their own words.
    expect(screen.getByTestId("letter-status-5")).toHaveTextContent("Withdrawn by Vantigo: nothing was left to pay");
    expect(within(await letterRow(6)).getByText("Debt collection notice")).toBeInTheDocument();
    expect(screen.getByTestId("letter-status-6")).toHaveTextContent("Withdrawn by another user: Kunden ringte");
  });

  it("offers the PDF of a printed or sent letter only, and downloads it", async () => {
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this);
    });
    const fetchMock = server(() => reminded(), {
      "GET /api/v1/invoices/reminders/3001/pdf": () =>
        new Response("%PDF-1.7", {
          status: 200,
          headers: {
            "Content-Type": "application/pdf",
            "Content-Disposition": 'attachment; filename="purring-1000-1.pdf"',
          },
        }),
    });
    renderRoute("/invoices/1001");

    await screen.findByTestId("reminders-card");
    expect(screen.getByRole("button", { name: "Download the PDF of Letter 1" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download the PDF of Letter 2" })).toBeInTheDocument();
    for (const n of [3, 4, 5, 6]) {
      expect(screen.queryByRole("button", { name: `Download the PDF of Letter ${n}` })).not.toBeInTheDocument();
    }
    await userEvent.click(screen.getByRole("button", { name: "Download the PDF of Letter 1" }));
    await waitFor(() => expect(clicked).toHaveLength(1));
    expect(clicked[0].download).toBe("purring-1000-1.pdf");
    expect(fetchMock.actualCalls.some(([url]) => String(url) === "/api/v1/invoices/reminders/3001/pdf")).toBe(true);
  });

  it("offers withdraw on a letter not yet sent and retry on a failed one, for invoices:payments", async () => {
    server(() => reminded());
    renderRoute("/invoices/1001");

    await screen.findByTestId("reminders-card");
    for (const n of [2, 3, 4]) expect(screen.getByRole("button", { name: `Withdraw Letter ${n}` })).toBeInTheDocument();
    for (const n of [1, 5, 6]) {
      expect(screen.queryByRole("button", { name: `Withdraw Letter ${n}` })).not.toBeInTheDocument();
    }
    expect(screen.getByRole("button", { name: "Send Letter 4 again" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /again$/ })).toHaveLength(1);
  });

  it("offers neither withdraw nor retry without invoices:payments, but still the PDF", async () => {
    server(() => reminded(), {}, { canRegisterPayments: false });
    renderRoute("/invoices/1001");

    await screen.findByTestId("reminders-card");
    expect(screen.getByRole("button", { name: "Download the PDF of Letter 1" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Withdraw Letter/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /again$/ })).not.toBeInTheDocument();
  });

  it("withdraws a letter with a reason, the button waiting for one", async () => {
    const pending = pendingResponse();
    const fetchMock = server(() => reminded(), {
      "POST /api/v1/invoices/reminders/3002/withdraw": () => pending.response,
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Withdraw Letter 2" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/A printed letter may already be in the post/)).toBeInTheDocument();
    const submit = within(dialog).getByRole("button", { name: "Withdraw" });
    expect(submit).toBeDisabled();
    // Spaces around the reason are not part of it.
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "  Tatt ut av posten  ");
    await userEvent.click(submit);
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Withdraw" })).toBeDisabled());
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/reminders/3002/withdraw")).toEqual({
      reason: "Tatt ut av posten",
    });
    expect(readsOf(fetchMock, INVOICE)).toBe(1);
    pending.answer(jsonResponse(200, reminderLetter({ id: 3002, status: "withdrawn" })));
    expect(await screen.findByText("Letter withdrawn")).toBeInTheDocument();
    // The answer is the letter: the invoice that holds it is read again.
    await waitFor(() => expect(readsOf(fetchMock, INVOICE)).toBeGreaterThan(1));
  });

  it("says reminder_not_withdrawable in words, never the server's English", async () => {
    server(() => reminded(), {
      "POST /api/v1/invoices/reminders/3003/withdraw": () => refusal(409, "reminder_not_withdrawable"),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Withdraw Letter 3" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Feil");
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw" }));
    expect(await screen.findByText(/This letter can no longer be withdrawn/)).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("sends a failed letter again, and says reminder_not_failed in words", async () => {
    let answer = jsonResponse(200, reminderLetter({ id: 3004, status: "queued" }));
    const fetchMock = server(() => reminded(), {
      "POST /api/v1/invoices/reminders/3004/retry": () => answer,
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Send Letter 4 again" }));
    expect(await screen.findByText("The letter is queued to be sent again")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/reminders/3004/retry")).toBe(NO_BODY);

    answer = refusal(409, "reminder_not_failed");
    await userEvent.click(screen.getByRole("button", { name: "Send Letter 4 again" }));
    expect(await screen.findByText("Only a letter that failed is sent again.")).toBeInTheDocument();
  });
});

describe("ChargesCard_FiguresPaymentAndWaive", () => {
  it("shows the charges apart from the principal, with the interest accrued today and every charge payment", async () => {
    server(() => reminded());
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("charges-card");
    expect(within(card).getByTestId("charges-claimed")).toHaveTextContent(/NOK\s?36\.20/);
    expect(within(card).getByTestId("charges-waived")).toHaveTextContent(/NOK\s?0\.00/);
    expect(within(card).getByTestId("charges-paid")).toHaveTextContent(/NOK\s?10\.00/);
    expect(within(card).getByTestId("charges-outstanding")).toHaveTextContent(/NOK\s?26\.20/);
    expect(within(card).queryByTestId("charges-refund-due")).not.toBeInTheDocument();
    expect(within(card).getByTestId("interest-today")).toHaveTextContent(
      /Late interest accrued to today: NOK\s?2\.10\. A figure only/,
    );
    expect(within(card).getByText("Gebyr del 1")).toBeInTheDocument();
  });

  it("registers a charge payment with the outstanding prefilled", async () => {
    const fetchMock = server(() => reminded(), {
      "POST /api/v1/invoices/1001/charge-payments": () => jsonResponse(200, reminded()),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register a charge payment" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: "Amount" })).toHaveValue("26.2");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));
    expect(await screen.findByText("Charge payment registered")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/charge-payments")).toEqual({
      paidOn: "2026-09-12",
      amount: 26.2,
    });
    // The answer carries no send defaults: the invoice is read again.
    await waitFor(() => expect(readsOf(fetchMock, INVOICE)).toBeGreaterThan(1));
  });

  it("offers no charge payment when nothing is outstanding, but still the waiver", async () => {
    server(() => reminded({ charges: { claimed: 36.2, waived: 0, paid: 36.2, outstanding: 0 } }));
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("charges-card");
    expect(within(card).getByTestId("charges-outstanding")).toHaveTextContent(/NOK\s?0\.00/);
    expect(within(card).queryByRole("button", { name: "Register a charge payment" })).not.toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "Waive" })).toBeInTheDocument();
  });

  it("says no interest accrued today when the server gives none", async () => {
    server(() => reminded({ charges: { claimed: 36.2, waived: 0, paid: 10, outstanding: 26.2 } }));
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("charges-card");
    expect(within(card).getByTestId("charges-outstanding")).toBeInTheDocument();
    expect(within(card).queryByTestId("interest-today")).not.toBeInTheDocument();
  });

  it("waives interest by the latest sent letter, and offers exactly the three reasons a person gives", async () => {
    const fetchMock = server(
      () => {
        const doc = reminded();
        // Letter 2 is sent too, with interest of its own: it is the latest that claims it.
        doc.reminders = doc.reminders?.map((r) =>
          r.id === 3002 ? { ...r, status: "sent", sentAt: "2026-11-02T07:00:00Z", printBatchId: undefined } : r,
        );
        return doc;
      },
      { "POST /api/v1/invoices/1001/charges/waive": () => jsonResponse(200, reminded()) },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Waive" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("checkbox", { name: /^The interest letter 1 claimed/ })).not.toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("checkbox", { name: /^The interest letter 2 claimed, NOK\s?1\.80/ }),
    );
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Reason" }));
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["The objection was upheld", "Claimed in error", "Goodwill"]);
    await userEvent.click(screen.getByRole("option", { name: "Goodwill" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Waive" }));
    expect(await screen.findByText("Charges waived")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/charges/waive")).toEqual({
      waivers: [{ reminderId: 3002, kind: "interest" }],
      reason: "goodwill",
    });
  });

  it.each([
    [
      "charge_payment_exceeds_outstanding",
      { chargesOutstanding: 26.2 },
      /The payment is more than the reminder charges outstanding, NOK\s?26\.20\./,
    ],
    ["no_charges_outstanding", {}, /No reminder charge is outstanding on this invoice/],
  ])("says %s in words", async (code, extra, words) => {
    server(() => reminded(), { "POST /api/v1/invoices/1001/charge-payments": () => refusal(409, code, extra) });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register a charge payment" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Register" }));
    expect(await screen.findByText(words)).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("puts a refused amount's words on its input, as the charges' own rule", async () => {
    server(() => reminded(), {
      "POST /api/v1/invoices/1001/charge-payments": () =>
        jsonResponse(400, { title: "Invalid", status: 400, errors: { amount: ["amount must be greater than 0"] } }),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register a charge payment" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Register" }));
    expect(
      await screen.findByText("More than 0 and at most the charges outstanding, with up to two decimals."),
    ).toBeInTheDocument();
  });

  it("waives the sent letter's fee and its interest — the interest as the latest letter's, an amount", async () => {
    const fetchMock = server(() => reminded(), {
      "POST /api/v1/invoices/1001/charges/waive": () => jsonResponse(200, reminded()),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Waive" }));
    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Waive" });
    expect(submit).toBeDisabled();
    // Letter 1 is the one sent; letter 2 is printed, not yet a claim.
    expect(within(dialog).queryByRole("checkbox", { name: /letter 2/ })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /^The fee of letter 1, NOK\s?35\.00$/ }));
    await userEvent.click(
      within(dialog).getByRole("checkbox", {
        name: /^The interest letter 1 claimed, NOK\s?1\.20, less what is already waived or paid of it$/,
      }),
    );
    expect(submit).toBeDisabled();
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Reason" }));
    await userEvent.click(await screen.findByRole("option", { name: "Goodwill" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Waive" }));
    expect(await screen.findByText("Charges waived")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/charges/waive")).toEqual({
      waivers: [
        { reminderId: 3001, kind: "fee" },
        { reminderId: 3001, kind: "interest" },
      ],
      reason: "goodwill",
    });
  });

  it("says charge_not_claimed in words", async () => {
    server(() => reminded(), { "POST /api/v1/invoices/1001/charges/waive": () => refusal(409, "charge_not_claimed") });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Waive" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /^The fee of letter 1/ }));
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Reason" }));
    await userEvent.click(await screen.findByRole("option", { name: "Claimed in error" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Waive" }));
    expect(await screen.findByText(/That charge cannot be waived/)).toBeInTheDocument();
  });

  it("lists every waiver, an interest waiver as the amount it released through its letter's day", async () => {
    server(() =>
      reminded({
        waivers: [
          {
            id: 1,
            reminderId: 3001,
            kind: "fee",
            amount: 35,
            reason: "objection_upheld",
            note: "",
            waivedAt: "2026-10-20T09:00:00Z",
            waivedBy: OTHER_USER_ID,
          },
          {
            id: 2,
            reminderId: 3001,
            kind: "interest",
            amount: 0.7,
            interestThrough: "2026-10-15",
            reason: "deadline_met",
            note: "Betalt i tide",
            waivedAt: "2026-10-21T09:00:00Z",
            waivedBy: OTHER_USER_ID,
          },
        ],
      }),
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("charges-card");
    const interest = within(card)
      .getByText("Interest claimed by Letter 1, through Oct 15, 2026")
      .closest("tr") as HTMLElement;
    expect(within(interest).getByText(/NOK\s?0\.70/)).toBeInTheDocument();
    expect(within(interest).getByText("Paid by the deadline after all (the bank's match)")).toBeInTheDocument();
    const fee = within(card).getByText("The fee of Letter 1").closest("tr") as HTMLElement;
    expect(within(fee).getByText("The objection was upheld")).toBeInTheDocument();
    // The fee is waived, so it is no longer offered; only the interest is.
    await userEvent.click(within(card).getByRole("button", { name: "Waive" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("checkbox", { name: /^The fee of letter 1/ })).not.toBeInTheDocument();
    expect(within(dialog).getByRole("checkbox", { name: /^The interest letter 1 claimed/ })).toBeInTheDocument();
  });

  it("offers neither a charge payment nor a waiver without invoices:payments", async () => {
    server(() => reminded(), {}, { canRegisterPayments: false });
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("charges-card");
    expect(within(card).getByTestId("charges-outstanding")).toBeInTheDocument();
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("DeliveriesCard_RecordAndRemove", () => {
  it("lists the deliveries recorded by hand, a removed one struck through with its reason", async () => {
    server(() => reminded());
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("deliveries-card");
    const live = within(card).getByText("Handed over on Sep 1, 2026").closest("tr") as HTMLElement;
    expect(within(live).getByText(/Recorded .* by you/)).toBeInTheDocument();
    const removed = within(card).getByText("Posted on Sep 2, 2026").closest("tr") as HTMLElement;
    expect(removed).toHaveAttribute("data-removed", "true");
    expect(within(removed).getByText("Removed: Feil faktura")).toBeInTheDocument();
    expect(within(removed).queryByRole("button")).not.toBeInTheDocument();
  });

  it("records a delivery as handed over unless the person says posted", async () => {
    const fetchMock = server(() => reminded(), {
      "POST /api/v1/invoices/1001/manual-deliveries": () => jsonResponse(200, reminded()),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Record a delivery" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Record" }));
    expect(await screen.findByText("Delivery recorded")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/manual-deliveries")).toEqual({
      kind: "handed_over",
      deliveredOn: "2026-09-12",
    });
  });

  it("records a delivery, handed over or posted, with invoices:issue", async () => {
    const fetchMock = server(() => reminded(), {
      "POST /api/v1/invoices/1001/manual-deliveries": () => jsonResponse(200, reminded()),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Record a delivery" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByText("Posted"));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "A-post");
    await userEvent.click(within(dialog).getByRole("button", { name: "Record" }));
    expect(await screen.findByText("Delivery recorded")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/manual-deliveries")).toEqual({
      kind: "posted",
      deliveredOn: "2026-09-12",
      note: "A-post",
    });
  });

  it("says delivery_relied_on in words when a removal is refused", async () => {
    server(() => reminded(), {
      "POST /api/v1/invoices/1001/manual-deliveries/5001/remove": () => refusal(409, "delivery_relied_on"),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Remove the record: Handed over on Sep 1, 2026" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Feil");
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove the delivery record" }));
    expect(
      await screen.findByText(/A reminder claims a charge that rests on this delivery, and no other delivery/),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("offers neither recording nor removing without invoices:issue", async () => {
    server(() => reminded(), {}, { canIssue: false });
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("deliveries-card");
    expect(within(card).getByText("Handed over on Sep 1, 2026")).toBeInTheDocument();
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("HoldCard_PlaceAndLiftDefaultingToObjectionUpheld", () => {
  it("puts the invoice on hold and names the printed letters left, with a way to withdraw them", async () => {
    const fetchMock = server(() => reminded(), {
      "POST /api/v1/invoices/1001/hold": () =>
        holdResult([
          { reminderId: 3002, status: "printed", printBatchId: 7 },
          { reminderId: 3003, status: "queued" },
        ]),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Put on hold" }));
    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Put on hold" });
    expect(submit).toBeDisabled();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "What the customer disputes" }), "Feil timer");
    await userEvent.click(submit);
    expect(await screen.findByText("The invoice is on hold")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/hold")).toEqual({ note: "Feil timer" });
    // The answer's invoice is not set into the cache: the invoice is read again.
    await waitFor(() => expect(readsOf(fetchMock, INVOICE)).toBeGreaterThan(1));

    const left = await screen.findByTestId("letters-left");
    expect(within(left).getByText(/Letter 2 is printed in print batch 7 and may be in the post/)).toBeInTheDocument();
    expect(within(left).getByText("Letter 3 is being sent by e-mail right now and will be sent.")).toBeInTheDocument();
    // Only the printed one can be withdrawn by hand.
    await userEvent.click(within(left).getByRole("button", { name: "Withdraw Letter 2" }));
    expect(await screen.findByRole("dialog", { name: "Withdraw Letter 2" })).toBeInTheDocument();
    expect(within(left).queryByRole("button", { name: "Withdraw Letter 3" })).not.toBeInTheDocument();
  });

  it("lifts the hold with the groundless question answered no unless the person says yes", async () => {
    const fetchMock = server(
      () =>
        reminded({
          hold: {
            id: 1,
            kind: "disputed",
            note: "Feil timer",
            placedAt: "2026-10-20T08:00:00Z",
            placedBy: OTHER_USER_ID,
          },
        }),
      { "POST /api/v1/invoices/1001/hold/lift": () => holdResult() },
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("hold-card");
    expect(within(card).getByText("On hold")).toBeInTheDocument();
    expect(within(card).getByText("Disputed: Feil timer")).toBeInTheDocument();
    expect(within(card).getByText(/Placed .* by another user/)).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Lift the hold" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: "No — it had reasonable grounds" })).toBeChecked();
    expect(within(dialog).getByTestId("lift-consequence")).toHaveTextContent(
      /Every fee and compensation claimed on this invoice is waived/,
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Lift the hold" }));
    expect(await screen.findByText("The hold is lifted")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/hold/lift")).toEqual({ chargesAllowed: false });
  });

  it("keeps the charges only when the person answers yes", async () => {
    const fetchMock = server(
      () =>
        reminded({
          hold: {
            id: 1,
            kind: "disputed",
            note: "Feil timer",
            placedAt: "2026-10-20T08:00:00Z",
            placedBy: OTHER_USER_ID,
          },
        }),
      { "POST /api/v1/invoices/1001/hold/lift": () => holdResult() },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Lift the hold" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Yes — it was obviously groundless" }));
    expect(within(dialog).getByTestId("lift-consequence")).toHaveTextContent("Nothing is waived");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Kunden trakk innsigelsen");
    await userEvent.click(within(dialog).getByRole("button", { name: "Lift the hold" }));
    await screen.findByText("The hold is lifted");
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/hold/lift")).toEqual({
      chargesAllowed: true,
      note: "Kunden trakk innsigelsen",
    });
  });

  it("says what a lift decided: charges barred for good", async () => {
    server(() =>
      reminded({
        hold: {
          id: 1,
          kind: "disputed",
          note: "Feil timer",
          placedAt: "2026-10-20T08:00:00Z",
          placedBy: OTHER_USER_ID,
          liftedAt: "2026-10-25T08:00:00Z",
          liftedBy: OTHER_USER_ID,
          chargesAllowed: false,
        },
        nextAction: { action: "reminder", earliestOn: "2026-10-26", reasons: [], chargeNotes: ["charges_barred"] },
      }),
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("hold-card");
    expect(within(card).getByTestId("hold-outcome")).toHaveTextContent(
      "The objection had reasonable grounds: fees and the compensation are barred on this invoice for good.",
    );
    expect(within(card).getByRole("button", { name: "Put on hold" })).toBeInTheDocument();
    expect(screen.getByTestId("next-action")).toHaveTextContent(
      "No fee or compensation: they are barred on this invoice since a hold was lifted with the objection upheld.",
    );
  });

  it.each([["invoice_on_hold", "POST /api/v1/invoices/1001/hold", /This invoice is on hold already/]])(
    "says %s in words",
    async (code, route, words) => {
      server(() => reminded(), { [route]: () => refusal(409, code) });
      renderRoute("/invoices/1001");

      await userEvent.click(await screen.findByRole("button", { name: "Put on hold" }));
      const dialog = await screen.findByRole("dialog");
      await userEvent.type(within(dialog).getByRole("textbox", { name: "What the customer disputes" }), "x");
      await userEvent.click(within(dialog).getByRole("button", { name: "Put on hold" }));
      expect(await screen.findByText(words)).toBeInTheDocument();
    },
  );

  it("says invoice_not_on_hold in words", async () => {
    server(
      () =>
        reminded({
          hold: { id: 1, kind: "disputed", note: "x", placedAt: "2026-10-20T08:00:00Z", placedBy: OTHER_USER_ID },
        }),
      { "POST /api/v1/invoices/1001/hold/lift": () => refusal(409, "invoice_not_on_hold") },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Lift the hold" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Lift the hold" }));
    expect(await screen.findByText(/This invoice is not on hold, so there is no hold to lift/)).toBeInTheDocument();
  });

  it("offers neither hold nor lift without invoices:payments", async () => {
    server(() => reminded(), {}, { canRegisterPayments: false });
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("hold-card");
    expect(within(card).getByText("The customer has not disputed this invoice.")).toBeInTheDocument();
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("HandoffCard_HandOffWithdrawAndExport", () => {
  it("says invoice_not_delivered in words, offers to record the delivery, and hands off once acknowledged", async () => {
    let calls = 0;
    const fetchMock = server(() => reminded(), {
      "POST /api/v1/invoices/1001/collection": () => {
        calls += 1;
        return calls === 1
          ? refusal(409, "invoice_not_delivered")
          : holdResult([{ reminderId: 3002, status: "printed", printBatchId: 7 }]);
      },
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Hand off to collection" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Collection agency" }), "Inkasso AS");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "The agency's case number" }), "K-77");
    await userEvent.click(within(dialog).getByRole("button", { name: "Hand off to collection" }));

    const notDelivered = await within(dialog).findByTestId("not-delivered");
    expect(notDelivered).toHaveTextContent(/No delivery of this invoice is recorded by its due date/);
    expect(
      within(notDelivered).getByRole("button", { name: "It was delivered: record the delivery" }),
    ).toBeInTheDocument();
    const submit = within(dialog).getByRole("button", { name: "Hand off to collection" });
    expect(submit).toBeDisabled();
    await userEvent.click(
      within(notDelivered).getByRole("checkbox", { name: "Hand it off anyway, without a delivery by the due date" }),
    );
    await userEvent.click(submit);
    expect(await screen.findByText("The hand-off is recorded")).toBeInTheDocument();
    const bodies = fetchMock.actualCalls
      .filter(([url, init]) => String(url) === "/api/v1/invoices/1001/collection" && init?.method === "POST")
      .map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies).toEqual([
      { handedOn: "2026-09-12", agency: "Inkasso AS", agencyReference: "K-77" },
      { handedOn: "2026-09-12", agency: "Inkasso AS", agencyReference: "K-77", acknowledgeNotDelivered: true },
    ]);
    expect(await screen.findByTestId("letters-left")).toHaveTextContent(/Letter 2 is printed in print batch 7/);
  });

  it("takes the person from invoice_not_delivered to recording the delivery", async () => {
    server(() => reminded(), { "POST /api/v1/invoices/1001/collection": () => refusal(409, "invoice_not_delivered") });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Hand off to collection" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Collection agency" }), "Inkasso AS");
    await userEvent.click(within(dialog).getByRole("button", { name: "Hand off to collection" }));
    await userEvent.click(await within(dialog).findByRole("button", { name: "It was delivered: record the delivery" }));
    expect(await screen.findByRole("dialog", { name: "Record a delivery" })).toBeInTheDocument();
  });

  it("shows a live hand-off, says to report direct payments beside the payments, and withdraws it", async () => {
    const fetchMock = server(
      () =>
        reminded({
          handoff: {
            id: 1,
            handedOn: "2026-11-10",
            agency: "Inkasso AS",
            agencyReference: "K-77",
            note: "",
            createdAt: "2026-11-10T08:00:00Z",
            createdBy: OTHER_USER_ID,
          },
        }),
      { "POST /api/v1/invoices/1001/collection/withdraw": () => holdResult() },
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("handoff-card");
    expect(within(card).getByText("Handed off")).toBeInTheDocument();
    expect(within(card).getByText("To Inkasso AS on Nov 10, 2026, recorded by another user")).toBeInTheDocument();
    expect(within(card).getByText("Case number: K-77")).toBeInTheDocument();
    expect(within(screen.getByTestId("payments-card")).getByTestId("report-to-agency")).toHaveTextContent(
      "This invoice is handed off to Inkasso AS. Report every payment you receive directly to the agency.",
    );

    await userEvent.click(within(card).getByRole("button", { name: "Withdraw the hand-off" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Betalt til oss");
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw the hand-off" }));
    expect(await screen.findByText("The hand-off is withdrawn")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/collection/withdraw")).toEqual({
      withdrawnOn: "2026-09-12",
      reason: "Betalt til oss",
    });
  });

  it("says nothing about the agency beside the payments once the hand-off is withdrawn", async () => {
    server(() =>
      reminded({
        handoff: {
          id: 1,
          handedOn: "2026-09-10",
          agency: "Inkasso AS",
          agencyReference: "",
          note: "",
          createdAt: "2026-09-10T08:00:00Z",
          createdBy: OTHER_USER_ID,
          withdrawnOn: "2026-09-11",
          withdrawalReason: "Betalt",
        },
      }),
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("handoff-card");
    expect(
      within(card).getByText("The hand-off to Inkasso AS was withdrawn on Sep 11, 2026: Betalt"),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("report-to-agency")).not.toBeInTheDocument();
  });

  it("exports this invoice's collection file", async () => {
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this);
    });
    server(() => reminded(), {
      "GET /api/v1/invoices/collection-export.csv?invoiceId=1001": () =>
        new Response("Invoice number;Issue date\n1000;2026-09-01\n", {
          status: 200,
          headers: {
            "Content-Type": "text/csv",
            "Content-Disposition": 'attachment; filename="invoices-collection-2026-09-12.csv"',
          },
        }),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Export for the agency" }));
    await waitFor(() => expect(clicked).toHaveLength(1));
    expect(clicked[0].download).toBe("invoices-collection-2026-09-12.csv");
  });

  it.each([
    ["invoice_handed_off", /handed off to a collection agency already/],
    ["invoice_settled", /Nothing of this invoice is open: it is paid or credited, so there is no claim to hand off\./],
  ])("says %s in words", async (code, words) => {
    server(() => reminded(), { "POST /api/v1/invoices/1001/collection": () => refusal(409, code) });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Hand off to collection" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Collection agency" }), "Inkasso AS");
    await userEvent.click(within(dialog).getByRole("button", { name: "Hand off to collection" }));
    expect(await screen.findByText(words)).toBeInTheDocument();
  });

  it("offers no hand-off on a paid invoice, and nothing at all without invoices:payments", async () => {
    server(() => reminded({ state: "paid", openAmount: 0, paidAmount: 124.99 }));
    renderRoute("/invoices/1001");
    const card = await screen.findByTestId("handoff-card");
    expect(within(card).queryByRole("button", { name: "Hand off to collection" })).not.toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "Export for the agency" })).toBeInTheDocument();
  });

  it("offers neither hand-off nor export without invoices:payments", async () => {
    server(() => reminded(), {}, { canRegisterPayments: false });
    renderRoute("/invoices/1001");
    const card = await screen.findByTestId("handoff-card");
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("InvoicePage_NextAction", () => {
  it("says the next letter, from when, and how it would go today", async () => {
    server(() =>
      reminded({
        nextAction: {
          action: "collection_notice",
          earliestOn: "2026-11-20",
          reasons: [],
          chargeNotes: ["fee_before_14_days"],
          letter: {
            level: "collection_notice",
            announcesCollection: false,
            regime: "inkassolov_1988",
            principalOpen: 74.99,
            chargesEarlier: 35,
            fee: 0,
            feeKind: "none",
            compensation: 0,
            interest: 2.1,
            interestWaived: 0,
            interestPaid: 0,
            interestSegments: [],
            total: 112.09,
            deadline: "2026-12-04",
          },
        },
      }),
    );
    renderRoute("/invoices/1001");

    const next = await screen.findByTestId("next-action");
    expect(next).toHaveTextContent("Next: a debt collection notice from Nov 20, 2026");
    expect(within(next).getByTestId("next-letter")).toHaveTextContent(
      /Debt collection notice sent today would be for NOK\s?112\.09, with a deadline of Dec 4, 2026\. Interest NOK\s?2\.10\./,
    );
    expect(next).toHaveTextContent("No fee: fewer than 14 days have passed since the last letter.");
  });

  it("says why no letter goes, each reason in words, the outdated rate by its kind and half-year", async () => {
    server(() =>
      reminded({
        nextAction: {
          action: "blocked",
          reasons: ["on_hold", "not_delivered", "collection_rates_outdated"],
          outdated: { kind: "late_interest_percent", halfYear: "2027-H1" },
          chargeNotes: [],
        },
      }),
    );
    renderRoute("/invoices/1001");

    const next = await screen.findByTestId("next-action");
    expect(next).toHaveTextContent("No letter can go now");
    expect(within(next).getByText("The invoice is on hold: the customer disputes it.")).toBeInTheDocument();
    expect(within(next).getByText(/No delivery by the due date is recorded/)).toBeInTheDocument();
    expect(
      within(next).getByText(
        "No collection rate for 2027-H1: Late interest rate. A manager adds it under the invoice settings, or a release brings it.",
      ),
    ).toBeInTheDocument();
  });

  it.each([
    ["none", ["handed_off"], "No letter is due", "The invoice is handed off to a collection agency."],
    [
      "hand_off",
      [],
      "Next: hand the invoice to a collection agency — a suggestion; Vantigo never does it itself",
      undefined,
    ],
    ["waiting", ["waiting"], "Waiting for the last letter's deadline", "The last letter's deadline and the grace"],
  ] as const)("says the %s action in words", async (action, reasons, heading, reason) => {
    server(() => reminded({ nextAction: { action, reasons: [...reasons], chargeNotes: [] } }));
    renderRoute("/invoices/1001");

    const next = await screen.findByTestId("next-action");
    expect(next).toHaveTextContent(heading);
    if (reason) expect(next).toHaveTextContent(reason);
  });

  it("shows no receivables cards on a credit note", async () => {
    server(() =>
      partlyPaid({ kind: "credit_note", state: "issued", credits: { id: 1000, number: 999, issueDate: "2026-08-01" } }),
    );
    renderRoute("/invoices/1001");

    expect(await screen.findByTestId("deliveries-card")).toBeInTheDocument();
    for (const id of ["reminders-card", "charges-card", "hold-card", "handoff-card"]) {
      expect(screen.queryByTestId(id)).not.toBeInTheDocument();
    }
    expect(screen.queryByRole("button", { name: "Record a delivery" })).not.toBeInTheDocument();
  });
});

describe("both catalogs", () => {
  it("say every receivables screen's words in Norwegian too", () => {
    const prefixes =
      /^(reminder|charges|delivery|hold|handoff|lettersLeft|policy|reminderSettings|rates)\.|^fieldInvalid\.(policy|chargePayment|waive|delivery|hold|handoff|handoffWithdraw|reminderSettings|rate)\./;
    const en = Object.keys(invoicesCatalog.en).filter((k) => prefixes.test(k));
    const nb = Object.keys(invoicesCatalog.nb).filter((k) => prefixes.test(k));
    expect(en.length).toBeGreaterThan(250);
    expect(nb.sort()).toEqual(en.sort());
    const same = en.filter(
      (k) =>
        invoicesCatalog.nb[k as keyof typeof invoicesCatalog.nb] ===
        invoicesCatalog.en[k as keyof typeof invoicesCatalog.en],
    );
    // Only words that are the same in both languages — a status, a label of the law's own, a placeholder.
    expect(same.sort()).toEqual(
      ["reminder.pdf", "reminder.col.status", "policy.mode.normal", "rates.addedBy", "rates.kind.inkassosats"].sort(),
    );
  });
});
