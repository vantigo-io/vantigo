import { describe, expect, it } from "vitest";
import { fiscalYearStart, yearBefore } from "./periods";

describe("the reports' default periods", () => {
  it("compares with the same day a year before, 29 February with 28 February", () => {
    expect(yearBefore("2026-03-31")).toBe("2025-03-31");
    expect(yearBefore("2028-02-29")).toBe("2027-02-28");
  });

  it("starts at the fiscal year's first day", () => {
    expect(fiscalYearStart("2026-11-14", 1)).toBe("2026-01-01");
    expect(fiscalYearStart("2026-11-14", 7)).toBe("2026-07-01");
    expect(fiscalYearStart("2026-03-14", 7)).toBe("2025-07-01");
  });
});
