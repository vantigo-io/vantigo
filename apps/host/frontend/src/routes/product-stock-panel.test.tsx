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
import { stubFetch, TEST_SESSION } from "../test/fetch";
import { Route as ProductRoute } from "./products/$productId";
import "../i18n";

// Products' page and Inventory's Stock card are their packages' own, tested
// there; here they only show what the host route hands the page.
vi.mock("@vantigo/products-ui", () => ({
  ProductDetailsPage: ({
    extraPanels,
    stockHref,
  }: {
    extraPanels?: ReactNode;
    stockHref?: (variantId: number) => string;
  }) => (
    <section aria-label="Product">
      {extraPanels}
      {stockHref && <p>Stock link: {stockHref(7)}</p>}
    </section>
  ),
}));
vi.mock("@vantigo/inventory-ui", () => ({
  ProductStock: ({ productId }: { productId: number }) => <p>Stock card for product {productId}</p>,
}));

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

/** Product 1's page with the modules named enabled and the permissions named held. */
const renderProductPage = async (modules: string[], permissions: string[], basePath = "/") => {
  window.__VANTIGO_APP__ = { basePath, title: "Vantigo", support: {}, modules };
  // Under a base path every request carries it, the session's too, so this
  // stub answers the session itself rather than leaving it to stubFetch.
  const prefix = basePath.replace(/\/$/, "");
  const fetchMock = stubFetch(
    (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === `${prefix}/api/v1/identity/session`) return Promise.resolve(json(TEST_SESSION));
      if (url.startsWith(`${prefix}/api/v1/identity/access/me`))
        return Promise.resolve(json({ permissions, canManageAuthorization: false, administrationScope: "none" }));
      return Promise.resolve(new Response(null, { status: 404 }));
    },
    { session: "delegate" },
  );
  // The page reads its params through `Route.useParams()`, so the test route
  // has the same path and parsing, and with them the same id.
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({
    getParentRoute: () => root,
    path: "/products/$productId",
    params: ProductRoute.options.params as never,
    component: ProductRoute.options.component as RouteComponent,
  });
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ["/products/1"] }),
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

/** Waits until the route's reads have settled, so an absent panel is absent for good. */
const settled = async (queryClient: QueryClient) => {
  await screen.findByRole("region", { name: "Product" });
  await waitFor(() => expect(queryClient.isFetching()).toBe(0));
};

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
  vi.unstubAllGlobals();
});

describe("the Stock card on Products' product page", () => {
  it("is drawn, with the variant stock link, for inventory:stock-view and inventory:locations-view", async () => {
    await renderProductPage(["products", "inventory"], ["inventory:locations-view", "inventory:stock-view"]);
    expect(await screen.findByText("Stock card for product 1")).toBeInTheDocument();
    expect(screen.getByText("Stock link: /inventory/variants/7")).toBeInTheDocument();
  });

  // Products draws the link as a plain anchor in a notification, outside the
  // router, so the href carries the base path itself.
  it("gives the variant stock link with the base path the app is served under", async () => {
    await renderProductPage(
      ["products", "inventory"],
      ["inventory:locations-view", "inventory:stock-view"],
      "/vantigo/",
    );
    expect(await screen.findByText("Stock link: /vantigo/inventory/variants/7")).toBeInTheDocument();
  });

  it("is not drawn without inventory:stock-view", async () => {
    const { fetchMock, queryClient } = await renderProductPage(["products", "inventory"], ["inventory:locations-view"]);
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => String(url).startsWith("/api/v1/identity/access/me"))).toBe(true),
    );
    await settled(queryClient);
    expect(screen.queryByText(/Stock card/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Stock link/)).not.toBeInTheDocument();
  });

  it("is not drawn when the inventory module is off, and nothing is asked for it", async () => {
    const { fetchMock, queryClient } = await renderProductPage(["products"], ["*"]);
    await settled(queryClient);
    expect(screen.queryByText(/Stock card/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Stock link/)).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url).startsWith("/api/v1/identity/access/me"))).toBe(false);
  });
});
