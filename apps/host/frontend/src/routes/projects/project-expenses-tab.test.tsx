import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sessionQueryKey } from "../../api/auth";
import { ProjectExpensesTab } from "./-project-expenses-tab";
import "../../i18n";

// The tab lives on the project page but calls the expenses API, so it is the
// host that decides whether the module is mounted at all, and the host that
// owns the one thing the package cannot reach: the Economy tab's cache, which
// reads the same expenses from the projects API.
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useParams: () => ({ projectId: 31 }),
    // No router in this test: the not-enabled page's "Go home" renders as a plain anchor.
    Link: ({ children }: { children: React.ReactNode }) => <a href="/">{children}</a>,
    useRouter: () => ({ history: { back: vi.fn() } }),
  };
});

vi.mock("@vantigo/expenses-ui/components/project-expenses-panel", () => ({
  ProjectExpensesPanel: ({ projectId, onChanged }: { projectId: number; onChanged?: () => void }) => (
    <div>
      <span>expenses for project {projectId}</span>
      <button type="button" onClick={() => onChanged?.()}>
        pretend something changed
      </button>
    </div>
  ),
}));

const renderTab = (modules?: string[], permissions: string[] = ["expenses:access"]) => {
  if (modules) window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // The layout above this tab has already asked for both, so the gate reads
  // them from the cache; seeding them is what a click on the tab row does.
  queryClient.setQueryData(sessionQueryKey, { user: { id: "user-1", roles: [] } });
  queryClient.setQueryData(["authorization", "me", "none"], { permissions });
  const invalidate = vi.spyOn(queryClient, "invalidateQueries");
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectExpensesTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { invalidate };
};

describe("the project page's expenses tab", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  it("hands the panel the project the page is on", () => {
    renderTab(["projects", "expenses"]);

    expect(screen.getByText("expenses for project 31")).toBeInTheDocument();
  });

  // A pasted URL must not reach an API that will refuse every read: the tab
  // is not even in the tab strip for this caller, so a panel of red alerts
  // would be a dead end with nothing to act on.
  it("renders the forbidden page for a deep link without expenses:access", () => {
    renderTab(["projects", "expenses"], ["projects:access"]);

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
    expect(screen.queryByText(/expenses for project/)).not.toBeInTheDocument();
  });

  it("renders the not-enabled page in place when the installation did not mount expenses", () => {
    renderTab(["projects"]);

    expect(screen.getByRole("heading", { name: "Expenses is not enabled" })).toBeInTheDocument();
    expect(screen.queryByText(/expenses for project/)).not.toBeInTheDocument();
  });

  // The two tabs read the same expenses through two different modules' APIs.
  // The package refreshes its own caches; only the host may refresh the
  // Economy tab's, and no module package may import another's query keys.
  it("refreshes the project's economy figures when the panel says something changed", async () => {
    const { invalidate } = renderTab(["projects", "expenses"]);

    await userEvent.click(screen.getByRole("button", { name: "pretend something changed" }));

    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["projects", "economy"] });
  });
});
