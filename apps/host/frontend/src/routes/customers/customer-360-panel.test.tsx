import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import type { AnchorHTMLAttributes, ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { request } from "../../api/request";
import { Customer360Panel } from "./-customer-360-panel";
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
      params: { projectId: number };
      children?: ReactNode;
    } & AnchorHTMLAttributes<HTMLAnchorElement>) => (
      <a {...props} href={to.replace("$projectId", String(params.projectId))}>
        {children}
      </a>
    ),
  };
});

const renderPanel = (body: unknown) => {
  if (body instanceof Error) vi.mocked(request).mockRejectedValue(body);
  else vi.mocked(request).mockResolvedValue(body as never);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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
  });

  it("asks for this customer's overview, showing the skeleton card until it answers", async () => {
    renderPanel({ lastActivity: {} });
    // Loading: the region is there with its skeleton tile, and no figure yet.
    expect(screen.getByRole("region", { name: "Customer 360" })).toBeInTheDocument();
    expect(screen.queryByText("Nothing recorded yet")).not.toBeInTheDocument();
    await screen.findByText("Nothing recorded yet");
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
    expect(screen.getByRole("link", { name: "KVEM1000" })).toHaveAttribute("href", "/projects/1001");
    expect(screen.getByText("Sep 11, 2026")).toBeInTheDocument();
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

    expect(await screen.findByText("of the first 2000 projects")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("hides itself, not the page, when the overview cannot be read", async () => {
    renderPanel(new Error("boom"));

    expect(screen.getByRole("region", { name: "Customer 360" })).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("region", { name: "Customer 360" })).not.toBeInTheDocument());
  });
});
