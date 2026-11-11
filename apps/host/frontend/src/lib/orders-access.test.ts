import { describe, expect, it } from "vitest";
import { ordersAccessOf } from "./orders-access";

const every = ["orders", "customers", "products", "invoices", "projects", "communications", "manufacturing"];

describe("what the Orders pages may offer", () => {
  it("asks orders:access beside each Orders key", () => {
    expect(ordersAccessOf(["orders:quotes-manage"], every).canManageQuotes).toBe(false);
    const access = ordersAccessOf(["orders:access", "orders:quotes-manage", "orders:sales-confirm"], every);
    expect(access.canManageQuotes).toBe(true);
    expect(access.canConfirmOrders).toBe(true);
    expect(access.canManageOrders).toBe(false);
    expect(access.canManageSettings).toBe(false);
    expect(access.canViewAnalysis).toBe(false);
    expect(ordersAccessOf(["orders:analysis-view"], every).canViewAnalysis).toBe(false);
    expect(ordersAccessOf(["orders:access", "orders:analysis-view"], every).canViewAnalysis).toBe(true);
    expect(ordersAccessOf(["orders:subscriptions-manage"], every).canManageSubscriptions).toBe(false);
    expect(ordersAccessOf(["orders:access", "orders:subscriptions-manage"], every).canManageSubscriptions).toBe(true);
    expect(ordersAccessOf(["orders:deliveries-manage"], every).canManageDeliveries).toBe(false);
    expect(ordersAccessOf(["orders:access", "orders:deliveries-manage"], every).canManageDeliveries).toBe(true);
  });

  it("offers a picker only with its module mounted and its own key", () => {
    const keys = ["orders:access", "customers:view", "invoices:access", "projects:access"];
    expect(ordersAccessOf(keys, every)).toMatchObject({
      canViewCustomers: true,
      canViewContacts: false,
      canReadVatCodes: true,
      canViewProjects: true,
    });
    expect(ordersAccessOf(keys, ["orders", "customers"])).toMatchObject({
      canViewCustomers: true,
      canReadVatCodes: false,
      canViewProjects: false,
    });
  });

  it("offers Invoice with both invoice keys and its invoices' links with invoices:access", () => {
    const viewer = ordersAccessOf(["orders:access", "invoices:access"], every);
    expect(viewer).toMatchObject({ canCreateInvoices: false, canViewInvoices: true });
    const invoicer = ordersAccessOf(["orders:access", "invoices:access", "invoices:create"], every);
    expect(invoicer).toMatchObject({ canCreateInvoices: true, canViewInvoices: true });
    expect(ordersAccessOf(["invoices:access", "invoices:create"], ["orders"])).toMatchObject({
      canCreateInvoices: false,
      canViewInvoices: false,
    });
  });

  it("links a line's manufacturing orders with manufacturing:access, and creates one with orders-manage too", () => {
    const viewer = ordersAccessOf(["orders:access", "manufacturing:access"], every);
    expect(viewer).toMatchObject({ canViewManufacturing: true, canCreateManufacturingOrders: false });
    const planner = ordersAccessOf(["orders:access", "manufacturing:access", "manufacturing:orders-manage"], every);
    expect(planner).toMatchObject({ canViewManufacturing: true, canCreateManufacturingOrders: true });
    expect(ordersAccessOf(["manufacturing:access", "manufacturing:orders-manage"], ["orders"])).toMatchObject({
      canViewManufacturing: false,
      canCreateManufacturingOrders: false,
    });
  });

  it("gives an owner's * everything, and names the signed-in user", () => {
    const access = ordersAccessOf(["*"], every, { id: "u-1", displayName: "Kari" });
    expect(Object.values(access).filter((value) => value === false)).toEqual([]);
    expect(access.userId).toBe("u-1");
    expect(access.userDisplayName).toBe("Kari");
  });
});
