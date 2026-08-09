import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProductsPage } from "./products.index";

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => ({
    ...options,
    useSearch: () => ({ page: 1, search: "", status: "" }),
    useNavigate: () => vi.fn(),
  }),
}));

describe("ProductsPage", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });
  it("renders product rows and status filters", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            data: [
              {
                id: 1,
                name: "Widget",
                sku: "W-1",
                type: "Goods",
                status: "Active",
                unit: "pcs",
                effectivePrices: [{ currency: "NOK", amount: 12.5 }],
              },
            ],
            pagination: { totalCount: 1, totalPages: 1 },
          }),
          { status: 200 },
        ),
      ),
    );
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ProductsPage />
        </QueryClientProvider>
      </MantineProvider>,
    );
    expect(await screen.findByText("Widget")).toBeInTheDocument();
    expect(screen.getByText("W-1")).toBeInTheDocument();
    expect(screen.getAllByText("Active").length).toBeGreaterThan(0);
    expect(screen.getByText("New product")).toBeInTheDocument();
  });
});
