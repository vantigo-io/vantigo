import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProjectDetailLayout } from "./-project-detail-layout";
import "../../i18n";

// Pins the production call site itself, not just the pure filter function (see
// project-detail-tabs.test.ts): the layout must read the project it loaded and
// hand its capabilities to the gate. Passing `undefined` there would drop the
// Billing tab for everyone, permanently, with nothing else in the suite
// noticing — the same failure customer-detail-route.test.tsx guards against.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useParams: () => ({ projectId: 31 }),
    useMatches: () => [],
    useNavigate: () => vi.fn(),
    Outlet: () => null,
  };
});

// The header does its own data fetching; irrelevant to tab wiring, so it is
// reduced to the project id it was handed.
vi.mock("@vantigo/projects-ui/pages/projects.$projectId", () => ({
  ProjectDetailHeader: ({ projectId }: { projectId: number }) => <div>project {projectId}</div>,
}));

const renderLayout = (capabilities: { canManage: boolean; canSeeFinancials: boolean }) => {
  vi.mocked(useQuery).mockImplementation((options) => {
    if (options.queryKey[0] === "authorization") return { data: { permissions: ["*"] }, isPending: false } as never;
    if (options.queryKey[0] === "projects") return { data: { id: 31, capabilities } } as never;
    return { data: { user: { id: "user-1", roles: [] } } } as never;
  });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectDetailLayout />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the project detail route's tab row", () => {
  afterEach(cleanup);

  it("shows the Billing tab when the project reports canSeeFinancials", () => {
    renderLayout({ canManage: true, canSeeFinancials: true });

    expect(screen.getByText("project 31")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "People" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Billing" })).toBeInTheDocument();
  });

  it("hides the Billing tab when the project does not, leaving the other two", () => {
    renderLayout({ canManage: true, canSeeFinancials: false });

    expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "People" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Billing" })).not.toBeInTheDocument();
  });
});
