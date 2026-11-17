import { describe, expect, it } from "vitest";
import { gs1Date, readGs1 } from "./gs1";

const GS = "\u001d";
const now = new Date("2026-11-17T12:00:00Z");

describe("reading a GS1 element string", () => {
  it("reads the human-readable form in any order", () => {
    expect(readGs1("(01)07070000000101(17)270531(10)LOT-42", now)).toEqual({
      gtin: "07070000000101",
      expiry: "2027-05-31",
      lot: "LOT-42",
    });
  });

  it("reads the raw form with the symbology identifier, FNC1 after a variable field", () => {
    expect(readGs1(`]C10107070000000101${"10"}LOT-42${GS}11261101${"15"}270100`, now)).toEqual({
      gtin: "07070000000101",
      lot: "LOT-42",
      productionDate: "2026-11-01",
      bestBefore: "2027-01-31",
    });
  });

  it("reads the raw form without an identifier, a variable field last", () => {
    expect(readGs1("010707000000010117270531" + "21SN-0001", now)).toEqual({
      gtin: "07070000000101",
      expiry: "2027-05-31",
      serial: "SN-0001",
    });
  });

  it("is not GS1 for a SKU, a plain barcode, a GTIN without its AI or a string that does not read through", () => {
    expect(readGs1("SKR-10", now)).toBeNull();
    expect(readGs1("7070000000101", now)).toBeNull();
    expect(readGs1("07070000000101", now)).toBeNull();
    expect(readGs1("0107070000000101" + "88XYZ", now)).toBeNull();
    expect(readGs1("(10)LOT-42", now)).toBeNull();
    expect(readGs1("(01)07070000000101(17)271332", now)).toBeNull();
  });
});

describe("a GS1 label with fields the screen does not use", () => {
  it("skips a net weight (3103), a best before and reads the lot last, raw and human-readable", () => {
    const want = { gtin: "07070000000101", bestBefore: "2027-05-31", lot: "LOT-42" };
    expect(readGs1(`]C10107070000000101${"3103"}000500${"15"}270531${"10"}LOT-42`, now)).toEqual(want);
    expect(readGs1("(01)07070000000101(3103)000500(15)270531(10)LOT-42", now)).toEqual(want);
  });

  it("skips an SSCC (00), a content GTIN (02), a count (37), a packaging date (13), a variant (20) and a ship-to GLN (410)", () => {
    expect(
      readGs1(
        `]C1${"00"}370123450000000017${"02"}07070000000101${"37"}12${GS}${"13"}261101${"20"}01${"410"}7070000000017${"11"}261031${"17"}270430${"10"}B7`,
        now,
      ),
    ).toEqual({ gtin: "07070000000101", productionDate: "2026-10-31", expiry: "2027-04-30", lot: "B7" });
  });

  it("reports the lot unread when it ran to the end without FNC1 and a date could follow it", () => {
    expect(readGs1("0107070000000101" + "10LOT42" + "17270531", now)).toEqual({
      gtin: "07070000000101",
      lotUnread: true,
    });
    // With FNC1 after it the lot is plain.
    expect(readGs1(`0107070000000101${"10"}LOT42${GS}17270531`, now)).toEqual({
      gtin: "07070000000101",
      lot: "LOT42",
      expiry: "2027-05-31",
    });
    // A lot whose tail is no field is read.
    expect(readGs1("0107070000000101" + "10LOT-1015", now)).toEqual({ gtin: "07070000000101", lot: "LOT-1015" });
  });

  it("stops at an AI it does not know on a marked scan, keeping the GTIN and asking for the lot and serial", () => {
    expect(readGs1(`]C10107070000000101${"8008"}2611011200${"10"}X`, now)).toEqual({
      gtin: "07070000000101",
      lotUnread: true,
      serialUnread: true,
    });
  });
});

describe("a GS1 date", () => {
  it("takes DD 00 as the month's last day, February in a leap year too", () => {
    expect(gs1Date("280200", now)).toBe("2028-02-29");
    expect(gs1Date("270200", now)).toBe("2027-02-28");
  });

  it("puts the century by the sliding window", () => {
    expect(gs1Date("750101", now)).toBe("2075-01-01");
    expect(gs1Date("770101", now)).toBe("1977-01-01");
    expect(gs1Date("990101", now)).toBe("1999-01-01");
  });

  it("is no date for a month or a day that does not exist", () => {
    expect(gs1Date("271301", now)).toBeUndefined();
    expect(gs1Date("270231", now)).toBeUndefined();
    expect(gs1Date("2705", now)).toBeUndefined();
  });
});
