import { describe, expect, it } from "vitest";
import { visibleOrdersNavItems } from "./navigation";

describe("the Orders sidebar", () => {
  it("offers quotes, orders, deliveries and subscriptions to orders:access, the analysis with orders:analysis-view, and templates and settings only with orders:settings too", () => {
    expect(visibleOrdersNavItems(["orders:access"]).map((item) => item.key)).toEqual([
      "quotes",
      "orders",
      "deliveries",
      "subscriptions",
    ]);
    expect(visibleOrdersNavItems(["orders:access", "orders:subscriptions-manage"]).map((item) => item.key)).toEqual([
      "quotes",
      "orders",
      "deliveries",
      "subscriptions",
    ]);
    expect(visibleOrdersNavItems(["orders:subscriptions-manage"])).toEqual([]);
    expect(visibleOrdersNavItems(["orders:access", "orders:settings"]).map((item) => item.key)).toEqual([
      "quotes",
      "orders",
      "deliveries",
      "subscriptions",
      "templates",
      "settings",
    ]);
    expect(visibleOrdersNavItems(["orders:access", "orders:analysis-view"]).map((item) => item.key)).toEqual([
      "quotes",
      "orders",
      "deliveries",
      "subscriptions",
      "analysis",
    ]);
    expect(visibleOrdersNavItems(["orders:analysis-view"])).toEqual([]);
    expect(visibleOrdersNavItems(["orders:settings"])).toEqual([]);
    expect(visibleOrdersNavItems(undefined)).toEqual([]);
  });
});
