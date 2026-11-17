import { describe, expect, it } from "vitest";
import { visiblePurchasingNavItems } from "./navigation";

describe("the Purchasing sidebar", () => {
  it("offers the orders and the suppliers to purchasing:access, and the settings only with purchasing:settings too", () => {
    expect(visiblePurchasingNavItems(["purchasing:access"]).map((item) => item.key)).toEqual(["orders", "suppliers"]);
    expect(visiblePurchasingNavItems(["purchasing:access", "purchasing:settings"]).map((item) => item.key)).toEqual([
      "orders",
      "suppliers",
      "settings",
    ]);
    expect(visiblePurchasingNavItems(["purchasing:settings"])).toEqual([]);
    expect(visiblePurchasingNavItems(undefined)).toEqual([]);
  });
});
