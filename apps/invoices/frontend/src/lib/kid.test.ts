import { describe, expect, it } from "vitest";
import { checkMod10, checkMod11, computeKid, kidFits } from "./kid";

/**
 * The same check digits as the server's `kid` package (EHF and KID design
 * D3), pinned against the specification's own worked example: 12345678 is
 * 123456782 under MOD10 and 123456785 under MOD11.
 */
describe("the KID check digits", () => {
  it("computes the specification's worked example under both algorithms", () => {
    expect(checkMod10("12345678")).toBe("2");
    expect(checkMod11("12345678")).toBe("5");
  });

  it("gives MOD11 a '-' when the remainder is 1, and 0 when it is 0", () => {
    // 1·2 = 2 + 0·3 … : "6" weighs 6·2 = 12, remainder 1 → "-".
    expect(checkMod11("6")).toBe("-");
    // "0" sums to 0, remainder 0 → "0".
    expect(checkMod11("0")).toBe("0");
  });

  it("pads the number to the agreed length less one, then appends the check", () => {
    expect(computeKid(12345678, 9, "mod10")).toBe("123456782");
    expect(computeKid(12345678, 9, "mod11")).toBe("123456785");
    expect(computeKid(1000, 7, "mod10")).toBe(`001000${checkMod10("001000")}`);
  });

  it("refuses a number that does not fit, and an unknown algorithm", () => {
    expect(computeKid(12345678, 8, "mod10")).toBeUndefined();
    expect(computeKid(1000, 7, "mod97")).toBeUndefined();
  });

  it("judges the fit and the headroom as the settings do", () => {
    expect(kidFits(1000, 4)).toEqual({ fits: false, headroomLow: false });
    expect(kidFits(1000, 5)).toEqual({ fits: true, headroomLow: true });
    expect(kidFits(1000, 6)).toEqual({ fits: true, headroomLow: true });
    expect(kidFits(1000, 7)).toEqual({ fits: true, headroomLow: false });
  });
});
