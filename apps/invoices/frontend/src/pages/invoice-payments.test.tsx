import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoiceDocument } from "../api/invoices";
import { jsonResponse } from "../test/api";
import { stubFetch } from "../test/fetch";
import { issued, meta, partlyPaid } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const path = (input: RequestInfo | URL) => String(input);

/** A refusal as the server answers it: the invoices conflict problem. */
const refusal = (status: number, code: string, extra: Record<string, unknown> = {}) =>
  jsonResponse(status, {
    type: "about:blank",
    title: "Refused",
    status,
    code,
    detail: "The server's English.",
    ...extra,
  });

type Answer = Response | Promise<Response> | (() => Response | Promise<Response>);

/**
 * The fetch fake: meta, document 1001 as `doc` says, and each write by
 * "METHOD url" — the writes this page makes are POSTs to the payments.
 */
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

/** The request a write made, found by its method and URL — never "the last fetch". */
const requestTo = (fetchMock: ReturnType<typeof stubFetch>, method: string, url: string) => {
  const call = fetchMock.actualCalls.find(([u, init]) => path(u) === url && (init?.method ?? "GET") === method);
  return call ? JSON.parse(String(call[1]?.body ?? "{}")) : undefined;
};

describe("an issued invoice's money", () => {
  it("shows the state beside the number and what is paid and open in the totals", async () => {
    server(() => partlyPaid());
    renderRoute("/invoices/1001");

    expect(await screen.findByRole("heading", { name: "Invoice 1000" })).toBeInTheDocument();
    expect(screen.getByTestId("document-state")).toHaveTextContent("Partially paid");
    expect(screen.getByTestId("paid-amount")).toHaveTextContent(/NOK\s?50\.00/);
    expect(screen.getByTestId("open-amount")).toHaveTextContent(/NOK\s?74\.99/);
    expect(screen.queryByTestId("refund-due")).not.toBeInTheDocument();
  });

  it("says the refund due once a credit note followed a payment", async () => {
    server(() =>
      partlyPaid({ state: "credited", creditedAmount: 124.99, uncreditedAmount: 0, openAmount: -50, refundDue: 50 }),
    );
    renderRoute("/invoices/1001");

    expect(await screen.findByTestId("refund-due")).toHaveTextContent(/NOK\s?50\.00/);
    expect(screen.getByTestId("document-state")).toHaveTextContent("Credited");
  });
});

