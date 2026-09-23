import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import type { AnchorHTMLAttributes, ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type CustomerOverview, customerOverviewQueryOptions } from "../../api/customer-overview";
import { request } from "../../api/request";
import { Customer360Panel, latestActivity } from "./-customer-360-panel";
import "../../i18n";

// The panel is the host's (customer 360 design D3): it reads nothing but the
// overview response, so the response is the one thing these tests vary.
vi.mock("../../api/request", () => ({ request: vi.fn() }));

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    // No router in this test: a project row's link renders as the anchor it resolves to.
    Link: ({
      to,
      params,
      children,
      ...props
    }: {
      to: string;
      params: Record<string, number>;
      children?: ReactNode;
    } & AnchorHTMLAttributes<HTMLAnchorElement>) => (
      <a {...props} href={to.replace(/\$(\w+)/g, (_, name: string) => String(params[name]))}>
        {children}
      </a>
    ),
  };
});

const renderPanel = (body: unknown, queryClient = new QueryClient()) => {
  if (body instanceof Error) vi.mocked(request).mockRejectedValue(body);
  else vi.mocked(request).mockResolvedValue(body as never);
  // A bare client, as main.tsx builds it: the query's own options (retry among
  // them) are what these tests exercise, not a test-only default.
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <Customer360Panel customerId={42} />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

const everything = {
  projects: {
    openCount: 2,
    totalCount: 5,
    truncated: false,
    open: [
      { id: 1001, code: "KVEM1000", name: "Kraft-Verket modernisering", status: "active", lastWorkOn: "2026-09-11" },
      { id: 1002, code: "KVEM1001", name: "Kraft-Verket drift", status: "active" },
    ],
  },
  work: {
    unbilledHoursHundredths: 1250,
    unbilledAmounts: [
      { currency: "EUR", amount: 300 },
      { currency: "NOK", amount: 1250 },
    ],
    approvedHoursHundredths: 2000,
    submittedHoursHundredths: 200,
    draftHoursHundredths: 100,
    lastWorkOn: "2026-09-12",
  },
  expenses: { readyCount: 2, readyAmounts: [{ currency: "NOK", amount: 450.5 }], lastExpenseOn: "2026-09-05" },
  lastActivity: { timelineOn: "2026-09-01", workOn: "2026-09-12", expenseOn: "2026-09-05" },
};

describe("the customer 360 panel", () => {
  afterEach(() => {
    cleanup();
    vi.mocked(request).mockReset();
    act(() => {
      setLanguagePreference("auto");
    });
  });

  it("asks for this customer's overview, showing the skeleton card until it answers", async () => {
    renderPanel({ lastActivity: {} });
    // Loading: the region is busy and holds only the skeleton — KpiCard's
    // loading branch renders no label, so not even "Last activity" is there.
    const region = screen.getByRole("region", { name: "Customer 360" });
    expect(region).toHaveAttribute("aria-busy", "true");
    expect(screen.queryByText("Last activity")).not.toBeInTheDocument();
    expect(screen.queryByText("Nothing recorded yet")).not.toBeInTheDocument();
    await screen.findByText("Nothing recorded yet");
    expect(screen.getByRole("region", { name: "Customer 360" })).not.toHaveAttribute("aria-busy");
    expect(request).toHaveBeenCalledWith(
      "/api/v1/customers/42/overview",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
  });

  it("shows only the Last activity tile for the body the server sends when nothing is set", async () => {
    renderPanel({ lastActivity: {} });

    expect(await screen.findByText("Last activity")).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.getByText("Nothing recorded yet")).toBeInTheDocument();
    expect(screen.queryByText("Open projects")).not.toBeInTheDocument();
    expect(screen.queryByText("Unbilled hours")).not.toBeInTheDocument();
    expect(screen.queryByText("Expenses ready to invoice")).not.toBeInTheDocument();
  });

  it("shows every tile and the open projects for a caller who may see them all", async () => {
    renderPanel(everything);

    expect(await screen.findByText("of 5 projects")).toBeInTheDocument();
    // The tile's label and the rows' card title.
    expect(screen.getAllByText("Open projects")).toHaveLength(2);
    expect(screen.getByText("12.5 h")).toBeInTheDocument();
    expect(screen.getByText(/€300\.00 · NOK\s1,250\.00/)).toBeInTheDocument();
    expect(screen.getByText("Expenses ready to invoice")).toBeInTheDocument();
    expect(screen.getByText(/NOK\s450\.50/)).toBeInTheDocument();
    // The latest of the three dates, and which it was.
    expect(screen.getByText("Sep 12, 2026")).toBeInTheDocument();
    expect(screen.getByText("Logged work")).toBeInTheDocument();
    // The name is the link, so a screen reader's link list reads the project;
    // the code stays in its own cell.
    expect(screen.getByRole("link", { name: "Kraft-Verket modernisering" })).toHaveAttribute("href", "/projects/1001");
    expect(screen.queryByRole("link", { name: "KVEM1000" })).not.toBeInTheDocument();
    const [, first, second] = screen.getAllByRole("row");
    expect(within(first).getByText("KVEM1000")).toBeInTheDocument();
    expect(within(first).getByText("Active")).toBeInTheDocument();
    expect(within(first).getByText("Sep 11, 2026")).toBeInTheDocument();
    expect(within(second).getByText("Active")).toBeInTheDocument();
    expect(within(second).getByText("—")).toBeInTheDocument();
    // Every open project is listed, so there is no "see all" line.
    expect(screen.queryByText(/See all/)).not.toBeInTheDocument();
  });

  it("shows the translated tiles and rows in Norwegian", async () => {
    act(() => {
      setLanguagePreference("nb");
    });
    renderPanel(everything);

    expect(await screen.findByText("av 5 prosjekter")).toBeInTheDocument();
    expect(screen.getAllByText("Aktiv")).toHaveLength(2);
    expect(screen.getByText("12,5 t")).toBeInTheDocument();
  });

  it("shows a calendar date as that day in every time zone", async () => {
    // new Date("2026-01-01") is UTC midnight: read in any zone west of
    // Greenwich it is still the 31st of December, so only a UTC rendering
    // shows the day the server meant. Run this file under
    // TZ=America/Los_Angeles to see the difference.
    renderPanel({
      projects: {
        openCount: 1,
        totalCount: 1,
        truncated: false,
        open: [
          {
            id: 1001,
            code: "KVEM1000",
            name: "Kraft-Verket modernisering",
            status: "active",
            lastWorkOn: "2026-01-01",
          },
        ],
      },
      lastActivity: { workOn: "2026-01-01" },
    });

    expect(await screen.findAllByText("Jan 1, 2026")).toHaveLength(2);
    expect(screen.queryByText(/Dec 31, 2025/)).not.toBeInTheDocument();
  });

  it("says project, not projects, for one, and formats the counts for the locale", async () => {
    renderPanel({
      projects: {
        openCount: 1,
        totalCount: 1,
        truncated: false,
        open: [{ id: 1001, code: "KVEM1000", name: "Kraft-Verket modernisering", status: "active" }],
      },
      lastActivity: {},
    });
    expect(await screen.findByText("of 1 project")).toBeInTheDocument();
    cleanup();

    renderPanel({
      projects: { openCount: 1500, totalCount: 1800, truncated: false, open: [] },
      expenses: { readyCount: 1200, readyAmounts: [] },
      lastActivity: {},
    });
    expect(await screen.findByText("of 1,800 projects")).toBeInTheDocument();
    expect(screen.getByText("1,500")).toBeInTheDocument();
    expect(screen.getByText("1,200")).toBeInTheDocument();
  });

  it("links to the customer's Projects tab when there are more open projects than rows", async () => {
    renderPanel({ ...everything, projects: { ...everything.projects, openCount: 1234 } });

    expect(await screen.findByRole("link", { name: "See all 1,234 open projects" })).toHaveAttribute(
      "href",
      "/customers/42/projects",
    );
  });

  it("leaves the money out for a caller without financial rights", async () => {
    // The body the server sends without projects:view-financials: no
    // unbilledAmounts in work, and no expenses section at all.
    renderPanel({
      projects: everything.projects,
      work: {
        unbilledHoursHundredths: 1250,
        approvedHoursHundredths: 2000,
        submittedHoursHundredths: 200,
        draftHoursHundredths: 100,
        lastWorkOn: "2026-09-12",
      },
      lastActivity: everything.lastActivity,
    });

    expect(await screen.findByText("Unbilled hours")).toBeInTheDocument();
    expect(screen.queryByText(/NOK/)).not.toBeInTheDocument();
    expect(screen.queryByText("Expenses ready to invoice")).not.toBeInTheDocument();
  });

  it("says the count was cut when the customer reached the cap, and lists no rows it was not given", async () => {
    renderPanel({ projects: { openCount: 0, totalCount: 2000, truncated: true, open: [] }, lastActivity: {} });

    // Not a number: for a caller who sees only their role projects the count is
    // of those, so "of the first N" would name the wrong N.
    expect(await screen.findByText("This customer has more projects than the panel counts")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("hides itself, not the page, when the overview cannot be read, and does not ask again", async () => {
    renderPanel(new Error("boom"));

    expect(screen.getByRole("region", { name: "Customer 360" })).toBeInTheDocument();
    // Within waitFor's one second: with the default three retries the skeleton
    // would stay for about seven.
    await waitFor(() => expect(screen.queryByRole("region", { name: "Customer 360" })).not.toBeInTheDocument());
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("keeps what it showed when a background refetch fails", async () => {
    const queryClient = new QueryClient();
    const { queryKey } = customerOverviewQueryOptions(42);
    const cached: CustomerOverview = {
      projects: null,
      work: null,
      expenses: null,
      lastActivity: { timelineOn: "2026-09-01", workOn: null, expenseOn: null },
    };
    // Stale from the start, so mounting refetches — and that refetch fails.
    queryClient.setQueryData(queryKey, cached, { updatedAt: 0 });
    renderPanel(new Error("boom"), queryClient);

    await waitFor(() => expect(queryClient.getQueryState(queryKey)?.status).toBe("error"));
    expect(request).toHaveBeenCalledWith("/api/v1/customers/42/overview", expect.anything());
    expect(screen.getByRole("region", { name: "Customer 360" })).toBeInTheDocument();
    expect(screen.getByText("Sep 1, 2026")).toBeInTheDocument();
  });
});

describe("the latest activity", () => {
  it("is the customer's own timeline on a tie", () => {
    expect(latestActivity({ timelineOn: "2026-09-12", workOn: "2026-09-12", expenseOn: "2026-09-12" })).toEqual({
      on: "2026-09-12",
      source: "timeline",
    });
  });

  it("is work over expenses on a tie", () => {
    expect(latestActivity({ timelineOn: null, workOn: "2026-09-12", expenseOn: "2026-09-12" })).toEqual({
      on: "2026-09-12",
      source: "work",
    });
  });

  it("is an expense when that is all there is", () => {
    expect(latestActivity({ timelineOn: null, workOn: null, expenseOn: "2026-09-05" })).toEqual({
      on: "2026-09-05",
      source: "expense",
    });
  });

  it("is work when that is all there is", () => {
    expect(latestActivity({ timelineOn: null, workOn: "2026-09-11", expenseOn: null })).toEqual({
      on: "2026-09-11",
      source: "work",
    });
  });

  it("is nothing when nothing is recorded", () => {
    expect(latestActivity({ timelineOn: null, workOn: null, expenseOn: null })).toBeNull();
  });
});
