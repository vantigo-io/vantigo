import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CategoriesPage } from "./products.categories";

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => options,
  Link: ({ children }: { children: React.ReactNode }) => <a href="/products">{children}</a>,
}));

const categories = [
  { id: 1, name: "Furniture", parentId: null },
  { id: 2, name: "Desks", parentId: 1 },
  { id: 3, name: "Services", parentId: null },
];

const stubFetch = (onDelete?: () => Response) =>
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/auth/antiforgery")) {
        return Promise.resolve(new Response(JSON.stringify({ token: "test-token" }), { status: 200 }));
      }
      if (init?.method === "DELETE" && onDelete) {
        return Promise.resolve(onDelete());
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
