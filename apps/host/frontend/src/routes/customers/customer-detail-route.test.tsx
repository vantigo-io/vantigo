import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { CustomerDetailLayout } from "./-customer-detail-layout";
import "../../i18n";

// Pins the production call site itself, not just the pure filter functions
// (see customer-detail-tabs.test.ts): the route component must pass the
// caller's enabled modules through to the visibility helpers. Passing
// `undefined` there (as it once did, permanently, once the tenant-capabilities
// endpoint was deleted) silently drops the Energy tab and the inbox action
// from the UI forever, with nothing else in the suite noticing.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useParams: () => ({ customerId: 42 }),
    useMatches: () => [],
    useNavigate: () => vi.fn(),
    Outlet: () => null,
    // No router in this test: the inbox action's Link renders as a plain anchor.
    Link: ({ to, children }: { to: string; children?: ReactNode }) => <a href={to}>{children}</a>,
  };
});

// The header does its own suspense-query data fetching; irrelevant to tab
// wiring, so it is reduced to its actions slot plus the four capability props
// this route computes from permissions (see the archive/restore assertions
// below — the package's own tests cover what the header does with them).
vi.mock("@vantigo/customers-ui/pages/customers.$customerId", () => ({
  CustomerDetailHeader: ({
    actions,
    canArchive,
    canRestore,
    canMerge,
    canManagePersonalData,
  }: {
    actions?: ReactNode;
    canArchive?: boolean;
    canRestore?: boolean;
    canMerge?: boolean;
    canManagePersonalData?: boolean;
  }) => (
    <div>
      {actions}
      <span data-testid="can-archive">{String(Boolean(canArchive))}</span>
      <span data-testid="can-restore">{String(Boolean(canRestore))}</span>
      <span data-testid="can-merge">{String(Boolean(canMerge))}</span>
      <span data-testid="can-manage-personal-data">{String(Boolean(canManagePersonalData))}</span>
    </div>
  ),
}));

const renderCustomerDetailLayout = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <CustomerDetailLayout />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the customer detail route's tab row", () => {
  it("shows the Energy tab and the inbox action when the caller holds every permission", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["*"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderCustomerDetailLayout();

    expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Energy" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Correspondence" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open in inbox" })).toHaveAttribute("href", "/communications/inbox");
  });

  it("renders no tab row and no inbox action when permissions leave only Overview visible (proving the module gate alone is not enough)", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: [] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderCustomerDetailLayout();

    // A single visible tab hides the whole row (visibleTabs.length > 1), so
    // none of the tab labels render, Overview included.
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(screen.queryByText("Overview")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Open in inbox" })).not.toBeInTheDocument();
  });
});

describe("the customer detail route's archive/restore capability props", () => {
  it("passes canArchive from customers:delete and canRestore from customers:update", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:delete", "customers:update"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderCustomerDetailLayout();

    expect(screen.getByTestId("can-archive")).toHaveTextContent("true");
    expect(screen.getByTestId("can-restore")).toHaveTextContent("true");
  });

  it("withholds each capability without its own permission", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:update"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderCustomerDetailLayout();

    expect(screen.getByTestId("can-archive")).toHaveTextContent("false");
    expect(screen.getByTestId("can-restore")).toHaveTextContent("true");
  });

  it("passes canMerge from customers:merge, and only from it", () => {
    // customers:delete archives, customers:update restores; neither merges
    // (customers merge design D2).
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:delete", "customers:update"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderCustomerDetailLayout();
    expect(screen.getByTestId("can-merge")).toHaveTextContent("false");
    cleanup();

    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:merge"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderCustomerDetailLayout();
    expect(screen.getByTestId("can-merge")).toHaveTextContent("true");
  });

  it("passes canManagePersonalData from customers:personal-data, and only from it", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({
            data: { permissions: ["customers:delete", "customers:update", "customers:merge"] },
            isPending: false,
          } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderCustomerDetailLayout();
    expect(screen.getByTestId("can-manage-personal-data")).toHaveTextContent("false");
    cleanup();

    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:personal-data"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderCustomerDetailLayout();
    expect(screen.getByTestId("can-manage-personal-data")).toHaveTextContent("true");
  });
});
