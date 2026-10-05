import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import type { InvoiceDocument, InvoiceInput } from "../api/invoices";
import { customerSearch, jsonResponse, path, refusal } from "../test/api";
import { requestTo } from "../test/document-server";
import { stubFetch } from "../test/fetch";
import {
  creditDraft,
  deductible,
  draft,
  issued,
  meta,
  settlementDraft,
  vatCodes,
  workCreditDraft,
  workDraft,
} from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

/**
 * The fake for one document, 1001 (and its original, 1000's id 1001 for a
 * credit note's), the customers' search, the deductible list, and a PUT that
 * answers the body back as the server would keep it: the lines as sent,
 * `saved` merged over them.
 */
const server = (
  doc: InvoiceDocument,
  { saved = {}, answers = {} }: { saved?: Partial<InvoiceDocument>; answers?: Record<string, () => Response> } = {},
) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`];
    if (answer) return answer();
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
    if (url.startsWith("/api/v1/customers?")) return customerSearch(url);
    if (url === `/api/v1/invoices/${doc.id}/deductible`) return jsonResponse(200, deductible());
    if (url === "/api/v1/invoices/1001" && doc.id !== 1001 && method === "GET") return jsonResponse(200, issued());
    if (url === `/api/v1/invoices/${doc.id}` && method === "GET") return jsonResponse(200, doc);
    if (url === `/api/v1/invoices/${doc.id}` && method === "PUT") {
      const body = JSON.parse(String(init?.body)) as InvoiceInput;
      return jsonResponse(200, {
        ...doc,
        customerId: body.customerId,
        revision: doc.revision + 1,
        timesheet: body.timesheet ?? doc.timesheet,
        lines: body.lines.map((l, i) => ({
          ...doc.lines[0],
          ...l,
          id: 7000 + i,
          position: i + 1,
          unit: l.unit ?? "",
          discountPercent: l.discountPercent ?? 0,
          sources: doc.lines[i]?.sources,
          warnings: [],
        })),
        warnings: [],
        releasedSources: undefined,
        ...saved,
      });
    }
    return new Response(null, { status: 404 });
  });

const save = () => userEvent.click(screen.getByRole("button", { name: "Save" }));

describe("an invoice draft that bills work", () => {
  it("InvoiceEditor_SendsEachLinesSourcesBackOnEverySave", async () => {
    const fetchMock = server(workDraft());
    renderRoute("/invoices/1001");

    const description = await screen.findByRole("textbox", { name: "Line 3 description" });
    await userEvent.type(description, " (revidert)");
    await userEvent.click(screen.getByRole("button", { name: "Add a line" }));
    await save();

    await waitFor(() => expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/1001")).toBeDefined());
    const body = requestTo(fetchMock, "PUT", "/api/v1/invoices/1001") as InvoiceInput;
    // Each line's work by identity, as read — never a figure of the server's —
    // and [] on a line that bills none, a new line included.
    expect(body.lines.map((l) => l.sources)).toEqual([
      [
        { kind: "time.entry", id: 501 },
        { kind: "time.entry", id: 502 },
      ],
      [{ kind: "expenses.entry", id: 601 }],
      [],
      [],
    ]);
    expect(body.refreshSources).toBeUndefined();

    // And again on the next save, from the saved draft.
    await screen.findByText("Saved");
    await userEvent.type(screen.getByRole("textbox", { name: "Line 2 description" }), "!");
    await save();
    await waitFor(() => expect(fetchMock.actualCalls.filter(([, init]) => init?.method === "PUT").length).toBe(2));
    const second = JSON.parse(
      String(fetchMock.actualCalls.filter(([, init]) => init?.method === "PUT")[1][1]?.body),
    ) as InvoiceInput;
    expect(second.lines[0].sources).toEqual([
      { kind: "time.entry", id: 501 },
      { kind: "time.entry", id: 502 },
    ]);
  });

  it("InvoiceEditor_SendsEmptySourcesWhenTheCustomerChanges", async () => {
    const fetchMock = server(workDraft());
    renderRoute("/invoices/1001");

    const buyer = await screen.findByRole("combobox", { name: "Customer" });
    await userEvent.clear(buyer);
    await userEvent.type(buyer, "Bygg");
    await userEvent.click(await screen.findByRole("option", { name: "Bygg AS (10003)" }));
    expect(await screen.findByTestId("customer-change-releases")).toHaveTextContent(
      "Changing the customer releases the work this draft holds",
    );
    await save();

    await waitFor(() => expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/1001")).toBeDefined());
    const body = requestTo(fetchMock, "PUT", "/api/v1/invoices/1001") as InvoiceInput;
    expect(body.customerId).toBe(2003);
    expect(body.lines.map((l) => l.sources)).toEqual([[], [], []]);
  });

  it("shows each line's work and warnings, the project and what the save released", async () => {
    server(workDraft());
    renderRoute("/invoices/1001");

    const first = await screen.findByTestId("line-work-1");
    expect(first).toHaveTextContent("Hour entry 501 · Sep 1, 2026 · 4 · NOK 4,800.00");
    expect(first).toHaveTextContent("Hour entry 502 · Sep 2, 2026 · 3.5 · NOK 4,200.00");
    expect(first).toHaveTextContent("A line's amount differs from the work it bills.");
    expect(screen.getByTestId("line-work-2")).toHaveTextContent("Expense 601 · Sep 3, 2026 · 90 · NOK 450.00");
    expect(screen.queryByTestId("line-work-3")).not.toBeInTheDocument();
    expect(screen.getByTestId("document-project")).toHaveTextContent("Project: P-41");
    expect(screen.getByTestId("released-sources")).toHaveTextContent("Released: Milestone 701.");
  });

  it("says a source changed or no longer invoiceable on its line", async () => {
    const doc = workDraft();
    doc.lines[1] = { ...doc.lines[1], warnings: ["source_not_invoiceable"] };
    server(doc);
    renderRoute("/invoices/1001");
    expect(await screen.findByTestId("line-work-2")).toHaveTextContent("Work on this draft can no longer be invoiced");
  });

  it("refreshes the work with the draft as it stands, once nothing is left unsaved", async () => {
    const fetchMock = server(workDraft(), {
      saved: { warnings: ["sources_released"], releasedSources: [{ kind: "time.entry", id: 502 }] },
    });
    renderRoute("/invoices/1001");

    const refresh = await screen.findByRole("button", { name: "Refresh work" });
    await userEvent.type(screen.getByRole("textbox", { name: "Line 3 description" }), "x");
    expect(refresh).toBeDisabled();
    await save();
    await screen.findByText("Saved");

    await userEvent.click(screen.getByRole("button", { name: "Refresh work" }));
    expect(await screen.findByText("The work is refreshed")).toBeInTheDocument();
    const puts = fetchMock.actualCalls.filter(([, init]) => init?.method === "PUT");
    const body = JSON.parse(String(puts[puts.length - 1][1]?.body)) as InvoiceInput;
    expect(body.refreshSources).toBe(true);
    expect(body.lines[0].sources).toEqual([
      { kind: "time.entry", id: 501 },
      { kind: "time.entry", id: 502 },
    ]);
    expect(await screen.findByTestId("released-sources")).toHaveTextContent("Released: Hour entry 502.");
  });

  it("says a refused refresh in words", async () => {
    server(workDraft(), { answers: { "PUT /api/v1/invoices/1001": () => refusal(409, "invoice_changed") } });
    renderRoute("/invoices/1001");
    await userEvent.click(await screen.findByRole("button", { name: "Refresh work" }));
    expect(await screen.findByText("Could not refresh the work")).toBeInTheDocument();
    expect(screen.getByText("The invoice changed; try again.")).toBeInTheDocument();
  });

  it("offers no Refresh on a draft that bills no work, and sends no sources", async () => {
    const fetchMock = server(draft());
    renderRoute("/invoices/1001");
    await userEvent.type(await screen.findByRole("textbox", { name: "Line 1 description" }), "!");
    expect(screen.queryByRole("button", { name: "Refresh work" })).not.toBeInTheDocument();
    await save();
    await waitFor(() => expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/1001")).toBeDefined());
    const body = requestTo(fetchMock, "PUT", "/api/v1/invoices/1001") as InvoiceInput;
    expect(body.lines.every((l) => !("sources" in l))).toBe(true);
    expect(body.timesheet).toBe(false);
  });
});

describe("the timesheet", () => {
  const rows = [
    { position: 1, date: "2026-09-01", personLabel: "KN", hours: 4, workType: "Utvikling", description: "Apollo" },
    { position: 2, date: "2026-09-02", personLabel: "OH", hours: 3.5, description: "Apollo" },
  ];

  it("shows the rows as written, each person by the settings' label, and turns it off on save", async () => {
    const fetchMock = server(workDraft({ timesheet: true, timesheetRows: rows }));
    renderRoute("/invoices/1001");

    const sheet = await screen.findByRole("table", { name: "Timesheet" });
    expect(within(sheet).getAllByRole("row")).toHaveLength(3);
    expect(within(sheet).getByText("KN")).toBeInTheDocument();
    expect(within(sheet).getByText("Utvikling")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("checkbox", { name: /Attach a timesheet/ }));
    expect(screen.queryByRole("table", { name: "Timesheet" })).not.toBeInTheDocument();
    await save();
    await waitFor(() => expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/1001")?.timesheet).toBe(false));
  });

  it("says the rows are written when a draft turning it on is saved", async () => {
    const fetchMock = server(workDraft());
    renderRoute("/invoices/1001");
    await userEvent.click(await screen.findByRole("checkbox", { name: /Attach a timesheet/ }));
    expect(screen.getByText("The timesheet's rows are written when the draft is saved.")).toBeInTheDocument();
    await save();
    await waitFor(() => expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/1001")?.timesheet).toBe(true));
  });
});

describe("deducting earlier invoices", () => {
  it("lists what each earlier invoice has left per VAT code and adds the chosen deduction", async () => {
    const fetchMock = server(draft());
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Deduct earlier invoices" }));
    const dialog = await screen.findByRole("dialog", { name: "Deduct earlier invoices" });
    const rows = await within(dialog).findAllByRole("row");
    expect(rows[1]).toHaveTextContent("985");
    expect(rows[1]).toHaveTextContent("3 — Utgående mva 25 %");
    expect(rows[1]).toHaveTextContent("NOK 100,000.00");
    expect(rows[2]).toHaveTextContent("31 — Utgående mva 15 %");

    await userEvent.click(
      within(dialog).getByRole("checkbox", { name: "Deduct invoice 985 at 3 — Utgående mva 25 %" }),
    );
    const amount = within(dialog).getByRole("textbox", {
      name: "Amount to deduct from invoice 985 at 3 — Utgående mva 25 %",
    });
    await userEvent.clear(amount);
    await userEvent.type(amount, "100001");
    expect(within(dialog).getByRole("button", { name: "Add the deduction lines" })).toBeDisabled();
    await userEvent.clear(amount);
    await userEvent.type(amount, "60000");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add the deduction lines" }));

    expect(await screen.findByRole("textbox", { name: "Line 4 description" })).toHaveValue(
      "Previously invoiced on account, invoice 985",
    );
    expect(screen.getByTestId("line-work-4")).toHaveTextContent("Deducts an earlier invoice");
    await save();
    await waitFor(() => expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/1001")).toBeDefined());
    const body = requestTo(fetchMock, "PUT", "/api/v1/invoices/1001") as InvoiceInput;
    expect(body.lines[3]).toEqual({
      description: "Previously invoiced on account, invoice 985",
      quantity: -1,
      unit: "",
      unitPrice: 60000,
      discountPercent: 0,
      vatCodeId: 1,
      deductsInvoiceId: 990,
    });
  });

  it("offers no second deduction of an invoice at a code the draft already deducts, and keeps the one it has", async () => {
    const fetchMock = server(settlementDraft());
    renderRoute("/invoices/1001");

    // The deduction's quantity is -1 and stays so.
    expect(await screen.findByRole("textbox", { name: "Line 2 quantity" })).toHaveAttribute("readonly");
    await userEvent.click(screen.getByRole("button", { name: "Deduct earlier invoices" }));
    const dialog = await screen.findByRole("dialog", { name: "Deduct earlier invoices" });
    expect(
      await within(dialog).findByRole("checkbox", { name: "Deduct invoice 985 at 3 — Utgående mva 25 %" }),
    ).toBeDisabled();
    expect(within(dialog).getByText("Already on this draft")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await userEvent.type(screen.getByRole("textbox", { name: "Line 1 description" }), "!");
    await save();
    await waitFor(() => expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/1001")).toBeDefined());
    const body = requestTo(fetchMock, "PUT", "/api/v1/invoices/1001") as InvoiceInput;
    expect(body.lines[1]).toMatchObject({ quantity: -1, unitPrice: 100000, deductsInvoiceId: 990 });
  });

  // The server refuses a deduction's discount and a code other than the
  // a-konto's: the inputs are locked as its quantity is.
  it("locks a deduction line's quantity, discount and VAT code, and leaves the work line's open", async () => {
    server(settlementDraft());
    renderRoute("/invoices/1001");
    expect(await screen.findByRole("textbox", { name: "Line 2 quantity" })).toHaveAttribute("readonly");
    expect(screen.getByRole("textbox", { name: "Line 2 discount" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Line 2 VAT code" })).toBeDisabled();
    expect(screen.getByRole("textbox", { name: "Line 1 discount" })).toBeEnabled();
    expect(screen.getByRole("combobox", { name: "Line 1 VAT code" })).toBeEnabled();
  });

  // The wizard writes a draft's lines in the buyer's language; a deduction
  // proposed for a Norwegian customer reads Norwegian whatever the reader's.
  it("proposes the deduction text in the customer's language", async () => {
    server(draft(), {
      answers: { "GET /api/v1/customers/2001/billing-profile": () => jsonResponse(200, { language: "nb" }) },
    });
    renderRoute("/invoices/1001");
    await userEvent.click(await screen.findByRole("button", { name: "Deduct earlier invoices" }));
    const dialog = await screen.findByRole("dialog", { name: "Deduct earlier invoices" });
    await userEvent.click(
      await within(dialog).findByRole("checkbox", { name: "Deduct invoice 985 at 3 — Utgående mva 25 %" }),
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Add the deduction lines" }));
    expect(await screen.findByRole("textbox", { name: "Line 4 description" })).toHaveValue(
      "Tidligere fakturert a konto, faktura 985",
    );
  });

  /** Opens the step, deducts invoice 985 at code 3 in full, and answers the line it proposed. */
  const deductAtCode3 = async () => {
    await userEvent.click(await screen.findByRole("button", { name: "Deduct earlier invoices" }));
    const dialog = await screen.findByRole("dialog", { name: "Deduct earlier invoices" });
    await userEvent.click(
      await within(dialog).findByRole("checkbox", { name: "Deduct invoice 985 at 3 — Utgående mva 25 %" }),
    );
    const add = within(dialog).getByRole("button", { name: "Add the deduction lines" });
    await waitFor(() => expect(add).toBeEnabled());
    await userEvent.click(add);
    return screen.findByRole("textbox", { name: "Line 4 description" });
  };
  const profileReads = (fetchMock: ReturnType<typeof server>) =>
    fetchMock.actualCalls.filter(([url]) => path(url) === "/api/v1/customers/2001/billing-profile").length;

  it("reads no billing profile without customers:view, and proposes the text in the reader's language", async () => {
    const fetchMock = server(draft(), {
      answers: { "GET /api/v1/customers/2001/billing-profile": () => jsonResponse(200, { language: "nb" }) },
    });
    renderRoute("/invoices/1001", { canViewCustomers: false });
    expect(await deductAtCode3()).toHaveValue("Previously invoiced on account, invoice 985");
    expect(profileReads(fetchMock)).toBe(0);
  });

  it("takes the buyer snapshot's language over the billing profile's, and reads no profile", async () => {
    const fetchMock = server(
      draft({
        buyer: { customerNumber: 10001, type: "business", name: "Acme AS", language: "en" },
      }),
      { answers: { "GET /api/v1/customers/2001/billing-profile": () => jsonResponse(200, { language: "nb" }) } },
    );
    setLanguagePreference("nb");
    try {
      renderRoute("/invoices/1001");
      await userEvent.click(await screen.findByRole("button", { name: "Trekk fra tidligere fakturaer" }));
      const dialog = await screen.findByRole("dialog", { name: "Trekk fra tidligere fakturaer" });
      await userEvent.click(
        await within(dialog).findByRole("checkbox", { name: "Trekk fra faktura 985 på 3 — Utgående mva 25 %" }),
      );
      await userEvent.click(within(dialog).getByRole("button", { name: "Legg til fradragslinjene" }));
      expect(await screen.findByRole("textbox", { name: "Beskrivelse på linje 4" })).toHaveValue(
        "Previously invoiced on account, invoice 985",
      );
      expect(profileReads(fetchMock)).toBe(0);
    } finally {
      setLanguagePreference("auto");
    }
  });

  it("says a refused deduction save by the line it names", async () => {
    server(settlementDraft(), {
      answers: { "PUT /api/v1/invoices/1001": () => refusal(409, "deduction_duplicated", { linePosition: 2 }) },
    });
    renderRoute("/invoices/1001");
    await userEvent.type(await screen.findByRole("textbox", { name: "Line 1 description" }), "!");
    await save();
    expect(await screen.findByText(/Line 2 deducts the same invoice at the same VAT code/)).toBeInTheDocument();
  });

  it("says when nothing is left to deduct", async () => {
    server(draft(), { answers: { "GET /api/v1/invoices/1001/deductible": () => jsonResponse(200, []) } });
    renderRoute("/invoices/1001");
    await userEvent.click(await screen.findByRole("button", { name: "Deduct earlier invoices" }));
    expect(
      await screen.findByText("No earlier invoice of this customer has anything left to deduct."),
    ).toBeInTheDocument();
  });
});

describe("a credit-note draft of work", () => {
  it("says the work its issue would release", async () => {
    server(workCreditDraft());
    renderRoute("/invoices/1002");
    const card = await screen.findByTestId("would-release");
    expect(card).toHaveTextContent("Work this credit note gives back");
    expect(card).toHaveTextContent("Hour entry 501, Hour entry 502");
  });

  it("says when its issue would release nothing", async () => {
    server(workCreditDraft({ sources: { count: 0, held: 0, invoiced: 0, released: 0, wouldRelease: [] } }));
    renderRoute("/invoices/1002");
    expect(await screen.findByTestId("would-release")).toHaveTextContent("As it stands, issuing it gives no work back");
  });

  it("never sends work, a timesheet or a deducted invoice, and lets a deduction's credit only shrink", async () => {
    const credit = creditDraft({
      lines: [
        { ...creditDraft().lines[0] },
        {
          ...creditDraft().lines[0],
          id: 6002,
          description: "Tidligere fakturert a konto, faktura 985",
          quantity: -1,
          unitPrice: 100000,
          creditsLineId: 5002,
          deductsInvoiceId: 990,
          lineGross: -100000,
          lineNet: -100000,
        },
      ],
    });
    const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = path(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
      if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
      if (url === "/api/v1/invoices/1001")
        return jsonResponse(200, settlementDraft({ status: "issued", number: 1000 }));
      if (url === "/api/v1/invoices/1002" && method === "GET") return jsonResponse(200, credit);
      if (url === "/api/v1/invoices/1002" && method === "PUT") return jsonResponse(200, { ...credit, revision: 2 });
      return new Response(null, { status: 404 });
    });
    renderRoute("/invoices/1002");

    expect(await screen.findByRole("link", { name: "Deducts an earlier invoice" })).toHaveAttribute(
      "href",
      "/invoices/990",
    );
    expect(screen.queryByRole("button", { name: "Deduct earlier invoices" })).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: /Attach a timesheet/ })).not.toBeInTheDocument();
    const quantity = screen.getByRole("textbox", { name: "Line 2 quantity" });
    await waitFor(() => expect(quantity).toHaveValue("-1"));
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "-0.5");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/1002")).toBeDefined());
    const body = requestTo(fetchMock, "PUT", "/api/v1/invoices/1002") as InvoiceInput;
    expect(body.lines[1]).toMatchObject({ quantity: -0.5, creditsLineId: 5002 });
    expect(body.lines.some((l) => "deductsInvoiceId" in l || "sources" in l)).toBe(false);
    expect("timesheet" in body).toBe(false);
  });
});
