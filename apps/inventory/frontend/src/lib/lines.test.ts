import { describe, expect, it } from "vitest";
import {
  adjustmentBody,
  type EditorLine,
  lineGap,
  lineQuantity,
  moveBody,
  type PostingHeader,
  receiptBody,
  roundQuantity,
} from "./lines";

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
  packs: {},
  decrease: false,
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

const packed: EditorLine = {
  ...drifted,
  variant: {
    ...(drifted.variant as NonNullable<EditorLine["variant"]>),
    packagings: [
      { id: 7, name: "Euro pallet", kind: "pallet", quantity: 600 },
      { id: 8, name: "Box", kind: "case", quantity: 15 },
    ],
  },
  quantity: "",
  packs: { 7: 3, 8: 2 },
};

describe("quantities entered in packagings (I25)", () => {
  it("send entered when a packaging is counted, and quantity otherwise — never both", () => {
    expect(receiptBody(header, [packed]).lines[0]).toEqual({
      variantId: 101,
      entered: {
        packagings: [
          { packagingId: 7, count: 3 },
          { packagingId: 8, count: 2 },
        ],
      },
    });
    expect(moveBody(header, [{ ...packed, quantity: 4 }]).lines[0]).toEqual({
      variantId: 101,
      fromLocationId: 12,
      toLocationId: 13,
      entered: {
        packagings: [
          { packagingId: 7, count: 3 },
          { packagingId: 8, count: 2 },
        ],
        loose: 4,
      },
    });
    expect(moveBody(header, [{ ...packed, packs: { 7: 0 }, quantity: 4 }]).lines[0]).toEqual({
      variantId: 101,
      fromLocationId: 12,
      toLocationId: 13,
      quantity: 4,
    });
  });

  it("take stock away in an adjustment's change with decrease, the quantity alone with its sign", () => {
    expect(adjustmentBody(header, [{ ...packed, decrease: true }]).lines[0]).toEqual({
      variantId: 101,
      locationId: 12,
      mode: "change",
      entered: {
        packagings: [
          { packagingId: 7, count: 3 },
          { packagingId: 8, count: 2 },
        ],
      },
      decrease: true,
    });
    expect(adjustmentBody(header, [{ ...packed, packs: {}, quantity: 5, decrease: true }]).lines[0]).toEqual({
      variantId: 101,
      locationId: 12,
      mode: "change",
      quantity: -5,
    });
    // Setting the on hand has no direction.
    expect(adjustmentBody(header, [{ ...packed, mode: "set", decrease: true }]).lines[0]).not.toHaveProperty(
      "decrease",
    );
  });

  it("count the packagings and the loose rest toward what the row stands for", () => {
    expect(lineQuantity("receipt", { ...packed, quantity: 4 })).toBe(1834);
    expect(lineQuantity("adjustment", { ...packed, decrease: true })).toBe(-1830);
    expect(lineGap("receipt", packed)).toBeUndefined();
    expect(lineGap("receipt", { ...packed, packs: {} })).toBe("quantity");
  });
});
