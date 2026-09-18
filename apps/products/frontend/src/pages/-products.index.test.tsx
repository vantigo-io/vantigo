import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProductsPage } from "./products.index";

const router = vi.hoisted(() => ({
  search: { page: 1, search: "", status: "", categoryId: "" } as Record<string, unknown>,
  navigate: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => ({
    ...options,
    useSearch: () => router.search,
    useNavigate: () => router.navigate,
  }),
  useSearch: () => router.search,
  useNavigate: () => router.navigate,
}));

const stubFetch = () =>
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/v1/products/tax-categories")) {
        return Promise.resolve(new Response("[]", { status: 200 }));
      }
      if (url.includes("/api/v1/products/categories")) {
        return Promise.resolve(
          new Response(
            JSON.stringify([
              { id: 10, name: "Furniture", parentId: null, productCount: 0 },
              { id: 11, name: "Desks", parentId: 10, productCount: 1 },
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
                taxCategory: { id: 1, name: "Standard", kind: "Standard", rate: 0.25 },
                variants: [{ id: 1, sku: "W-1", unit: "pcs", standardCost: 1, optionValues: {}, effectivePrices: [] }],
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
    router.search = { page: 1, search: "", status: "", categoryId: "" };
    router.navigate.mockReset();
  });

  it("keeps the create form closed on an ordinary list URL", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Widget");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens the create form when the URL asks for it, and drops the intent when the form closes", async () => {
    stubFetch();
    router.search = { page: 1, search: "", status: "", categoryId: "", create: true };
    renderPage();

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Create new product");

    // Creating a product is a two-step wizard: pick a type before the form
    // (with its Cancel button) appears, so the intent-dropping test has to
    // walk through the picker like a real user would.
    fireEvent.click(screen.getByRole("button", { name: /Goods/ }));

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: { page: 1, search: "", status: "", categoryId: "" }, replace: true }),
    );
  });

  it("renders product rows and status filters", async () => {
    stubFetch();
    renderPage();
    expect(await screen.findByText("Widget")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Products/ })).toBeInTheDocument();
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
  });

  it("offers an Uncategorised option in the category filter", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Widget");

    fireEvent.click(screen.getByPlaceholderText("All categories"));

    expect(await screen.findByText("Uncategorised")).toBeInTheDocument();
  });
});
