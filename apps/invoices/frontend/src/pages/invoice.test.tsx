import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it, vi } from "vitest";
import type { InvoiceDocument, InvoiceInput } from "../api/invoices";
import { setUnauthorizedHandler } from "../api/request";
import { customerSearch, jsonResponse, listedCustomers, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { creditDraft, draft, issued, listPage, meta, vatCodes } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const path = (input: RequestInfo | URL) => String(input);

/** Beyond the documents: the meta to answer, and any other answer by "METHOD url". */
interface ServerOptions {
  meta?: Parameters<typeof meta>[0];
  answers?: Record<string, Response | (() => Response)>;
  /**
   * The server's own view of a document when it has moved on since the page
   * read it — another user's credit note, a day that has turned. The rules
   * below decide by it; the page's GET still answers the document it read.
   */
  current?: Record<number, InvoiceDocument>;
  /** The dates the server allows an issue now, when the day has turned since the draft was read. */
  allowedNow?: string[];
}

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

const bodyOf = (init?: RequestInit) => (init?.body ? JSON.parse(String(init.body)) : {});

/**
 * The fetch fake over a set of documents by id. A canned answer by
 * "METHOD url" wins; without one, each write is decided by the server's own
 * rules the page relies on: an issue only into a date `allowedIssueDates`
 * names and only with a store, a credit only of an issued invoice with money
 * left, a save only at the current revision and, for a credit note, only
 * lowering a quantity or a price (D6, D8).
 */
const server = (
  documents: Record<number, InvoiceDocument>,
  answers: Record<string, InvoiceDocument> = {},
  options: ServerOptions = {},
) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const other = options.answers?.[`${method} ${url}`];
    if (other) return typeof other === "function" ? other() : other.clone();
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta(options.meta));
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
    const answer = answers[`${method} ${url}`];
    if (answer) return jsonResponse(method === "POST" && url.endsWith("/credit") ? 201 : 200, answer);
    if (url.startsWith("/api/v1/customers?")) return customerSearch(url);

    const match = /^\/api\/v1\/invoices\/(\d+)(\/issue|\/credit)?$/.exec(url);
    if (!match) return new Response(null, { status: 404 });
    const id = Number(match[1]);
    const doc = options.current?.[id] ?? documents[id];
    if (!doc) return new Response(null, { status: 404 });
    const action = `${method} ${match[2] ?? ""}`;

    if (action === "GET ") return jsonResponse(200, documents[id]);
    if (action === "POST /issue") {
      if (doc.status !== "draft") return refusal(409, "invoice_issued");
      if (meta(options.meta).storageAvailable === false) return refusal(503, "storage_unavailable");
      const allowed = options.allowedNow ?? doc.allowedIssueDates ?? [];
      const issueDate = bodyOf(init).issueDate ?? allowed[allowed.length - 1];
      if (!allowed.includes(issueDate)) return refusal(409, "issue_date_not_allowed", { allowedIssueDates: allowed });
      if (doc.lines.length === 0) return refusal(409, "no_lines");
      documents[id] = issued({ id, issueDate });
      return jsonResponse(200, documents[id]);
    }
    if (action === "POST /credit") {
      if (doc.status === "draft") return refusal(409, "invoice_draft");
      if (doc.kind === "credit_note") return refusal(409, "credit_note_not_creditable");
      if ((doc.uncreditedAmount ?? 0) <= 0) return refusal(409, "invoice_fully_credited");
      return jsonResponse(
        201,
        creditDraft({ credits: { id, number: doc.number ?? 0, issueDate: doc.issueDate ?? "" } }),
      );
    }
    if (action === "PUT ") {
      const body = bodyOf(init) as InvoiceInput;
      if (body.revision !== doc.revision) {
        return jsonResponse(409, {
          title: "Conflict",
          status: 409,
          detail: `Invoice revision ${doc.revision} is current`,
        });
      }
      const errors: Record<string, string[]> = {};
      // A credit note changes only what a correction may (credits.go's
      // putCreditDraft): every field it copied from its original is compared
      // with the draft's own, each line with the original line it credits.
      if (doc.kind === "credit_note") {
        const originals = (doc.credits && documents[doc.credits.id]?.lines) || [];
        const add = (field: string, message: string) => {
          errors[field] = [message];
        };
        if (body.customerId !== doc.customerId) add("customerId", "A credit note's customer is its original's");
        if (body.paymentTermsDays !== undefined) add("paymentTermsDays", "A credit note has no payment terms");
        if (
          body.deliveryDate !== doc.deliveryDate ||
          body.deliveryFrom !== doc.deliveryFrom ||
          body.deliveryTo !== doc.deliveryTo
        )
          add("deliveryDate", "A credit note keeps its original's delivery");
        const place = (a?: InvoiceInput["deliveryAddress"]) =>
          [a?.line1, a?.line2, a?.postalCode, a?.city, a?.country].map((v) => v ?? "").join("|");
        if (place(body.deliveryAddress) !== place(doc.deliveryAddress))
          add("deliveryAddress", "A credit note keeps its original's place of delivery");
        for (const field of ["yourReference", "ourReference", "orderReference"] as const) {
          if ((body[field] ?? "") !== doc[field]) add(field, "A credit note keeps its original's references");
        }
        body.lines.forEach((l, i) => {
          const o = originals.find((ol) => ol.id === l.creditsLineId);
          const field = (name: string) => `lines[${i}].${name}`;
          if (!o) {
            add(field("creditsLineId"), "A credit note only credits the original's lines; it adds none");
            return;
          }
          if (l.vatCodeId !== o.vatCodeId) add(field("vatCodeId"), "A credit note keeps the original line's VAT code");
          if ((l.unit ?? "") !== o.unit) add(field("unit"), "A credit note keeps the original line's unit");
          if (l.quantity > o.quantity) add(field("quantity"), "A credit note may lower a quantity, never raise it");
          if (l.unitPrice > o.unitPrice) add(field("unitPrice"), "A credit note may lower a price, never raise it");
          if ((l.discountPercent ?? 0) < o.discountPercent)
            add(field("discountPercent"), "A credit note may not lower a discount");
        });
      }
      if (Object.keys(errors).length > 0) return jsonResponse(400, { title: "Invalid", status: 400, errors });
      return jsonResponse(200, {
        ...doc,
        customerId: body.customerId,
        customerName: listedCustomers.find((c) => c.id === body.customerId)?.name ?? doc.customerName,
        revision: doc.revision + 1,
        lines: body.lines.map((l, i) => ({
          ...doc.lines[0],
          ...l,
          id: 7000 + i,
          position: i + 1,
          unit: l.unit ?? "",
          discountPercent: l.discountPercent ?? 0,
        })),
      });
    }
    return new Response(null, { status: 404 });
  });

