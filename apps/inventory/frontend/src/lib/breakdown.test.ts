import { describe, expect, it } from "vitest";
import { breakdown, breakdownText, enteredText, packedQuantity } from "./breakdown";

const box = { id: 1, name: "Box", quantity: 15 };
const pallet = { id: 2, name: "Euro pallet", quantity: 600 };
const plain = (n: number) => String(n);

describe("the breakdown (I26)", () => {
  it("takes the largest packaging first, then the next: 1,830 is 3 Euro pallet, 2 Box", () => {
    // The order the packagings come in does not matter: the largest is taken first.
    const value = breakdown(1830, [box, pallet]);
    expect(value).toEqual({
      parts: [
        { packagingId: 2, name: "Euro pallet", count: 3 },
        { packagingId: 1, name: "Box", count: 2 },
      ],
      loose: 0,
    });
    expect(value && breakdownText(value, "pcs", plain)).toBe("3 Euro pallet, 2 Box");
  });

  it("keeps the loose rest in the variant's unit", () => {
    const value = breakdown(1837, [box, pallet]);
    expect(value?.loose).toBe(7);
    expect(value && breakdownText(value, "pcs", plain)).toBe("3 Euro pallet, 2 Box, 7 pcs");
  });

  it("is exact in decimals: 2.5 kg in bags of 0.1 is 25 bags, and 1.118 in 0.5 leaves 0.118", () => {
    const bag = { id: 3, name: "Bag", quantity: 0.1 };
    expect(breakdown(2.5, [bag])).toEqual({ parts: [{ packagingId: 3, name: "Bag", count: 25 }], loose: 0 });
    const half = { id: 4, name: "Half", quantity: 0.5 };
    expect(breakdown(1.118, [half])).toEqual({ parts: [{ packagingId: 4, name: "Half", count: 2 }], loose: 0.118 });
  });

  it("is nothing without packagings, for nothing, or for less than the smallest packaging", () => {
    expect(breakdown(1830, [])).toBeUndefined();
    expect(breakdown(1830)).toBeUndefined();
    expect(breakdown(0, [box])).toBeUndefined();
    expect(breakdown(-30, [box])).toBeUndefined();
    expect(breakdown(14, [box, pallet])).toBeUndefined();
  });

  it("skips a packaging that does not fit what is left", () => {
    expect(breakdown(615, [box, pallet])?.parts.map((part) => part.name)).toEqual(["Euro pallet", "Box"]);
    expect(breakdown(30, [box, pallet])?.parts).toEqual([{ packagingId: 1, name: "Box", count: 2 }]);
  });
});

describe("entered quantities (I25)", () => {
  it("read as the packagings joined by +, and the loose rest", () => {
    const entered = {
      packagings: [
        { packagingId: 2, name: "Euro pallet", count: 3, quantity: 600 },
        { packagingId: 1, name: "Box", count: 2, quantity: 15 },
      ],
    };
    expect(enteredText(entered, "pcs", plain)).toBe("3 Euro pallet + 2 Box");
    expect(enteredText({ ...entered, loose: 4 }, "pcs", plain)).toBe("3 Euro pallet + 2 Box + 4 pcs");
  });

  it("make their base quantity exactly", () => {
    expect(packedQuantity([box, pallet], { 1: 2, 2: 3 }, 0)).toBe(1830);
    expect(packedQuantity([{ id: 3, name: "Bag", quantity: 0.1 }], { 3: 3 }, 0.2)).toBe(0.5);
  });
});
