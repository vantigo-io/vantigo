import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications, notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { ProductDetailsPage, type ProductDetailsPageProps } from "./products.$productId";

vi.mock("@tanstack/react-router", () => ({
  Link: () => null,
  useParams: () => ({ productId: 1 }),
}));

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// The server omits empty optional fields (weight, dimensions, barcode …)
// instead of sending null, so the fixtures leave them out the same way.
const variant = (id: number, sku: string, optionValues: Record<string, string>) => ({
  id,
  sku,
  unit: "pcs",
  standardCost: 10,
  optionValues,
  effectivePrices: [],
  createdAt: "",
  updatedAt: "",
});

type Dimensions = { weightKg?: number; lengthCm?: number; widthCm?: number; heightCm?: number };

const product = (
  variants: ReturnType<typeof variant>[],
  { type = "Goods" as "Goods" | "Service", ...dimensions }: Dimensions & { type?: "Goods" | "Service" } = {},
) => ({
  id: 1,
  name: "Widget",
  sku: variants[0]?.sku ?? "W-1",
  type,
  status: "Active" as const,
  unit: "pcs",
  standardCost: 10,
  taxCategory: { id: 1, name: "Standard", kind: "Standard", rate: 0.25 },
  category: null,
  effectivePrices: [],
  variants,
  createdAt: "",
  updatedAt: "",
  ...dimensions,
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

const renderPage = (props: ProductDetailsPageProps = {}) =>
  render(
    <MantineProvider>
      <ModalsProvider>
        <Notifications />
        <QueryClientProvider
          client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
        >
          <ProductDetailsPage {...props} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );

const twoVariants = () => product([variant(1, "W-ONLY", {}), variant(2, "W-SECOND", {})]);

// Asks to delete W-ONLY and confirms it in the dialog.
const deleteFirstVariant = async () => {
  await screen.findByRole("heading", { name: "Variants" });
  fireEvent.click(screen.getByRole("button", { name: "Delete W-ONLY" }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
};

const variantHasStock = () =>
  jsonResponse(409, {
    title: "Variant has stock",
    status: 409,
    detail:
      "This variant still has 34 pcs in stock at 3 locations. Move the stock or adjust it to zero before you delete the variant.",
  });

const variantInTransit = () =>
  jsonResponse(409, {
    title: "Variant has stock",
    status: 409,
    detail:
      "This variant still has 12 pcs in transit between sites in 2 transfers. Receive or cancel the transfers before you delete the variant.",
  });

describe("ProductDetailsPage variants", () => {
  afterEach(() => {
    cleanup();
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("renders a single variant inline without a variant table", async () => {
    stubProductFetch(product([variant(1, "W-SINGLE", {})]));
    renderPage();

    expect(await screen.findByRole("heading", { name: /Widget/ })).toBeInTheDocument();
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

  it("asks before deleting a variant and deletes nothing until confirmed", async () => {
    const fetchMock = stubProductFetch(twoVariants());
    renderPage();

    await screen.findByRole("heading", { name: "Variants" });
    fireEvent.click(screen.getByRole("button", { name: "Delete W-ONLY" }));

    expect(await screen.findByText("Delete the variant W-ONLY? Its prices are deleted with it.")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "DELETE")).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(fetchMock.actualCalls.some(([, init]) => init?.method === "DELETE")).toBe(true));
  });

  it("shows the friendly error when deleting the last variant is rejected with 409", async () => {
    stubProductFetch(twoVariants(), jsonResponse(409, { detail: "A product must keep at least one variant." }));
    renderPage();

    await deleteFirstVariant();

    expect(await screen.findByText("A product must keep at least one variant.")).toBeInTheDocument();
  });

  it("tells a Last variant problem by its title", async () => {
    stubProductFetch(
      twoVariants(),
      jsonResponse(409, { title: "Last variant", status: 409, detail: "A product must have at least one variant." }),
    );
    renderPage();

    await deleteFirstVariant();

    expect(await screen.findByText("A product must keep at least one variant.")).toBeInTheDocument();
    expect(screen.queryByText("This variant still has stock")).not.toBeInTheDocument();
  });

  it("says in its own words that a variant with stock cannot be deleted, linking to the stock", async () => {
    stubProductFetch(twoVariants(), variantHasStock());
    renderPage({ stockHref: (variantId) => `/inventory/variants/${variantId}` });

    await deleteFirstVariant();

    expect(await screen.findByText("This variant still has stock")).toBeInTheDocument();
    expect(screen.getByText("Move the stock or adjust it to zero before you delete the variant.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "See where it lies" })).toHaveAttribute("href", "/inventory/variants/1");
    expect(screen.queryByText("A product must keep at least one variant.")).not.toBeInTheDocument();
  });

  it("says a variant on its way between sites cannot be deleted until its transfers are received or cancelled", async () => {
    stubProductFetch(twoVariants(), variantInTransit());
    renderPage({ stockHref: (variantId) => `/inventory/variants/${variantId}` });

    await deleteFirstVariant();

    expect(await screen.findByText("This variant is on its way between sites")).toBeInTheDocument();
    expect(
      screen.getByText("Receive or cancel the transfers that carry it before you delete the variant."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Move the stock or adjust it to zero before you delete the variant."),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "See where it lies" })).toHaveAttribute("href", "/inventory/variants/1");
  });

  it("leaves the link out of the stock refusal when the host gives no stockHref", async () => {
    stubProductFetch(twoVariants(), variantHasStock());
    renderPage();

    await deleteFirstVariant();

    expect(await screen.findByText("This variant still has stock")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "See where it lies" })).not.toBeInTheDocument();
  });
});

describe("ProductDetailsPage extra panels", () => {
  afterEach(() => {
    cleanup();
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("draws the host's panels after the variants and before the prices", async () => {
    stubProductFetch(twoVariants());
    renderPage({ extraPanels: <h3>Stock panel</h3> });

    const panel = await screen.findByRole("heading", { name: "Stock panel" });
    const variants = screen.getByRole("heading", { name: "Variants" });
    const prices = screen.getByRole("heading", { name: "Prices" });
    expect(variants.compareDocumentPosition(panel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(panel.compareDocumentPosition(prices) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

describe("ProductDetailsPage logistics", () => {
  afterEach(() => {
    cleanup();
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("tells the user when goods have no weight or dimensions recorded, never printing undefined", async () => {
    stubProductFetch(product([variant(1, "W-1", {})]));
    renderPage();

    expect(await screen.findByRole("heading", { name: "Logistics" })).toBeInTheDocument();
    expect(screen.getByText("No weight or dimensions recorded.")).toBeInTheDocument();
    expect(screen.queryByText("Weight:")).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain("undefined");
  });

  it("renders the recorded values and a dash for each missing dimension", async () => {
    stubProductFetch(product([variant(1, "W-1", {})], { weightKg: 2.5, lengthCm: 30, heightCm: 10 }));
    renderPage();

    expect(await screen.findByRole("heading", { name: "Logistics" })).toBeInTheDocument();
    expect(screen.getByText("2.5 kg")).toBeInTheDocument();
    expect(screen.getByText("30 cm × — × 10 cm")).toBeInTheDocument();
    expect(screen.queryByText("No weight or dimensions recorded.")).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain("undefined");
  });

  it("hides logistics entirely for a service", async () => {
    stubProductFetch(product([variant(1, "S-1", {})], { type: "Service", weightKg: 2.5 }));
    renderPage();

    expect(await screen.findByRole("heading", { name: /Widget/ })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Logistics" })).not.toBeInTheDocument();
    expect(screen.queryByText("2.5 kg")).not.toBeInTheDocument();
  });
});
