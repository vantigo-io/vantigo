import { MantineProvider } from "@mantine/core";
import { Notifications, notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { ProductDetailsPage } from "./products.$productId";

vi.mock("@tanstack/react-router", () => ({
  Link: () => null,
  useParams: () => ({ productId: 1 }),
}));

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const variant = (id: number, sku: string, optionValues: Record<string, string>) => ({
  id,
  sku,
  barcode: null,
  unit: "pcs",
  standardCost: 10,
  weightKg: null,
  lengthCm: null,
  widthCm: null,
  heightCm: null,
  optionValues,
  effectivePrices: [],
  createdAt: "",
  updatedAt: "",
});

const product = (variants: ReturnType<typeof variant>[]) => ({
  id: 1,
  name: "Widget",
  sku: variants[0]?.sku ?? "W-1",
  type: "Goods" as const,
  status: "Active" as const,
  unit: "pcs",
  standardCost: 10,
  taxCategory: { id: 1, name: "Standard", kind: "Standard", rate: 0.25 },
  description: null,
  category: null,
  barcode: null,
  weightKg: null,
  lengthCm: null,
  widthCm: null,
  heightCm: null,
  effectivePrices: [],
  variants,
  createdAt: "",
  updatedAt: "",
});

const stubProductFetch = (productResponse: unknown, deleteResponse = new Response(null, { status: 204 })) =>
  stubFetch((url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === "/api/v1/products/1" && (init?.method ?? "GET") === "GET") {
      return Promise.resolve(jsonResponse(200, productResponse));
    }
    if (String(url) === "/api/v1/products/1/variants/1" && init?.method === "DELETE") {
      return Promise.resolve(deleteResponse);
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderPage = () =>
  render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
      >
        <ProductDetailsPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("ProductDetailsPage variants", () => {
  afterEach(() => {
    cleanup();
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("renders a single variant inline without a variant table", async () => {
    stubProductFetch(product([variant(1, "W-SINGLE", {})]));
    renderPage();

    expect(await screen.findByRole("heading", { name: "Widget" })).toBeInTheDocument();
    expect(screen.getByText("W-SINGLE")).toBeInTheDocument();
    expect(screen.getByText("Unit:")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Variants" })).not.toBeInTheDocument();
  });

  it("renders the variant table with SKUs and option values for multiple variants", async () => {
    stubProductFetch(
      product([
        variant(1, "W-RED-M", { Color: "Red", Size: "M" }),
        variant(2, "W-BLUE-L", { Color: "Blue", Size: "L" }),
      ]),
    );
    renderPage();

    expect(await screen.findByRole("heading", { name: "Variants" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "W-RED-M" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "W-BLUE-L" })).toBeInTheDocument();
    expect(screen.getByText("Color: Red · Size: M")).toBeInTheDocument();
    expect(screen.getByText("Color: Blue · Size: L")).toBeInTheDocument();
  });

  it("shows the friendly error when deleting the last variant is rejected with 409", async () => {
    stubProductFetch(
      product([variant(1, "W-ONLY", {}), variant(2, "W-SECOND", {})]),
      jsonResponse(409, { detail: "A product must keep at least one variant." }),
    );
    renderPage();

    await screen.findByRole("heading", { name: "Variants" });
    fireEvent.click(screen.getByRole("button", { name: "Delete W-ONLY" }));

    expect(await screen.findByText("A product must keep at least one variant.")).toBeInTheDocument();
  });
});