describe("the draft editor", () => {
  it("totals VAT per rate on the sum of the nets, live, the server's way to the øre", async () => {
    server({ 1001: draft() });
    renderRoute("/invoices/1001");

    // Three lines of 33.33 at 25 %: per line 3 × 8.33 = 24.99, per rate 25.00.
    expect(await screen.findByTestId("vat-total")).toHaveTextContent("25.00");
    expect(screen.getByTestId("gross-total")).toHaveTextContent("124.99");

    const quantity = screen.getByRole("textbox", { name: "Line 1 quantity" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "2");
    // 2 × 33.33 + 2 × 33.33 = 133.32, VAT round(33.33) = 33.33.
    expect(screen.getByTestId("net-total")).toHaveTextContent("133.32");
    expect(screen.getByTestId("vat-total")).toHaveTextContent("33.33");
  });

  it("saves the whole draft with its revision", async () => {
    const saved = draft({ revision: 4 });
    const fetchMock = server({ 1001: draft() }, { "PUT /api/v1/invoices/1001": saved });
    renderRoute("/invoices/1001");

    const description = await screen.findByRole("textbox", { name: "Line 2 description" });
    await userEvent.clear(description);
    await userEvent.type(description, "Rådgivning");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/1001"));
    const body = sent(fetchMock, "PUT").body;
    expect(body.revision).toBe(3);
    expect(body.paymentTermsDays).toBe(30);
    expect(body.deliveryDate).toBe("2026-09-10");
    expect(body.lines[1]).toEqual({
      description: "Rådgivning",
      quantity: 1,
      unit: "timer",
      unitPrice: 33.33,
      discountPercent: 0,
      vatCodeId: 1,
    });
  });

  // A warning a newer server added is said in general words; a key the
  // catalog lacks would throw outside production and take the editor down.
  it("says a warning it has no words for, rather than failing", async () => {
    server({ 1001: draft({ warnings: ["something_new"] }) });
    renderRoute("/invoices/1001");
    expect(
      await screen.findByText("The server flags something this version has no words for (something_new)."),
    ).toBeInTheDocument();
  });

  it("nudges when the buyer's reference is empty and shows the draft's warnings", async () => {
    server({ 1001: draft({ yourReference: "", warnings: ["issued_late"] }) });
    renderRoute("/invoices/1001");

    expect(await screen.findByText(/an e-invoice \(EHF\) will need the buyer's reference/)).toBeInTheDocument();
    expect(screen.getByText(/the law asks for the invoice within a month/)).toBeInTheDocument();
  });
});

describe("the issue dialog", () => {
  it("offers the last day of the previous month when the server allows it, and issues with the chosen date", async () => {
    const answer = issued({ warnings: ["issued_late"] });
    const fetchMock = server(
      { 1001: draft({ allowedIssueDates: ["2026-08-31", "2026-09-12"] }) },
      { "POST /api/v1/invoices/1001/issue": answer },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Issue" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("This assigns the next number and cannot be undone.")).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: "Sep 12, 2026" })).toBeChecked();
    await userEvent.click(within(dialog).getByRole("radio", { name: "Aug 31, 2026" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Issue" }));

    await waitFor(() => expect(sent(fetchMock, "POST").body).toEqual({ issueDate: "2026-08-31" }));
    expect(await screen.findByText("Issued as number 1000")).toBeInTheDocument();
    expect(await screen.findByText(/the law asks for the invoice within a month/)).toBeInTheDocument();
  });

  it("shows the issued document once it is issued", async () => {
    const fetchMock = server({ 1001: draft() });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Issue" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Issue" }));

    expect(await screen.findByRole("heading", { name: "Invoice 1000" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download PDF" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "POST").body).toEqual({ issueDate: "2026-09-12" });
  });

  it("offers today alone after the 15th", async () => {
    server({ 1001: draft({ allowedIssueDates: ["2026-09-16"] }) });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Issue" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("radio")).not.toBeInTheDocument();
    expect(within(dialog).getByText("It is issued today, Sep 16, 2026.")).toBeInTheDocument();
  });
});

describe("an issued document", () => {
  it("downloads its PDF, says when it is not stored yet, and credits into a new draft", async () => {
    const fetchMock = server(
      { 1001: issued({ pdfStored: false }), 1002: creditDraft() },
      { "POST /api/v1/invoices/1001/credit": creditDraft() },
    );
    const { router } = renderRoute("/invoices/1001");

    expect(await screen.findByRole("heading", { name: "Invoice 1000" })).toBeInTheDocument();
    expect(screen.getByText(/It is stored the first time it is downloaded/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Credit" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1002"));
    expect(
      fetchMock.actualCalls.some(
        ([url, init]) => path(url) === "/api/v1/invoices/1001/credit" && init?.method === "POST",
      ),
    ).toBe(true);
  });

  it("offers no credit for an invoice credited in full", async () => {
    server({ 1001: issued({ uncreditedAmount: 0, creditedAmount: 124.99 }) });
    renderRoute("/invoices/1001");
    await screen.findByRole("heading", { name: "Invoice 1000" });
    expect(screen.queryByRole("button", { name: "Credit" })).not.toBeInTheDocument();
  });
});

/**
 * A day on which code 3's rate is 26 % (its period from 2027) while the
 * original invoice was issued at 25 %: a credit note reverses at the
 * original line's snapshot rate, never the code's rate today, and only a day
 * like this tells the two apart.
 */
const afterTheRateChange = { today: "2027-01-05" };

describe("a credit-note draft", () => {
  it("names its original, adds no lines, keeps the VAT codes, and shows the cap warnings", async () => {
    server(
      { 1002: creditDraft({ warnings: ["credit_exceeds_line"] }), 1001: issued() },
      {},
      { meta: afterTheRateChange },
    );
    renderRoute("/invoices/1002");

    expect(await screen.findByText("Credit note for invoice")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "1000" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add a line" })).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Line 1 VAT code" })).toBeDisabled();
    expect(screen.getByText(/credits more than the original line had left/)).toBeInTheDocument();
    // The estimate while editing is at the original line's own 25 % — 8.33 —
    // not the code's 26 % today, which would be 8.67.
    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), " retur");
    expect(screen.getByText(/An estimate while you edit/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("8.33"));
  });

  it("names a code deactivated since, and never raises a quantity past the original's", async () => {
    const base = creditDraft();
    server({ 1002: creditDraft({ lines: [{ ...base.lines[0], vatCodeId: 9 }] }), 1001: issued() });
    renderRoute("/invoices/1002");

    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Line 1 VAT code" })).toHaveValue(
        "3G — Gammel sats (no longer offered)",
      ),
    );
    const quantity = screen.getByRole("textbox", { name: "Line 1 quantity" });
    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("8.33"));
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "5");
    await userEvent.tab();
    expect(quantity).toHaveValue("1");
  });
});

