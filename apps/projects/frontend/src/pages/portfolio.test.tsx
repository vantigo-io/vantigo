import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { EconomyPortfolioPage, EconomyRow } from "../api/economy";
import { stubFetch } from "../test/fetch";
import { makeRouteTree } from "../test/route-tree";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** The same formatter the page uses, so no assertion hard-codes a locale's separators. */
const money = (amount: number, currency = "NOK") =>
  new Intl.NumberFormat("en-US", { style: "currency", currency }).format(amount).replace(/\u00a0/g, " ");

const row = (overrides: Partial<EconomyRow> = {}): EconomyRow => ({
  project: {
    id: 7,
    code: "KVEWEBS",
    name: "Website",
    status: "active",
    customer: { id: 1001, name: "Kverneland" },
  },
  currency: "NOK",
  overBudget: false,
  readyCount: 1,
  readyAmount: 200000,
  budgetUsed: { basis: "amount", percent: 75, approvedPercent: 50 },
  actuals: {
    approved: { hours: 210, amount: 240000 },
    submitted: { hours: 62, amount: 120000 },
    draft: { hours: 40, amount: 0 },
    totalHours: 312,
    totalAmount: 360000,
  },
  pendingHours: 102,
  nextMilestone: {
    id: 11,
    name: "Launch",
    status: "planned",
    overdue: false,
    plannedDate: "2026-06-30",
    effectiveAmount: 300000,
  },
  ...overrides,
});

/** A row from an installation that tracks expenses, with nothing of its own ready. */
const trackedRow = (overrides: Partial<EconomyRow> = {}): EconomyRow =>
  row({ readyExpenseCount: 0, readyTotalAmount: 200000, ...overrides });

/** A page from an installation that tracks expenses; the totals carry the expense halves. */
const trackedPortfolio = (rows: EconomyRow[], overrides: Partial<EconomyPortfolioPage> = {}): EconomyPortfolioPage =>
  portfolio(rows, {
    expenseTracking: true,
    totals: {
      projectCount: rows.length,
      overBudgetCount: 0,
      readyCount: rows.length,
      readyExpenseCount: 0,
      readyExpenseOtherCurrencyCount: 0,
      readyAmounts: [{ currency: "NOK", amount: 200000, expenseAmount: 0, totalAmount: 200000 }],
    },
    ...overrides,
  });

/**
 * The rules the server keeps about the expense figures, checked on the way out,
 * so no test can make the page render a page the API would never send: the
 * three row figures and the two totals figures are there exactly when the
 * installation tracks expenses, a row's expense amount only when it has lines
 * ready in its own currency, and the row total only when one of its halves is
 * there.
 */
const asTheServerWouldSend = (body: EconomyPortfolioPage): EconomyPortfolioPage => {
  for (const project of body.data) {
    const expenseCount = project.readyExpenseCount;
    if (body.expenseTracking !== (expenseCount != null)) {
      throw new Error("A row carries readyExpenseCount exactly when the installation tracks expenses");
    }
    const hasExpenseAmount = project.readyExpenseAmount != null;
    if (hasExpenseAmount && (!project.currency || !expenseCount)) {
      throw new Error("readyExpenseAmount needs a currency and lines ready in it");
    }
    const halves = project.readyAmount != null || hasExpenseAmount;
    if ((project.readyTotalAmount != null) !== (body.expenseTracking && halves)) {
      throw new Error("readyTotalAmount is there exactly when expenses are tracked and a half has an amount");
    }
  }
  if (body.expenseTracking !== (body.totals.readyExpenseCount != null)) {
    throw new Error("The totals carry readyExpenseCount exactly when the installation tracks expenses");
  }
  if (body.expenseTracking !== (body.totals.readyExpenseOtherCurrencyCount != null)) {
    throw new Error("The totals carry readyExpenseOtherCurrencyCount exactly when the installation tracks expenses");
  }
  const flagged = body.data.filter((project) => project.readyExpenseOtherCurrency).length;
  if (body.data.some((project) => project.readyExpenseOtherCurrency === false)) {
    throw new Error("readyExpenseOtherCurrency is present only when true, never false");
  }
  if (!body.expenseTracking && flagged > 0) {
    throw new Error("No row is flagged where the installation does not track expenses");
  }
  if (body.expenseTracking && (body.totals.readyExpenseOtherCurrencyCount ?? 0) < flagged) {
    throw new Error("readyExpenseOtherCurrencyCount counts at least the flagged rows on this page");
  }
  for (const ready of body.totals.readyAmounts) {
    if (
      body.expenseTracking !== (ready.expenseAmount != null) ||
      body.expenseTracking !== (ready.totalAmount != null)
    ) {
      throw new Error("A currency's expenseAmount and totalAmount are there exactly when expenses are tracked");
    }
  }
  return body;
};

