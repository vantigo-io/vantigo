import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CustomersListPage } from "./-customers-list";
import "../../i18n";

// Pins the production call site: the list route must pass canEdit, derived from
// the caller's customers:update permission, through to the package's
// CustomersPage — that prop is what puts Manage tags beside the Tag filter
// (owner and tags design D3), and nothing else in the list needs a capability.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/customers-ui/pages/customers.index", () => ({
  CustomersPage: ({ canEdit }: { canEdit?: boolean }) => <span data-testid="can-edit">{String(Boolean(canEdit))}</span>,
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

describe("the customers list route's canEdit capability prop", () => {
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
});
