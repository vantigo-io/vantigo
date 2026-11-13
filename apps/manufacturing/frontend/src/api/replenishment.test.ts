import { describe, expect, it } from "vitest";
import { buySuggestionsCsv, type ManufacturingSuggestion } from "./replenishment";

const buy = (sku: string, projected: string): ManufacturingSuggestion => ({
  id: 1,
  siteId: 1,
  variant: { id: 1, sku, productName: "Wheel", unit: "pcs", productType: "Goods", productStatus: "Active" },
  kind: "buy",
  quantity: "2.5",
  needBy: "2026-11-13",
  reason: {
    projected,
    reorderPoint: "10",
    maximum: null,
    multiple: null,
    onHand: "0",
    reserved: "0",
    incoming: "0",
    inProduction: "0",
    neededByProduction: "0",
    neededBySales: "0",
  },
  bomId: null,
  status: "open",
  reorderOpen: false,
  orderId: null,
  orderNumber: null,
  createdAt: "2026-11-13T01:00:00Z",
});

describe("the buy suggestions' CSV (M111)", () => {
  it("guards a text cell against formulas, but writes a negative number as a number", async () => {
    const text = await buySuggestionsCsv([{ site: "MAIN", suggestion: buy("=WHEEL", "-14.5") }]).text();
    const [, row] = text.split("\r\n");
    expect(row).toBe("MAIN;'=WHEEL;Wheel;2,5;pcs;2026-11-13;-14,5;10");
  });
});
