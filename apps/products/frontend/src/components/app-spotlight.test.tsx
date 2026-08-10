import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { routeTree } from "../test/route-tree";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const paginated = (data: unknown[]) => ({
  data,
  pagination: {
    page: 1,
    pageSize: 5,
    totalCount: data.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});

const renderApp = async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: ["/products"] }),
  });
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
  return router;
};

describe("app spotlight", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("searches products and navigates to a result", async () => {
    stubFetch((url: RequestInfo | URL) => {
      if (String(url).startsWith("/api/v1/products/categories")) return Promise.resolve(jsonResponse(200, []));
      if (String(url).startsWith("/api/v1/products?"))
        return Promise.resolve(
          jsonResponse(
            200,
            paginated([{ id: 12, name: "Widget", sku: "W-12", type: "Goods", status: "Active", effectivePrices: [] }]),
          ),
        );
      if (String(url) === "/api/v1/products/12")
        return Promise.resolve(
          jsonResponse(200, {
            id: 12,
            name: "Widget",
            sku: "W-12",
            type: "Goods",
            status: "Active",
            unit: "pcs",
            standardCost: null,
            taxCategory: { id: 1, name: "Standard", kind: "Standard", rate: 0.25 },
            variants: [{ id: 1, sku: "W-12", unit: "pcs", standardCost: null, optionValues: {}, effectivePrices: [] }],
            effectivePrices: [],
            createdAt: "",
            updatedAt: "",
          }),
        );
      if (String(url) === "/api/v1/products/12/variants/1/prices") return Promise.resolve(jsonResponse(200, []));
      return Promise.resolve(jsonResponse(200, paginated([])));
    });
    const router = await renderApp();
    await userEvent.click(screen.getByRole("button", { name: /search/i }));
    await userEvent.type(await screen.findByPlaceholderText(/search products/i), "widget");
    expect(await screen.findByText("W-12 · Goods")).toBeInTheDocument();
    await userEvent.click(screen.getByText("W-12 · Goods").closest("button") ?? screen.getByText("W-12 · Goods"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/products/12"));
  });
});
