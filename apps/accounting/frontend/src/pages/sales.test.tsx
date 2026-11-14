import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { InvoiceVouchers } from "../components/invoice-vouchers";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { meta, posting, report, stubAccountingApi, unposted, voucher } from "../test/server";

// Delivery B's screens: the posting rules, Needs attention with Retry, the
// overview's count and the catch-up, and the links between a voucher and its
// invoice.

describe("the posting rules", () => {
  it("lists the rules with their version and adds one", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/posting-rules");
    const table = await screen.findByTestId("posting-rules");
    expect(within(table).getByText("Avgiftspliktig salg, alminnelig sats (kode 3)")).toBeInTheDocument();
    expect(within(table).getByText("Tax code (default)")).toBeInTheDocument();
    expect(within(table).getByText("Hours")).toBeInTheDocument();
    // A default rule is never deleted; another rule is.
    expect(within(table).getAllByRole("button", { name: /^Archive the rule/ })).toHaveLength(1);

    await userEvent.click(screen.getByRole("button", { name: "Add a rule" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Name/ }), "Milestones");
    await userEvent.click(within(dialog).getByRole("combobox", { name: /^Account/ }));
    // A sales rule credits income (R15): the picker offers no other account.
    expect(screen.queryByRole("option", { name: "6300 Leie av lokaler" })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("option", { name: "3000 Salgsinntekt, avgiftspliktig" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/posting-rules$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/posting-rules$/)[0].body).toEqual({
      name: "Milestones",
      scope: "sales",
      match: "line_kind",
      lineKind: "hours",
      accountId: 1005,
    });
  });

  it("changes a rule's account, saying it makes a new version", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/posting-rules");
    await userEvent.click(await screen.findByRole("button", { name: "Timer" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/makes this version 3/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/posting-rules\/1002$/)).toHaveLength(1));
  });

  it("offers no change to someone who may not manage the books", async () => {
    stubAccountingApi({ meta: meta({ canClose: false }) });
    renderRoute("/accounting/posting-rules");
    await screen.findByTestId("posting-rules");
    expect(screen.queryByRole("button", { name: "Add a rule" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Archive the rule/ })).not.toBeInTheDocument();
  });
});

describe("needs attention", () => {
  it("says why a fact is not posted, links its invoice, and retries it", async () => {
    const fetchMock = stubAccountingApi({ unposted: [unposted()] });
    renderRoute("/accounting/needs-attention");
    const table = await screen.findByTestId("unposted");
    expect(within(table).getByText("The tax code is not in the books, or is inactive.")).toBeInTheDocument();
    expect(within(table).getByRole("link", { name: "101" })).toHaveAttribute("href", "/invoices/9001");
    expect(within(table).queryByRole("button", { name: /dismiss/i })).not.toBeInTheDocument();
    await userEvent.click(within(table).getByRole("button", { name: "Retry posting 101" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/unposted\/7001\/retry$/)).toHaveLength(1));
    expect(await screen.findByText("Posted as 2026-000019.")).toBeInTheDocument();
  });

  it("says nothing waits when nothing does", async () => {
    stubAccountingApi();
    renderRoute("/accounting/needs-attention");
    expect(await screen.findByText("Every sale from Invoices is in the books.")).toBeInTheDocument();
  });
});

describe("the overview", () => {
  it("counts what needs attention and says where the catch-up stands", async () => {
    stubAccountingApi({ meta: meta({ unposted: 2 }) });
    renderRoute("/accounting");
    expect(await screen.findByRole("link", { name: "2 facts wait to be posted." })).toHaveAttribute(
      "href",
      "/accounting/needs-attention",
    );
    const card = await screen.findByTestId("catch-up-card");
    expect(within(card).getByText(/Read through 101/)).toBeInTheDocument();
    expect(within(card).getByText(/3 waiting/)).toBeInTheDocument();
  });
});

describe("a voucher and its invoice", () => {
  it("links a system voucher to its invoice and marks a loss's VAT", async () => {
    stubAccountingApi({
      vouchers: [
        voucher({
          system: true,
          kind: "write_off",
          rule: "sales.write_off_vat",
          ruleVersion: 1,
          sourceDocumentId: 9001,
          source: { module: "invoices", kind: "write_off", id: 31, part: "" },
          postings: [
            posting(1, 1001, "1500", "Kundefordringer", "-1250.00", { customerId: 501 }),
            posting(2, 1004, "2700", "Utgående mva", "250.00", {
              taxCodeId: 2001,
              taxCode: "3",
              taxRole: "tax",
              taxSpecification: "tapPåKrav",
            }),
            posting(3, 1003, "7830", "Tap på fordringer", "1000.00"),
          ],
        }),
      ],
    });
    renderRoute("/accounting/vouchers/5001");
    expect(await screen.findByTestId("source-document")).toHaveAttribute("href", "/invoices/9001");
    expect(screen.getByText("tapPåKrav")).toBeInTheDocument();
  });

  it("links a customer report's item to its invoice", async () => {
    const r = report();
    r.columns.push({ key: "document", label: "Dokument", type: "text" });
    r.rows[0] = { ...r.rows[0], cells: { ...r.rows[0].cells, document: "101" }, documentId: 9001 };
    stubAccountingApi({ report: r });
    renderRoute("/accounting/reports");
    expect(await screen.findByRole("link", { name: "101" })).toHaveAttribute("href", "/invoices/9001");
  });

  it("shows an invoice's vouchers, and nothing when there are none", async () => {
    stubAccountingApi({ vouchers: [voucher({ system: true, sourceDocumentId: 9001 })] });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={client}>
          <InvoiceVouchers documentId={9001} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    const vouchers = await screen.findByTestId("invoice-vouchers");
    expect(within(vouchers).getByRole("link", { name: "2026-000007" })).toHaveAttribute(
      "href",
      "/accounting/vouchers/5001",
    );
    expect(within(vouchers).queryByRole("button", { name: /more/ })).not.toBeInTheDocument();
  });

  it("says how many more vouchers an invoice has past the first page, and reads them", async () => {
    const many = Array.from({ length: 23 }, (_, i) =>
      voucher({ id: 5001 + i, number: 7 + i, display: `2026-${String(7 + i).padStart(6, "0")}`, system: true }),
    );
    stubAccountingApi({ vouchers: many });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={client}>
          <InvoiceVouchers documentId={9001} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    const vouchers = await screen.findByTestId("invoice-vouchers");
    expect(within(vouchers).getAllByRole("link")).toHaveLength(20);
    await userEvent.click(within(vouchers).getByRole("button", { name: "3 more" }));
    expect(await within(vouchers).findByRole("link", { name: "2026-000029" })).toBeInTheDocument();
    expect(within(vouchers).getAllByRole("link")).toHaveLength(23);
    expect(within(vouchers).queryByRole("button", { name: /more/ })).not.toBeInTheDocument();
  });
});
