import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
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
  taxCategory: { id: 1, name: "Standard", kind: "Standard", rate: 0.25 },
  description: null,
  category: null,
  barcode: null,
  weightKg: null,
  lengthCm: null,
  widthCm: null,
  heightCm: null,
  effectivePrices: [],
  variants: [
    {
      id: 1,
      sku: "W-1",
      barcode: null,
      unit: "pcs",
      standardCost: 1,
      weightKg: null,
      lengthCm: null,
      widthCm: null,
      heightCm: null,
      optionValues: {},
      effectivePrices: [],
      createdAt: "",
      updatedAt: "",
    },
  ],
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

  it("renders the new DTO tax category picker", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "edit", product });
    expect(screen.getByRole("combobox", { name: "Tax category" })).toBeInTheDocument();
  });

  it("renders the product fields", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "create" });
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Category" })).toBeInTheDocument();
    expect(screen.getByText(/default variant/i)).toBeInTheDocument();
  });
});
