import { MantineProvider } from "@mantine/core";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { InventoryLowStockRow } from "../api/placements";
import { LowStockList } from "./low-stock-list";
import "../i18n";

const row = (id: number, sku: string): InventoryLowStockRow => ({
  site: { id: 2, code: "BGO", name: "Bergen", kind: "warehouse", status: "active" },
  variant: { id, sku, productName: sku, unit: "pcs" },
  onHand: 0,
  reserved: 0,
  available: 0,
  incoming: 0,
  inTransit: 0,
  onOrder: 0,
  reorderPoint: 5,
  maximum: null,
  needed: 6,
  suggested: 6,
  packaging: null,
  elsewhere: [],
  openDrafts: [],
});

const renderList = (renderRowAction?: (r: InventoryLowStockRow) => React.ReactNode) => {
  const root = createRootRoute({
    component: () => (
      <LowStockList rows={[row(101, "BOLT"), row(102, "NUT")]} showSite renderRowAction={renderRowAction} />
    ),
  });
  const router = createRouter({
    routeTree: root.addChildren([createRoute({ getParentRoute: () => root, path: "/" })]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  render(
    <MantineProvider env="test">
      <RouterProvider router={router} />
    </MantineProvider>,
  );
};

describe("the low-stock list's row action (purchasing design P35)", () => {
  it("draws what the host composes at each row's end", async () => {
    renderList((r) => <button type="button">{`Order ${r.variant.sku} at ${r.site.code}`}</button>);
    const rows = await screen.findAllByRole("row");
    expect(within(rows[1]).getByRole("button", { name: "Order BOLT at BGO" })).toBeInTheDocument();
    expect(within(rows[2]).getByRole("button", { name: "Order NUT at BGO" })).toBeInTheDocument();
  });

  it("draws no column without it", async () => {
    renderList();
    const header = (await screen.findAllByRole("row"))[0];
    expect(within(header).getAllByRole("columnheader")).toHaveLength(9);
  });
});
