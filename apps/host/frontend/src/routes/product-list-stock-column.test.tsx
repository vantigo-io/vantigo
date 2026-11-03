import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  type RouteComponent,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { Route as ProductsRoute } from "./products/index";
import "../i18n";

/** Page 1 of the list: a Goods product with two variants, one with one, and a Service product. */
const products = [
  { id: 1, name: "T-shirt", type: "Goods", variants: [{ id: 101 }, { id: 102 }] },
  { id: 2, name: "Bolt", type: "Goods", variants: [{ id: 201 }] },
  { id: 3, name: "Installation", type: "Service", variants: [{ id: 301 }] },
];

// Products' list and Inventory's stock read are their packages' own, tested
// there; here the list only draws the columns the host route hands it, for
// the page's rows, and the read answers fixed totals.
vi.mock("@vantigo/products-ui", () => ({
  ProductsPage: ({
    extraColumns: columnsFor,
  }: {
    extraColumns?: (
      rows: readonly never[],
    ) => readonly { key: string; header: ReactNode; cell: (product: never) => ReactNode }[];
  }) => {
    // Products hands the page's rows to the function, as its list does.
    const extraColumns = columnsFor?.(products as never[]);
    return (
      <table aria-label="Products">
        <thead>
          <tr>
            <th>Name</th>
            {extraColumns?.map((column) => (
              <th key={column.key}>{column.header}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {products.map((product) => (
            <tr key={product.id}>
              <td>{product.name}</td>
              {extraColumns?.map((column) => (
                <td key={column.key} data-testid={`${column.key}-${product.id}`}>
                  {column.cell(product as never)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    );
  },
}));
const useStockTotals = vi.hoisted(() =>
  vi.fn((variantIds: readonly number[]) => ({
    data: new Map(variantIds.map((id) => [id, ({ 101: 3, 102: 4.5, 201: 0, 301: 9 }[id] ?? 0) as number])),
    isPending: false,
    error: null,
  })),
);
vi.mock("@vantigo/inventory-ui/lib/stock-totals", () => ({ useStockTotals }));

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

/** The product list with the modules named enabled and the permissions named held. */
const renderProductList = async (modules: string[], permissions: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const fetchMock = stubFetch((input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith("/api/v1/identity/access/me"))
      return Promise.resolve(json({ permissions, canManageAuthorization: false, administrationScope: "none" }));
    if (url.startsWith("/api/v1/products"))
      return Promise.resolve(
        json({
          data: products,
          pagination: {
            page: 1,
            pageSize: 25,
            totalCount: 3,
            totalPages: 1,
            hasNextPage: false,
            hasPreviousPage: false,
          },
        }),
      );
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  // The page reads its search through `Route.useSearch()`, so the test route
  // has the same path and validation.
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({
    getParentRoute: () => root,
    path: "/products/",
    validateSearch: ProductsRoute.options.validateSearch as (
      search: Record<string, unknown>,
    ) => Record<string, unknown>,
    component: ProductsRoute.options.component as RouteComponent,
  });
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ["/products/"] }),
  });
  await router.load();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router as never} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { fetchMock, queryClient };
};

/** Waits until the route's reads have settled, so an absent column is absent for good. */
const settled = async (queryClient: QueryClient) => {
  await screen.findByRole("table", { name: "Products" });
  await waitFor(() => expect(queryClient.isFetching()).toBe(0));
};

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("the On hand column on Products' product list", () => {
  it("is drawn for inventory:stock-view and inventory:locations-view, with each product's total", async () => {
    await renderProductList(["products", "inventory"], ["inventory:locations-view", "inventory:stock-view"]);
    expect(await screen.findByRole("columnheader", { name: "On hand" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("inventory-on-hand-1")).toHaveTextContent("7.5"));
    expect(screen.getByTestId("inventory-on-hand-2")).toHaveTextContent("0");
    // A Service product holds no stock: its cell is blank, and its variants are not asked for.
    expect(screen.getByTestId("inventory-on-hand-3")).toBeEmptyDOMElement();
    expect(useStockTotals).toHaveBeenLastCalledWith([101, 102, 201]);
  });

  it("is not drawn without inventory:stock-view", async () => {
    const { fetchMock, queryClient } = await renderProductList(["products", "inventory"], ["inventory:locations-view"]);
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => String(url).startsWith("/api/v1/identity/access/me"))).toBe(true),
    );
    await settled(queryClient);
    expect(screen.queryByRole("columnheader", { name: "On hand" })).not.toBeInTheDocument();
    expect(useStockTotals).not.toHaveBeenCalled();
  });

  it("is not drawn when the inventory module is off, and nothing is asked for it", async () => {
    const { fetchMock, queryClient } = await renderProductList(["products"], ["*"]);
    await settled(queryClient);
    expect(screen.queryByRole("columnheader", { name: "On hand" })).not.toBeInTheDocument();
    expect(useStockTotals).not.toHaveBeenCalled();
    expect(fetchMock.actualCalls.some(([url]) => String(url).startsWith("/api/v1/identity/access/me"))).toBe(false);
    expect(fetchMock.actualCalls.some(([url]) => String(url).startsWith("/api/v1/inventory"))).toBe(false);
  });
});
