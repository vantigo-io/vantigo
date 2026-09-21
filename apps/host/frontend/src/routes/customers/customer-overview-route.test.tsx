import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CustomerOverviewTab } from "./-customer-overview-tab";
import "../../i18n";

// Pins the production call site: the Overview tab must pass canEdit and
// canManageBilling, derived from the caller's customers:update and
// customers:billing-manage permissions, through to the package's
// CustomerOverview (design D6) — not just that some helper function
// computes the right booleans in isolation.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return { ...actual, useParams: () => ({ customerId: 42 }) };
});

vi.mock("@vantigo/customers-ui/pages/customers.$customerId", () => ({
  CustomerOverview: ({
    canEdit,
    canManageBilling,
  }: {
    customerId: number;
    canEdit?: boolean;
    canManageBilling?: boolean;
  }) => (
    <>
      <span data-testid="can-edit">{String(Boolean(canEdit))}</span>
      <span data-testid="can-manage-billing">{String(Boolean(canManageBilling))}</span>
    </>
  ),
}));

const renderTab = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <CustomerOverviewTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the customer overview tab's canEdit capability prop", () => {
  it("passes canEdit from customers:update", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:update"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderTab();

    expect(screen.getByTestId("can-edit")).toHaveTextContent("true");
  });

  it("withholds canEdit without the permission", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: [] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderTab();

    expect(screen.getByTestId("can-edit")).toHaveTextContent("false");
  });
});

describe("the customer overview tab's canManageBilling capability prop", () => {
  it("passes canManageBilling from customers:billing-manage", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:billing-manage"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderTab();

    expect(screen.getByTestId("can-manage-billing")).toHaveTextContent("true");
  });

  it("withholds canManageBilling without the permission", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:update"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

    renderTab();

    expect(screen.getByTestId("can-manage-billing")).toHaveTextContent("false");
  });
});
