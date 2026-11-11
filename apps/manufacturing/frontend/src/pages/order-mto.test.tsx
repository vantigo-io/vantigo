import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";
import type { ManufacturingSalesOrderLine } from "../api/orders";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { orderFixture, stubManufacturingApi, variants } from "../test/server";

const line = (): ManufacturingSalesOrderLine => {
  const [table] = variants();
  return {
    lineId: 501,
    salesOrderId: 77,
    reference: "SO-1042",
    variant: {
      id: table.id,
      sku: table.sku,
      productName: table.productName,
      unit: table.unit,
      productType: "Goods",
      productStatus: "Active",
    },
    quantity: "3",
    dueDate: "2026-12-10",
    siteId: 1,
    bomId: 7,
    orders: [],
  } as ManufacturingSalesOrderLine;
};

describe("a new order made for a sales order line (M45)", () => {
  // The form's note autosizes when fonts load; jsdom has no document.fonts.
  beforeAll(() => {
    if (!("fonts" in document))
      Object.defineProperty(document, "fonts", {
        configurable: true,
        value: { addEventListener: () => {}, removeEventListener: () => {} },
      });
  });

  it("is prefilled from the line and sent with it", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true }, salesOrderLines: { 501: line() } });
    renderRoute("/manufacturing/orders/new?salesOrderLineId=501");

    expect(await screen.findByText("Made for SO-1042")).toBeInTheDocument();
    expect(screen.getByLabelText(/^Quantity/)).toHaveValue("3");
    expect(screen.getByLabelText("Due date")).toHaveValue("2026-12-10");
    await userEvent.click(screen.getByRole("button", { name: "Create manufacturing order" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/manufacturing\/orders$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/manufacturing\/orders$/)[0].body).toMatchObject({
      variantId: line().variant.id,
      quantity: "3",
      dueDate: "2026-12-10",
      salesOrderLineId: 501,
    });
  });

  it("says so when the line is not found", async () => {
    stubManufacturingApi({ meta: { canManageOrders: true } });
    renderRoute("/manufacturing/orders/new?salesOrderLineId=999");

    expect(
      await screen.findByText("The sales order line was not found, or it is not a goods line."),
    ).toBeInTheDocument();
  });
});

describe("an order made for a sales order", () => {
  it("shows its origin, the customer when given, and that the sales order was cancelled", async () => {
    stubManufacturingApi({
      orders: [
        orderFixture({
          origin: {
            kind: "sales_order",
            orderId: 77,
            lineId: 501,
            reference: "SO-1042",
            cancelled: true,
            customerName: "Fjord AS",
          },
        }),
      ],
    });
    renderRoute("/manufacturing/orders/5");

    expect(await screen.findByTestId("origin-cancelled")).toHaveTextContent("Sales order cancelled");
    expect(screen.getByText("The sales order was cancelled")).toBeInTheDocument();
    expect(screen.getByText("Sales order · SO-1042")).toBeInTheDocument();
    expect(screen.getByText("Fjord AS")).toBeInTheDocument();
  });

  it("links its parent and children", async () => {
    stubManufacturingApi({
      orders: [
        orderFixture({
          parent: { id: 3, number: "MO-10", status: "confirmed", quantity: "1", produced: "0", variantId: 1 },
          children: [{ id: 8, number: "MO-14", status: "confirmed", quantity: "2", produced: "0", variantId: 2 }],
        }),
      ],
    });
    renderRoute("/manufacturing/orders/5");

    expect(await screen.findByRole("link", { name: "MO-10" })).toHaveAttribute("href", "/manufacturing/orders/3");
    expect(screen.getByRole("link", { name: "MO-14" })).toHaveAttribute("href", "/manufacturing/orders/8");
  });
});

describe("create sub-assembly orders (M47)", () => {
  const parent = () => {
    const order = orderFixture();
    return orderFixture({
      components: [{ ...order.components[0], missing: "12", covered: false, defaultBomId: 9 }],
    });
  };

  it("lists the made components missing something and sends the chosen ones", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      orders: [parent()],
      write: (method, path) =>
        method === "POST" && path === "/orders/5/children"
          ? jsonResponse(200, {
              order: parent(),
              children: [
                {
                  id: 9,
                  number: "MO-13",
                  status: "confirmed",
                  quantity: "12",
                  produced: "0",
                  variantId: parent().components[0].variant.id,
                },
              ],
            })
          : undefined,
    });
    renderRoute("/manufacturing/orders/5");

    await userEvent.click(await screen.findByRole("button", { name: "Create sub-assembly orders" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("checkbox", { name: /BOARD .*12/ })).toBeChecked();
    await userEvent.click(within(dialog).getByRole("button", { name: "Create sub-assembly orders" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/children$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/5\/children$/)[0].body).toEqual({ revision: 4, componentIds: [60] });
  });

  it("lists the open sub-assembly orders and offers only the rest", async () => {
    const order = parent();
    stubManufacturingApi({
      meta: { canManageOrders: true },
      orders: [
        {
          ...order,
          children: [
            {
              id: 8,
              number: "MO-14",
              status: "in_progress",
              quantity: "10",
              produced: "2",
              variantId: order.components[0].variant.id,
            },
          ],
        },
      ],
    });
    renderRoute("/manufacturing/orders/5");

    await userEvent.click(await screen.findByRole("button", { name: "Create sub-assembly orders" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByTestId("open-children")).toHaveTextContent(/MO-14 · BOARD.*: 8.* still to make/);
    expect(within(dialog).getByRole("checkbox", { name: /BOARD .*: 4.* missing/ })).toBeChecked();
  });

  it("is not offered when no made component misses anything", async () => {
    stubManufacturingApi({ meta: { canManageOrders: true } });
    renderRoute("/manufacturing/orders/5");

    await screen.findByRole("button", { name: "Close" });
    expect(screen.queryByRole("button", { name: "Create sub-assembly orders" })).not.toBeInTheDocument();
  });
});
