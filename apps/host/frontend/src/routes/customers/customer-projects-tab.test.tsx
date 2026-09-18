import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CustomerProjectsTab } from "./-customer-projects-tab";
import "../../i18n";

// The tab is the host's, not the package's: it decides whether the module is
// mounted at all and whether this caller may create a project. Both answers
// are invisible to the package's own tests, so they are pinned here.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useParams: () => ({ customerId: 42 }),
    // No router in this test: the not-enabled page's "Go home" renders as a plain anchor.
    Link: ({ children }: { children: React.ReactNode }) => <a href="/">{children}</a>,
    useRouter: () => ({ history: { back: vi.fn() } }),
  };
});

vi.mock("@vantigo/projects-ui", () => ({
  CustomerProjectsPanel: ({ customerId, canCreate }: { customerId: number; canCreate?: boolean }) => (
    <div>
      customer {customerId} canCreate {String(canCreate)}
    </div>
  ),
}));

const renderTab = (permissions: string[], modules?: string[]) => {
  if (modules) window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  vi.mocked(useQuery).mockImplementation((options) =>
    options.queryKey[0] === "authorization"
      ? ({ data: { permissions }, isPending: false } as never)
      : ({ data: { user: { id: "user-1", roles: [] } } } as never),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <CustomerProjectsTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the customer page's projects tab", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  it("lets the caller create a project from the panel only with projects:create", () => {
    renderTab(["projects:access", "projects:create"]);
    expect(screen.getByText("customer 42 canCreate true")).toBeInTheDocument();

    cleanup();
    renderTab(["projects:access"]);
    expect(screen.getByText("customer 42 canCreate false")).toBeInTheDocument();
  });

  it("renders the not-enabled page in place when the installation did not mount projects", () => {
    renderTab(["*"], ["customers", "energy"]);

    expect(screen.getByRole("heading", { name: "Projects is not enabled" })).toBeInTheDocument();
    expect(screen.queryByText(/canCreate/)).not.toBeInTheDocument();
  });
});
