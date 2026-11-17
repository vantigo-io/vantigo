import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ManufacturingSuggestion } from "../api/replenishment";
import { stubManufacturingApi } from "../test/server";
import { SuggestionsPage } from "./suggestions";
import "../i18n";

vi.mock("@vantigo/frontend-shell", async (actual) => ({
  ...(await actual<typeof import("@vantigo/frontend-shell")>()),
  useLiveTopic: () => true,
}));

const suggestion = (id: number, kind: "make" | "buy", sku: string): ManufacturingSuggestion => ({
  id,
  siteId: 1,
  variant: { id: id + 100, sku, productName: sku, unit: "pcs", productType: "Goods", productStatus: "Active" },
  kind,
  quantity: "5",
  needBy: "2026-11-13",
  reason: {
    projected: "1",
    reorderPoint: "5",
    maximum: null,
    multiple: null,
    onHand: "1",
    reserved: "0",
    incoming: "0",
    inProduction: "0",
    neededByProduction: "0",
    neededBySales: "0",
  },
  bomId: kind === "make" ? 7 : null,
  status: "open",
  reorderOpen: false,
  orderId: null,
  orderNumber: null,
  createdAt: "2026-11-13T01:00:00Z",
});

describe("a buy suggestion's action (purchasing design P35)", () => {
  it("draws what the host composes on a buy row, and nothing on a make row", async () => {
    stubManufacturingApi({
      meta: { canManageOrders: false },
      suggestions: {
        sites: [
          {
            site: { id: 1, code: "MAIN", name: "Main", rootLocationId: 10 },
            suggestions: [suggestion(11, "make", "BIKE-1"), suggestion(12, "buy", "WHEEL-1")],
          },
        ],
        lastRun: null,
        salesCounted: true,
      },
    });
    const root = createRootRoute({
      component: () => (
        <SuggestionsPage
          renderBuyAction={(s, siteId) => <button type="button">{`Order ${s.variant.sku} at ${siteId}`}</button>}
        />
      ),
    });
    const router = createRouter({
      routeTree: root.addChildren([createRoute({ getParentRoute: () => root, path: "/" })]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider env="test">
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <RouterProvider router={router} />
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>,
    );
    expect(await screen.findByRole("button", { name: "Order WHEEL-1 at 1" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Order BIKE-1/ })).not.toBeInTheDocument();
  });
});
