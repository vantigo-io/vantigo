import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { PricesSearch } from "@vantigo/energy-ui/pages/prices";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PricesPage } from "./-prices-page";
import { Route } from "./prices";

// Pins the production call site: the Prices route passes canImport —
// energy:prices-manage — through to the package's PricesPage, which puts
// Import prices in the header (energy consumption billing design D11), and
// keeps the page's view in its search parameters (spot-price service design S8).
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/energy-ui/pages/prices", () => ({
  PricesPage: ({
    canImport,
    search,
    onSearchChange,
  }: {
    canImport?: boolean;
    search?: PricesSearch;
    onSearchChange?: (search: PricesSearch) => void;
  }) => (
    <>
      <span data-testid="can-import">{String(Boolean(canImport))}</span>
      <span data-testid="search">{JSON.stringify(search)}</span>
      <button type="button" onClick={() => onSearchChange?.({ ...search, period: "month" })}>
        Month
      </button>
    </>
  ),
}));

const validate = Route.options.validateSearch as (search: Record<string, unknown>) => Record<string, unknown>;

/** The host's page in a one-route router whose route validates as the real one does. */
const renderPage = async (url = "/energy/prices") => {
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({
    getParentRoute: () => root,
    path: "/energy/prices",
    validateSearch: validate,
    component: PricesPage,
  });
  const history = createMemoryHistory({ initialEntries: [url] });
  const router = createRouter({ routeTree: root.addChildren([page]), history });
  await router.load();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router as never} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { router, history };
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
  it("passes canImport only with energy:prices-manage", async () => {
    withPermissions(["energy:prices-view", "energy:prices-manage"]);
    await renderPage();
    expect(await screen.findByTestId("can-import")).toHaveTextContent("true");
    cleanup();

    withPermissions(["energy:prices-view"]);
    await renderPage();
    expect(await screen.findByTestId("can-import")).toHaveTextContent("false");
  });

  it("hands the view in the URL to the page, and writes the page's changes back in place", async () => {
    withPermissions(["energy:prices-view"]);
    const { router, history } = await renderPage("/energy/prices?areas=NO1,NO2&period=year&date=2025-06-01");
    expect(JSON.parse((await screen.findByTestId("search")).textContent ?? "")).toEqual({
      areas: ["NO1", "NO2"],
      period: "year",
      date: "2025-06-01",
    });
    const entries = history.length;

    fireEvent.click(screen.getByRole("button", { name: "Month" }));
    await waitFor(() =>
      expect(router.state.location.search).toEqual({ areas: "NO1,NO2", period: "month", date: "2025-06-01" }),
    );
    expect(router.state.location.searchStr).toContain("period=month");
    expect(history.length).toBe(entries);
    expect(JSON.parse(screen.getByTestId("search").textContent ?? "")).toMatchObject({ period: "month" });
  });

  it("drops what is not a view, so the page shows its default", () => {
    expect(validate({})).toEqual({});
    expect(
      validate({
        areas: "no1,NO2,XX,NO1,toolong,SE3,SE4,FI",
        period: "decade",
        date: "2026-02-30",
        from: "2025-13",
        to: "2026-03",
      }),
    ).toEqual({ areas: "NO1,NO2,XX,SE3,SE4", to: "2026-03" });
    expect(validate({ period: "range", from: "2024-01", date: 20250601 })).toEqual({
      period: "range",
      from: "2024-01",
    });
  });
});
