import { describe, expect, it } from "vitest";
import { isDecimal } from "./decimal";
import { LOT_NUMBER, lotRowsBody, renderLotPattern, sampleLotNumber, sumLotRows } from "./lots";

describe("renderLotPattern", () => {
  const day = new Date(2026, 10, 12);
  it("renders the tokens the server renders", () => {
    expect(renderLotPattern("{MO}", "JAM-1", day, "MO-12-2")).toBe("MO-12-2");
    expect(renderLotPattern("L{YYMMDD}-{N}", "JAM-1", day, "MO-1", 7)).toBe("L261112-7");
    expect(renderLotPattern("{SKU}/{YYYY}{MM}{DD}", "JAM-1", day)).toBe("JAM-1/20261112");
    expect(renderLotPattern("{XX}", "JAM-1", day)).toBe("{XX}");
  });
  it("tells a lot number from one that is none", () => {
    expect(LOT_NUMBER.test(renderLotPattern("{MO}", "", day))).toBe(true);
    expect(LOT_NUMBER.test(renderLotPattern("{MO} {N}", "", day))).toBe(false);
  });
  it("judges the widest sample, as the server does", () => {
    expect(sampleLotNumber("LOTSX-{MO}-{N}", "")).toBe("LOTSX-MO-99999-9-9999");
    expect(LOT_NUMBER.test(renderLotPattern("LOTSX-{MO}-{N}", "", day))).toBe(true);
    expect(LOT_NUMBER.test(sampleLotNumber("LOTSX-{MO}-{N}", ""))).toBe(false);
    expect(sampleLotNumber("L{YYMMDD}", "")).toBe("L261231");
  });
});

describe("lotRowsBody", () => {
  it("takes the consumed first, then the scrap, in the rows' order", () => {
    const rows = [
      { lotId: 1, locationId: 10, quantity: "3", allowExpired: false },
      { lotId: 2, locationId: 10, quantity: "2.5", allowExpired: true },
    ];
    expect(sumLotRows(rows, isDecimal)).toBe("5.5");
    expect(lotRowsBody(rows, "4")).toEqual([
      { lotId: 1, locationId: 10, consumed: "3", scrapped: "0" },
      { lotId: 2, locationId: 10, consumed: "1", scrapped: "1.5", allowExpired: true },
    ]);
  });
  it("leaves out a row with no lot chosen and sums nothing while a quantity is no decimal", () => {
    expect(lotRowsBody([{ lotId: null, locationId: null, quantity: "1", allowExpired: false }], "1")).toEqual([]);
    expect(sumLotRows([{ lotId: 1, locationId: 1, quantity: "x", allowExpired: false }], isDecimal)).toBeUndefined();
  });
});
