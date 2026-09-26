import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { InvoiceDocument, InvoiceInput } from "../api/invoices";
import { jsonResponse, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { creditDraft, draft, issued, meta, vatCodes } from "../test/fixtures";
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
    if (url.startsWith("/api/v1/customers?")) return jsonResponse(200, { data: [] });

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
      return jsonResponse(200, issued({ id, issueDate }));
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
      if (doc.kind === "credit_note") {
        const originals = (doc.credits && documents[doc.credits.id]?.lines) || [];
        body.lines.forEach((l, i) => {
          const o = originals.find((ol) => ol.id === l.creditsLineId);
          if (!o)
            errors[`lines[${i}].creditsLineId`] = ["A credit note only credits the original's lines; it adds none"];
          else if (l.quantity > o.quantity) errors[`lines[${i}].quantity`] = ["A credit note may lower a quantity"];
          else if (l.unitPrice > o.unitPrice) errors[`lines[${i}].unitPrice`] = ["A credit note may lower a price"];
        });
        if (body.paymentTermsDays !== undefined) errors.paymentTermsDays = ["A credit note has no payment terms"];
      }
      if (Object.keys(errors).length > 0) return jsonResponse(400, { title: "Invalid", status: 400, errors });
      return jsonResponse(200, {
        ...doc,
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

describe("a credit-note draft", () => {
  it("names its original, adds no lines, keeps the VAT codes, and shows the cap warnings", async () => {
    server({ 1002: creditDraft({ warnings: ["credit_exceeds_line"] }), 1001: issued() });
    renderRoute("/invoices/1002");

    expect(await screen.findByText("Credit note for invoice")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "1000" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add a line" })).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Line 1 VAT code" })).toBeDisabled();
    expect(screen.getByText(/credits more than the original line had left/)).toBeInTheDocument();
    // At the original line's own 25 %.
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
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
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
    expect(click).toHaveBeenCalled();
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
    server({ 1001: draft() }, {}, { meta: { capabilities: { canCreate: false, canIssue: false, canManage: false } } });
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
    // 25 % (and refuses it at issue), so the editor does too.
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Line 1 VAT code" })).toHaveValue(
        "3G — Gammel sats (no longer offered)",
      ),
    );
    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("25.00"));
  });

  it("totals a line whose code has no rate today at 0 %, as the server does, with its warning", async () => {
    const lines = draft().lines.map((l) => ({ ...l, vatCodeId: 1 }));
    server({ 1001: draft({ lines, warnings: ["vat_code_not_valid"] }) }, {}, { meta: { today: "2025-06-01" } });
    renderRoute("/invoices/1001");

    expect(await screen.findByText(/has no rate today, so it counts at 0 %/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("0.00"));
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
    const fetchMock = server({ 1002: creditDraft(), 1001: issued() });
    renderRoute("/invoices/1002");

    await waitFor(() => expect(screen.getByTestId("vat-total")).toHaveTextContent("8.33"));
    const quantity = screen.getByRole("textbox", { name: "Line 1 quantity" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "0.5");
    // 0.5 × 33.33 = 16.665 → 16.67; its VAT 4.1675 → 4.17.
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
