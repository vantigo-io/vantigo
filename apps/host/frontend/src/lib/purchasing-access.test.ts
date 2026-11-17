import { describe, expect, it } from "vitest";
import {
  canOrderFromProposals,
  purchasingAccessOf,
  purchasingHomeFor,
  supplierFormHasBooks,
} from "./purchasing-access";

describe("what the Purchasing pages may offer", () => {
  it("asks purchasing:access beside each Purchasing key", () => {
    expect(purchasingAccessOf(["purchasing:orders-manage"]).canManageOrders).toBe(false);
    const access = purchasingAccessOf(["purchasing:access", "purchasing:orders-manage", "purchasing:orders-send"], {
      id: "u1",
    });
    expect(access).toEqual({
      canManageOrders: true,
      canSendOrders: true,
      canManageSupplierItems: false,
      canManageSettings: false,
      canReceive: false,
      canRead: true,
      canOpenInventoryMovements: false,
      userId: "u1",
    });
    expect(purchasingAccessOf(["purchasing:access", "purchasing:supplier-items-manage"]).canManageSupplierItems).toBe(
      true,
    );
    expect(purchasingAccessOf(["purchasing:access", "purchasing:settings"]).canManageSettings).toBe(true);
    expect(purchasingAccessOf(undefined).canSendOrders).toBe(false);
    // Receiving asks for purchasing:receive alone (P30, A47).
    expect(purchasingAccessOf(["purchasing:receive"])).toMatchObject({ canReceive: true, canRead: false });
    expect(purchasingAccessOf(["purchasing:access", "purchasing:receive"])).toMatchObject({
      canReceive: true,
      canRead: true,
    });
    expect(purchasingAccessOf(["purchasing:access"]).canReceive).toBe(false);
    expect(purchasingAccessOf(["inventory:stock-view"]).canOpenInventoryMovements).toBe(true);
  });

  it("sends a receiver who may not read orders to the orders to receive, everyone else to the orders", () => {
    expect(purchasingHomeFor(purchasingAccessOf(["purchasing:receive"]))).toBe("/purchasing/receive");
    expect(purchasingHomeFor(purchasingAccessOf(["purchasing:access", "purchasing:receive"]))).toBe(
      "/purchasing/orders",
    );
    expect(purchasingHomeFor(purchasingAccessOf(["purchasing:access"]))).toBe("/purchasing/orders");
  });

  it("keeps the books' fields in the supplier form only for a poster of the books", () => {
    expect(supplierFormHasBooks(["accounting:post", "accounting:read", "accounting:suppliers-manage"])).toBe(true);
    expect(supplierFormHasBooks(["accounting:suppliers-manage"])).toBe(false);
    expect(supplierFormHasBooks(["accounting:read", "accounting:suppliers-manage"])).toBe(false);
    expect(supplierFormHasBooks(["accounting:post"])).toBe(false);
    expect(supplierFormHasBooks(undefined)).toBe(false);
  });

  it("offers Order… only with Purchasing on and purchasing:orders-manage beside purchasing:access (P35)", () => {
    const buyer = ["purchasing:access", "purchasing:orders-manage"];
    expect(canOrderFromProposals(buyer, ["inventory", "purchasing"])).toBe(true);
    expect(canOrderFromProposals(buyer, ["inventory"])).toBe(false);
    expect(canOrderFromProposals(["purchasing:access"], ["purchasing"])).toBe(false);
    expect(canOrderFromProposals(["purchasing:orders-manage"], ["purchasing"])).toBe(false);
  });
});
