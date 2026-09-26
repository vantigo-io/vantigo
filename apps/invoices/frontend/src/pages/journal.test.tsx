import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoiceJournal } from "../api/journal";
import { jsonResponse } from "../test/api";
import { stubFetch } from "../test/fetch";
import { journal, meta } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { JournalPage } from "./journal";

const path = (input: RequestInfo | URL) => String(input);

const server = (answer: InvoiceJournal | ((url: string) => InvoiceJournal)) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = path(input);
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url.startsWith("/api/v1/invoices/journal?")) {
      return jsonResponse(200, typeof answer === "function" ? answer(url) : answer);
    }
    return new Response(null, { status: 404 });
  });

describe("the invoice journal", () => {
  it("reads this month by default and says in words that the series has no gaps", async () => {
    const fetchMock = server(journal());
    renderWithProviders(<JournalPage />);

    expect(await screen.findByText("No gaps between 1000 and 1002.")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => path(url).includes("from=2026-09-01&to=2026-09-12"))).toBe(true);
    expect(screen.getByText("Credit note for 1000")).toBeInTheDocument();
    expect(screen.getByText(/Net NOK\s?800\.00/)).toBeInTheDocument();
  });

  it("names the missing numbers, and warns when the counter ran ahead of every document", async () => {
    server(journal({ gaps: [1004, 1005], counterLast: 1006, checkedTo: 1006 }));
    renderWithProviders(<JournalPage />);

    expect(await screen.findByText("Missing numbers: 1004, 1005.")).toBeInTheDocument();
    expect(screen.getByText(/The counter is at 1006 but the highest document is 1002/)).toBeInTheDocument();
  });

  // A broken series is never routine (the counter rolls back with the issue
  // and an issued row cannot be deleted), so it is said as an alarm, over the
  // numbers the server checked — which can start before the range's first
  // document — and a truncated list says it is only the start.
  it("says which numbers it checked, that a break is never normal, and that a long list is cut", async () => {
    server(journal({ gaps: [1001], gapsTruncated: true, checkedFrom: 1001, checkedTo: 1004 }));
    renderWithProviders(<JournalPage />);

    expect(await screen.findByText("Missing numbers: 1001.")).toBeInTheDocument();
    expect(screen.getByText("Checked 1001 to 1004.")).toBeInTheDocument();
    expect(screen.getByText("Only the first 1000 missing numbers are shown.")).toBeInTheDocument();
    expect(screen.getByText(/This never happens in normal use/)).toBeInTheDocument();
  });

  it("says a range without documents has nothing to check, not that it has no gaps", async () => {
    server(
      journal({
        data: [],
        pagination: { page: 1, pageSize: 25, totalCount: 0, totalPages: 0, hasNextPage: false, hasPreviousPage: false },
        totals: { byCode: [], netTotal: 0, vatTotal: 0, grossTotal: 0 },
        checkedFrom: undefined,
        checkedTo: undefined,
      }),
    );
    renderWithProviders(<JournalPage />);

    expect(
      await screen.findByText("No documents were issued in this range, so there is nothing to check."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/No gaps/)).not.toBeInTheDocument();
  });

  it("reads a past range over two pages from the server's facts, not the page's", async () => {
    // August: 1000 to 1003 over two pages of two; the series has gone on to
    // 1010 since, and the counter with it — no warning, on either page.
    const pagination = {
      page: 1,
      pageSize: 2,
      totalCount: 4,
      totalPages: 2,
      hasNextPage: true,
      hasPreviousPage: false,
    };
    const rows = journal().data;
    const pageOf = (url: string): InvoiceJournal => {
      const second = url.includes("page=2");
      return journal({
        data: second
          ? [
              { ...rows[1], id: 12, number: 1002 },
              { ...rows[1], id: 13, number: 1003 },
            ]
          : [
              { ...rows[0], id: 10, number: 1000 },
              { ...rows[1], id: 11, number: 1001 },
            ],
        pagination: second ? { ...pagination, page: 2, hasNextPage: false, hasPreviousPage: true } : pagination,
        checkedFrom: 1000,
        checkedTo: 1003,
        highestIssued: 1010,
        counterLast: 1010,
      });
    };
    server(pageOf);
    renderWithProviders(<JournalPage />);

    expect(await screen.findByText("No gaps between 1000 and 1003.")).toBeInTheDocument();
    expect(screen.queryByText(/The counter is at/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "2" }));
    expect(await screen.findByText("1003")).toBeInTheDocument();
    expect(screen.getByText("No gaps between 1000 and 1003.")).toBeInTheDocument();
    expect(screen.queryByText(/The counter is at/)).not.toBeInTheDocument();
  });

  // The range starts from meta's today: without meta the journal is never
  // asked for, and the page said nothing while its skeleton spun for ever.
  it("says when the metadata cannot be loaded, rather than loading for ever", async () => {
    stubFetch((input: RequestInfo | URL) =>
      path(input) === "/api/v1/invoices/meta"
        ? jsonResponse(500, { title: "Boom", status: 500 })
        : jsonResponse(200, journal()),
    );
    renderWithProviders(<JournalPage />);

    expect(await screen.findByText("Could not load Invoices")).toBeInTheDocument();
    expect(screen.queryByTestId("content-skeleton")).not.toBeInTheDocument();
  });
});
