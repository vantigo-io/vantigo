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
import { Route as LowStockRoute } from "./inventory/low-stock";
import { Route as SuggestionsRoute } from "./manufacturing/suggestions";
import "../i18n";

// Inventory's and Manufacturing's pages are their packages' own, tested
// there; here each only draws what the host hands its slot, for one row.
vi.mock("@vantigo/inventory-ui/pages/low-stock", () => ({
  LowStockPage: ({ renderRowAction }: { renderRowAction?: (row: unknown) => ReactNode }) => (
    <section aria-label="Low stock">
      {renderRowAction ? renderRowAction({ variant: { id: 101 }, site: { id: 3 } }) : <p>No row action</p>}
    </section>
  ),
}));
vi.mock("@vantigo/manufacturing-ui/pages/suggestions", () => ({
  SuggestionsPage: ({ renderBuyAction }: { renderBuyAction?: (s: unknown, siteId: number) => ReactNode }) => (
    <section aria-label="Suggestions">
      {renderBuyAction ? renderBuyAction({ variant: { id: 202 } }, 4) : <p>No buy action</p>}
    </section>
  ),
}));

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

/** A route of the host at its path, with the modules named enabled and the permissions named held. */
const renderRoute = async (path: string, component: RouteComponent, modules: string[], permissions: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const fetchMock = stubFetch(
    (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/identity/session") return Promise.resolve(json(TEST_SESSION));
      if (url.startsWith("/api/v1/identity/access/me"))
        return Promise.resolve(json({ permissions, canManageAuthorization: false, administrationScope: "none" }));
      return Promise.resolve(new Response(null, { status: 404 }));
    },
    { session: "delegate" },
  );
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({ getParentRoute: () => root, path, component });
  const proposals = createRoute({ getParentRoute: () => root, path: "/purchasing/proposals", component: () => null });
  const router = createRouter({
    routeTree: root.addChildren([page, proposals]),
    history: createMemoryHistory({ initialEntries: [path] }),
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
  await waitFor(() =>
    expect(fetchMock.actualCalls.some(([url]) => String(url).startsWith("/api/v1/identity/access/me"))).toBe(true),
  );
  await waitFor(() => expect(queryClient.isFetching()).toBe(0));
};

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
  vi.unstubAllGlobals();
});

const lowStock = LowStockRoute.options.component as RouteComponent;
const suggestions = SuggestionsRoute.options.component as RouteComponent;
const buyer = ["purchasing:access", "purchasing:orders-manage"];

describe("Order… on Inventory's low stock and Manufacturing's buy suggestions", () => {
  it("links a low-stock row to its proposal with Purchasing on and purchasing:orders-manage", async () => {
    await renderRoute("/inventory/low-stock", lowStock, ["inventory", "purchasing"], buyer);
    expect(await screen.findByRole("link", { name: "Order…" })).toHaveAttribute(
      "href",
      "/purchasing/proposals?variantId=101&siteId=3",
    );
  });

  it("links a buy suggestion to its proposal", async () => {
    await renderRoute("/manufacturing/suggestions", suggestions, ["manufacturing", "purchasing"], buyer);
    expect(await screen.findByRole("link", { name: "Order…" })).toHaveAttribute(
      "href",
      "/purchasing/proposals?variantId=202&siteId=4",
    );
  });

  it("is not offered without purchasing:orders-manage, nor with Purchasing off", async () => {
    await renderRoute("/inventory/low-stock", lowStock, ["inventory", "purchasing"], ["purchasing:access"]);
    expect(screen.getByText("No row action")).toBeInTheDocument();
    cleanup();
    await renderRoute("/manufacturing/suggestions", suggestions, ["manufacturing"], buyer);
    expect(screen.getByText("No buy action")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Order…" })).not.toBeInTheDocument();
  });
});
