import { describe, expect, it } from "vitest";
import { purchasingAccessOf, supplierFormHasBooks } from "./purchasing-access";

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
      userId: "u1",
    });
    expect(purchasingAccessOf(["purchasing:access", "purchasing:supplier-items-manage"]).canManageSupplierItems).toBe(
      true,
    );
    expect(purchasingAccessOf(["purchasing:access", "purchasing:settings"]).canManageSettings).toBe(true);
    expect(purchasingAccessOf(undefined).canSendOrders).toBe(false);
  });

  it("keeps the books' fields in the supplier form only for a poster of the books", () => {
    expect(supplierFormHasBooks(["accounting:post", "accounting:read", "accounting:suppliers-manage"])).toBe(true);
    expect(supplierFormHasBooks(["accounting:suppliers-manage"])).toBe(false);
    expect(supplierFormHasBooks(["accounting:read", "accounting:suppliers-manage"])).toBe(false);
    expect(supplierFormHasBooks(["accounting:post"])).toBe(false);
    expect(supplierFormHasBooks(undefined)).toBe(false);
  });
});
