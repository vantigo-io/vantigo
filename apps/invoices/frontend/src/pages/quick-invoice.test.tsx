import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { OnSitePaymentStatus } from "../api/attempts";
import { invoicesCatalog } from "../i18n";
import { customerSearch, jsonResponse, path, problemResponse, refusal } from "../test/api";
import { type Answer, NO_BODY, pendingResponse, requestTo } from "../test/document-server";
import { stubFetch } from "../test/fetch";
import { issued, meta, onSitePayment } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

/** What an `<img>` shows when it is a QR code the server rendered: the markup, base64-encoded, as data. */
const dataUrlOf = (svg: string) => `data:image/svg+xml;base64,${btoa(svg)}`;

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** Acme's primary invoice address, as the customers module lists it. */
const acmeAddress = {
  id: 1,
  type: "invoice",
  isPrimary: true,
  line1: "Kundeveien 2",
  postalCode: "0150",
  city: "Oslo",
  country: "NO",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

interface QuickServer {
  /** The quick invoice's POST: its answer, given the body sent. */
  quick?: (body: Record<string, unknown>) => Response | Promise<Response>;
  capabilities?: Partial<ReturnType<typeof meta>["capabilities"]>;
  metaOverrides?: Partial<Omit<ReturnType<typeof meta>, "capabilities">>;
  addresses?: unknown[];
  /** Each on-site status read of attempt 1001 of invoice 1001. */
  status?: () => OnSitePaymentStatus;
  answers?: Record<string, Answer>;
}

/**
 * The fetch fake for the quick invoice: meta (the quick invoice allowed by
 * default), the customers' list and Acme's addresses, the quick invoice's
 * POST, invoice 1001 and its on-site request 1001. Anything else is a 404.
 */
const quickServer = ({
  quick = () => jsonResponse(201, { invoice: issued() }),
  capabilities = {},
  metaOverrides = {},
  addresses = [acmeAddress],
  status = () => ({ state: "created", qrSvg: onSitePayment().qrSvg }),
  answers = {},
}: QuickServer = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`];
    if (answer) return typeof answer === "function" ? answer() : answer;
    if (url === "/api/v1/invoices/meta") {
      return jsonResponse(
        200,
        meta({ ...metaOverrides, capabilities: { ...meta().capabilities, canQuickInvoice: true, ...capabilities } }),
      );
    }
    if (url.startsWith("/api/v1/customers?")) return customerSearch(url);
    if (url === "/api/v1/customers/2001/addresses") return jsonResponse(200, { data: addresses });
    if (method === "POST" && url === "/api/v1/invoices/quick") return quick(JSON.parse(String(init?.body)));
    if (method === "GET" && url === "/api/v1/invoices/1001") return jsonResponse(200, issued());
    if (method === "GET" && url === "/api/v1/invoices/1001/payment-requests/1001") {
      return jsonResponse(200, status());
    }
    return new Response(null, { status: 404 });
  });

const quickPosts = (fetchMock: ReturnType<typeof quickServer>) =>
  fetchMock.actualCalls
    .filter(([u, init]) => path(u) === "/api/v1/invoices/quick" && init?.method === "POST")
    .map(([, init]) => JSON.parse(String(init?.body)));

const pickAcme = async () => {
  await userEvent.click(await screen.findByRole("combobox", { name: "Customer" }));
  await userEvent.click(await screen.findByRole("option", { name: "Acme AS (10001)" }));
};

/** Fills line 1: "Service", 2 × 500 at the first VAT code offered. */
const fillLine = async () => {
  await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), "Service");
  const quantity = screen.getByRole("textbox", { name: "Line 1 quantity" });
  await userEvent.clear(quantity);
  await userEvent.type(quantity, "2");
  const price = screen.getByRole("textbox", { name: "Line 1 unit price" });
  await userEvent.clear(price);
  await userEvent.type(price, "500");
};

const fillForm = async () => {
  await pickAcme();
  await fillLine();
};

const issueButton = () => screen.getByRole("button", { name: "Issue" });
const payButton = () => screen.getByRole("button", { name: "Issue and take payment with Vipps" });
const ISSUED = "Invoice 1000 is issued and recorded as handed over.";

const onSiteOn = {
  capabilities: { canTakeOnSitePayment: true },
  metaOverrides: { onSitePaymentsAvailable: true },
};

/** A refusal as the quick invoice's answer carries it in paymentRequestError. */
const problem = (code?: string, status = 409, extra: Record<string, unknown> = {}) => ({
  type: "about:blank",
  title: "Refused",
  status,
  detail: "The server's English.",
  ...(code ? { code } : {}),
  ...extra,
});

describe("QuickInvoicePage_IssueAndPdf", () => {
  it("issues the customer's lines in one request with its own request id, and offers the PDF as a download only", async () => {
    const fetchMock = quickServer();
    renderRoute("/invoices/quick");

    await fillForm();
    expect(await screen.findByText("Kundeveien 2, 0150 Oslo, NO")).toBeInTheDocument();
    await userEvent.click(issueButton());

    expect(await screen.findByText(ISSUED)).toBeInTheDocument();
    expect(quickPosts(fetchMock)).toEqual([
      {
        requestId: expect.stringMatching(UUID),
        customerId: 2001,
        lines: [{ description: "Service", quantity: 2, unitPrice: 500, vatCodeId: 1 }],
        payOnSite: false,
      },
    ]);
    expect(screen.getByRole("button", { name: "Download PDF" })).toBeInTheDocument();
    // Without payment on site the PDF is never shown to the customer on screen (D15, I21).
    expect(screen.queryByRole("button", { name: "Show the PDF" })).not.toBeInTheDocument();
    expect(document.querySelector("iframe, embed, object")).toBeNull();
  });

  it("shows the PDF on screen too while payment on site is available", async () => {
    quickServer(onSiteOn);
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());

    await screen.findByText(ISSUED);
    expect(screen.getByRole("button", { name: "Show the PDF" })).toBeInTheDocument();
  });

  it("sends once while the answer is on its way, both buttons disabled", async () => {
    const pending = pendingResponse();
    const fetchMock = quickServer({ ...onSiteOn, quick: () => pending.response });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());
    await waitFor(() => expect(issueButton()).toBeDisabled());
    expect(payButton()).toBeDisabled();
    await userEvent.click(issueButton());
    expect(quickPosts(fetchMock)).toHaveLength(1);
    pending.answer(jsonResponse(201, { invoice: issued() }));
    expect(await screen.findByText(ISSUED)).toBeInTheDocument();
  });

  it("opens the invoice", async () => {
    quickServer();
    const { router } = renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());
    await userEvent.click(await screen.findByRole("button", { name: "Open the invoice" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1001"));
  });
});

describe("QuickInvoicePage_RequestId", () => {
  it("sends the same request id again after a lost answer, and shows the invoice the replay answers", async () => {
    let posts = 0;
    const fetchMock = quickServer({
      quick: () =>
        posts++ === 0
          ? Promise.reject(new TypeError("Failed to fetch"))
          : Promise.resolve(jsonResponse(200, { invoice: issued() })),
    });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());
    expect(await screen.findByText("No answer came back")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));

    expect(await screen.findByText(ISSUED)).toBeInTheDocument();
    const [first, second] = quickPosts(fetchMock);
    expect(first.requestId).toMatch(UUID);
    expect(second).toEqual(first);
  });

  it("takes a new request id once the form is changed", async () => {
    let posts = 0;
    const fetchMock = quickServer({
      quick: () => (posts++ === 0 ? refusal(409, "customer_blocked") : jsonResponse(201, { invoice: issued() })),
    });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());
    await screen.findByText(invoicesCatalog.en["refusal.customer_blocked"]);
    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), " and parts");
    await userEvent.click(issueButton());

    await screen.findByText(ISSUED);
    const [first, second] = quickPosts(fetchMock);
    expect(second.requestId).toMatch(UUID);
    expect(second.requestId).not.toBe(first.requestId);
  });

  it("says request_id_reused in words", async () => {
    quickServer({ quick: () => refusal(409, "request_id_reused") });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());
    expect(
      await screen.findByText(
        "This quick invoice was sent before with other contents. Start a new quick invoice to issue another.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });
});

describe("QuickInvoicePage_EveryRefusalInWords", () => {
  it("shows a 400 on the line it names", async () => {
    quickServer({
      quick: () => problemResponse(400, "Invalid", { "lines[0].quantity": ["quantity must be greater than 0"] }),
    });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());
    expect(await screen.findByText(invoicesCatalog.en["fieldInvalid.line.quantity"])).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Line 1 quantity" })).toHaveAttribute("aria-invalid", "true");
  });

  it("names the missing address on buyer_incomplete, with a link to the customer", async () => {
    quickServer({ quick: () => refusal(409, "buyer_incomplete") });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());
    expect(
      await screen.findByText("The buyer has neither a complete address nor an organisation number."),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the customer" })).toHaveAttribute("href", "/customers/2001");
  });

  it.each([
    ["customer_blocked", 409],
    ["seller_incomplete", 409],
    ["kid_length_exceeded", 409],
    ["storage_unavailable", 503],
  ])("says %s in words", async (code, status) => {
    quickServer({ quick: () => refusal(status, code) });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(issueButton());
    const key = `refusal.${code}` as keyof typeof invoicesCatalog.en;
    expect(await screen.findByText(invoicesCatalog.en[key])).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("says a payment request's refusal after an issued invoice plainly", async () => {
    quickServer({
      ...onSiteOn,
      quick: () =>
        jsonResponse(201, { invoice: issued(), paymentRequestError: problem("on_site_payments_unavailable") }),
    });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(payButton());
    expect(await screen.findByText(ISSUED)).toBeInTheDocument();
    expect(await screen.findByText("The invoice is issued, but no Vipps payment request was made")).toBeInTheDocument();
    expect(screen.getByText(invoicesCatalog.en["refusal.on_site_payments_unavailable"])).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("says a request that failed without a code: the invoice is issued, take the payment from its page", async () => {
    quickServer({
      ...onSiteOn,
      quick: () => jsonResponse(201, { invoice: issued(), paymentRequestError: problem(undefined, 500) }),
    });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(payButton());
    expect(
      await screen.findByText("The invoice is issued; take the payment from the invoice page."),
    ).toBeInTheDocument();
  });

  it("links buyer_identity_missing to the customer", async () => {
    quickServer({
      ...onSiteOn,
      quick: () => jsonResponse(201, { invoice: issued(), paymentRequestError: problem("buyer_identity_missing") }),
    });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(payButton());
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText(invoicesCatalog.en["refusal.buyer_identity_missing"])).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: "Open the customer" })).toHaveAttribute("href", "/customers/2001");
  });
});

describe("QuickInvoicePage_GatedOnCanQuickInvoice", () => {
  it("says not allowed to invoices:issue without invoices:create, and asks nothing but meta", async () => {
    const fetchMock = quickServer({ capabilities: { canQuickInvoice: false, canCreate: false, canIssue: true } });
    renderRoute("/invoices/quick");

    expect(
      await screen.findByText("Making a quick invoice needs both invoices:create and invoices:issue."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Issue" })).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.map(([u]) => path(u))).toEqual(["/api/v1/invoices/meta"]);
  });
});

describe("QuickInvoicePage_NoPayButtonWithoutOnSite", () => {
  it("offers only Issue, without the kontantsalg reminder, while payment on site is not available", async () => {
    quickServer();
    renderRoute("/invoices/quick");

    await pickAcme();
    expect(issueButton()).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Issue and take payment with Vipps" })).not.toBeInTheDocument();
    expect(screen.queryByText(invoicesCatalog.en["quick.kontantsalgReminder"])).not.toBeInTheDocument();
  });

  it("offers the Vipps button with the reminder, and requires the customer's address for it", async () => {
    quickServer({ ...onSiteOn, addresses: [] });
    renderRoute("/invoices/quick");

    await fillForm();
    expect(await screen.findByText(invoicesCatalog.en["quick.noAddress"])).toBeInTheDocument();
    expect(payButton()).toBeDisabled();
    expect(issueButton()).toBeEnabled();
    expect(screen.getByText(invoicesCatalog.en["quick.kontantsalgReminder"])).toBeInTheDocument();
  });

  it("allows at most ten lines and at least one", async () => {
    quickServer();
    renderRoute("/invoices/quick");

    await pickAcme();
    expect(screen.getByRole("button", { name: "Remove line 1" })).toBeDisabled();
    for (let n = 2; n <= 10; n++) await userEvent.click(screen.getByRole("button", { name: "Add a line" }));
    expect(screen.getByRole("textbox", { name: "Line 10 description" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add a line" })).toBeDisabled();
  });
});

describe("QuickInvoicePage_IssueAndTakePayment", () => {
  it("shows the QR as an image, polls it to paid, and opens the invoice", async () => {
    let state: OnSitePaymentStatus = { state: "created", qrSvg: onSitePayment().qrSvg };
    const fetchMock = quickServer({
      ...onSiteOn,
      quick: () => jsonResponse(201, { invoice: issued(), paymentRequest: onSitePayment() }),
      status: () => state,
    });
    const { router } = renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(payButton());
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("img", { name: "QR code to scan with the Vipps app" })).toHaveAttribute(
      "src",
      dataUrlOf(onSitePayment().qrSvg as string),
    );
    expect(quickPosts(fetchMock)[0].payOnSite).toBe(true);
    expect(document.querySelector('path[d="M4 4h1v1h-1z"]')).toBeNull();
    state = { state: "captured", capturedAt: "2026-09-12T10:32:00Z" };
    expect(
      await within(dialog).findByText("Paid. The payment is registered on the invoice.", {}, { timeout: 6000 }),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1001"));
  });

  it("shows the replay's open request again by reading it, when the replay carries no QR", async () => {
    const fetchMock = quickServer({
      ...onSiteOn,
      quick: () => jsonResponse(200, { invoice: issued(), paymentRequest: onSitePayment({ qrSvg: undefined }) }),
    });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(payButton());
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("img", { name: "QR code to scan with the Vipps app" })).toHaveAttribute(
      "src",
      dataUrlOf(onSitePayment().qrSvg as string),
    );
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payment-requests")).toBeUndefined();
  });

  it.each([
    ["a replay", () => jsonResponse(200, { invoice: issued(), paymentRequest: onSitePayment({ qrSvg: undefined }) })],
    [
      "payment_request_active",
      () =>
        jsonResponse(201, {
          invoice: issued(),
          paymentRequestError: problem("payment_request_active", 409, { attemptId: 1001 }),
        }),
    ],
  ])("keeps an approved request on %s: no QR, no cancel", async (_, answer) => {
    const fetchMock = quickServer({ ...onSiteOn, quick: answer, status: () => ({ state: "authorized" }) });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(payButton());
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Approved. The money is being taken.")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Cancel the request" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Cancel it and make a new one" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("img")).not.toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payment-requests/1001/cancel")).toBeUndefined();
  });

  it("cancels an open request whose QR is gone and makes a new one", async () => {
    let cancelled = false;
    const fetchMock = quickServer({
      ...onSiteOn,
      quick: () => jsonResponse(200, { invoice: issued(), paymentRequest: onSitePayment({ qrSvg: undefined }) }),
      status: () => (cancelled ? { state: "created", qrSvg: onSitePayment().qrSvg } : { state: "creating" }),
      answers: {
        "POST /api/v1/invoices/1001/payment-requests/1001/cancel": () => {
          cancelled = true;
          return jsonResponse(200, { state: "cancelled" });
        },
        "POST /api/v1/invoices/1001/payment-requests": () => jsonResponse(201, onSitePayment()),
      },
    });
    renderRoute("/invoices/quick");

    await fillForm();
    await userEvent.click(payButton());
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(await within(dialog).findByRole("button", { name: "Cancel it and make a new one" }));
    expect(await within(dialog).findByRole("img", { name: "QR code to scan with the Vipps app" })).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payment-requests/1001/cancel")).toBe(NO_BODY);
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payment-requests")).toEqual({ flow: "on_site" });
  });
});

describe("QuickInvoicePage_NewCustomer", () => {
  it("creates a customer with its address through the customers API and picks it", async () => {
    const fetchMock = quickServer({
      answers: {
        "POST /api/v1/customers": () => jsonResponse(201, { id: 2001, customerNumber: 10001 }),
        "POST /api/v1/customers/2001/addresses": () => jsonResponse(201, acmeAddress),
      },
    });
    renderRoute("/invoices/quick", { canCreateCustomers: true });

    await userEvent.click(await screen.findByRole("button", { name: "New customer" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Acme AS");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Address" }), "Kundeveien 2");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Postal code" }), "0150");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "City" }), "Oslo");
    await userEvent.click(within(dialog).getByRole("button", { name: "Create the customer" }));

    expect(await screen.findByText("Kundeveien 2, 0150 Oslo, NO")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/customers")).toEqual({ name: "Acme AS", type: "business" });
    expect(requestTo(fetchMock, "POST", "/api/v1/customers/2001/addresses")).toEqual({
      type: "invoice",
      isPrimary: true,
      line1: "Kundeveien 2",
      postalCode: "0150",
      city: "Oslo",
      country: "NO",
    });
  });

  it("offers no New customer without the customers permissions", async () => {
    quickServer();
    renderRoute("/invoices/quick");

    await screen.findByRole("combobox", { name: "Customer" });
    expect(screen.queryByRole("button", { name: "New customer" })).not.toBeInTheDocument();
  });
});

describe("the quick invoice's words", () => {
  it("names the screen and its buttons in Norwegian", () => {
    const nb = invoicesCatalog.nb as Record<string, string>;
    expect(nb["quick.title"]).toBe("Hurtigfaktura");
    expect(nb["quick.issueAndPay"]).toBe("Utsted og ta betalt med Vipps");
    expect(nb["quick.kontantsalgReminder"]).toMatch(/kontantsalg/);
  });
});
