import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
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
// wiring, so it is reduced to its actions slot.
vi.mock("@vantigo/customers-ui/pages/customers.$customerId", () => ({
  CustomerDetailHeader: ({ actions }: { actions?: ReactNode }) => <div>{actions}</div>,
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
