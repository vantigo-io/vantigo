import { describe, expect, it } from "vitest";
import { adjustmentBody, type EditorLine, moveBody, type PostingHeader, receiptBody, roundQuantity } from "./lines";

const header: PostingHeader = {
  clientRequestId: "k",
  siteId: 1,
  note: "",
  reason: "correction",
  toLocationId: 12,
  supplierName: "",
  supplierReference: "",
};
const drifted: EditorLine = {
  key: "line-x",
  variant: { id: 101, productId: 1, sku: "TS-M", productName: "T-shirt", unit: "pcs" },
  quantity: 0.118 + 1,
  locationId: 12,
  toLocationId: 13,
  mode: "change",
};

describe("quantities", () => {
  it("rounds to the three decimals a quantity is stored with (I11)", () => {
    expect(0.118 + 1).not.toBe(1.118);
    expect(roundQuantity(0.118 + 1)).toBe(1.118);
    expect(roundQuantity(-2.5)).toBe(-2.5);
  });

  it("sends every body's quantities rounded, whatever the row holds", () => {
    expect(adjustmentBody(header, [drifted]).lines[0].quantity).toBe(1.118);
    expect(moveBody(header, [drifted]).lines[0].quantity).toBe(1.118);
    expect(receiptBody(header, [drifted]).lines[0].quantity).toBe(1.118);
  });
});
