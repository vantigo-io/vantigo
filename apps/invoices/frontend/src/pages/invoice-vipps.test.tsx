import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { OnSitePaymentStatus } from "../api/attempts";
import { invoicesCatalog } from "../i18n";
import { jsonResponse, path, refusal } from "../test/api";
import { NO_BODY, requestTo, documentServer as server } from "../test/document-server";
import { abandonedPaymentAttempt, issued, onSitePayment, payLink, paymentAttempt } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

/** What an `<img>` shows when it is a QR code the server rendered: the markup, base64-encoded, as data. */
const dataUrlOf = (svg: string) => `data:image/svg+xml;base64,${btoa(svg)}`;

/**
 * Whether any of the server's SVG markup made it into the page as elements:
 * the fixtures' QR codes draw the module square `M4 4h1v1h-1z`, which no icon
 * draws. Every QR is an image, never inlined.
 */
const inlinedServerSvg = () => document.querySelector('path[d="M4 4h1v1h-1z"]');

const capturing = (id: number, overrides = {}) =>
  paymentAttempt({
    id,
    reference: `acme01-1-${id}`,
    flow: "on_site",
    state: "capture_unknown",
    authorizedAmount: 124.99,
    authorizedAt: "2026-09-12T10:31:00Z",
    reservedAmount: 124.99,
    ...overrides,
  });

