import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { routeTree } from "../test/route-tree";
import { wireNavigationProgress } from "./navigation-progress";

const { startSpy, completeSpy } = vi.hoisted(() => ({
  startSpy: vi.fn(),
  completeSpy: vi.fn(),
}));

vi.mock("@mantine/nprogress", () => ({
  nprogress: { start: startSpy, complete: completeSpy },
}));

describe("wireNavigationProgress", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    startSpy.mockClear();
    completeSpy.mockClear();
  });

  it("starts the progress bar when a navigation loads and completes it when resolved", async () => {
    stubFetch((url: RequestInfo | URL) => {
      const body = String(url).includes("/products/1001")
        ? {
            id: 1001,
            name: "Acme",
            sku: "A-1",
            type: "Goods",
            status: "Draft",
            unit: "pcs",
            standardCost: null,
            vatRate: 0.25,
            effectivePrices: [],
            createdAt: "",
            updatedAt: "",
          }
        : {
            data: [],
            pagination: {
              page: 1,
              pageSize: 25,
              totalCount: 0,
              totalPages: 0,
              hasNextPage: false,
              hasPreviousPage: false,
            },
          };
      return Promise.resolve(
        new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
      );
    });

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createRouter({
      routeTree,
      context: { queryClient },
      history: createMemoryHistory({ initialEntries: ["/products"] }),
    });

    wireNavigationProgress(router);

    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <ModalsProvider>
            <RouterProvider router={router} />
          </ModalsProvider>
        </QueryClientProvider>
      </MantineProvider>,
    );

    await screen.findByRole("heading", { name: "Products" });
    startSpy.mockClear();
    completeSpy.mockClear();

    await router.navigate({ to: "/products/$productId", params: { productId: 1001 } });

    await vi.waitFor(() => expect(startSpy).toHaveBeenCalled(), { timeout: 5000 });
    await vi.waitFor(() => expect(completeSpy).toHaveBeenCalled(), { timeout: 5000 });
  });
});
