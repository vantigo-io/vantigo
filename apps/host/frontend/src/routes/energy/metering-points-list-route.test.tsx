import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MeteringPointsListPage } from "./-metering-points-list";
import { Route } from "./metering-points/index";

// Pins the production call site: the list route passes canImport — both
// energy:import and energy:metering-points-manage — through to the package's
// MeteringPointsPage, which puts Import in the header (energy import design D9).
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/energy-ui/pages/metering-points.index", () => ({
  MeteringPointsPage: ({ canImport, canViewTariffs }: { canImport?: boolean; canViewTariffs?: boolean }) => (
    <>
      <span data-testid="can-import">{String(Boolean(canImport))}</span>
      <span data-testid="can-view-tariffs">{String(Boolean(canViewTariffs))}</span>
    </>
  ),
}));

const renderPage = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <MeteringPointsListPage />
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

describe("the metering-point list route", () => {
  it("passes canImport only with both energy:import and energy:metering-points-manage", () => {
    withPermissions(["energy:import", "energy:metering-points-manage"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("true");
    cleanup();

    withPermissions(["energy:import", "energy:metering-points-view"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("false");
    cleanup();

    withPermissions(["energy:metering-points-manage"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("false");
  });

  it("passes canViewTariffs only with energy:tariffs-view, for the new point's grid tariff", () => {
    withPermissions(["energy:metering-points-manage", "energy:tariffs-view"]);
    renderPage();
    expect(screen.getByTestId("can-view-tariffs")).toHaveTextContent("true");
    cleanup();

    withPermissions(["energy:metering-points-manage"]);
    renderPage();
    expect(screen.getByTestId("can-view-tariffs")).toHaveTextContent("false");
  });

  it("keeps the Without customer filter only when it is true", () => {
    const validate = Route.options.validateSearch as (search: Record<string, unknown>) => Record<string, unknown>;
    expect(validate({ withoutCustomer: "true" })).toEqual({ page: 1, search: "", withoutCustomer: true });
    expect(validate({ withoutCustomer: true, page: "2" })).toEqual({ page: 2, search: "", withoutCustomer: true });
    expect(validate({ withoutCustomer: "false" })).toEqual({ page: 1, search: "" });
  });
});