describe("refusals", () => {
  it("names the line a refusal names, in the reader's language", async () => {
    server(
      { 1001: draft() },
      {},
      { answers: { "POST /api/v1/invoices/1001/issue": refusal(409, "vat_code_inactive", { linePosition: 2 }) } },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Issue" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Issue" }));
    expect(await screen.findByText("The VAT code on line 2 is no longer offered.")).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("names the dates an issue may take", async () => {
    server(
      { 1001: draft() },
      {},
      {
        answers: {
          "POST /api/v1/invoices/1001/issue": refusal(409, "issue_date_not_allowed", {
            allowedIssueDates: ["2026-09-12"],
          }),
        },
      },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Issue" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Issue" }));
    expect(
      await screen.findByText("That issue date is not allowed today. It may be: Sep 12, 2026."),
    ).toBeInTheDocument();
  });

  it("says when the metadata cannot be loaded", async () => {
    server({ 1001: draft() }, {}, { answers: { "GET /api/v1/invoices/meta": jsonResponse(500, { title: "Boom" }) } });
    renderRoute("/invoices/1001");
    expect(await screen.findByText("Could not load Invoices")).toBeInTheDocument();
  });
});

describe("the PDF", () => {
  it("downloads the stored PDF behind a button", async () => {
    const createObjectURL = vi.fn(() => "blob:pdf");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this);
    });
    const fetchMock = server(
      { 1001: issued() },
      {},
      {
        answers: {
          "GET /api/v1/invoices/1001/pdf": () =>
            new Response("%PDF-1.7", {
              status: 200,
              headers: {
                "Content-Type": "application/pdf",
                "Content-Disposition": 'attachment; filename="faktura-1000.pdf"',
              },
            }),
        },
      },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Download PDF" }));
    await waitFor(() => expect(createObjectURL).toHaveBeenCalled());
    expect(fetchMock.actualCalls.some(([url]) => path(url) === "/api/v1/invoices/1001/pdf")).toBe(true);
    // Saved under the name the server gives it, from the blob it fetched.
    expect(clicked).toHaveLength(1);
    expect(clicked[0].download).toBe("faktura-1000.pdf");
    expect(clicked[0].href).toBe("blob:pdf");
  });

  it("saves a PDF the server names no file for under the catalog's name", async () => {
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:pdf"), revokeObjectURL: vi.fn() }));
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this);
    });
    server(
      { 1001: issued() },
      {},
      {
        answers: {
          "GET /api/v1/invoices/1001/pdf": () =>
            new Response("%PDF-1.7", { status: 200, headers: { "Content-Type": "application/pdf" } }),
        },
      },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Download PDF" }));
    await waitFor(() => expect(clicked).toHaveLength(1));
    expect(clicked[0].download).toBe("document.pdf");
  });

  // A PDF is a blob, so it cannot go through the shared client; a 401 still
  // reaches the host's handler, which signs the person in again.
  it("hands an expired session to the host, as every other request does", async () => {
    const expired = vi.fn();
    setUnauthorizedHandler(expired);
    server(
      { 1001: issued() },
      {},
      { answers: { "GET /api/v1/invoices/1001/pdf": jsonResponse(401, { title: "Unauthorized", status: 401 }) } },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Download PDF" }));
    await waitFor(() => expect(expired).toHaveBeenCalledTimes(1));
  });

  it("says a refusal as a notification, never the problem JSON in the browser", async () => {
    server(
      { 1001: issued() },
      {},
      { answers: { "GET /api/v1/invoices/1001/pdf": refusal(503, "storage_unavailable") } },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Download PDF" }));
    expect(await screen.findByText("Could not open the PDF")).toBeInTheDocument();
    expect(
      screen.getByText("The document store is unavailable, so nothing can be issued or downloaded now."),
    ).toBeInTheDocument();
  });
});

