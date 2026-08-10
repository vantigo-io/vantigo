import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProductsPage } from "./products.index";

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => ({
    ...options,
    useSearch: () => ({ page: 1, search: "", status: "", categoryId: "" }),
    useNavigate: () => vi.fn(),
  }),
  Link: ({ children }: { children: React.ReactNode }) => <a href="/products/categories">{children}</a>,
}));

const stubFetch = () =>
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/v1/categories")) {
        return Promise.resolve(
          new Response(
            JSON.stringify([
              { id: 10, name: "Furniture", parentId: null },
              { id: 11, name: "Desks", parentId: 10 },
            ]),
            { status: 200 },
          ),
        );
      }
      return Promise.resolve(
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
                category: { id: 11, name: "Desks" },
                effectivePrices: [{ currency: "NOK", amount: 12.5 }],
              },
            ],
            pagination: { totalCount: 1, totalPages: 1 },
          }),
          { status: 200 },
        ),
      );
    }),
  );

const renderPage = () =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ProductsPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("ProductsPage", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });
  it("renders product rows and status filters", async () => {
    stubFetch();
    renderPage();
    expect(await screen.findByText("Widget")).toBeInTheDocument();
    expect(screen.getByText("W-1")).toBeInTheDocument();
    expect(screen.getAllByText("Active").length).toBeGreaterThan(0);
    expect(screen.getByText("New product")).toBeInTheDocument();
  });

  it("renders the category column and category filter", async () => {
    stubFetch();
    renderPage();
    expect(await screen.findByText("Widget")).toBeInTheDocument();
    expect(screen.getAllByText("Desks").length).toBeGreaterThan(0);
    expect(screen.getByPlaceholderText("All categories")).toBeInTheDocument();
    expect(screen.getByText("Categories")).toBeInTheDocument();
  });
});
