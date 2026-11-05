import { describe, expect, it } from "vitest";
import { line } from "../test/fixtures";
import {
  applyGrid,
  editLine,
  fromLine,
  incompleteLines,
  moveLine,
  newLine,
  remainingToDeliver,
  toLineInput,
} from "./lines";

describe("a line as the save sends it", () => {
  it("leaves an untouched price out, so the server keeps or takes the catalog's", () => {
    expect(toLineInput(fromLine(line()), { quote: true })).toEqual({
      id: 5001,
      kind: "product",
      variantId: 301,
      description: "Skjøtekabel",
      quantity: 3,
      unit: "stk",
      discountPercent: 0,
      optional: false,
    });
  });

  it("sends a typed price, which makes it manual", () => {
    const typed = editLine(fromLine(line()), { unitPrice: 450, priceTouched: true });
    expect(toLineInput(typed, { quote: false })).toMatchObject({ unitPrice: 450 });
    expect(toLineInput(typed, { quote: false })).not.toHaveProperty("optional");
  });

  it("sends a free-text line's name and VAT code, and a section's text only", () => {
    const free = { ...newLine("product"), name: "Montering", unitPrice: 900 as const, vatCodeId: 5 };
    expect(toLineInput(free, { quote: false })).toMatchObject({
      variantId: null,
      name: "Montering",
      vatCodeId: 5,
      unitPrice: 900,
    });
    expect(toLineInput({ ...newLine("section"), description: "Arbeid" }, { quote: true })).toEqual({
      kind: "section",
      description: "Arbeid",
    });
  });
});

describe("the rows a save would send incomplete", () => {
  it("names a catalog row with no variant, a free-text row with no name and a row with no quantity", () => {
    const problems = incompleteLines([
      newLine("product", true),
      newLine("product"),
      { ...fromLine(line()), quantity: "" },
      newLine("note"),
    ]);
    expect([...problems]).toEqual([
      [0, "linePickVariant"],
      [1, "lineNameMissing"],
      [2, "lineQuantityMissing"],
    ]);
  });
});

describe("moving and delivering", () => {
  it("moves a row one step and never past the ends", () => {
    const rows = [newLine("section"), newLine("note")];
    expect(moveLine(rows, 1, -1).map((r) => r.kind)).toEqual(["note", "section"]);
    expect(moveLine(rows, 0, -1).map((r) => r.kind)).toEqual(["section", "note"]);
  });

  it("leaves to deliver what is ordered, less cancelled and delivered", () => {
    expect(remainingToDeliver(line({ quantity: 5, qtyCancelled: 1, qtyDelivered: 2 }))).toBe(2);
    expect(remainingToDeliver(line({ quantity: 5, qtyCancelled: 3, qtyDelivered: 3 }))).toBe(0);
  });
});

describe("packagings and the grid (O15, O34)", () => {
  it("sends a line entered in a packaging as its count, not its quantity", () => {
    const packed = fromLine(line({ quantity: 45, packagingId: 7, packagingQuantity: 3, packagingName: "Eske" }));
    expect(packed.packagings).toEqual([{ id: 7, name: "Eske", quantity: 15 }]);
    const input = toLineInput(packed, { quote: false });
    expect(input).toMatchObject({ packagingId: 7, packagingQuantity: 3 });
    expect(input).not.toHaveProperty("quantity");
  });

  it("updates a variant's line from the grid and adds the others", () => {
    const lines = applyGrid(
      [fromLine(line())],
      [
        { variantId: 301, sku: "KAB-10", name: "Kabel", unit: "stk", quantity: 7 },
        { variantId: 302, sku: "KAB-20", name: "Kabel", unit: "stk", quantity: 2 },
        { variantId: 303, sku: "KAB-30", name: "Kabel", unit: "stk", quantity: 0 },
      ],
    );
    expect(lines.map((l) => [l.variantId, l.quantity, l.catalog])).toEqual([
      [301, 7, true],
      [302, 2, true],
    ]);
    expect(lines[0].changed).toBe(true);
  });
});