describe("what the editor offers", () => {
  it("offers no issue without an object store, and says why", async () => {
    server({ 1001: draft() }, {}, { meta: { storageAvailable: false } });
    renderRoute("/invoices/1001");

    expect(await screen.findByRole("button", { name: "Issue" })).toBeDisabled();
    expect(screen.getByText("This installation has no document store, so nothing can be issued.")).toBeInTheDocument();
  });

  it("is read-only to a caller who may not create drafts", async () => {
    server(
      { 1001: draft() },
      {},
      {
        meta: {
          capabilities: {
            canCreate: false,
            canIssue: false,
            canManage: false,
            canRegisterPayments: false,
            canSend: false,
          },
        },
      },
    );
    renderRoute("/invoices/1001");

    expect(await screen.findByRole("textbox", { name: "Line 1 description" })).toHaveAttribute("readonly");
    expect(screen.getByRole("textbox", { name: "Line 1 quantity" })).toHaveAttribute("readonly");
    expect(screen.getByRole("combobox", { name: "Line 1 VAT code" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add a line" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove line 1" })).not.toBeInTheDocument();
  });

  it("names and totals a line whose code is no longer offered as the server does", async () => {
    const lines = draft().lines.map((l) => ({ ...l, vatCodeId: 9 }));
    server({ 1001: draft({ lines }) });
    renderRoute("/invoices/1001");

    // Code 9 is deactivated but still has 25 % today: the server totals it at
    // 25 % (and refuses it at issue), so the editor's estimate does too.
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Line 1 VAT code" })).toHaveValue(
        "3G — Gammel sats (no longer offered)",
      ),
    );
    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), " endret");
    expect(screen.getByText(/An estimate while you edit/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("25.00"));
  });

  it("totals a line whose code has no rate today at 0 %, as the server does, with its warning", async () => {
    const lines = draft().lines.map((l) => ({ ...l, vatCodeId: 1 }));
    // As the server answers it: the lines at 0 %, the draft warned.
    const answered = draft({
      lines,
      warnings: ["vat_code_not_valid"],
      vatSummaries: [
        { vatCategory: "S", ratePercent: 0, safTCode: "3", taxableAmount: 99.99, vatAmount: 0, vatAmountNok: 0 },
      ],
      vatTotal: 0,
      vatTotalNok: 0,
      grossTotal: 99.99,
    });
    server({ 1001: answered }, {}, { meta: { today: "2025-06-01" } });
    renderRoute("/invoices/1001");

    expect(await screen.findByText(/has no rate today, so it counts at 0 %/)).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), " endret");
    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("0.00"));
    expect(screen.getByTestId("gross-total")).toHaveTextContent("99.99");
  });
});

