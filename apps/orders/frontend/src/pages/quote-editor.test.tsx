import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { line, meta, order, quote, settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

const url = "/api/v1/orders/quotes/1001";

/** The quote's server: the quote as given, every write answered by `writes`, the rest empty. */
const server = (
  current = quote(),
  writes: (call: { method: string; url: string; body: unknown }) => Response | undefined = () => undefined,
  options: { meta?: unknown } = {},
) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
    if (call.method !== "GET") return writes(call);
    return commonAnswers(call, settings(), options.meta ?? meta());
  });

describe("the quote editor", () => {
  it("warns, in yellow, when the delivery address is not a delivery address on the customer", async () => {
    const current = quote();
    server({ ...current, customer: { ...current.customer, deliveryAddressId: 6 } });
    renderRoute("/orders/quotes/1001");
    expect(await screen.findByTestId("delivery-address-warning")).toHaveTextContent(
      "This is not a delivery address on the customer.",
    );
  });

  it("gives no warning for one of the customer's delivery addresses", async () => {
    const current = quote();
    server({ ...current, customer: { ...current.customer, deliveryAddressId: 7 } });
    renderRoute("/orders/quotes/1001");
    expect(await screen.findByRole("heading", { name: /Quote 1042/ })).toBeInTheDocument();
    await waitFor(() =>
      expect((screen.getByPlaceholderText("The customer's address") as HTMLInputElement).value).toContain("Lager"),
    );
    expect(screen.queryByTestId("delivery-address-warning")).not.toBeInTheDocument();
  });

  it("shows the quote's header, its lines and the server's totals", async () => {
    server();
    renderRoute("/orders/quotes/1001");

    expect(await screen.findByRole("heading", { name: /Quote 1042/ })).toBeInTheDocument();
    expect(screen.getByTestId("quote-status")).toHaveTextContent("Draft");
    expect(screen.getByLabelText("Quantity of line 1")).toHaveValue("3");
    expect(screen.getByTestId("line-amount-1")).toHaveTextContent("1,500.00");
    expect(screen.getByTestId("grand-total")).toHaveTextContent("1,875.00");
    expect(screen.getByLabelText("Your reference")).toHaveValue("PO-77");
  });

  it("saves the header and the lines with the revision it read, leaving the catalog price to the server", async () => {
    const saved = quote({
      lines: [line({ quantity: 5, lineNet: 2500, lineGross: 2500 })],
      totals: { net: 2500, vat: 625, gross: 3125, vatSummaries: [] },
      updatedAt: "2026-11-02T08:00:00Z",
    });
    const fake = server(quote(), (call) => (call.method === "PUT" ? jsonResponse(200, saved) : undefined));
    renderRoute("/orders/quotes/1001");

    const quantity = await screen.findByLabelText("Quantity of line 1");
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "5");
    // A changed row shows no amount until the server has computed it.
    expect(screen.getByTestId("line-amount-1")).toHaveTextContent("—");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    const body = fake.sent("PUT", url)?.body as Record<string, unknown> & { lines: Record<string, unknown>[] };
    expect(body.revision).toBe(1);
    expect(body.customerId).toBe(42);
    expect(body.yourReference).toBe("PO-77");
    expect(body.lines).toEqual([
      {
        id: 5001,
        kind: "product",
        variantId: 301,
        description: "Skjøtekabel",
        quantity: 5,
        unit: "stk",
        discountPercent: 0,
        optional: false,
      },
    ]);
    expect(await screen.findByText("The quote is saved")).toBeInTheDocument();
    expect(await screen.findByTestId("grand-total")).toHaveTextContent("3,125.00");
  });

  it("sends a typed price, and adds a free-text line with its own VAT code", async () => {
    const fake = server(quote(), (call) =>
      call.method === "PUT" ? jsonResponse(200, quote({ updatedAt: "2026-11-02T08:00:00Z" })) : undefined,
    );
    renderRoute("/orders/quotes/1001");

    const price = await screen.findByLabelText("Unit price of line 1");
    await userEvent.clear(price);
    await userEvent.type(price, "450");
    await userEvent.click(screen.getByRole("button", { name: "Free-text line" }));
    await userEvent.type(screen.getByLabelText("Name of line 2"), "Montering");
    await userEvent.type(screen.getByLabelText("Unit price of line 2"), "900");
    await userEvent.click(screen.getByRole("combobox", { name: "VAT code of line 2" }));
    await userEvent.click(await screen.findByRole("option", { name: /Fritatt/ }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    const lines = (fake.sent("PUT", url)?.body as { lines: Record<string, unknown>[] } | undefined)?.lines ?? [];
    expect(lines[0]).toMatchObject({ id: 5001, unitPrice: 450 });
    expect(lines[1]).toEqual({
      kind: "product",
      variantId: null,
      name: "Montering",
      vatCodeId: 5,
      description: "",
      quantity: 1,
      unit: "stk",
      unitPrice: 900,
      discountPercent: 0,
      optional: false,
    });
  });

  it("refuses to save a catalog row with no product picked, before asking the server", async () => {
    const fake = server();
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Product" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Pick a product, or remove the line.")).toBeInTheDocument();
    expect(fake.count("PUT", url)).toBe(0);
  });

  it("moves a line and removes one", async () => {
    const fake = server(
      quote({ lines: [line(), line({ id: 5002, kind: "section", description: "Montasje", variantId: null })] }),
      (call) => (call.method === "PUT" ? jsonResponse(200, quote({ updatedAt: "2026-11-02T08:00:00Z" })) : undefined),
    );
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Move line 2 up" }));
    await userEvent.click(screen.getByRole("button", { name: "Remove line 2" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    expect((fake.sent("PUT", url)?.body as { lines: unknown[] } | undefined)?.lines).toEqual([
      { id: 5002, kind: "section", description: "Montasje" },
    ]);
  });

  it("says a line's refusal under the line, in the reader's words", async () => {
    server(quote(), (call) =>
      call.method === "PUT"
        ? refusal(400, "line_unpriced", { lines: [{ index: 0, code: "line_unpriced", detail: "No price." }] })
        : undefined,
    );
    renderRoute("/orders/quotes/1001");

    await userEvent.type(await screen.findByLabelText("Your reference"), "-2");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    const alerts = await screen.findAllByText("A line has no price in this currency. Give it a price.");
    expect(alerts.length).toBeGreaterThan(0);
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("offers Reload when somebody else saved the quote first, and reads it anew", async () => {
    let current = quote();
    const fake = server(current, (call) => (call.method === "PUT" ? refusal(409, "version_conflict") : undefined));
    renderRoute("/orders/quotes/1001");

    await userEvent.type(await screen.findByLabelText("Your reference"), "-2");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    const alert = await screen.findByTestId("conflict-alert");
    expect(alert).toHaveTextContent("Changed by someone else");
    current = quote({ yourReference: "PO-99", updatedAt: "2026-11-03T08:00:00Z" });
    fake.calls.length = 0;
    // The next read answers the newer quote.
    server(current);
    await userEvent.click(within(alert).getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(screen.getByLabelText("Your reference")).toHaveValue("PO-99"));
    expect(screen.queryByTestId("conflict-alert")).not.toBeInTheDocument();
  });

  it("sends the quote to the contact by default, with CC, message and valid until", async () => {
    const fake = server(quote(), (call) =>
      call.url === `${url}/send`
        ? jsonResponse(
            200,
            quote({ status: "sent", sentAt: "2026-11-05T10:00:00Z", updatedAt: "2026-11-05T10:00:00Z" }),
          )
        : undefined,
    );
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Send" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText(/Recipient/)).toHaveValue("ola@acme.no");
    await userEvent.type(within(dialog).getByRole("combobox", { name: /^CC/ }), "innkjop@acme.no{enter}");
    await userEvent.type(within(dialog).getByLabelText(/Message/), "Takk for praten");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

    await waitFor(() => expect(fake.sent("POST", `${url}/send`)).toBeDefined());
    expect(fake.sent("POST", `${url}/send`)?.body).toEqual({
      recipient: "ola@acme.no",
      cc: ["innkjop@acme.no"],
      message: "Takk for praten",
      validUntil: "2026-12-01",
      revision: 1,
      expectedUpdatedAt: "2026-11-01T08:00:00Z",
    });
    expect(await screen.findByText("The quote is sent")).toBeInTheDocument();
    expect(await screen.findByTestId("quote-status")).toHaveTextContent("Sent");
  });

  it("says in the dialog that sending is off when the server refuses it", async () => {
    server(quote(), (call) => (call.url === `${url}/send` ? refusal(503, "sending_disabled") : undefined), {
      meta: meta({ sendingAvailable: true }),
    });
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Send" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Sending is off");
  });

  it("disables Send with the reason when Communications cannot send, and still marks the quote as sent", async () => {
    const fake = server(
      quote(),
      (call) =>
        call.url === `${url}/mark-sent`
          ? jsonResponse(200, quote({ status: "sent", updatedAt: "2026-11-05T10:00:00Z" }))
          : undefined,
      { meta: meta({ sendingAvailable: false }) },
    );
    renderRoute("/orders/quotes/1001");

    await waitFor(() => expect(screen.getByRole("button", { name: "Send" })).toBeDisabled());
    expect(screen.getByText("Download the PDF and mark the quote as sent instead.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Mark as sent" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Mark as sent" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/mark-sent`)?.body).toEqual({
        revision: 1,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
        validUntil: "2026-12-01",
      }),
    );
  });

  it("shows a sent quote read-only, and revises it back to a draft", async () => {
    const sent = quote({ status: "sent", sentAt: "2026-11-05T10:00:00Z" });
    const fake = server(sent, (call) =>
      call.url === `${url}/revise`
        ? jsonResponse(200, quote({ revision: 2, updatedAt: "2026-11-06T10:00:00Z" }))
        : undefined,
    );
    renderRoute("/orders/quotes/1001");

    await screen.findByRole("heading", { name: /Quote 1042/ });
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Quantity of line 1")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Revise" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/revise`)?.body).toEqual({
        revision: 1,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
      }),
    );
    expect(await screen.findByText("revision 2")).toBeInTheDocument();
  });

  it("offers Revise and Decline on an expired quote, sending the updatedAt it read", async () => {
    const expired = quote({
      status: "expired",
      sentAt: "2026-10-01T10:00:00Z",
      updatedAt: "2026-11-02T00:00:00.123456Z",
    });
    const fake = server(expired, (call) =>
      call.url === `${url}/revise`
        ? jsonResponse(200, quote({ revision: 2, updatedAt: "2026-11-06T10:00:00Z" }))
        : undefined,
    );
    renderRoute("/orders/quotes/1001");

    await screen.findByRole("heading", { name: /Quote 1042/ });
    expect(screen.getByRole("button", { name: "Decline" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Revise" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/revise`)?.body).toEqual({
        revision: 1,
        expectedUpdatedAt: "2026-11-02T00:00:00.123456Z",
      }),
    );
  });

  it("accepts with the optional lines the customer took into a draft order, and opens it", async () => {
    const sent = quote({
      status: "sent",
      lines: [line(), line({ id: 5003, name: "Ekstra kabel", optional: true, lineNet: 300 })],
    });
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, sent);
      if (call.url === `${url}/accept`)
        return jsonResponse(200, { quote: quote({ status: "accepted" }), order: order({ id: 2009, number: 5009 }) });
      if (call.method === "GET" && call.url === "/api/v1/orders/orders/2009")
        return jsonResponse(200, order({ id: 2009, number: 5009 }));
      return commonAnswers(call, settings());
    });
    const { router } = renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Accept" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Ekstra kabel/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Accept" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/orders/orders/2009"));
    expect(fake.sent("POST", `${url}/accept`)?.body).toEqual({
      revision: 1,
      expectedUpdatedAt: "2026-11-01T08:00:00Z",
      into: "draft_order",
      optionalLineIds: [5003],
    });
  });

  it("offers Accept and confirm only with orders:sales-confirm", async () => {
    server(quote({ status: "sent" }));
    renderRoute("/orders/quotes/1001", { canConfirmOrders: false });

    await userEvent.click(await screen.findByRole("button", { name: "Accept" }));
    expect(within(await screen.findByRole("dialog")).getByRole("radio", { name: /A confirmed order/ })).toBeDisabled();
  });

  it("asks once more before accepting an expired quote", async () => {
    server(quote({ status: "expired" }));
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Accept" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "Accept" })).toBeDisabled();
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Accept it anyway/ }));
    expect(within(dialog).getByRole("button", { name: "Accept" })).toBeEnabled();
  });

  it("declines with a reason", async () => {
    const fake = server(quote({ status: "sent" }), (call) =>
      call.url === `${url}/decline`
        ? jsonResponse(200, quote({ status: "declined", declineReason: "For dyrt", updatedAt: "2026-11-06T10:00:00Z" }))
        : undefined,
    );
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Decline" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Reason"), "For dyrt");
    await userEvent.click(within(dialog).getByRole("button", { name: "Decline" }));
    await waitFor(() =>
      expect(fake.sent("POST", `${url}/decline`)?.body).toEqual({
        revision: 1,
        expectedUpdatedAt: "2026-11-01T08:00:00Z",
        reason: "For dyrt",
      }),
    );
    expect(await screen.findByText("For dyrt")).toBeInTheDocument();
  });

  it("offers no edits and no actions without orders:quotes-manage", async () => {
    server();
    renderRoute("/orders/quotes/1001", { canManageQuotes: false });

    await screen.findByRole("heading", { name: /Quote 1042/ });
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
    expect(screen.queryByTestId("quote-actions")).not.toBeInTheDocument();
  });

  it("says a quote that is gone is not found", async () => {
    fakeServer(() => undefined);
    renderRoute("/orders/quotes/1001");
    expect(await screen.findByText("Quote not found")).toBeInTheDocument();
  });
});

