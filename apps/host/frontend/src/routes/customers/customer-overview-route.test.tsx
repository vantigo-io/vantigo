import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CustomerOverviewTab } from "./-customer-overview-tab";
import "../../i18n";

// Pins the production call site: the Overview tab must pass canEdit,
// canManageBilling and the two legal-identity capabilities, derived from the
// caller's customers:update, customers:billing-manage,
// customers:legal-identity-view and customers:legal-identity-manage
// permissions, through to the package's CustomerOverview (design D6; Brreg in
// full design D5) — not just that some helper function computes the right
// booleans in isolation.
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
    canViewIdentity,
    canManageIdentity,
  }: {
    customerId: number;
    canEdit?: boolean;
    canManageBilling?: boolean;
    canViewIdentity?: boolean;
    canManageIdentity?: boolean;
  }) => (
    <>
      <span data-testid="can-edit">{String(Boolean(canEdit))}</span>
      <span data-testid="can-manage-billing">{String(Boolean(canManageBilling))}</span>
      <span data-testid="can-view-identity">{String(Boolean(canViewIdentity))}</span>
      <span data-testid="can-manage-identity">{String(Boolean(canManageIdentity))}</span>
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

describe("the customer overview tab's legal-identity capability props", () => {
  const withPermissions = (permissions: string[]) =>
    vi
      .mocked(useQuery)
      .mockImplementation((options) =>
        options.queryKey[0] === "authorization"
          ? ({ data: { permissions }, isPending: false } as never)
          : ({ data: { user: { id: "user-1", roles: [] } } } as never),
      );

  it("passes canViewIdentity from customers:legal-identity-view, which the Registry card sits behind", () => {
    withPermissions(["customers:legal-identity-view"]);

    renderTab();

    expect(screen.getByTestId("can-view-identity")).toHaveTextContent("true");
    // Seeing the record is not permission to re-read the register or to
    // rewrite the legal name.
    expect(screen.getByTestId("can-manage-identity")).toHaveTextContent("false");
  });

  it("passes canManageIdentity from customers:legal-identity-manage", () => {
    withPermissions(["customers:legal-identity-manage"]);

    renderTab();

    expect(screen.getByTestId("can-manage-identity")).toHaveTextContent("true");
  });

  it("withholds both without either permission", () => {
    withPermissions(["customers:update"]);

    renderTab();

    expect(screen.getByTestId("can-view-identity")).toHaveTextContent("false");
    expect(screen.getByTestId("can-manage-identity")).toHaveTextContent("false");
  });
});
