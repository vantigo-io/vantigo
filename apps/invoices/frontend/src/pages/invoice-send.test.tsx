import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoiceDocument } from "../api/invoices";
import { jsonResponse } from "../test/api";
import { stubFetch } from "../test/fetch";
import { issued, meta, partlyPaid } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const path = (input: RequestInfo | URL) => String(input);

/** A refusal as the server answers it: the invoices conflict problem, a 409, a 502 or a 503 alike. */
const refusal = (status: number, code: string) =>
  jsonResponse(status, { type: "about:blank", title: "Refused", status, code, detail: "The server's English." });

type Answer = Response | Promise<Response> | (() => Response | Promise<Response>);

/** The fetch fake: meta, document 1001 as `doc` says, and each write by "METHOD url". */
const server = (
  doc: () => InvoiceDocument,
  answers: Record<string, Answer> = {},
  capabilities: Partial<ReturnType<typeof meta>["capabilities"]> = {},
) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`];
    if (answer) return typeof answer === "function" ? answer() : answer;
    if (url === "/api/v1/invoices/meta") {
      return jsonResponse(200, meta({ capabilities: { ...meta().capabilities, ...capabilities } }));
    }
    if (method === "GET" && url === "/api/v1/invoices/1001") return jsonResponse(200, doc());
    return new Response(null, { status: 404 });
  });

/** The body a write sent, found by its method and URL — never "the last fetch". */
const requestTo = (fetchMock: ReturnType<typeof stubFetch>, method: string, url: string) => {
  const call = fetchMock.actualCalls.find(([u, init]) => path(u) === url && (init?.method ?? "GET") === method);
  return call ? JSON.parse(String(call[1]?.body ?? "{}")) : undefined;
};

const openSendDialog = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "Send" }));
  return screen.findByRole("dialog");
};

describe("the Send dialog", () => {
  it("prefills the customer's invoice e-mail and sends to it, saying where it went", async () => {
    let answer: (response: Response) => void = () => {};
    const pending = new Promise<Response>((resolve) => {
      answer = resolve;
    });
    const fetchMock = server(() => issued(), { "POST /api/v1/invoices/1001/send": () => pending });
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    expect(within(dialog).getByRole("textbox", { name: "Recipient" })).toHaveValue("faktura@acme.no");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Send" })).toBeDisabled());
    // The address the customer has is the server's default: nothing to override.
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/send")).toEqual({});
    answer(jsonResponse(200, issued()));
    expect(await screen.findByText("Sent to faktura@acme.no")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("sends an edited recipient as the override", async () => {
    const fetchMock = server(() => issued(), { "POST /api/v1/invoices/1001/send": () => jsonResponse(200, issued()) });
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
      "This customer expects EHF. An e-mailed PDF does not meet the e-invoicing duty; phase 2 adds EHF.",
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
      /Part of the invoice is paid, so the e-mail asks for the outstanding NOK\s?74\.99 only\./,
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
    [409, "customer_anonymised", "The customer has been anonymised and is not written to again."],
    [503, "mail_unavailable", "This installation cannot send e-mail: SMTP is not configured."],
    [502, "mail_failed", "The mail server did not take the e-mail. Nothing was sent; try again later."],
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
      await screen.findByText("Too many e-mails were sent in a short time. Wait a few minutes and try again."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Too many requests")).not.toBeInTheDocument();
  });

  it.each([
    ["without invoices:issue or mail", { canSend: false }, issued()],
    ["without the send defaults", {}, issued({ sendDefaults: undefined })],
  ] as const)("is not offered %s", async (_why, capabilities, doc) => {
    server(() => doc, {}, capabilities);
    renderRoute("/invoices/1001");

    await screen.findByRole("heading", { name: "Invoice 1000" });
    expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
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
