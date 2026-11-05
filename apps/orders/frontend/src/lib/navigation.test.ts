import { describe, expect, it } from "vitest";
import { visibleOrdersNavItems } from "./navigation";

describe("the Orders sidebar", () => {
  it("offers quotes and orders to orders:access, the analysis with orders:analysis-view, and templates and settings only with orders:settings too", () => {
    expect(visibleOrdersNavItems(["orders:access"]).map((item) => item.key)).toEqual(["quotes", "orders"]);
    expect(visibleOrdersNavItems(["orders:access", "orders:settings"]).map((item) => item.key)).toEqual([
      "quotes",
      "orders",
      "templates",
      "settings",
    ]);
    expect(visibleOrdersNavItems(["orders:access", "orders:analysis-view"]).map((item) => item.key)).toEqual([
      "quotes",
      "orders",
      "analysis",
    ]);
    expect(visibleOrdersNavItems(["orders:analysis-view"])).toEqual([]);
    expect(visibleOrdersNavItems(["orders:settings"])).toEqual([]);
    expect(visibleOrdersNavItems(undefined)).toEqual([]);
  });
});
