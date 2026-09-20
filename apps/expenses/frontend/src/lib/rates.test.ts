import { describe, expect, it } from "vitest";
import type { ExpenseRate } from "../api/rates";
import { mileagePreview, rateOn } from "./rates";

const rate = (id: number, kind: string, validFrom: string, value: number): ExpenseRate => ({
  id,
  kind,
  validFrom,
  value,
  currency: "NOK",
});

const table: ExpenseRate[] = [
  rate(1, "mileage", "2026-01-01", 5.3),
  rate(2, "mileage", "2026-07-01", 5.5),
  rate(3, "mileage_passenger", "2026-01-01", 1),
];

describe("rateOn", () => {
  it("takes the row with the greatest validFrom on or before the date", () => {
    expect(rateOn(table, "mileage", "2026-06-30")?.value).toBe(5.3);
    expect(rateOn(table, "mileage", "2026-07-01")?.value).toBe(5.5);
  });

  it("answers nothing for a day before every row of the kind", () => {
    expect(rateOn(table, "mileage", "2025-12-31")).toBeUndefined();
    expect(rateOn(table, "mileage_customer", "2026-09-01")).toBeUndefined();
    expect(rateOn(undefined, "mileage", "2026-09-01")).toBeUndefined();
  });
});

describe("mileagePreview", () => {
  it("prices a line at the rate in force on its own date", () => {
    expect(mileagePreview(table, "2026-03-10", 120, 0)).toEqual({ rate: 5.3, amount: 636 });
  });

  it("adds the passenger supplement once per passenger per kilometre", () => {
    expect(mileagePreview(table, "2026-03-10", 120, 2)).toEqual({
      rate: 5.3,
      passengerRate: 1,
      amount: 876,
    });
  });

  it("rounds half-up to two places, the way the server does", () => {
    expect(mileagePreview([rate(9, "mileage", "2026-01-01", 5.305)], "2026-03-10", 1, 0)?.amount).toBe(5.31);
  });

  it("answers nothing when no rate applies, so the form can say so before the save does", () => {
    expect(mileagePreview(table, "2025-12-31", 120, 0)).toBeUndefined();
  });

  it("answers nothing when passengers are carried and no supplement applies", () => {
    expect(mileagePreview([rate(1, "mileage", "2026-01-01", 5.3)], "2026-03-10", 120, 1)).toBeUndefined();
  });
});
