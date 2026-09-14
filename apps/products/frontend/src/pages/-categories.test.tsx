import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CategoriesPage } from "./categories";

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => options,
}));

const categories = [
  { id: 1, name: "Furniture", parentId: null, productCount: 0 },
  { id: 2, name: "Desks", parentId: 1, productCount: 3 },
  { id: 3, name: "Services", parentId: null, productCount: 0 },
];

const productsPage = (totalCount: number) => ({
  data: [],
  pagination: { page: 1, pageSize: 1, totalCount, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
});

const stubFetch = (onDelete?: () => Response) =>
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "DELETE" && onDelete) {
        return Promise.resolve(onDelete());
      }
      if (url.includes("/api/v1/products?") || (url.includes("/api/v1/products/") && !url.includes("/categories"))) {
        return Promise.resolve(new Response(JSON.stringify(productsPage(2)), { status: 200 }));
      }
      return Promise.resolve(new Response(JSON.stringify(categories), { status: 200 }));
    }),
  );

const renderPage = () =>
  render(
    <MantineProvider>
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <CategoriesPage />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );

describe("CategoriesPage", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders the category tree with subcategories", async () => {
    stubFetch();
    renderPage();
    expect(await screen.findByText("Furniture")).toBeInTheDocument();
    expect(screen.getByText("Desks")).toBeInTheDocument();
    expect(screen.getByText("Services")).toBeInTheDocument();
    expect(screen.getByText("New category")).toBeInTheDocument();
  });

  it("shows key stats for the catalog", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Furniture");

    expect(screen.getByText("Total categories")).toBeInTheDocument();
    expect(screen.getAllByText("3").length).toBeGreaterThan(0); // total categories
    expect(screen.getByText("Root categories")).toBeInTheDocument();
    expect(screen.getByText("Max depth")).toBeInTheDocument();
    // Services has no products anywhere in its subtree; Furniture inherits Desks'.
    expect(screen.getByText("Empty categories")).toBeInTheDocument();
    expect(screen.getByText("Uncategorised products")).toBeInTheDocument();
    expect((await screen.findAllByText("2")).length).toBeGreaterThan(0); // uncategorised count from products query
  });

  it("marks empty subtrees and shows subtree totals", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Furniture");

    // Only Services is empty: Furniture has 3 products via Desks.
    expect(screen.getAllByText("Empty")).toHaveLength(1);
    expect(screen.getByText(/3 in subtree/)).toBeInTheDocument();
  });

  it("asks for confirmation before deleting", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Furniture");

    fireEvent.click(screen.getByLabelText("Delete Services"));

    expect(await screen.findByText("Delete category")).toBeInTheDocument();
    expect(screen.getByText("Delete")).toBeInTheDocument();
  });

  it("surfaces 409 delete restrictions as an error notification", async () => {
    stubFetch(
      () =>
        new Response(
          JSON.stringify({
            title: "Category has products",
            detail: "Reassign or uncategorise the products before deleting the category.",
          }),
          { status: 409 },
        ),
    );
    renderPage();
    await screen.findByText("Furniture");

    fireEvent.click(screen.getByLabelText("Delete Services"));
    fireEvent.click(await screen.findByText("Delete"));

    expect(await screen.findByText("Failed to delete category")).toBeInTheDocument();
    expect(await screen.findByText(/Reassign or uncategorise the products/i)).toBeInTheDocument();
  });
});
