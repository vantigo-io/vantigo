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

const portfolio = (rows: EconomyRow[], overrides: Partial<EconomyPortfolioPage> = {}): EconomyPortfolioPage => ({
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
  // This installation has no expenses module; the expenses half of the table is Task 4's.
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
});
