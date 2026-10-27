import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TariffsRoutePage } from "./-tariffs-page";

// Pins the production call site: the Tariffs route passes canManage —
// energy:tariffs-manage — through to the package's TariffsPage, which puts
// New tariff, Edit, Archive and Delete on the page (energy consumption
// billing design D11). The page itself is behind energy:tariffs-view in the
// navigation.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/energy-ui/pages/tariffs", () => ({
  TariffsPage: ({ canManage }: { canManage?: boolean }) => (
    <span data-testid="can-manage">{String(Boolean(canManage))}</span>
  ),
}));

const renderPage = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <TariffsRoutePage />
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

afterEach(cleanup);

describe("the Tariffs route", () => {
  it("passes canManage only with energy:tariffs-manage", () => {
    withPermissions(["energy:tariffs-view", "energy:tariffs-manage"]);
    renderPage();
    expect(screen.getByTestId("can-manage")).toHaveTextContent("true");
    cleanup();

    withPermissions(["energy:tariffs-view"]);
    renderPage();
    expect(screen.getByTestId("can-manage")).toHaveTextContent("false");
  });
});
