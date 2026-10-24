import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PricesPage } from "./-prices-page";

// Pins the production call site: the Prices route passes canImport —
// energy:prices-manage — through to the package's PricesPage, which puts
// Import prices in the header (energy consumption billing design D11).
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/energy-ui/pages/prices", () => ({
  PricesPage: ({ canImport }: { canImport?: boolean }) => (
    <span data-testid="can-import">{String(Boolean(canImport))}</span>
  ),
}));

const renderPage = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <PricesPage />
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

describe("the Prices route", () => {
  it("passes canImport only with energy:prices-manage", () => {
    withPermissions(["energy:prices-view", "energy:prices-manage"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("true");
    cleanup();

    withPermissions(["energy:prices-view"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("false");
  });
});
