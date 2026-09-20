import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sessionQueryKey } from "../../api/auth";
import { ProjectEconomyRoute } from "./-project-economy-route";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return { ...actual, useParams: () => ({ projectId: 31 }) };
});

vi.mock("@vantigo/projects-ui/pages/project-economy", () => ({
  ProjectEconomy: ({ projectId, expensesHref }: { projectId: number; expensesHref?: string }) => (
    <div>
      economy for project {projectId} → {expensesHref ?? "no expenses link"}
    </div>
  ),
}));

const renderRoute = (modules: string[], permissions: string[] = ["expenses:access"]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // What the layout above already put there. The link is decided from the same
  // three answers the tab row is, so it cannot disagree with it.
  queryClient.setQueryData(sessionQueryKey, { user: { id: "user-1", roles: [] } });
  queryClient.setQueryData(["authorization", "me", "none"], { permissions });
  queryClient.setQueryData(["projects", "detail", 31], {
    id: 31,
    capabilities: { canManage: true, canContribute: true, canSeeFinancials: true },
  });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectEconomyRoute />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the project page's economy route", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  // The projects package knows no route of the Expenses app and imports
  // nothing from it, so the host is the one that can say where the expenses
  // behind "ready to invoice" live.
  it("points the ready-to-invoice row at the project's own Expenses tab", () => {
    renderRoute(["projects", "expenses"]);

    expect(screen.getByText(/economy for project 31 → \/projects\/31\/expenses/)).toBeInTheDocument();
  });

  it("offers no link at all when the installation did not mount expenses", () => {
    renderRoute(["projects"]);

    expect(screen.getByText(/no expenses link/)).toBeInTheDocument();
  });

  // Economy needs only `projects:*`, so a manager with financial rights and no
  // Expenses app is an ordinary combination — and every operation of that API
  // demands `expenses:access`. A link for them would land on a page of
  // refusals under a tab strip the tab is not even in.
  it("offers no link to a caller who may not open the Expenses tab", () => {
    renderRoute(["projects", "expenses"], ["projects:access"]);

    expect(screen.getByText(/no expenses link/)).toBeInTheDocument();
  });
});