describe("PayLinkCard_OnScreenOnlyWithOnSite", () => {
  it("shows no address, QR code or copy when the server answered none", async () => {
    server(
      () => issued({ payLink: payLink({ url: undefined, qrSvg: undefined }) }),
      {},
      {},
      { payLinksAvailable: true },
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("pay-link-card");
    expect(within(card).getByText(/^Made Sep 12, 2026/)).toBeInTheDocument();
    expect(within(card).queryByTestId("pay-link-url")).not.toBeInTheDocument();
    expect(within(card).queryByRole("img")).not.toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Copy link" })).not.toBeInTheDocument();
    expect(
      within(card).getByText(
        "The link travels with the e-mail and the reminder letters. It is shown on screen only while payment on site is on: showing it to a customer on site is a cash sale.",
      ),
    ).toBeInTheDocument();
  });

  it("shows the address, its QR code as an image of the server's own SVG, and copy, when the server answered them", async () => {
    const link = payLink();
    server(() => issued({ payLink: link }), {}, { canTakeOnSitePayment: true }, { onSitePaymentsAvailable: true });
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("pay-link-card");
    expect(within(card).getByTestId("pay-link-url")).toHaveTextContent(link.url as string);
    expect(within(card).getByRole("img", { name: "QR code of the pay link" })).toHaveAttribute(
      "src",
      dataUrlOf(link.qrSvg as string),
    );
    expect(within(card).getByRole("button", { name: "Copy link" })).toBeInTheDocument();
    expect(inlinedServerSvg()).toBeNull();
  });

  it("makes a link for a caller who may issue while pay links are available", async () => {
    let made = false;
    const fetchMock = server(
      () => issued(made ? { payLink: payLink({ url: undefined, qrSvg: undefined }) } : {}),
      {
        "POST /api/v1/invoices/1001/pay-link": () => {
          made = true;
          return jsonResponse(201, issued({ payLink: payLink({ url: undefined, qrSvg: undefined }) }));
        },
      },
      {},
      { payLinksAvailable: true },
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("pay-link-card");
    expect(within(card).getByText("This invoice has no pay link.")).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Make a pay link" }));
    expect(await screen.findByText("The pay link is made")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/pay-link")).toBe(NO_BODY);
    expect(await within(card).findByText(/^Made Sep 12, 2026/)).toBeInTheDocument();
  });

  it("revokes the link after asking, and says pay_links_unavailable in words", async () => {
    const fetchMock = server(
      () => issued({ payLink: payLink({ url: undefined, qrSvg: undefined }) }),
      { "POST /api/v1/invoices/1001/pay-link/revoke": () => new Response(null, { status: 204 }) },
      {},
      { payLinksAvailable: true },
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("pay-link-card");
    await userEvent.click(within(card).getByRole("button", { name: "Revoke the link" }));
    const dialog = await screen.findByRole("dialog");
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/pay-link/revoke")).toBeUndefined();
    await userEvent.click(within(dialog).getByRole("button", { name: "Revoke the link" }));
    expect(await screen.findByText("The pay link is revoked")).toBeInTheDocument();
  });

  it("says a refused make in words", async () => {
    server(
      () => issued(),
      { "POST /api/v1/invoices/1001/pay-link": () => refusal(409, "pay_links_unavailable") },
      {},
      { payLinksAvailable: true },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(
      within(await screen.findByTestId("pay-link-card")).getByRole("button", { name: "Make a pay link" }),
    );
    expect(
      await screen.findByText("Pay links are switched off or not set up in the payment settings."),
    ).toBeInTheDocument();
  });

  it("offers no make or revoke to a reader who may not issue", async () => {
    server(
      () => issued({ payLink: payLink({ url: undefined, qrSvg: undefined }) }),
      {},
      { canIssue: false },
      { payLinksAvailable: true },
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("pay-link-card");
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
  });

  it("shows no card while pay links are unavailable and the invoice has none", async () => {
    server(() => issued());
    renderRoute("/invoices/1001");

    await screen.findByTestId("payments-card");
    expect(screen.queryByTestId("pay-link-card")).not.toBeInTheDocument();
  });
});

describe("AttemptsCard_StatesAndAbandon", () => {
  const attempts = [
    paymentAttempt(),
    capturing(1002),
    abandonedPaymentAttempt({ id: 1003, reference: "acme01-1-1003" }),
    paymentAttempt({
      id: 1004,
      reference: "acme01-1-1004",
      state: "captured",
      authorizedAmount: 124.99,
      capturedAmount: 124.99,
      refundedAmount: 20,
      capturedAt: "2026-09-12T10:32:00Z",
    }),
  ];

  it("shows each attempt's state and amounts, and Abandon only on an unfinished one for a manager", async () => {
    server(() => issued({ paymentAttempts: attempts }), {}, { canManage: true });
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("attempts-card");
    const row = (reference: string) => within(card).getByText(reference).closest("tr") as HTMLElement;
    expect(within(row("acme01-1-1001")).getByText("Waiting for the customer")).toBeInTheDocument();
    expect(within(row("acme01-1-1001")).getByText("Pay link")).toBeInTheDocument();
    expect(within(row("acme01-1-1002")).getByText("Outcome unknown")).toBeInTheDocument();
    expect(within(row("acme01-1-1002")).getByText("On site")).toBeInTheDocument();
    expect(within(row("acme01-1-1002")).getByText(/^Held on the invoice NOK\s?124\.99$/)).toBeInTheDocument();
    expect(within(row("acme01-1-1003")).getByText("Abandoned")).toBeInTheDocument();
    expect(
      within(row("acme01-1-1003")).getByText(/^Abandoned Sep 21, 2026.*: Sjekket i Vipps-portalen: ingenting trukket$/),
    ).toBeInTheDocument();
    expect(within(row("acme01-1-1004")).getByText("Paid")).toBeInTheDocument();
    expect(within(row("acme01-1-1004")).getByText(/^Refunded in the Vipps portal NOK\s?20\.00$/)).toBeInTheDocument();

    const abandons = within(card).getAllByRole("button", { name: /^Abandon the Vipps payment/ });
    expect(abandons.map((b) => b.getAttribute("aria-label"))).toEqual([
      "Abandon the Vipps payment acme01-1-1001",
      "Abandon the Vipps payment acme01-1-1002",
    ]);
  });

  it("offers no Abandon without invoices:manage", async () => {
    server(() => issued({ paymentAttempts: attempts }), {}, { canManage: false });
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("attempts-card");
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
  });

  it("abandons with a reason and the portal check confirmed, and says a refusal in words", async () => {
    let refuse = true;
    const fetchMock = server(
      () => issued({ paymentAttempts: attempts }),
      {
        "POST /api/v1/invoices/payment-attempts/1002/abandon": () =>
          refuse
            ? refusal(409, "attempt_not_abandonable")
            : jsonResponse(200, capturing(1002, { state: "abandoned", reservedAmount: undefined })),
      },
      { canManage: true },
    );
    renderRoute("/invoices/1001");

    const card = await screen.findByTestId("attempts-card");
    await userEvent.click(within(card).getByRole("button", { name: "Abandon the Vipps payment acme01-1-1002" }));
    const dialog = await screen.findByRole("dialog", { name: "Abandon the Vipps payment" });
    const submit = within(dialog).getByRole("button", { name: "Abandon the payment" });
    expect(submit).toBeDisabled();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Ingenting trukket i portalen");
    expect(submit).toBeDisabled();
    await userEvent.click(
      within(dialog).getByRole("checkbox", { name: "I have checked in the Vipps portal that nothing was captured" }),
    );
    expect(submit).toBeEnabled();
    await userEvent.click(submit);

    expect(
      await screen.findByText(
        "This Vipps payment cannot be abandoned now. Vipps may still hold it, the money may have been captured, or it is being completed; read the payment again.",
      ),
    ).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/payment-attempts/1002/abandon")).toEqual({
      reason: "Ingenting trukket i portalen",
      confirmNotCaptured: true,
    });
    refuse = false;
    await userEvent.click(submit);
    expect(await screen.findByText("The Vipps payment is abandoned")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("names the abandon in Norwegian as forlat", () => {
    expect(invoicesCatalog.nb["abandon.title"]).toBe("Forlat Vipps-betalingen");
    expect(invoicesCatalog.nb["abandon.submit"]).toBe("Forlat betalingen");
    expect(invoicesCatalog.nb["attempt.state.abandoned"]).toBe("Forlatt");
    expect(invoicesCatalog.nb["fieldInvalid.abandon.reason"]).toMatch(/^Oppgi en begrunnelse/);
  });

  it("shows no card when no Vipps payment was started", async () => {
    server(() => issued());
    renderRoute("/invoices/1001");

    await screen.findByTestId("payments-card");
    expect(screen.queryByTestId("attempts-card")).not.toBeInTheDocument();
  });
});

describe("PaymentsCard_ShowsVipps", () => {
  it("names a Vipps capture's source", async () => {
    server(() =>
      issued({
        state: "paid",
        paidAmount: 124.99,
        openAmount: 0,
        payments: [
          {
            id: 1009,
            paidOn: "2026-09-12",
            amount: 124.99,
            currency: "NOK",
            source: "vipps",
            reference: "acme01-1-1004",
            note: "",
            registeredAt: "2026-09-12T10:32:00Z",
            paymentAttemptId: 1004,
          },
        ],
      }),
    );
    renderRoute("/invoices/1001");

    expect(
      await within(await screen.findByTestId("payments-card")).findByTestId("payment-source-1009"),
    ).toHaveTextContent("Vipps");
    expect(invoicesCatalog.nb["paymentSource.vipps"]).toBe("Vipps");
  });
});

/**
 * The on-site payment's fake on top of the document's: the create answers
 * `create`, each status read the current `status`, the cancel `cancel`.
 */
const onSite = ({
  create = () => jsonResponse(201, onSitePayment()),
  status = (): OnSitePaymentStatus => ({ state: "created", qrSvg: onSitePayment().qrSvg }),
  cancel = () => jsonResponse(200, { state: "cancelled" }),
}: {
  create?: () => Response;
  status?: () => OnSitePaymentStatus;
  cancel?: () => Response;
} = {}) =>
  server(
    () => issued(),
    {
      "POST /api/v1/invoices/1001/payment-requests": create,
      "GET /api/v1/invoices/1001/payment-requests/1001": () => jsonResponse(200, status()),
      "POST /api/v1/invoices/1001/payment-requests/1001/cancel": cancel,
    },
    { canTakeOnSitePayment: true },
    { onSitePaymentsAvailable: true },
  );

const statusReads = (fetchMock: ReturnType<typeof server>) =>
  fetchMock.actualCalls.filter(([u]) => path(u) === "/api/v1/invoices/1001/payment-requests/1001").length;
const creates = (fetchMock: ReturnType<typeof server>) =>
  fetchMock.actualCalls.filter(
    ([u, init]) => path(u) === "/api/v1/invoices/1001/payment-requests" && init?.method === "POST",
  ).length;

const takePayment = async () =>
  userEvent.click(await screen.findByRole("button", { name: "Take a Vipps payment now" }));

describe("TakePayment_QrPollCancel", () => {
  it("shows the QR code as an image, polls the request every two seconds, and says when it is paid", async () => {
    let state: OnSitePaymentStatus = { state: "created", qrSvg: onSitePayment().qrSvg };
    const fetchMock = onSite({ status: () => state });
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("img", { name: "QR code to scan with the Vipps app" })).toHaveAttribute(
      "src",
      dataUrlOf(onSitePayment().qrSvg as string),
    );
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payment-requests")).toEqual({ flow: "on_site" });
    expect(inlinedServerSvg()).toBeNull();
    await waitFor(() => expect(statusReads(fetchMock)).toBeGreaterThanOrEqual(1));
    state = { state: "captured", capturedAt: "2026-09-12T10:32:00Z" };
    expect(
      await within(dialog).findByText("Paid. The payment is registered on the invoice.", {}, { timeout: 6000 }),
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole("img")).not.toBeInTheDocument();
  });

  it("cancels a request the customer has not answered", async () => {
    const fetchMock = onSite();
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(await within(dialog).findByRole("button", { name: "Cancel the request" }));
    expect(await within(dialog).findByTestId("qr-state")).toHaveTextContent("Cancelled");
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payment-requests/1001/cancel")).toBe(NO_BODY);
  });

  it.each([
    ["on_site_payments_unavailable", 409],
    ["invoice_settled", 409],
    ["invoice_handed_off", 409],
    ["payment_in_progress", 409],
    ["amount_out_of_range", 409],
    ["too_many_attempts", 409],
    ["payments_unavailable", 409],
    ["provider_failed", 502],
  ])("says %s in words", async (code, status) => {
    onSite({ create: () => refusal(status, code) });
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    const key = `refusal.${code}` as keyof typeof invoicesCatalog.en;
    expect(await within(dialog).findByText(invoicesCatalog.en[key])).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("says buyer_identity_missing with a link to the customer", async () => {
    onSite({ create: () => refusal(409, "buyer_identity_missing") });
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    expect(
      await within(dialog).findByText(
        "The buyer is not identified as a cash invoice requires: a name, and an organisation number or a complete address.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: "Open the customer" })).toHaveAttribute("href", "/customers/2001");
  });

  it("shows an open request's QR code again on payment_request_active, without making another", async () => {
    const fetchMock = onSite({ create: () => refusal(409, "payment_request_active", { attemptId: 1001 }) });
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("img", { name: "QR code to scan with the Vipps app" })).toHaveAttribute(
      "src",
      dataUrlOf(onSitePayment().qrSvg as string),
    );
    expect(within(dialog).getByText("The request already open is shown again.")).toBeInTheDocument();
    expect(creates(fetchMock)).toBe(1);
  });

  it("cancels an open request whose QR code is gone and makes a new one", async () => {
    // The open request was approved and its code is gone; once cancelled, the new one waits for the customer.
    let cancelled = false;
    let asked = 0;
    const fetchMock = onSite({
      create: () =>
        asked++ === 0
          ? refusal(409, "payment_request_active", { attemptId: 1001 })
          : jsonResponse(201, onSitePayment()),
      status: () => (cancelled ? { state: "created", qrSvg: onSitePayment().qrSvg } : { state: "creating" }),
      cancel: () => {
        cancelled = true;
        return jsonResponse(200, { state: "cancelled" });
      },
    });
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(await within(dialog).findByRole("button", { name: "Cancel it and make a new one" }));
    expect(await within(dialog).findByRole("img", { name: "QR code to scan with the Vipps app" })).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payment-requests/1001/cancel")).toBe(NO_BODY);
    expect(creates(fetchMock)).toBe(2);
  });

  it("tells the user to have a stuck request abandoned when its cancel is refused", async () => {
    onSite({
      create: () => refusal(409, "payment_request_active", { attemptId: 1001 }),
      status: () => ({ state: "creating" }),
      cancel: () => refusal(409, "payment_request_active"),
    });
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(await within(dialog).findByRole("button", { name: "Cancel it and make a new one" }));
    expect(
      await within(dialog).findByText(
        "The open request cannot be cancelled now: Vipps has not confirmed it, or the customer approved it. Wait a moment and try again. If it stays, someone who manages Invoices can abandon it on the invoice's Vipps payments card.",
      ),
    ).toBeInTheDocument();
  });

  it.each(["authorized", "capturing", "capture_unknown", "cancelling"] as const)(
    "follows an open request the customer approved (%s) on payment_request_active, offering no cancel",
    async (state) => {
      const fetchMock = onSite({
        create: () => refusal(409, "payment_request_active", { attemptId: 1001 }),
        status: () => ({ state }),
      });
      renderRoute("/invoices/1001");

      await takePayment();
      const dialog = await screen.findByRole("dialog");
      expect(await within(dialog).findByTestId("qr-state")).toHaveTextContent(
        invoicesCatalog.en[`attempt.state.${state}`],
      );
      if (state !== "cancelling") {
        expect(within(dialog).getByText("Approved. The money is being taken.")).toBeInTheDocument();
      }
      expect(within(dialog).queryByRole("img")).not.toBeInTheDocument();
      expect(within(dialog).queryByRole("button", { name: "Cancel the request" })).not.toBeInTheDocument();
      expect(within(dialog).queryByRole("button", { name: "Cancel it and make a new one" })).not.toBeInTheDocument();
      expect(creates(fetchMock)).toBe(1);
    },
  );

  it("offers a new request when the one the refusal named is over", async () => {
    let asked = 0;
    const fetchMock = onSite({
      create: () =>
        asked++ === 0
          ? refusal(409, "payment_request_active", { attemptId: 1001 })
          : jsonResponse(201, onSitePayment()),
      status: () => (asked > 1 ? { state: "created", qrSvg: onSitePayment().qrSvg } : { state: "expired" }),
    });
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("No Vipps payment request of this invoice is open.")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Cancel it and make a new one" })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Take a Vipps payment now" }));
    expect(await within(dialog).findByRole("img", { name: "QR code to scan with the Vipps app" })).toBeInTheDocument();
    expect(creates(fetchMock)).toBe(2);
  });

  it("offers cancel-and-retry when the open request cannot be read", async () => {
    server(
      () => issued(),
      {
        "POST /api/v1/invoices/1001/payment-requests": () =>
          refusal(409, "payment_request_active", { attemptId: 1001 }),
        "GET /api/v1/invoices/1001/payment-requests/1001": () => new Response(null, { status: 500 }),
      },
      { canTakeOnSitePayment: true },
      { onSitePaymentsAvailable: true },
    );
    renderRoute("/invoices/1001");

    await takePayment();
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("button", { name: "Cancel it and make a new one" })).toBeInTheDocument();
  });

  it("offers no on-site payment without canTakeOnSitePayment", async () => {
    server(() => issued(), {}, { canTakeOnSitePayment: false });
    renderRoute("/invoices/1001");

    await screen.findByTestId("payments-card");
    expect(screen.queryByRole("button", { name: "Take a Vipps payment now" })).not.toBeInTheDocument();
  });
});