const portfolio = (rows: EconomyRow[], overrides: Partial<EconomyPortfolioPage> = {}): EconomyPortfolioPage =>
  asTheServerWouldSend({
    data: rows,
    pagination: {
      page: 1,
      pageSize: 25,
      totalCount: rows.length,
      totalPages: 1,
      hasNextPage: false,
      hasPreviousPage: false,
    },
    totals: {
      projectCount: rows.length,
      overBudgetCount: 0,
      readyCount: rows.length,
      readyAmounts: [{ currency: "NOK", amount: 200000 }],
    },
    timeTracking: true,
    expenseTracking: false,
    ...overrides,
  });

const stubPortfolio = (answer: Response) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/economy") return Promise.resolve(answer.clone());
    if (url.pathname === "/api/v1/customers") {
      return Promise.resolve(jsonResponse(200, { data: [], pagination: { page: 1, pageSize: 20 } }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderPage = (url = "/projects/economy") => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: makeRouteTree(true),
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  return { router };
};

const askedFor = (fetchMock: { actualCalls: [RequestInfo | URL, RequestInit | undefined][] }) =>
  new URL(String(fetchMock.actualCalls.filter(([url]) => String(url).includes("/economy")).at(-1)?.[0]), "http://x");

describe("EconomyPortfolio", () => {
  it("lists a project, its budget, its work and what is ready to invoice, linked to its own economy", async () => {
    stubPortfolio(jsonResponse(200, portfolio([row()])));
    renderPage();

    const link = await screen.findByRole("link", { name: "KVEWEBS" });
    expect(link).toHaveAttribute("href", "/projects/7/economy");

    const projectRow = link.closest("tr") as HTMLElement;
    expect(projectRow).toHaveTextContent("Website");
    expect(projectRow).toHaveTextContent("Kverneland");
    expect(within(projectRow).getByText("Active")).toBeInTheDocument();
    expect(projectRow).toHaveTextContent("75 % of the budget");
    expect(projectRow).toHaveTextContent(money(360000));
    expect(projectRow).toHaveTextContent("102 h");
    expect(projectRow).toHaveTextContent("Launch");
    expect(projectRow).toHaveTextContent("Jun 30, 2026");
    // What the next milestone is worth is the reason to look at the column.
    expect(projectRow).toHaveTextContent(money(300000));
    expect(projectRow).toHaveTextContent(money(200000));
  });

  it("names the table after the page it belongs to", async () => {
    stubPortfolio(jsonResponse(200, portfolio([row()])));
    renderPage();

    expect(await screen.findByRole("table", { name: "Project economy" })).toBeInTheDocument();
  });

  // Every surface shows the three buckets (E2), bar or no bar.
  it("says what has been logged in words when there is no budget to measure against", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        portfolio([
          row({
            budgetUsed: undefined,
            actuals: {
              approved: { hours: 20, amount: 24000 },
              submitted: { hours: 10, amount: 12000 },
              draft: { hours: 5, amount: 0 },
              totalHours: 35,
              totalAmount: 36000,
            },
          }),
        ]),
      ),
    );
    renderPage();

    const projectRow = (await screen.findByRole("link", { name: "KVEWEBS" })).closest("tr") as HTMLElement;
    expect(within(projectRow).queryByTestId("budget-bar")).not.toBeInTheDocument();
    expect(projectRow).toHaveTextContent("20 h approved · 10 h submitted · 5 h draft");
    expect(projectRow).toHaveTextContent("—");
  });

  // The totals are over the whole filtered set, and two currencies never add
  // up — so each is its own figure.
  it("sums the whole filtered set above the table, one ready amount per currency", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        portfolio([row()], {
          totals: {
            projectCount: 12,
            overBudgetCount: 3,
            readyCount: 5,
            readyAmounts: [
              { currency: "EUR", amount: 40000 },
              { currency: "NOK", amount: 200000 },
            ],
          },
        }),
      ),
    );
    renderPage();

    const totals = await screen.findByTestId("economy-portfolio-totals");
    expect(within(totals).getByText("Projects").parentElement).toHaveTextContent("12");
    expect(within(totals).getByText("Over budget").parentElement).toHaveTextContent("3");
    expect(totals).toHaveTextContent(money(40000, "EUR"));
    expect(totals).toHaveTextContent(money(200000));
  });

  it("asks the API for exactly what the URL says", async () => {
    const fetchMock = stubPortfolio(jsonResponse(200, portfolio([row()])));
    renderPage("/projects/economy?status=all&overBudget=true&hasReady=true&sort=readyAmount&page=2&customerId=1001");

    await screen.findByRole("link", { name: "KVEWEBS" });
    expect(Object.fromEntries(askedFor(fetchMock).searchParams)).toMatchObject({
      status: "all",
      overBudget: "true",
      hasReady: "true",
      sort: "readyAmount",
      page: "2",
      customerId: "1001",
    });
  });

  it("writes a filter into the URL and goes back to the first page", async () => {
    stubPortfolio(jsonResponse(200, portfolio([row()])));
    const { router } = renderPage("/projects/economy?page=3");

    await screen.findByRole("link", { name: "KVEWEBS" });
    await userEvent.click(screen.getByLabelText("Only over budget"));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ overBudget: true, page: 1 }));
  });

  it("holds the status and the order in the URL too", async () => {
    stubPortfolio(jsonResponse(200, portfolio([row()])));
    const { router } = renderPage();

    await screen.findByRole("link", { name: "KVEWEBS" });
    await userEvent.click(screen.getByRole("combobox", { name: "Status" }));
    await userEvent.click(await screen.findByRole("option", { name: "All statuses" }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ status: "all" }));

    await userEvent.click(screen.getByRole("combobox", { name: "Sort by" }));
    await userEvent.click(await screen.findByRole("option", { name: "Project code" }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ status: "all", sort: "code" }));
  });

  // A project with no budget at all has no percentage — which is a different
  // thing from having used none of it.
  it("writes a dash where a project has no budget to measure against", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        portfolio([
          row({ budgetUsed: undefined, project: { id: 8, code: "INTOFFS", name: "Offsite", status: "active" } }),
        ]),
      ),
    );
    renderPage();

    const projectRow = (await screen.findByRole("link", { name: "INTOFFS" })).closest("tr") as HTMLElement;
    expect(projectRow).toHaveTextContent("—");
    expect(projectRow).not.toHaveTextContent("0 %");
  });

  it("marks an overdue next milestone", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        portfolio([
          row({
            nextMilestone: { id: 11, name: "Launch", status: "planned", overdue: true, plannedDate: "2026-01-31" },
          }),
        ]),
      ),
    );
    renderPage();

    const projectRow = (await screen.findByRole("link", { name: "KVEWEBS" })).closest("tr") as HTMLElement;
    expect(within(projectRow).getByText("Overdue")).toBeInTheDocument();
  });

  it("says when this installation cannot report hours at all", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        portfolio([row({ actuals: undefined, budgetUsed: undefined, pendingHours: undefined })], {
          timeTracking: false,
        }),
      ),
    );
    renderPage();

    expect(await screen.findByText("Hours appear here when Time tracking is enabled.")).toBeInTheDocument();
  });

  it("explains who appears here when nothing does", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        portfolio([], { totals: { projectCount: 0, overBudgetCount: 0, readyCount: 0, readyAmounts: [] } }),
      ),
    );
    renderPage();

    expect(await screen.findByText("Projects whose financials you can see appear here.")).toBeInTheDocument();
    // Eight empty column headers above an empty state say nothing.
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  // Too many matching projects for one answer comes back as a 400 naming the
  // parameter to narrow; the message is the only thing that tells the reader
  // what to do, so it is shown rather than swallowed.
  it("shows the server's own refusal above the table when too many projects match", async () => {
    stubPortfolio(
      jsonResponse(400, {
        title: "Invalid project",
        errors: {
          status: [
            "More than 2000 projects match, which is more than one answer can carry. Narrow the portfolio with 'status', 'customerId' or 'search'.",
          ],
        },
      }),
    );
    renderPage();

    expect(
      await screen.findByText(
        "More than 2000 projects match, which is more than one answer can carry. Narrow the portfolio with 'status', 'customerId' or 'search'.",
      ),
    ).toBeInTheDocument();
  });

  // The column is what is ready to invoice on the project altogether, and the
  // two halves are named under it — the server has already added them up.
  it("shows the whole of what is ready and says how much of it is expenses", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        trackedPortfolio([
          trackedRow({
            readyCount: 1,
            readyAmount: 200000,
            readyExpenseCount: 3,
            readyExpenseAmount: 5000,
            readyTotalAmount: 205000,
          }),
        ]),
      ),
    );
    renderPage();

    const projectRow = (await screen.findByRole("link", { name: "KVEWEBS" })).closest("tr") as HTMLElement;
    expect(projectRow).toHaveTextContent(money(205000));
    expect(projectRow).toHaveTextContent(`milestones ${money(200000)} · expenses ${money(5000)}`);
  });

  // The case the ready filter was relabelled for: receipts waiting for an
  // invoice and no milestone at all. The server says "none", not "cannot say",
  // and the total above already is the expense half — so there is nothing to
  // split and a dash would be a different claim.
  it("splits nothing on a project whose only ready money is expenses", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        trackedPortfolio([
          trackedRow({
            readyCount: 0,
            readyAmount: undefined,
            readyExpenseCount: 3,
            readyExpenseAmount: 5000,
            readyTotalAmount: 5000,
          }),
        ]),
      ),
    );
    renderPage();

    const projectRow = (await screen.findByRole("link", { name: "KVEWEBS" })).closest("tr") as HTMLElement;
    expect(projectRow).toHaveTextContent(money(5000));
    expect(projectRow).toHaveTextContent("0 milestones · 3 expense lines");
    expect(within(projectRow).queryByTestId("ready-split")).not.toBeInTheDocument();
    expect(projectRow).not.toHaveTextContent("milestones —");
  });

  it("splits nothing on a project with no expense lines ready", async () => {
    stubPortfolio(jsonResponse(200, trackedPortfolio([trackedRow()])));
    renderPage();

    const projectRow = (await screen.findByRole("link", { name: "KVEWEBS" })).closest("tr") as HTMLElement;
    expect(projectRow).toHaveTextContent(money(200000));
    expect(projectRow).toHaveTextContent("1 milestone · 0 expense lines");
    expect(within(projectRow).queryByTestId("ready-split")).not.toBeInTheDocument();
  });

  // A project whose only invoiceable money is in a currency its row cannot
  // report is the one the server flags rather than drops. The row shows no
  // amount for it — there is none to show in this row's currency — but it says
  // there is something to find, and the project link beside it is the way there.
  it("says when a row has more ready in another currency", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        trackedPortfolio(
          [
            trackedRow({ readyExpenseOtherCurrency: true }),
            trackedRow({ project: { ...row().project, id: 8, code: "KVEAPP0", name: "App" } }),
          ],
          {
            totals: {
              projectCount: 2,
              overBudgetCount: 0,
              readyCount: 2,
              readyExpenseCount: 0,
              readyExpenseOtherCurrencyCount: 1,
              readyAmounts: [{ currency: "NOK", amount: 200000, expenseAmount: 0, totalAmount: 200000 }],
            },
          },
        ),
      ),
    );
    renderPage();

    const flaggedRow = (await screen.findByRole("link", { name: "KVEWEBS" })).closest("tr") as HTMLElement;
    expect(within(flaggedRow).getByTestId("ready-other-currency")).toHaveTextContent("+ ready in another currency");
    const plainRow = (await screen.findByRole("link", { name: "KVEAPP0" })).closest("tr") as HTMLElement;
    expect(within(plainRow).queryByTestId("ready-other-currency")).not.toBeInTheDocument();
  });

  it("counts the projects with something ready in another currency under the cards", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        trackedPortfolio([trackedRow({ readyExpenseOtherCurrency: true })], {
          totals: {
            projectCount: 1,
            overBudgetCount: 0,
            readyCount: 1,
            readyExpenseCount: 0,
            readyExpenseOtherCurrencyCount: 2,
            readyAmounts: [{ currency: "NOK", amount: 200000, expenseAmount: 0, totalAmount: 200000 }],
          },
        }),
      ),
    );
    renderPage();

    const totals = await screen.findByTestId("economy-portfolio-totals");
    expect(within(totals).getByTestId("ready-other-currency-count")).toHaveTextContent(
      "2 projects have something ready in another currency",
    );
  });

  it("says nothing about other currencies when no project has any", async () => {
    stubPortfolio(jsonResponse(200, trackedPortfolio([trackedRow()])));
    renderPage();

    await screen.findByTestId("economy-portfolio-totals");
    expect(screen.queryByTestId("ready-other-currency-count")).not.toBeInTheDocument();
    expect(screen.queryByTestId("ready-other-currency")).not.toBeInTheDocument();
  });

  it("keeps the ready column exactly as it was where expenses are not tracked", async () => {
    stubPortfolio(jsonResponse(200, portfolio([row()])));
    renderPage();

    const projectRow = (await screen.findByRole("link", { name: "KVEWEBS" })).closest("tr") as HTMLElement;
    expect(projectRow).toHaveTextContent(money(200000));
    expect(projectRow).not.toHaveTextContent("milestone");
    expect(projectRow).not.toHaveTextContent("expense");
    expect(within(projectRow).queryByTestId("ready-split")).not.toBeInTheDocument();
    expect(screen.queryByTestId("portfolio-other-currency-note")).not.toBeInTheDocument();
  });

  // A currency can be in the list for its expenses alone, where the milestone
  // half is a sum over nothing rather than a figure that went missing.
  it("counts the expense lines in the ready card, per currency and in words", async () => {
    stubPortfolio(
      jsonResponse(
        200,
        trackedPortfolio([trackedRow({ readyExpenseCount: 3, readyExpenseAmount: 5000, readyTotalAmount: 205000 })], {
          totals: {
            projectCount: 12,
            overBudgetCount: 3,
            readyCount: 5,
            readyExpenseCount: 4,
            readyExpenseOtherCurrencyCount: 0,
            readyAmounts: [
              { currency: "NOK", amount: 200000, expenseAmount: 5000, totalAmount: 205000 },
              { currency: "EUR", amount: 0, expenseAmount: 400, totalAmount: 400 },
            ],
          },
        }),
      ),
    );
    renderPage();

    const totals = await screen.findByTestId("economy-portfolio-totals");
    expect(totals).toHaveTextContent(money(205000));
    expect(totals).toHaveTextContent(money(400, "EUR"));
    expect(totals).toHaveTextContent("5 milestones · 4 expense lines");
    // Each currency is named and stands on its own line: two currencies in one
    // flat list put a NOK expense figure beside a EUR "milestones".
    const split = within(totals).getByTestId("ready-amount-split");
    expect(within(split).getByText(`NOK: milestones ${money(200000)} · expenses ${money(5000)}`)).toBeInTheDocument();
    expect(
      within(split).getByText(`EUR: milestones ${money(0, "EUR")} · expenses ${money(400, "EUR")}`),
    ).toBeInTheDocument();
    // A row folds in no other currency, by design — the note says where those
    // expenses are reported instead.
    expect(
      screen.getByText(
        "Expenses recorded in a currency other than the project's are not in these figures; the project's own Economy tab reports them.",
      ),
    ).toBeInTheDocument();
  });

  // Where expenses count too, a project can be waiting for an invoice with no
  // milestone at all, so the filter is no longer about milestones alone.
  it("names the ready filter after everything that can be ready", async () => {
    stubPortfolio(jsonResponse(200, trackedPortfolio([trackedRow()])));
    renderPage();

    expect(await screen.findByLabelText("Only with something ready to invoice")).toBeInTheDocument();
    expect(screen.queryByLabelText("Only with milestones ready to invoice")).not.toBeInTheDocument();
  });
});