describe("the payments card", () => {
  it("lists every payment, a removed one struck through with its reason", async () => {
    server(() => partlyPaid());
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("payments-card");
    const removed = within(card).getByText("Feil KID").closest("tr") as HTMLElement;
    expect(removed).toHaveAttribute("data-removed", "true");
    expect(within(removed).getByText("Removed: Registrert på feil faktura")).toBeInTheDocument();
    expect(within(removed).queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
    const live = within(card).getByText("Bank 4471").closest("tr") as HTMLElement;
    expect(live).not.toHaveAttribute("data-removed");
    expect(within(live).getByText("Første avdrag")).toBeInTheDocument();
    expect(within(live).getByText(/NOK\s?50\.00/)).toBeInTheDocument();
    expect(within(live).getByRole("button", { name: "Remove" })).toBeInTheDocument();
  });

  it("registers a payment with today and the open amount prefilled, and disables the button while it is sent", async () => {
    let answer: (response: Response) => void = () => {};
    const pending = new Promise<Response>((resolve) => {
      answer = resolve;
    });
    let registered = false;
    const fetchMock = server(
      () => (registered ? partlyPaid({ state: "paid", paidAmount: 124.99, openAmount: 0 }) : partlyPaid()),
      {
        "POST /api/v1/invoices/1001/payments": () => pending,
      },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register payment" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: "Amount" })).toHaveValue("74.99");
    expect(within(dialog).getByRole("textbox", { name: "Paid on" })).toHaveValue("Sep 12, 2026");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reference" }), "Bank 5512");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Resten");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));

    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Register" })).toBeDisabled());
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payments")).toEqual({
      paidOn: "2026-09-12",
      amount: 74.99,
      reference: "Bank 5512",
      note: "Resten",
    });
    registered = true;
    answer(
      jsonResponse(200, partlyPaid({ state: "paid", paidAmount: 124.99, openAmount: 0, sendDefaults: undefined })),
    );
    expect(await screen.findByText("Payment registered")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    // The document is read again rather than set from the answer, which carries no send defaults.
    await waitFor(() => expect(screen.getByTestId("document-state")).toHaveTextContent("Paid"));
    expect(
      fetchMock.actualCalls.filter(([u, init]) => path(u) === "/api/v1/invoices/1001" && !init?.method).length,
    ).toBeGreaterThan(1);
  });

  it("says invoice_settled in the reader's language", async () => {
    server(() => partlyPaid(), { "POST /api/v1/invoices/1001/payments": () => refusal(409, "invoice_settled") });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register payment" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Register" }));
    expect(
      await screen.findByText("Nothing is left to pay on this invoice, so no payment can be registered."),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("says payment_exceeds_open with the open amount the server names", async () => {
    server(() => partlyPaid(), {
      "POST /api/v1/invoices/1001/payments": () => refusal(409, "payment_exceeds_open", { openAmount: 24.99 }),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register payment" }));
    const dialog = await screen.findByRole("dialog");
    const amount = within(dialog).getByRole("textbox", { name: "Amount" });
    await userEvent.clear(amount);
    await userEvent.type(amount, "80");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));
    expect(
      await screen.findByText(
        /The payment is more than the open amount, NOK\s?24\.99\. An overpayment cannot be registered\./,
      ),
    ).toBeInTheDocument();
  });

  it("puts a refused field's words on its input", async () => {
    server(() => partlyPaid(), {
      "POST /api/v1/invoices/1001/payments": () =>
        jsonResponse(400, { title: "Invalid", status: 400, errors: { amount: ["amount must be greater than 0"] } }),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register payment" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Register" }));
    expect(
      await screen.findByText("More than 0 and at most the open amount, with up to two decimals."),
    ).toBeInTheDocument();
  });

  it("removes a payment with a reason, the button disabled until there is one and while it is sent", async () => {
    let answer: (response: Response) => void = () => {};
    const pending = new Promise<Response>((resolve) => {
      answer = resolve;
    });
    const fetchMock = server(() => partlyPaid(), {
      "POST /api/v1/invoices/1001/payments/1002/remove": () => pending,
    });
    renderRoute("/invoices/1001");

    const live = (await screen.findByText("Bank 4471")).closest("tr") as HTMLElement;
    await userEvent.click(within(live).getByRole("button", { name: "Remove" }));
    const dialog = await screen.findByRole("dialog");
    const remove = within(dialog).getByRole("button", { name: "Remove the payment" });
    expect(remove).toBeDisabled();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Feil beløp");
    await userEvent.click(remove);

    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Remove the payment" })).toBeDisabled());
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payments/1002/remove")).toEqual({
      reason: "Feil beløp",
    });
    answer(jsonResponse(200, partlyPaid()));
    expect(await screen.findByText("Payment removed")).toBeInTheDocument();
  });

  it("says payment_removed in the reader's language", async () => {
    server(() => partlyPaid(), {
      "POST /api/v1/invoices/1001/payments/1002/remove": () => refusal(409, "payment_removed"),
    });
    renderRoute("/invoices/1001");

    const live = (await screen.findByText("Bank 4471")).closest("tr") as HTMLElement;
    await userEvent.click(within(live).getByRole("button", { name: "Remove" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Dobbel");
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove the payment" }));
    expect(await screen.findByText("This payment is already removed.")).toBeInTheDocument();
  });

  it.each([
    ["paid", { state: "paid", paidAmount: 124.99, openAmount: 0 }],
    ["credited", { state: "credited", creditedAmount: 124.99, uncreditedAmount: 0, openAmount: -50, refundDue: 50 }],
  ] as const)("offers no registration on a %s invoice", async (_state, overrides) => {
    server(() => partlyPaid(overrides));
    renderRoute("/invoices/1001");

    expect(await screen.findByTestId("payments-card")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Register payment" })).not.toBeInTheDocument();
  });

  it("offers neither registration nor removal without invoices:payments", async () => {
    server(() => partlyPaid(), {}, { canRegisterPayments: false });
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("payments-card");
    expect(within(card).getByText("Bank 4471")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Register payment" })).not.toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
  });

  it("is not on a credit note, which takes no payments", async () => {
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

    await screen.findByRole("heading", { name: "Credit note 1000" });
    expect(screen.queryByTestId("payments-card")).not.toBeInTheDocument();
    expect(screen.getByTestId("document-state")).toHaveTextContent("Issued");
  });
});
