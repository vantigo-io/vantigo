import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { setUnauthorizedHandler } from "../api/request";
import { jsonResponse, refusal } from "../test/api";
import { pendingResponse, readsOf, requestTo, documentServer as server } from "../test/document-server";
import { issued, partlyPaid } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

/** The answer to a send: the document with the delivery the send logged, to `recipient`. */
const sentTo = (recipient: string) =>
  issued({
    deliveries: [
      {
        id: 1003,
        recipient,
        sentAt: "2026-09-12T10:00:00Z",
        sentByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
        subject: "Faktura 1000 fra Kraft-Verket AS",
      },
    ],
  });

const openSendDialog = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "Send" }));
  return screen.findByRole("dialog");
};

describe("the Send dialog", () => {
  it("prefills the customer's invoice e-mail and sends to it, saying where it went", async () => {
    const pending = pendingResponse();
    const fetchMock = server(() => issued(), { "POST /api/v1/invoices/1001/send": () => pending.response });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    expect(within(dialog).getByRole("textbox", { name: "Recipient" })).toHaveValue("faktura@acme.no");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Send" })).toBeDisabled());
    // The address the customer has is the server's default: nothing to override.
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/send")).toEqual({});
    const reads = readsOf(fetchMock, "/api/v1/invoices/1001");
    pending.answer(jsonResponse(200, sentTo("faktura@acme.no")));
    expect(await screen.findByText("Sent to faktura@acme.no")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    // Read again after the send, as after every write (design D4).
    await waitFor(() => expect(readsOf(fetchMock, "/api/v1/invoices/1001")).toBeGreaterThan(reads));
  });

  it("says the address the server logged, the newest delivery's, over the one prefilled", async () => {
    server(() => partlyPaid(), {
      "POST /api/v1/invoices/1001/send": () =>
        jsonResponse(
          200,
          partlyPaid({
            deliveries: [
              ...(partlyPaid().deliveries ?? []),
              {
                id: 1003,
                recipient: "bokføring@acme.no",
                sentAt: "2026-09-12T10:00:00Z",
                sentByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
                subject: "Faktura 1000 fra Kraft-Verket AS",
              },
            ],
          }),
        ),
    });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    expect(await screen.findByText("Sent to bokføring@acme.no")).toBeInTheDocument();
  });

  it("sends an edited recipient as the override", async () => {
    const fetchMock = server(() => issued(), {
      "POST /api/v1/invoices/1001/send": () => jsonResponse(200, sentTo("regnskap@acme.no")),
    });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    const recipient = within(dialog).getByRole("textbox", { name: "Recipient" });
    await userEvent.clear(recipient);
    await userEvent.type(recipient, "regnskap@acme.no");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

    expect(await screen.findByText("Sent to regnskap@acme.no")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/send")).toEqual({ recipient: "regnskap@acme.no" });
  });

  it.each([
    [
      "delivery_preference_ehf",
      "red",
      "This customer prefers EHF, but this document cannot be sent as EHF from here: you or this installation cannot send EHF. An e-mailed PDF does not meet the e-invoicing duty.",
    ],
    [
      "buyer_norwegian_business_required",
      "red",
      "Norwegian businesses must receive an e-invoice: an e-mailed PDF no longer meets the duty.",
    ],
    [
      "buyer_norwegian_business",
      "note",
      "From 1 January 2027 Norwegian businesses must receive an e-invoice; this is a PDF.",
    ],
    ["delivery_preference_other", "note", "This customer prefers paper; this sends a PDF by e-mail."],
  ] as const)("says %s as a %s", async (warning, loudness, words) => {
    server(() => issued({ sendDefaults: { recipient: "faktura@acme.no", preference: "paper", warnings: [warning] } }));
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    const said = within(dialog).getByText(words);
    const alert = said.closest("[data-send-warning]") as HTMLElement;
    expect(alert).toHaveAttribute("data-send-warning", warning);
    expect(alert).toHaveAttribute("data-loud", loudness === "red" ? "true" : "false");
    if (loudness === "red") expect(alert).toHaveAttribute("role", "alert");
  });

  it.each([
    [
      "a partly paid",
      partlyPaid(),
      /^Part of the invoice is paid or credited, so the e-mail asks only for the outstanding NOK\s?74\.99\.$/,
    ],
    [
      "a paid",
      partlyPaid({ state: "paid", paidAmount: 124.99, openAmount: 0 }),
      /^The invoice is settled, so the e-mail says nothing is due\.$/,
    ],
    [
      "a credited",
      partlyPaid({ state: "credited", creditedAmount: 124.99, uncreditedAmount: 0, openAmount: -50, refundDue: 50 }),
      /^The invoice is settled, so the e-mail says nothing is due\.$/,
    ],
  ] as const)("says what the mail will say about payment on %s invoice", async (_which, doc, words) => {
    server(() => doc);
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    expect(within(dialog).getByText(words)).toBeInTheDocument();
  });

  it("says nothing about payment on an invoice nothing is paid on", async () => {
    server(() => issued());
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    expect(within(dialog).queryByText(/e-mail says nothing is due|asks for the outstanding/)).not.toBeInTheDocument();
  });

  it.each([
    [409, "no_invoice_email", "The customer has no invoice e-mail. Enter an address to send to."],
    [409, "customer_anonymised", "The customer has been anonymised and is not contacted again."],
    [503, "mail_unavailable", "This installation cannot send e-mail: SMTP is not configured."],
    [
      502,
      "mail_failed",
      "The mail server did not confirm the e-mail. Nothing was recorded; it may still have arrived. Check with the customer before sending again.",
    ],
  ] as const)("says a %i %s in the reader's language", async (status, code, words) => {
    server(() => issued(), { "POST /api/v1/invoices/1001/send": () => refusal(status, code) });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    expect(await screen.findByText(words)).toBeInTheDocument();
    expect(screen.getByText("Could not send")).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("says the rate limit's 429, which is not a problem document", async () => {
    server(() => issued(), {
      "POST /api/v1/invoices/1001/send": () =>
        new Response(JSON.stringify({ error: { code: "rate_limited", message: "Too many requests" } }), {
          status: 429,
          headers: { "Content-Type": "application/json", "Retry-After": "60" },
        }),
    });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    expect(
      await screen.findByText("Too many requests in a short time; wait ten minutes and try again."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Too many requests")).not.toBeInTheDocument();
  });

  it("hands an expired session to the host and says nothing in red", async () => {
    const expired = vi.fn();
    setUnauthorizedHandler(expired);
    server(() => issued(), {
      "POST /api/v1/invoices/1001/send": () => jsonResponse(401, { title: "Unauthorized", status: 401 }),
    });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    await waitFor(() => expect(expired).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("Could not send")).not.toBeInTheDocument();
  });

  it("says a document that is gone in the reader's language", async () => {
    server(() => issued(), { "POST /api/v1/invoices/1001/send": () => new Response(null, { status: 404 }) });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    expect(await screen.findByText("The document no longer exists.")).toBeInTheDocument();
    expect(screen.getByText("Could not send")).toBeInTheDocument();
  });

  it("is not offered without invoices:issue or mail", async () => {
    server(() => issued(), {}, { canSend: false });
    renderRoute("/invoices/1001");

    await screen.findByRole("heading", { name: /^Invoice 1000/ });
    expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
  });

  // The send defaults are the server's best effort: a directory that cannot
  // be read leaves them out, and the person enters the address instead.
  it("is offered without the send defaults, the recipient empty and a note saying why", async () => {
    const fetchMock = server(() => issued({ sendDefaults: undefined }), {
      "POST /api/v1/invoices/1001/send": () => jsonResponse(200, sentTo("faktura@acme.no")),
    });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    const recipient = within(dialog).getByRole("textbox", { name: "Recipient" });
    expect(recipient).toHaveValue("");
    expect(
      within(dialog).getByText("The customer's invoice address could not be read; enter the address to send to."),
    ).toBeInTheDocument();
    expect(dialog.querySelector("[data-send-warning]")).toBeNull();
    await userEvent.type(recipient, "faktura@acme.no");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

    expect(await screen.findByText("Sent to faktura@acme.no")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/send")).toEqual({ recipient: "faktura@acme.no" });
  });

  // An emptied field must not fall back to the profile's address unseen.
  it("does not send with the recipient emptied while the customer has an address", async () => {
    const fetchMock = server(() => issued());
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    const recipient = within(dialog).getByRole("textbox", { name: "Recipient" });
    await userEvent.clear(recipient);
    expect(within(dialog).getByRole("button", { name: "Send" })).toBeDisabled();
    await userEvent.type(recipient, "   ");
    expect(within(dialog).getByRole("button", { name: "Send" })).toBeDisabled();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/send")).toBeUndefined();
    await userEvent.clear(recipient);
    await userEvent.type(recipient, "faktura@acme.no");
    expect(within(dialog).getByRole("button", { name: "Send" })).toBeEnabled();
  });

  // The server answers customerAnonymised in place of the send defaults: the
  // dialog says why, offers no send, and never says the address could not be read.
  it("offers no send for an anonymised customer, and says why", async () => {
    server(() => issued({ sendDefaults: undefined, customerAnonymised: true }));
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    expect(
      within(dialog).getByText("The customer has been anonymised and is not contacted again."),
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: "Recipient" })).not.toBeInTheDocument();
    expect(within(dialog).queryByText(/could not be read/)).not.toBeInTheDocument();
  });

  it("is offered for an issued credit note too", async () => {
    server(() =>
      issued({
        kind: "credit_note",
        state: "issued",
        paidAmount: undefined,
        openAmount: undefined,
        payments: undefined,
        credits: { id: 1000, number: 999, issueDate: "2026-09-01" },
      }),
    );
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    expect(within(dialog).getByRole("textbox", { name: "Recipient" })).toHaveValue("faktura@acme.no");
    expect(within(dialog).queryByText(/e-mail says nothing is due|asks for the outstanding/)).not.toBeInTheDocument();
  });
});

describe("the deliveries card", () => {
  it("lists each send, when and to whom", async () => {
    server(() => partlyPaid());
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("deliveries-card");
    expect(within(card).getByRole("columnheader", { name: "To" })).toBeInTheDocument();
    const rows = within(card).getAllByRole("row");
    expect(rows).toHaveLength(3);
    expect(within(rows[1]).getByText("faktura@acme.no")).toBeInTheDocument();
    expect(within(rows[2]).getByText("regnskap@acme.no")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Faktura 1000 fra Kraft-Verket AS")).toBeInTheDocument();
  });

  it("says (anonymised) for a recipient the anonymisation blanked", async () => {
    const base = partlyPaid();
    server(() => partlyPaid({ deliveries: base.deliveries?.map((d) => ({ ...d, recipient: "" })) }));
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("deliveries-card");
    expect(within(card).getAllByText("(anonymised)")).toHaveLength(2);
  });

  it("has no address column at all for a reader, whom the server sends no address", async () => {
    const base = partlyPaid();
    server(
      () =>
        partlyPaid({
          // A reader's deliveries as the server sends them: the address is never on the wire.
          deliveries: base.deliveries?.map((d) => ({
            id: d.id,
            sentAt: d.sentAt,
            sentByUserId: d.sentByUserId,
            subject: d.subject,
          })),
          sendDefaults: undefined,
        }),
      {},
      { canIssue: false, canSend: false },
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("deliveries-card");
    expect(within(card).queryByRole("columnheader", { name: "To" })).not.toBeInTheDocument();
    expect(within(card).queryByText("(anonymised)")).not.toBeInTheDocument();
    expect(within(card).getAllByText("Faktura 1000 fra Kraft-Verket AS")).toHaveLength(2);
  });

  it("says a document has not been sent yet", async () => {
    server(() => issued());
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("deliveries-card");
    expect(within(card).getByText("Not sent by e-mail yet.")).toBeInTheDocument();
  });
});
