import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProductFormModal } from "./-product-form-modal";

const product = {
  id: 1,
  name: "Widget",
  sku: "W-1",
  type: "Goods" as const,
  status: "Active" as const,
  unit: "pcs",
  standardCost: 1,
  vatRate: 0.25,
  description: null,
  category: null,
  barcode: null,
  weightKg: null,
  lengthCm: null,
  widthCm: null,
  heightCm: null,
  effectivePrices: [],
  createdAt: "",
  updatedAt: "",
};

const renderModal = (state: Parameters<typeof ProductFormModal>[0]["state"]) =>
  render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ProductFormModal state={state} onClose={vi.fn()} />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("ProductFormModal", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("disables SKU for active products", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "edit", product });
    expect(screen.getByLabelText(/sku/i)).toBeDisabled();
    expect(screen.getByLabelText(/sku/i)).toHaveAttribute("disabled");
  });

  it("renders description, category, barcode and logistics fields", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "create" });
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Category" })).toBeInTheDocument();
    expect(screen.getByLabelText(/barcode/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/weight \(kg\)/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/length \(cm\)/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/width \(cm\)/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/height \(cm\)/i)).toBeInTheDocument();
  });

  it("rejects a structurally invalid barcode client-side", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "create" });

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: "Widget" } });
    fireEvent.change(screen.getByLabelText(/sku/i), { target: { value: "W-1" } });
    fireEvent.change(screen.getByLabelText(/barcode/i), { target: { value: "not-a-gtin" } });
    fireEvent.click(screen.getByText("Create product"));

    expect(await screen.findByText(/must be a GTIN/i)).toBeInTheDocument();
  });

  it("maps server-side GTIN field errors onto the barcode input", async () => {
    // Structurally plausible (13 digits) but with a wrong check digit: the client
    // lets it through and the API responds with a field error.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/auth/antiforgery")) {
          return Promise.resolve(new Response(JSON.stringify({ token: "test-token" }), { status: 200 }));
        }
        if (url.includes("/api/v1/categories")) {
          return Promise.resolve(new Response("[]", { status: 200 }));
        }
        return Promise.resolve(
          new Response(
            JSON.stringify({
              title: "Invalid product",
              errors: { barcode: ["'barcode' must be a valid GTIN-8, GTIN-12, GTIN-13 or GTIN-14."] },
            }),
            { status: 400 },
          ),
        );
      }),
    );
    renderModal({ mode: "create" });

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: "Widget" } });
    fireEvent.change(screen.getByLabelText(/sku/i), { target: { value: "W-1" } });
    fireEvent.change(screen.getByLabelText(/barcode/i), { target: { value: "4006381333932" } });
    fireEvent.click(screen.getByText("Create product"));

    expect(await screen.findByText(/'barcode' must be a valid GTIN-8/i)).toBeInTheDocument();
  });
});
