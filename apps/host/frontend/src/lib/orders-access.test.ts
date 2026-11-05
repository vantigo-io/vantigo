import { describe, expect, it } from "vitest";
import { ordersAccessOf } from "./orders-access";

const every = ["orders", "customers", "products", "invoices", "projects", "communications"];

describe("what the Orders pages may offer", () => {
  it("asks orders:access beside each Orders key", () => {
    expect(ordersAccessOf(["orders:quotes-manage"], every).canManageQuotes).toBe(false);
    const access = ordersAccessOf(["orders:access", "orders:quotes-manage", "orders:sales-confirm"], every);
    expect(access.canManageQuotes).toBe(true);
    expect(access.canConfirmOrders).toBe(true);
    expect(access.canManageOrders).toBe(false);
    expect(access.canManageSettings).toBe(false);
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

  it("gives an owner's * everything, and names the signed-in user", () => {
    const access = ordersAccessOf(["*"], every, { id: "u-1", displayName: "Kari" });
    expect(Object.values(access).filter((value) => value === false)).toEqual([]);
    expect(access.userId).toBe("u-1");
    expect(access.userDisplayName).toBe("Kari");
  });
});