describe("an issued document's header", () => {
  // Each "label: value" is one catalog entry, so a language that writes it
  // differently — a space before the colon, another order — can.
  it("words each fact through the catalog, the colon included", async () => {
    server({ 1001: issued({ deliveryDate: undefined, deliveryFrom: "2026-09-01", deliveryTo: "2026-09-10" }) });
    renderRoute("/invoices/1001");

    expect(await screen.findByText("Issue date: Sep 12, 2026")).toBeInTheDocument();
    expect(screen.getByText("Due date: Oct 12, 2026")).toBeInTheDocument();
    expect(screen.getByText("Delivery period: Sep 1, 2026 – Sep 10, 2026")).toBeInTheDocument();
    expect(screen.getByText("Your reference: PO-77")).toBeInTheDocument();
    expect(screen.getByText(/^Left to credit: NOK\s?124\.99$/)).toBeInTheDocument();
  });

  // The same facts in Norwegian: each "label: value" is the nb catalog's own.
  it("words each fact in Norwegian from the nb catalog", async () => {
    setLanguagePreference("nb");
    try {
      server({ 1001: issued({ deliveryDate: undefined, deliveryFrom: "2026-09-01", deliveryTo: "2026-09-10" }) });
      renderRoute("/invoices/1001");

      expect(await screen.findByText("Fakturadato: 12. sep. 2026")).toBeInTheDocument();
      expect(screen.getByText("Forfallsdato: 12. okt. 2026")).toBeInTheDocument();
      expect(screen.getByText("Leveringsperiode: 1. sep. 2026 – 10. sep. 2026")).toBeInTheDocument();
      expect(screen.getByText("Deres referanse: PO-77")).toBeInTheDocument();
      expect(screen.getByText(/^Igjen å kreditere: 124,99\s?kr$/)).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });
});

describe("an issued document's buyer", () => {
  it("shows a foreign buyer's id", async () => {
    const buyer = {
      customerNumber: 10003,
      type: "business",
      name: "Svenska Aktiebolaget AB",
      foreignId: "SE556677889901",
      addressLine1: "Storgatan 1",
      postalCode: "111 22",
      city: "Stockholm",
      country: "SE",
      language: "en",
    };
    server({ 1001: issued({ buyer }) });
    renderRoute("/invoices/1001");
    expect(await screen.findByText("Foreign ID SE556677889901")).toBeInTheDocument();
  });
});

describe("the lines table", () => {
  it("reorders, removes and adds lines, and saves them in the order shown", async () => {
    const fetchMock = server({ 1001: draft() });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Move line 3 up" }));
    await userEvent.click(screen.getByRole("button", { name: "Remove line 1" }));
    await userEvent.click(screen.getByRole("button", { name: "Add a line" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Line 3 description" }), "Reise");
    const price = screen.getByRole("textbox", { name: "Line 3 unit price" });
    await userEvent.clear(price);
    await userEvent.type(price, "100");
    // 33.33 + 33.33 + 100 = 166.66; VAT 25 % of the sum, 41.665, is 41.67.
    expect(screen.getByTestId("vat-total")).toHaveTextContent("41.67");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Saved")).toBeInTheDocument();
    const lines = sent(fetchMock, "PUT").body.lines;
    expect(lines.map((l: { description: string }) => l.description)).toEqual(["Tredjedel 3", "Tredjedel 2", "Reise"]);
    expect(lines[2]).toEqual({
      description: "Reise",
      quantity: 1,
      unit: "",
      unitPrice: 100,
      discountPercent: 0,
      vatCodeId: 1,
    });
  });
});

describe("saving a credit-note draft", () => {
  it("sends a lowered quantity with the line it credits, and no payment terms", async () => {
    const fetchMock = server({ 1002: creditDraft(), 1001: issued() }, {}, { meta: afterTheRateChange });
    renderRoute("/invoices/1002");

    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("8.33"));
    const quantity = screen.getByRole("textbox", { name: "Line 1 quantity" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "0.5");
    // 0.5 × 33.33 = 16.665 → 16.67; its VAT at the original's 25 % 4.1675 →
    // 4.17 (at the code's 26 % today it would be 4.33).
    expect(screen.getByTestId("net-total")).toHaveTextContent("16.67");
    expect(screen.getByTestId("vat-total")).toHaveTextContent("4.17");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Saved")).toBeInTheDocument();
    const body = sent(fetchMock, "PUT").body;
    expect(body.paymentTermsDays).toBeUndefined();
    expect(body.yourReference).toBe("PO-77");
    expect(body.lines).toEqual([
      {
        description: "Tredjedel 1",
        quantity: 0.5,
        unit: "timer",
        unitPrice: 33.33,
        discountPercent: 0,
        vatCodeId: 1,
        creditsLineId: 5001,
      },
    ]);
  });
});

describe("a credit note's fixed fields", () => {
  // The server refuses, field by field, any change to what a credit note
  // copied from its original; the editor sends each back exactly as it came.
  it("are sent back as they came, a delivery period, a place of delivery and a discount included", async () => {
    const base = issued();
    const discounted = { ...base.lines[0], discountPercent: 10, lineAllowance: 3.33, lineNet: 30 };
    const original = issued({ lines: [discounted, ...base.lines.slice(1)] });
    const note = creditDraft({
      deliveryDate: undefined,
      deliveryFrom: "2026-09-01",
      deliveryTo: "2026-09-10",
      deliveryAddress: { line1: "Byggeplass 4", postalCode: "5003", city: "Bergen", country: "NO" },
      orderReference: "O-5",
      lines: [{ ...discounted, id: 6001, creditsLineId: 5001 }],
    });
    const fetchMock = server({ 1002: note, 1001: original });
    renderRoute("/invoices/1002");

    const quantity = await screen.findByRole("textbox", { name: "Line 1 quantity" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "0.5");
    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), " (halv)");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(screen.queryByText("Could not save the draft")).not.toBeInTheDocument();
    const body = sent(fetchMock, "PUT").body;
    expect(body).toMatchObject({
      customerId: 2001,
      deliveryFrom: "2026-09-01",
      deliveryTo: "2026-09-10",
      deliveryAddress: { line1: "Byggeplass 4", postalCode: "5003", city: "Bergen", country: "NO" },
      yourReference: "PO-77",
      ourReference: "Ola Nordmann",
      orderReference: "O-5",
    });
    expect(body.lines).toEqual([
      {
        description: "Tredjedel 1 (halv)",
        quantity: 0.5,
        unit: "timer",
        unitPrice: 33.33,
        discountPercent: 10,
        vatCodeId: 1,
        creditsLineId: 5001,
      },
    ]);
  });
});

describe("what the server decides since the page was read", () => {
  it("names the dates an issue may take now, once the day has turned", async () => {
    server({ 1001: draft({ allowedIssueDates: ["2026-08-31", "2026-09-12"] }) }, {}, { allowedNow: ["2026-09-16"] });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Issue" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Aug 31, 2026" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Issue" }));
    expect(
      await screen.findByText("That issue date is not allowed today. It may be: Sep 16, 2026."),
    ).toBeInTheDocument();
  });

  it("says why a credit is refused, and stays on the invoice", async () => {
    server({ 1001: issued() }, {}, { current: { 1001: issued({ uncreditedAmount: 0, creditedAmount: 124.99 }) } });
    const { router } = renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Credit" }));
    expect(await screen.findByText("Could not create the credit note")).toBeInTheDocument();
    expect(screen.getByText("The invoice is already credited in full.")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/invoices/1001");
  });
});

describe("an issued credit note", () => {
  it("links its original and offers no credit of itself", async () => {
    const note = issued({
      id: 1002,
      kind: "credit_note",
      number: 1001,
      dueDate: undefined,
      credits: { id: 1001, number: 1000, issueDate: "2026-09-12" },
      creditNotes: undefined,
      creditedAmount: undefined,
      uncreditedAmount: undefined,
    });
    server({ 1002: note, 1001: issued() });
    renderRoute("/invoices/1002");

    expect(await screen.findByRole("heading", { name: "Credit note 1001" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "1000" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download PDF" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Credit" })).not.toBeInTheDocument();
  });
});

describe("the preview", () => {
  const stubTab = () => {
    const tab = { location: { href: "" }, close: vi.fn() };
    vi.spyOn(window, "open").mockImplementation(() => tab as unknown as Window);
    vi.stubGlobal(
      "URL",
      Object.assign(URL, { createObjectURL: vi.fn(() => "blob:preview"), revokeObjectURL: vi.fn() }),
    );
    return tab;
  };

  it("opens the draft's watermarked PDF in the tab the click opened", async () => {
    const tab = stubTab();
    server(
      { 1001: draft() },
      {},
      {
        answers: {
          "GET /api/v1/invoices/1001/preview.pdf": () =>
            new Response("%PDF-1.7", { status: 200, headers: { "Content-Type": "application/pdf" } }),
        },
      },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Preview" }));
    await waitFor(() => expect(tab.location.href).toBe("blob:preview"));
    expect(tab.close).not.toHaveBeenCalled();
  });

  it("closes the tab and says a refusal in words", async () => {
    const tab = stubTab();
    server(
      { 1001: draft() },
      {},
      { answers: { "GET /api/v1/invoices/1001/preview.pdf": refusal(409, "invoice_issued") } },
    );
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Preview" }));
    expect(await screen.findByText("The document is already issued.")).toBeInTheDocument();
    expect(tab.close).toHaveBeenCalled();
    expect(tab.location.href).toBe("");
  });
});

describe("the totals the page shows", () => {
  it("are the server's until an edit, then the editor's estimate", async () => {
    // The last of three credit notes reversing lines of 33.33 at 25 %: the
    // server takes what the original charged less what was reversed, 8.34,
    // where round(33.33 × 25 %) alone is 8.33.
    const last = creditDraft({
      vatSummaries: [
        { vatCategory: "S", ratePercent: 25, safTCode: "3", taxableAmount: 33.33, vatAmount: 8.34, vatAmountNok: 8.34 },
      ],
      vatTotal: 8.34,
      vatTotalNok: 8.34,
      grossTotal: 41.67,
    });
    server({ 1002: last, 1001: issued() }, {}, { meta: afterTheRateChange });
    renderRoute("/invoices/1002");

    expect(await screen.findByTestId("vat-total")).toHaveTextContent("8.34");
    expect(screen.getByTestId("gross-total")).toHaveTextContent("41.67");
    expect(screen.queryByText(/An estimate while you edit/)).not.toBeInTheDocument();

    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), " retur");
    expect(screen.getByText(/An estimate while you edit/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("8.33"));
  });
});

describe("a draft someone else saved meanwhile", () => {
  it("says so on save, keeps the edits on screen, and reloads the latest on request", async () => {
    const documents: Record<number, InvoiceDocument> = { 1001: draft() };
    const fetchMock = server(documents);
    renderRoute("/invoices/1001");

    const description = await screen.findByRole("textbox", { name: "Line 2 description" });
    await userEvent.clear(description);
    await userEvent.type(description, "Mine endringer");
    // Another user saves revision 4 in the meantime.
    documents[1001] = draft({ revision: 4, yourReference: "PO-88" });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("The draft changed")).toBeInTheDocument();
    expect(screen.getByText(/Someone else saved this draft/)).toBeInTheDocument();
    expect(screen.queryByText(/Invoice revision 4 is current/)).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Line 2 description" })).toHaveValue("Mine endringer");
    expect(sent(fetchMock, "PUT").body.revision).toBe(3);

    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Your reference" })).toHaveValue("PO-88"));
    expect(screen.getByRole("textbox", { name: "Line 2 description" })).toHaveValue("Tredjedel 2");
    expect(screen.queryByText("The draft changed")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("never throws unsaved edits away on a background refetch, and takes the latest when there are none", async () => {
    const documents: Record<number, InvoiceDocument> = { 1001: draft() };
    server(documents);
    const { queryClient } = renderRoute("/invoices/1001");

    const description = await screen.findByRole("textbox", { name: "Line 2 description" });
    await userEvent.clear(description);
    await userEvent.type(description, "Mine endringer");
    documents[1001] = draft({ revision: 4, yourReference: "PO-88" });
    // What coming back from the Preview tab does: a refetch on focus.
    await queryClient.refetchQueries({ queryKey: ["invoices", "document", 1001] });
    // It says so rather than swapping the draft under the person's hands.
    expect(await screen.findByText("The draft changed")).toBeInTheDocument();

    expect(screen.getByRole("textbox", { name: "Line 2 description" })).toHaveValue("Mine endringer");
    expect(screen.getByRole("textbox", { name: "Your reference" })).toHaveValue("PO-77");
  });

  it("shows another's save at once while nothing is edited", async () => {
    const documents: Record<number, InvoiceDocument> = { 1001: draft() };
    server(documents);
    const { queryClient } = renderRoute("/invoices/1001");

    await screen.findByRole("textbox", { name: "Line 2 description" });
    documents[1001] = draft({ revision: 4, yourReference: "PO-88" });
    await queryClient.refetchQueries({ queryKey: ["invoices", "document", 1001] });

    await waitFor(() => expect(screen.getByRole("textbox", { name: "Your reference" })).toHaveValue("PO-88"));
  });
});

describe("deleting a draft", () => {
  // Invalidated while the page still showed it, the deleted draft was fetched
  // again and the page flashed "Could not load the document" on the way out.
  it("goes back to the list and never asks for the deleted draft again", async () => {
    const documents: Record<number, InvoiceDocument> = { 1001: draft() };
    const fetchMock = server(
      documents,
      {},
      {
        answers: {
          "DELETE /api/v1/invoices/1001": () => {
            delete documents[1001];
            return new Response(null, { status: 204 });
          },
          "GET /api/v1/invoices?page=1": () => jsonResponse(200, listPage()),
        },
      },
    );
    const { router } = renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices"));
    expect(await screen.findByText("Kari Nordmann")).toBeInTheDocument();

    const document = ([url]: [RequestInfo | URL, RequestInit | undefined]) => path(url) === "/api/v1/invoices/1001";
    const deletedAt = fetchMock.actualCalls.findIndex((call) => document(call) && call[1]?.method === "DELETE");
    expect(deletedAt).toBeGreaterThanOrEqual(0);
    const askedAgain = fetchMock.actualCalls
      .slice(deletedAt + 1)
      .filter((call) => document(call) && (call[1]?.method ?? "GET") === "GET");
    expect(askedAgain).toEqual([]);
    expect(screen.queryByText("Could not load the document")).not.toBeInTheDocument();
  });
});

describe("unsaved edits", () => {
  // The preview renders the draft as saved, so it is held back as Issue is.
  it("hold the preview back, as they hold Issue, and say why", async () => {
    server({ 1001: draft() });
    renderRoute("/invoices/1001");

    expect(await screen.findByRole("button", { name: "Preview" })).toBeEnabled();
    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), " endret");
    expect(screen.getByRole("button", { name: "Preview" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Issue" })).toBeDisabled();
    expect(screen.getByText("Save the changes before previewing or issuing.")).toBeInTheDocument();
  });

  // Typed past what the columns hold, a number reached 1e21 before the input
  // clamped it, and the live totals crashed the editor in render.
  it("never crash the editor on a number past what a line holds, which the inputs clamp", async () => {
    server({ 1001: draft() });
    renderRoute("/invoices/1001");

    const quantity = await screen.findByRole("textbox", { name: "Line 1 quantity" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "1000000000000000000000");
    expect(screen.getByTestId("net-total")).toBeInTheDocument();
    await userEvent.tab();
    expect(quantity).toHaveValue("999999999.999");

    const price = screen.getByRole("textbox", { name: "Line 1 unit price" });
    await userEvent.clear(price);
    await userEvent.type(price, "1000000000000000000000");
    expect(screen.getByTestId("net-total")).toBeInTheDocument();
    await userEvent.tab();
    expect(price).toHaveValue("9999999999.9999");
  });
});

describe("an issued document's lines", () => {
  it("write a unit price with its four decimals", async () => {
    const base = issued();
    server({ 1001: issued({ lines: [{ ...base.lines[0], unitPrice: 33.3333 }, base.lines[1]] }) });
    renderRoute("/invoices/1001");

    const first = (await screen.findByText("Tredjedel 1")).closest("tr") as HTMLElement;
    expect(within(first).getByText(/33\.3333/)).toBeInTheDocument();
    const second = screen.getByText("Tredjedel 2").closest("tr") as HTMLElement;
    expect(within(second).getAllByText(/33\.33$/).length).toBeGreaterThan(0);
  });
});

describe("changing the buyer", () => {
  /**
   * Runs a test on a fake clock, with userEvent's direct calls told so
   * (setup() would claim the clipboard the test setup stubs), and a `settle`
   * that advances past every debounced search until no new one is asked for
   * and every answer is in — so what a test then asserts is where the picker
   * rests, not a moment in a cycle.
   */
  const onFakeClock = async (
    body: (tools: {
      user: {
        clear: (el: Element) => Promise<void>;
        type: (el: Element, text: string) => Promise<void>;
        click: (el: Element) => Promise<void>;
      };
      settle: () => Promise<void>;
      searches: () => string[];
      fetchMock: ReturnType<typeof server>;
    }) => Promise<void>,
  ) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const timers = { advanceTimers: vi.advanceTimersByTime };
      const user = {
        clear: (el: Element) => userEvent.clear(el),
        type: (el: Element, text: string) => userEvent.type(el, text, timers),
        click: (el: Element) => userEvent.click(el, timers),
      };
      const fetchMock = server({ 1001: draft() });
      const { queryClient } = renderRoute("/invoices/1001");
      const searches = () =>
        fetchMock.actualCalls.map(([url]) => path(url)).filter((url) => url.startsWith("/api/v1/customers?"));
      const settle = async () => {
        for (let round = 0; round < 8; round++) {
          const before = searches().length;
          await act(async () => {
            await vi.advanceTimersByTimeAsync(1000);
          });
          await waitFor(() => expect(queryClient.isFetching()).toBe(0));
          if (searches().length === before) return;
        }
        throw new Error("the buyer picker never stopped searching");
      };
      await body({ user, settle, searches, fetchMock });
    } finally {
      vi.useRealTimers();
    }
  };

  // Mantine reports the selected option's label as a search whenever it sets
  // it. Read as a search, "Acme AS (10001)" found nothing, the picker fell
  // back to the draft's "Acme AS", which Mantine reported in turn: a request
  // every debounce, for ever, over whatever the person typed.
  it("rests on the draft's buyer, and never searches for its label", async () => {
    await onFakeClock(async ({ settle, searches }) => {
      await screen.findByRole("combobox", { name: "Customer" });
      await settle();
      expect(screen.getByRole("combobox", { name: "Customer" })).toHaveValue("Acme AS (10001)");
      await settle();
      expect(screen.getByRole("combobox", { name: "Customer" })).toHaveValue("Acme AS (10001)");
      expect(searches().filter((url) => url.includes("10001"))).toEqual([]);
    });
  });

  // Mantine reports the picked option's label as a search; the customers API
  // finds nothing for "Bygg AS (10003)", and the picker then labelled the new
  // id with the draft's old buyer's name.
  it("shows the customer picked, keeps it through the searches after, and saves its id", async () => {
    await onFakeClock(async ({ user, settle, searches, fetchMock }) => {
      const buyer = await screen.findByRole("combobox", { name: "Customer" });
      await settle();
      await user.clear(buyer);
      await user.type(buyer, "Bygg");
      await settle();
      await user.click(await screen.findByRole("option", { name: "Bygg AS (10003)" }));
      await settle();
      expect(screen.getByRole("combobox", { name: "Customer" })).toHaveValue("Bygg AS (10003)");
      expect(searches().some((url) => url.includes("10003"))).toBe(false);

      await user.click(screen.getByRole("button", { name: "Save" }));
      expect(await screen.findByText("Saved")).toBeInTheDocument();
      expect(sent(fetchMock, "PUT").body.customerId).toBe(2003);
    });
  });

  it("never offers to clear the buyer of a draft", async () => {
    server({ 1001: draft() });
    renderRoute("/invoices/1001");
    await screen.findByRole("combobox", { name: "Customer" });
    // Mantine's clear button is aria-hidden, so it is found by its class.
    expect(document.querySelector(".mantine-InputClearButton-root")).toBeNull();
  });
});

describe("moving between documents", () => {
  // The route keeps the page mounted from one document to the next. The
  // editor's edits go when the person leaves the draft; the snapshot they were
  // made on went with them only by accident, and came back as a dirty editor
  // with none of the edits.
  it("leaves no phantom edits on a draft the person comes back to", async () => {
    const original = issued({ creditNotes: [{ id: 1002, status: "draft", grossTotal: 41.66 }] });
    server({ 1002: creditDraft(), 1001: original });
    const { router } = renderRoute("/invoices/1002");

    await userEvent.type(await screen.findByRole("textbox", { name: "Line 1 description" }), " retur");
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    await userEvent.click(screen.getByRole("link", { name: "1000" }));
    expect(await screen.findByRole("heading", { name: "Invoice 1000" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("link", { name: "Credit note — Draft" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1002"));

    const description = await screen.findByRole("textbox", { name: "Line 1 description" });
    expect(description).toHaveValue("Tredjedel 1");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Preview" })).toBeEnabled();
    expect(screen.queryByText(/An estimate while you edit/)).not.toBeInTheDocument();
  });
});

describe("a refusal on a field not on screen", () => {
  // The day input is not rendered while a period is chosen: its refusal is
  // said in a notification, never put on an input nobody sees.
  it("is said in a notification rather than swallowed", async () => {
    server(
      { 1001: draft({ deliveryDate: undefined, deliveryFrom: "2026-09-01", deliveryTo: "2026-09-10" }) },
      {},
      {
        answers: {
          "PUT /api/v1/invoices/1001": jsonResponse(400, {
            title: "Invalid invoice",
            status: 400,
            errors: { deliveryDate: ["A delivery is a day or a period, not both"] },
          }),
        },
      },
    );
    renderRoute("/invoices/1001");

    await userEvent.type(await screen.findByRole("textbox", { name: "Line 1 description" }), " endret");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Could not save the draft")).toBeInTheDocument();
    expect(
      screen.getByText("A delivery is a day or a period, not both; a credit note keeps its original's."),
    ).toBeInTheDocument();
  });
});

describe("another draft at the same revision", () => {
  // The editor is keyed by the document and its revision: two drafts at
  // revision 3 are still two editors, never one holding the first's lines.
  it("gets an editor of its own", async () => {
    const other = draft({
      id: 1003,
      lines: [{ ...draft().lines[0], id: 5101, description: "Befaring" }],
    });
    server({ 1001: draft(), 1003: other });
    const { router, queryClient } = renderRoute("/invoices/1001");

    expect(await screen.findByRole("textbox", { name: "Line 1 description" })).toHaveValue("Tredjedel 1");
    // Already read, as a draft opened earlier is: the page draws it at once,
    // with no skeleton between the two editors.
    queryClient.setQueryData(["invoices", "document", 1003], other);
    await act(() => router.navigate({ to: "/invoices/$invoiceId", params: { invoiceId: "1003" } }));
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Line 1 description" })).toHaveValue("Befaring"));
    expect(screen.queryByRole("textbox", { name: "Line 2 description" })).not.toBeInTheDocument();
  });
});

describe("the delivery", () => {
  // The hint and the save follow the mode chosen: a date left behind in the
  // other mode is not a delivery, and half a period is never saved as none.
  it("follows the chosen mode, and a half-entered period waits for its other end", async () => {
    const fetchMock = server({ 1001: draft() });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("radio", { name: "Delivery period" }));
    expect(screen.getByText(/A delivery date or period is needed/)).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Delivered from" }), "Sep 1, 2026");
    expect(screen.getByText(/A delivery date or period is needed/)).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Delivered to" })).toHaveAccessibleDescription(
      "Give both the first and the last day, or choose a single day.",
    );
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();

    await userEvent.type(screen.getByRole("textbox", { name: "Delivered to" }), "Sep 10, 2026");
    expect(screen.queryByText(/A delivery date or period is needed/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").body).toBeDefined());
    const body = sent(fetchMock, "PUT").body;
    expect(body.deliveryFrom).toBe("2026-09-01");
    expect(body.deliveryTo).toBe("2026-09-10");
    expect(body.deliveryDate).toBeUndefined();
  });
});

describe("a refused save", () => {
  // Each refusal lands on its input in the catalog's words, never the
  // server's English one at a time in a notification.
  it("puts each field's words on its input, a line's on that line's", async () => {
    server(
      { 1001: draft() },
      {},
      {
        answers: {
          "PUT /api/v1/invoices/1001": jsonResponse(400, {
            title: "Invalid invoice",
            status: 400,
            errors: {
              "lines[0].description": ["A line needs a description"],
              "lines[1].quantity": ["A quantity has at most 3 decimals"],
              yourReference: ["A reference is at most 100 characters"],
            },
          }),
        },
      },
    );
    renderRoute("/invoices/1001");

    await userEvent.clear(await screen.findByRole("textbox", { name: "Line 1 description" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Line 1 description" })).toHaveAccessibleDescription(
        "A line needs a description of at most 500 characters.",
      ),
    );
    expect(screen.getByRole("textbox", { name: "Line 2 quantity" })).toHaveAccessibleDescription(
      "More than 0 and at most 999999999.999, with up to three decimals; a credit note may only lower it.",
    );
    expect(screen.getByRole("textbox", { name: "Your reference" })).toHaveAccessibleDescription(
      "At most 100 characters; a credit note keeps its original's.",
    );
    expect(screen.queryByText(/A quantity has at most 3 decimals/)).not.toBeInTheDocument();
    expect(screen.queryByText("Could not save the draft")).not.toBeInTheDocument();

    // Changing a field takes its refusal away; the others stay.
    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), "Rådgivning");
    expect(screen.getByRole("textbox", { name: "Line 1 description" })).not.toHaveAccessibleDescription(
      "A line needs a description of at most 500 characters.",
    );
    expect(screen.getByRole("textbox", { name: "Line 2 quantity" })).toHaveAccessibleDescription(
      "More than 0 and at most 999999999.999, with up to three decimals; a credit note may only lower it.",
    );
  });
});
