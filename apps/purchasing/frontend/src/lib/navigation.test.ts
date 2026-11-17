import { describe, expect, it } from "vitest";
import { visiblePurchasingNavItems } from "./navigation";

describe("the Purchasing sidebar", () => {
  it("offers the orders, the proposals and the suppliers to purchasing:access, received not invoiced with purchasing:invoices-match, and the settings only with purchasing:settings too", () => {
    expect(visiblePurchasingNavItems(["purchasing:access"]).map((item) => item.key)).toEqual([
      "orders",
      "proposals",
      "suppliers",
    ]);
    expect(visiblePurchasingNavItems(["purchasing:access", "purchasing:settings"]).map((item) => item.key)).toEqual([
      "orders",
      "proposals",
      "suppliers",
      "settings",
    ]);
    expect(
      visiblePurchasingNavItems(["purchasing:access", "purchasing:invoices-match"]).map((item) => item.key),
    ).toEqual(["orders", "proposals", "suppliers", "uninvoiced"]);
    expect(visiblePurchasingNavItems(["purchasing:invoices-match"])).toEqual([]);
    expect(visiblePurchasingNavItems(["purchasing:settings"])).toEqual([]);
    // Receiving is purchasing:receive's alone (P30, A47).
    expect(visiblePurchasingNavItems(["purchasing:receive"]).map((item) => item.key)).toEqual(["receive"]);
    expect(visiblePurchasingNavItems(["purchasing:access", "purchasing:receive"]).map((item) => item.key)).toEqual([
      "orders",
      "proposals",
      "receive",
      "suppliers",
    ]);
    expect(visiblePurchasingNavItems(undefined)).toEqual([]);
  });
});
