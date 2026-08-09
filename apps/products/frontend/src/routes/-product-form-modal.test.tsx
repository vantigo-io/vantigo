import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
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
  effectivePrices: [],
  createdAt: "",
  updatedAt: "",
};

describe("ProductFormModal", () => {
  it("disables SKU for active products", () => {
    render(
      <MantineProvider>
        <Notifications />
        <QueryClientProvider client={new QueryClient()}>
          <ProductFormModal state={{ mode: "edit", product }} onClose={vi.fn()} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    expect(screen.getByLabelText(/sku/i)).toBeDisabled();
    expect(screen.getByLabelText(/sku/i)).toHaveAttribute("disabled");
  });
});
