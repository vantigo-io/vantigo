import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CustomersListPage } from "./-customers-list";
import "../../i18n";

// Pins the production call site: the list route must pass canEdit, derived from
// the caller's customers:update permission, through to the package's
// CustomersPage — that prop is what puts Manage tags beside the Tag filter
// (owner and tags design D3) — and canExport and canImport, which put Export
// and Import in the header (customers import/export design D4).
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/customers-ui/pages/customers.index", () => ({
  CustomersPage: ({
    canEdit,
    canExport,
    canImport,
    canMerge,
  }: {
    canEdit?: boolean;
    canExport?: boolean;
    canImport?: boolean;
    canMerge?: boolean;
  }) => (
    <>
      <span data-testid="can-edit">{String(Boolean(canEdit))}</span>
      <span data-testid="can-export">{String(Boolean(canExport))}</span>
      <span data-testid="can-import">{String(Boolean(canImport))}</span>
      <span data-testid="can-merge">{String(Boolean(canMerge))}</span>
    </>
  ),
}));

const renderPage = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <CustomersListPage />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

const withPermissions = (permissions: string[]) =>
  vi
    .mocked(useQuery)
    .mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

describe("the customers list route's capability props", () => {
  it("passes canEdit from customers:update", () => {
    withPermissions(["customers:update"]);

    renderPage();

    expect(screen.getByTestId("can-edit")).toHaveTextContent("true");
  });

  it("withholds canEdit without the permission", () => {
    // Reading the list is not permission to rename or delete a tag every
    // customer in the installation shares.
    withPermissions(["customers:read"]);

    renderPage();

    expect(screen.getByTestId("can-edit")).toHaveTextContent("false");
  });

  it("passes canImport only for customers:import and customers:view together, and canExport from customers:view", () => {
    // The import's own rule (customers import wizard design D4): the key is
    // the door, and view because every answer names customers.
    withPermissions(["customers:view", "customers:create", "customers:update"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("false");
    expect(screen.getByTestId("can-export")).toHaveTextContent("true");
    cleanup();

    withPermissions(["customers:import"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("false");
    cleanup();

    withPermissions(["customers:view", "customers:import"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("true");
  });

  it("passes canMerge from customers:merge, since the list opens the same edit form", () => {
    withPermissions(["customers:update", "customers:delete"]);
    renderPage();
    expect(screen.getByTestId("can-merge")).toHaveTextContent("false");
    cleanup();

    withPermissions(["customers:merge"]);
    renderPage();
    expect(screen.getByTestId("can-merge")).toHaveTextContent("true");
  });
});
