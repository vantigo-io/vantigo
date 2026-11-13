import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ManufacturingSuggestion } from "../api/replenishment";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi } from "../test/server";

const reason = {
  projected: "6",
  reorderPoint: "20",
  maximum: "40",
  multiple: "5",
  onHand: "30",
  reserved: "25",
  incoming: "2",
  inProduction: "3",
  neededByProduction: "0",
  neededBySales: "4",
};

const suggestion = (patch: Partial<ManufacturingSuggestion> = {}): ManufacturingSuggestion => ({
  id: 11,
  siteId: 1,
  variant: {
    id: 101,
    sku: "BIKE-1",
    productName: "Bicycle",
    unit: "pcs",
    productType: "Goods",
    productStatus: "Active",
  },
  kind: "make",
  quantity: "35",
  needBy: "2026-11-13",
  reason,
  bomId: 7,
  status: "open",
  reorderOpen: false,
  orderId: null,
  orderNumber: null,
  createdAt: "2026-11-13T01:00:00Z",
  ...patch,
});

const list = (suggestions: ManufacturingSuggestion[], salesCounted = true) => ({
  sites: [{ site: { id: 1, code: "MAIN", name: "Main", rootLocationId: 10 }, suggestions }],
  lastRun: null,
  salesCounted,
});

describe("the replenishment suggestions (manufacturing phase 2 M111)", () => {
  it("creates confirmed orders from the selected make rows, with an edited quantity", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      suggestions: list([
        suggestion(),
        suggestion({
          id: 12,
          kind: "buy",
          bomId: null,
          quantity: "5",
          variant: {
            id: 103,
            sku: "WHEEL-1",
            productName: "Wheel",
            unit: "pcs",
            productType: "Goods",
            productStatus: "Active",
          },
        }),
      ]),
      write: (method, path) =>
        method === "POST" && path === "/replenishment/suggestions/orders"
          ? jsonResponse(200, { made: [{ suggestionId: 11, orderId: 40, number: "MO-40" }], failed: [] })
          : undefined,
    });
    renderRoute("/manufacturing/suggestions");
    expect(
      await screen.findAllByText(
        "Projected 6 is at or below the reorder point 20: usable on hand 30, reserved 25, incoming 2, in production 3, needed by production 0, needed by sales orders 4.",
      ),
    ).toHaveLength(2);
    // A buy row is read-only: no selection, no quantity field.
    expect(screen.queryByRole("checkbox", { name: "Select WHEEL-1" })).not.toBeInTheDocument();
    const create = screen.getByRole("button", { name: "Create and confirm" });
    expect(create).toBeDisabled();
    await userEvent.click(screen.getByRole("checkbox", { name: "Select BIKE-1" }));
    const quantity = screen.getByRole("textbox", { name: "Quantity of BIKE-1" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "0,");
    expect(create).toBeDisabled();
    await userEvent.type(quantity, "5");
    await userEvent.click(create);
    await waitFor(() => expect(sent(fetchMock, "POST", /\/replenishment\/suggestions\/orders$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/replenishment\/suggestions\/orders$/)[0].body).toEqual({
      confirm: true,
      items: [{ suggestionId: 11, quantity: "0.5" }],
    });
    expect(await screen.findByText("MO-40")).toBeInTheDocument();
  });

  it("dismisses a row, runs now, and says when sales orders are not counted", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      suggestions: list([suggestion({ reorderOpen: true })], false),
      write: (_method, path) => {
        if (path === "/replenishment/suggestions/11/dismiss")
          return jsonResponse(200, suggestion({ status: "dismissed" }));
        if (path === "/replenishment/runs")
          return jsonResponse(202, {
            id: 5,
            siteId: null,
            kind: "manual",
            status: "pending",
            requestedAt: "2026-11-13T08:00:00Z",
            startedAt: null,
            finishedAt: null,
            suggestions: 0,
            ordersCreated: 0,
            salesCounted: false,
            error: null,
          });
        return undefined;
      },
    });
    renderRoute("/manufacturing/suggestions");
    expect(await screen.findByText("Sales orders not counted")).toBeInTheDocument();
    expect(screen.getByText("An MO from an earlier suggestion is open")).toBeInTheDocument();
    const row = screen.getByText("BIKE-1").closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/suggestions\/11\/dismiss$/)).toHaveLength(1));
    await userEvent.click(screen.getByRole("button", { name: "Run now" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/replenishment\/runs$/)).toHaveLength(1));
  });

  it("offers no actions without orders-manage", async () => {
    stubManufacturingApi({ suggestions: list([suggestion()]) });
    renderRoute("/manufacturing/suggestions");
    expect(await screen.findByText("BIKE-1")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create draft MOs" })).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.getByText("35 pcs")).toBeInTheDocument();
  });
});