describe("the quote's salesperson", () => {
  it("picks another salesperson among those who may sell, and saves it", async () => {
    const fake = server(quote(), (call) => (call.method === "PUT" ? jsonResponse(200, quote()) : undefined));
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("combobox", { name: "Salesperson" }));
    await userEvent.click(await screen.findByRole("option", { name: "Per Selger" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    const body = fake.sent("PUT", url)?.body as Record<string, unknown> | undefined;
    expect(body?.salespersonId).toBe("00000000-0000-4000-8000-000000000002");
  });
});

describe("the new quote", () => {
  it("starts from the customer it was opened for, and opens the created quote", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "POST" && call.url === "/api/v1/orders/quotes") return jsonResponse(201, quote({ id: 1077 }));
      if (call.method === "GET" && call.url === "/api/v1/orders/quotes/1077")
        return jsonResponse(200, quote({ id: 1077 }));
      return commonAnswers(call, settings());
    });
    const { router } = renderRoute("/orders/quotes/new?customerId=42");

    await userEvent.click(await screen.findByRole("button", { name: "Create the quote" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orders/quotes/1077"));
    const body = fake.sent("POST", "/api/v1/orders/quotes")?.body as Record<string, unknown>;
    expect(body.customerId).toBe(42);
    expect(body.salespersonId).toBe("00000000-0000-4000-8000-000000000001");
    expect(body.clientRequestId).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("waits for a customer to be picked", async () => {
    fakeServer((call) => commonAnswers(call));
    renderRoute("/orders/quotes/new");
    expect(await screen.findByRole("button", { name: "Create the quote" })).toBeDisabled();
  });

  it("tells a caller without orders:quotes-manage they may not make one", async () => {
    fakeServer((call) => commonAnswers(call));
    renderRoute("/orders/quotes/new", { canManageQuotes: false });
    expect(await screen.findByText("You may not make quotes.")).toBeInTheDocument();
  });
});
