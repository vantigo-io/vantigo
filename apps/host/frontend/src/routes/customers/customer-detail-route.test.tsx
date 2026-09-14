import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CustomerDetailLayout } from "./-customer-detail-layout";
import "../../i18n";

// Pins the production call site itself, not just the pure filter function
// (see customer-detail-tabs.test.ts): the route component must pass the
// caller's enabled modules through to visibleCustomerDetailTabs. Passing
// `undefined` there (as it once did, permanently, once the tenant-capabilities
// endpoint was deleted) silently drops the Energy and Correspondence tabs
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
  };
});

// The header does its own suspense-query data fetching; irrelevant to tab
// wiring, so it is stubbed out entirely.
vi.mock("@vantigo/customers-ui/pages/customers.$customerId", () => ({
  CustomerDetailHeader: () => null,
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
  it("shows the Energy and Correspondence tabs when the caller holds every permission", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["*"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderCustomerDetailLayout();

    expect(screen.getByText("Overview")).toBeInTheDocument();
    expect(screen.getByText("Energy")).toBeInTheDocument();
    expect(screen.getByText("Correspondence")).toBeInTheDocument();
  });

  it("renders no tab row at all when permissions leave only Overview visible (proving the module gate alone is not enough)", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: [] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderCustomerDetailLayout();

    // A single visible tab hides the whole row (visibleTabs.length > 1), so
    // none of the tab labels render, Overview included.
    expect(screen.queryByText("Overview")).not.toBeInTheDocument();
    expect(screen.queryByText("Energy")).not.toBeInTheDocument();
    expect(screen.queryByText("Correspondence")).not.toBeInTheDocument();
  });
});
